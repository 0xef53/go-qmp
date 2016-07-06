package qmp

import (
	"bufio"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"time"
)

const (
	EVENTSCAP = 16
)

var (
	noDeadline = time.Time{}

	ErrHandshake   = errors.New("QMP Handshake error: invalid greeting")
	ErrNegotiation = errors.New("QMP Handshake error: negotiations failed")
)

type QMPResponse struct {
	Return   *json.RawMessage `json:"return"`
	Error    *QMPError        `json:"error"`
	Event    *json.RawMessage `json:"event"`
	Greeting *json.RawMessage `json:"QMP"`
}

type QMPError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

func (e *QMPError) Error() string {
	return fmt.Sprintf("%s: %s", e.Class, e.Desc)
}

type QMPEvent struct {
	Event     string          `json:"event"`
	Data      json.RawMessage `json:"data"`
	Timestamp *EventTimestamp `json:"timestamp"`
}

type EventTimestamp struct {
	Seconds      uint64 `json:"seconds"`
	Microseconds uint64 `json:"microseconds"`
}

type QMPCommand struct {
	Execute   string      `json:"execute"`
	Arguments interface{} `json:"arguments,omitempty"`
}

type QMPConn struct {
	path     string
	sock     net.Conn
	reader   *bufio.Reader
	writer   *bufio.Writer
	events   []QMPEvent
	greeting string
}

func Dial(path string) (*QMPConn, error) {
	s, err := net.DialTimeout("unix", path, time.Second*10)
	if err != nil {
		return nil, err
	}
	qmpConn := QMPConn{
		path:   path,
		sock:   s,
		reader: bufio.NewReader(s),
		writer: bufio.NewWriter(s),
		events: make([]QMPEvent, 0, EVENTSCAP),
	}
	if err := qmpConn.handshake(); err != nil {
		s.Close()
		return nil, err
	}
	return &qmpConn, nil
}

func (c *QMPConn) handshake() error {
	r := &QMPResponse{}
	buf, err := c.read()
	if err != nil {
		return err
	}
	if err := json.Unmarshal(buf, &r); err != nil {
		return err
	}
	if r.Greeting == nil {
		return ErrHandshake
	}
	res, err := c.Run([]byte(`{"execute":"qmp_capabilities"}`))
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

func (c *QMPConn) Close() {
	c.sock.Close()
}

func (c *QMPConn) SetDeadline(t time.Time) error {
	return c.sock.SetDeadline(t)
}

func (c *QMPConn) read() (buf []byte, err error) {
	for {
		r := QMPResponse{}
		buf, err = c.reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(buf, &r); err != nil {
			return nil, err
		}
		if r.Event != nil {
			var event QMPEvent
			if err := json.Unmarshal(buf, &event); err != nil {
				return nil, err
			}
			c.events = append(c.events, event)
			continue
		}
		break
	}
	return buf, nil
}

func (c *QMPConn) GetEvents() ([]QMPEvent, error) {
	if _, err := c.Run([]byte(`{"execute":"query-name"}`)); err != nil {
		return nil, err
	}
	events := make([]QMPEvent, len(c.events))
	copy(events, c.events)
	c.events = c.events[:0]
	return events, nil
}

func (c *QMPConn) Run(b []byte) ([]byte, error) {
	if _, err := c.writer.Write(append(b, '\x0a')); err != nil {
		return nil, err
	}
	if err := c.writer.Flush(); err != nil {
		return nil, err
	}
	return c.read()
}

func (c *QMPConn) Command(cmd interface{}, res interface{}) error {
	jcmd, err := json.Marshal(cmd)
	if err != nil {
		return err
	}
	jresp, err := c.Run(jcmd)
	if err != nil {
		return err
	}
	resp := &QMPResponse{}
	if err := json.Unmarshal(jresp, resp); err != nil {
		return err
	}
	if resp.Error != nil {
		return resp.Error
	}
	if res == nil {
		return nil
	}
	if err := json.Unmarshal(*resp.Return, res); err != nil {
		return err
	}
	return nil
}
