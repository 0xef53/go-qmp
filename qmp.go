package qmp

import (
	"bufio"
	"encoding/json"
	"fmt"
	"net"
	"time"
)

const (
	EVENTSCAP = 16
)

var (
	noDeadline = time.Time{}
)

type BaseResponse struct {
	Return   *json.RawMessage `json:"return"`
	Error    *QMPError        `json:"error"`
	Event    *QMPEvent        `json:"event"`
	Greeting *json.RawMessage `json:"QMP"`
}

type QMPError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

type QMPEvent struct {
	Event     string           `json:"event"`
	Data      *json.RawMessage `json:"data"`
	Timestamp *EventTimestamp  `json:"timestamp"`
}

type EventTimestamp struct {
	Seconds      uint64 `json:"seconds"`
	Microseconds uint64 `json:"microseconds"`
}

type QMP struct {
	path      string
	sock      net.Conn
	reader    *bufio.Reader
	writer    *bufio.Writer
	events    []string
	negotiate bool
}

func NewQmpConn(path string) *QMP {
	return &QMP{path: path, events: make([]string, 0, EVENTSCAP)}
}

func (qmpConn *QMP) Connect(negotiate bool) ([]byte, error) {
	c, err := net.Dial("unix", qmpConn.path)
	if err != nil {
		return nil, err
	}
	qmpConn.sock = c
	qmpConn.reader = bufio.NewReader(c)
	qmpConn.writer = bufio.NewWriter(c)
	if negotiate {
		return qmpConn.negotiateCapabilities()
	}
	return nil, nil
}

func (qmpConn *QMP) Close() {
	qmpConn.sock.Close()
}

func (qmpConn *QMP) SetDeadline(t time.Time) error {
	return qmpConn.sock.SetDeadline(t)
}

func (qmpConn *QMP) read() (buf []byte, err error) {
	for {
		r := &BaseResponse{}
		buf, err = qmpConn.reader.ReadBytes('\n')
		if err != nil {
			return nil, err
		}
		if err := json.Unmarshal(buf, &r); err != nil {
			return nil, err
		}
		if r.Event != nil {
			qmpConn.events = append(qmpConn.events, string(buf))
			continue
		}
		break
	}
	return
}

func (qmpConn *QMP) negotiateCapabilities() ([]byte, error) {
	r := &BaseResponse{}
	greeting, err := qmpConn.read()
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(greeting, &r); err != nil {
		return nil, err
	}
	if r.Greeting == nil {
		return nil, fmt.Errorf("QMP Capabilities Error 1")
	}
	resp, err := qmpConn.RawCommand([]byte(`{"execute":"qmp_capabilities"}`))
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(resp, &r); err != nil {
		return nil, err
	}
	if r.Return == nil {
		return nil, fmt.Errorf("QMP Capabilities Error 2")
	}
	return greeting, nil

}

func (qmpConn *QMP) GetEvents(e []QMPEvent) error {
	qmpConn.SetDeadline(time.Now().Add(0))
	qmpConn.read()
	qmpConn.SetDeadline(noDeadline)
	event := &QMPEvent{}
	for _, value := range qmpConn.events {
		if err := json.Unmarshal([]byte(value), &event); err != nil {
			return err
		}
		e = append(e, *event)
	}
	qmpConn.events = qmpConn.events[:0]
	return nil
}

func (qmpConn *QMP) RawCommand(b []byte) ([]byte, error) {
	if _, err := qmpConn.writer.Write(append(b, '\x0a')); err != nil {
		return nil, err
	}
	if err := qmpConn.writer.Flush(); err != nil {
		return nil, err
	}
	return qmpConn.read()
}

func (qmpConn *QMP) Command(q interface{}, r interface{}) error {
	jStr, err := json.Marshal(q)
	if err != nil {
		return err
	}
	jRespStr, err := qmpConn.RawCommand(jStr)
	if err != nil {
		return err
	}
	baseResp := &BaseResponse{}
	if err := json.Unmarshal(jRespStr, &baseResp); err != nil {
		return err
	}
	if baseResp.Error != nil {
		return fmt.Errorf("%s: %s\n", baseResp.Error.Class, baseResp.Error.Desc)
	}
	if r == nil {
		return nil
	}
	if err := json.Unmarshal(*baseResp.Return, r); err != nil {
		return err
	}
	return nil
}
