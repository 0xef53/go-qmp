package qmp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"sync"
	"syscall"
	"time"
)

var (
	noDeadline = time.Time{}

	ErrHandshake   = errors.New("QMP Handshake error: invalid greeting")
	ErrNegotiation = errors.New("QMP Handshake error: negotiations failed")

	ErrOperationCanceled = errors.New("Operation canceled: channel was closed")
)

// Monitor represents a connection to communicate with the QMP interface using a UNIX socket.
type Monitor struct {
	conn net.Conn

	reader *bufio.Reader
	writer *bufio.Writer

	resp  chan []byte
	evbuf *eventBuffer

	mu       sync.Mutex
	cancel   context.CancelFunc
	released chan struct{}
	closed   bool
	err      error
}

// NewMonitor creates and configures a connection to the QEMU monitor using a UNIX socket.
// An error is returned if the socket cannot be successfully dialed, or the dial attempt times out.
//
// Multiple connections to the same QMP socket are not permitted,
// and will result in the monitor blocking until the existing connection is closed.
func NewMonitor(path string, timeout time.Duration) (*Monitor, error) {
	conn, err := net.DialTimeout("unix", path, timeout)
	if err != nil {
		return nil, err
	}

	mon := Monitor{
		conn:   conn,
		reader: bufio.NewReader(conn),
		writer: bufio.NewWriter(conn),
		evbuf:  &eventBuffer{size: 100},
		resp:   make(chan []byte),
	}

	if err := mon.handshake(); err != nil {
		conn.Close()
		return nil, err
	}

	ctx, cancel := context.WithCancel(context.Background())

	mon.cancel = cancel
	mon.evbuf.ctx = ctx
	mon.released = make(chan struct{})

	// collect() reads data by line from the connection
	// and can be manually interrupted only using ctx.
	go func() {
		var err error
		defer func() {
			conn.Close()
		}()

		switch err = mon.collect(ctx); {
		case err == nil, err == io.EOF:
			// Function has been closed using Close() method.
			// In this case we just make the error,
			// indicating that the connection was closed:
			// ESHUTDOWN: Cannot send after transport endpoint shutdown
			mon.err = &net.OpError{Op: "read", Net: "unix", Err: &os.SyscallError{"syscall", syscall.ESHUTDOWN}}
		default:
			mon.err = err
			cancel()
		}
		mon.closed = true

		close(mon.resp)
		close(mon.released)
		// At this stage:
		// - connection is closed
		// - mon.resp is closed
		// - all evbuf.Get() instances are interrupted by mon.cancel()
		// - map evbuf.waiters is cleared at the end of each evbuf.Get() respectively
		// - other evbuf variables will be deleted by GC
	}()

	return &mon, nil
}

func (m *Monitor) handshake() error {
	var r Response

	// Handshake
	b, err := m.reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return err
	}
	if r.Greeting == nil {
		return ErrHandshake
	}

	// Negotiation
	if err := m.write([]byte(`{"execute":"qmp_capabilities"}`)); err != nil {
		return err
	}
	res, err := m.reader.ReadBytes('\n')
	if err != nil {
		return err
	}
	if err := json.Unmarshal(res, &r); err != nil {
		return err
	}
	if r.Return == nil {
		return ErrNegotiation
	}

	return nil
}

// Close closes the QMP connection and releases all resources.
//
// After this call any interaction with the monitor
// will generate an error of type net.OpError.
func (m *Monitor) Close() error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		return nil
	}

	// Stop the background socket reading
	m.cancel()
	// This needs to "wake up" the socket
	m.write([]byte(`{"execute":"query-name"}`))
	// And wait...
	<-m.resp
	<-m.released

	return m.conn.Close()
}

func (m *Monitor) write(b []byte) error {
	if _, err := m.writer.Write(append(b, '\x0a')); err != nil {
		return err
	}
	return m.writer.Flush()
}

func (m *Monitor) collect(ctx context.Context) error {
loop:
	for {
		select {
		case <-ctx.Done():
			break loop
		default:
		}

		data, err := m.reader.ReadBytes('\n')
		if err != nil {
			return err
		}

		var r Response
		if err := json.Unmarshal(data, &r); err != nil {
			return err
		}

		if r.Event != nil {
			var event Event
			if err := json.Unmarshal(data, &event); err != nil {
				return err
			}
			m.evbuf.Put(event)
			continue
		}

		m.resp <- data
	}

	return nil
}

// Run executes the given QAPI command.
func (m *Monitor) Run(cmd interface{}, res interface{}) error {
	m.mu.Lock()
	defer m.mu.Unlock()

	if m.closed {
		//panic("unable to work with closed monitor")
		return m.err
	}

	b, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	if err := m.write(b); err != nil {
		return err
	}

	var data []byte
	select {
	case b, ok := <-m.resp:
		if !ok {
			// we can be here for two reasons:
			// - collect() ended with an error.
			//   In this case m.err will be contain the corresponding error
			//   (ECONNREFUSED or EPIPE or something else)
			// - close() was called.
			//   In this case m.err will be equal to our special error ESHUTDOWN,
			//   which means "transport is closed"
			return m.err
		}
		data = b
	}

	var r Response
	if err := json.Unmarshal(data, &r); err != nil {
		return err
	}
	if r.Error != nil {
		return NewQMPError(r.Error)
	}

	if res == nil {
		return nil
	}

	if err := json.Unmarshal(*r.Return, res); err != nil {
		return err
	}

	return nil
}

// RunHuman executes a command using "human-monitor-command".
func (m *Monitor) RunHuman(cmdline string) (string, error) {
	var out string

	if err := m.Run(Command{"human-monitor-command", &HumanCommand{Cmd: cmdline}}, &out); err != nil {
		return "", err
	}

	return out, nil
}

// RunTransaction executes a number of transactionable QAPI commands atomically.
func (m *Monitor) RunTransaction(cmds []Command, res interface{}, properties *TransactionProperties) error {
	args := struct {
		Actions    []TransactionAction    `json:"actions"`
		Properties *TransactionProperties `json:"properties,omitempty"`
	}{
		Actions:    make([]TransactionAction, 0, len(cmds)),
		Properties: properties,
	}

	for _, cmd := range cmds {
		if _, ok := AllowedTransactionActions[cmd.Name]; !ok {
			return fmt.Errorf("Unknown transaction command", cmd.Name)
		}
		action := TransactionAction{
			Type: cmd.Name,
			Data: cmd.Arguments,
		}
		args.Actions = append(args.Actions, action)
	}

	return m.Run(Command{"transaction", &args}, &res)
}

// GetEvents returns an event list of the specified type
// that occurred after the specified Unix time (in seconds).
// If there are events in the buffer, then GetEvents will return them.
// Otherwise, the function will wait for the first event until the context is closed
// (manually or using context.WithTimeout).
func (m *Monitor) GetEvents(ctx context.Context, t string, after uint64) ([]Event, error) {
	if m.closed {
		//panic("unable to work with closed monitor")
		return nil, m.err
	}

	// m.evbuf.Get() can be interrupted by the global m.ctx,
	// that is in m.evbuf.
	ee, err := m.evbuf.Get(ctx, t, after)
	switch err {
	case nil:
	case ErrOperationCanceled:
		// This means that m.evbuf.Get() was interrupted by the global m.ctx.
		// The reason of that is in the m.err variable.
		return nil, m.err
	default:
		return nil, err
	}

	return ee, nil
}

// FindEvents tries to find in the buffer at least one event
// of the specified type that occurred after the specified Unix time (in seconds).
// If no matches found, the second return value will be false.
func (m *Monitor) FindEvents(t string, after uint64) ([]Event, bool) {
	if m.closed {
		//panic("unable to work with closed monitor")
		return nil, false
	}

	return m.evbuf.Find(t, after)
}

// WaitDeviceDeletedEvent waits a DEVICE_DELETED event for the specified device.
func (m *Monitor) WaitDeviceDeletedEvent(ctx context.Context, device string, after uint64) (*Event, error) {
	var event *Event

loop:
	for {
		events, err := m.GetEvents(ctx, "DEVICE_DELETED", after)
		if err != nil {
			return nil, err
		}
		for _, e := range events {
			var data DeviceDeletedEventData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				return nil, err
			}
			if data.Device == device || data.Path == device {
				event = &e
				break loop
			}
			after = e.Timestamp.Seconds
		}
	}

	return event, nil
}

// WaitJobStatusChangeEvent waits a JOB_STATUS_CHANGE event for the specified job ID.
func (m *Monitor) WaitJobStatusChangeEvent(ctx context.Context, jobID, status string, after uint64) (*Event, error) {
	var event *Event

loop:
	for {
		events, err := m.GetEvents(ctx, "JOB_STATUS_CHANGE", after)
		if err != nil {
			return nil, err
		}
		for _, e := range events {
			var data JobStatusChangeEventData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				return nil, err
			}
			if data.JobID == jobID && data.Status == status {
				event = &e
				break loop
			}
			after = e.Timestamp.Seconds
		}
	}

	return event, nil
}

// waitDeviceTrayMovedEvent waits a DEVICE_TRAY_MOVED event
// with a specified state for the specified device.
//
// The state variable can be -1 (any states), 0 (closed) and 1 (opened).
func (m *Monitor) waitDeviceTrayMovedEvent(ctx context.Context, device string, state int16, after uint64) (*Event, error) {
	if state < -1 || state > 1 {
		panic("incorrect state value")
	}

	var event *Event

loop:
	for {
		events, err := m.GetEvents(ctx, "DEVICE_TRAY_MOVED", after)
		if err != nil {
			return nil, err
		}
		for _, e := range events {
			var data DeviceTrayMovedEventData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				return nil, err
			}
			if data.Device == device || data.QdevID == device {
				switch state {
				case -1:
					event = &e
				case 0:
					if !data.Open {
						event = &e
					}
				case 1:
					if data.Open {
						event = &e
					}
				}
				if event != nil {
					break loop
				}
			}
			after = e.Timestamp.Seconds
		}
	}

	return event, nil
}

// WaitDeviceTrayMovedEvent waits a DEVICE_TRAY_MOVED event with any state for the specified device.
func (m *Monitor) WaitDeviceTrayMovedEvent(ctx context.Context, device string, after uint64) (*Event, error) {
	return m.waitDeviceTrayMovedEvent(ctx, device, -1, after)
}

// WaitDeviceTrayClosedEvent waits a DEVICE_TRAY_MOVED event with state == "close" for the specified device.
func (m *Monitor) WaitDeviceTrayClosedEvent(ctx context.Context, device string, after uint64) (*Event, error) {
	return m.waitDeviceTrayMovedEvent(ctx, device, 0, after)
}

// WaitDeviceTrayOpenedEvent waits a DEVICE_TRAY_MOVED event with state == "open" for the specified device.
func (m *Monitor) WaitDeviceTrayOpenedEvent(ctx context.Context, device string, after uint64) (*Event, error) {
	return m.waitDeviceTrayMovedEvent(ctx, device, 1, after)
}

// FindBlockJobErrorEvent tries to find a BLOCK_JOB_ERROR for the specified device.
func (m *Monitor) FindBlockJobErrorEvent(device string, after uint64) (*Event, bool, error) {
	events, found := m.FindEvents("BLOCK_JOB_ERROR", after)
	if found {
		for _, e := range events {
			var data BlockJobErrorEventData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				return nil, false, err
			}
			if data.Device == device {
				return &e, true, nil
			}
		}
	}

	return nil, false, nil
}

// FindBlockJobCompletedEvent tries to find a BLOCK_JOB_COMPLETED event for the specified device.
func (m *Monitor) FindBlockJobCompletedEvent(device string, after uint64) (*Event, bool, error) {
	events, found := m.FindEvents("BLOCK_JOB_COMPLETED", after)
	if found {
		for _, e := range events {
			var data BlockJobCompletedEventData
			if err := json.Unmarshal(e.Data, &data); err != nil {
				return nil, false, err
			}
			if data.Device == device {
				return &e, true, nil
			}
		}
	}

	return nil, false, nil
}

func NewQMPError(err *GenericError) error {
	switch err.Class {
	case "CommandNotFound":
		return &CommandNotFound{err}
	case "DeviceNotActive":
		return &DeviceNotActive{err}
	case "DeviceNotFound":
		return &DeviceNotFound{err}
	case "KVMMissingCap":
		return &KVMMissingCap{err}
	}
	return err
}

func IsSocketNotAvailable(err error) bool {
	switch err.(type) {
	case *net.OpError:
		err := err.(*net.OpError).Err
		switch err.(type) {
		case *os.SyscallError:
			if errno, ok := err.(*os.SyscallError).Err.(syscall.Errno); ok {
				return errno == syscall.ENOENT || errno == syscall.ECONNREFUSED || errno == syscall.ECONNRESET || errno == syscall.EPIPE
			}
		}
	}
	return false
}

func IsSocketClosed(err error) bool {
	switch err.(type) {
	case *net.OpError:
		err := err.(*net.OpError).Err
		switch err.(type) {
		case *os.SyscallError:
			if errno, ok := err.(*os.SyscallError).Err.(syscall.Errno); ok {
				return errno == syscall.ESHUTDOWN
			}
		}
	}
	return false
}
