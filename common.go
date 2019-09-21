package qmp

import (
	"encoding/json"
	"fmt"
)

// Command represents a QMP command. See https://wiki.qemu.org/QMP
// and https://github.com/qemu/qemu/blob/master/docs/interop/qmp-spec.txt
type Command struct {
	Execute   string      `json:"execute"`
	Arguments interface{} `json:"arguments,omitempty"`
}

// Response represents a common structure of QMP response.
type Response struct {
	// Contains the data returned by the command.
	Return *json.RawMessage `json:"return"`

	// Contains details about an error that occurred.
	Error *GenericError `json:"error"`

	// A status change notification message
	// that can be sent unilaterally by the QMP server.
	Event *json.RawMessage `json:"event"`

	// A greeting message that is sent once when
	// a new QMP connection is established.
	Greeting *json.RawMessage `json:"QMP"`
}

// Event represents a QMP asynchronous event.
type Event struct {
	// Type or name of event. E.g., BLOCK_JOB_COMPLETE.
	Type string `json:"event"`

	// Arbitrary event data.
	Data json.RawMessage `json:"data"`

	// Event timestamp, provided by QEMU.
	Timestamp struct {
		Seconds      uint64 `json:"seconds"`
		Microseconds uint64 `json:"microseconds"`
	} `json:"timestamp"`
}

// Version represents a QEMU version structure returned when a QMP connection is initiated.
type Version struct {
	Package string `json:"package"`
	QEMU    struct {
		Major int `json:"major"`
		Micro int `json:"micro"`
		Minor int `json:"minor"`
	} `json:"qemu"`
}

// HumanCommand represents a query struct to execute a command
// over the human monitor.
type HumanCommand struct {
	Cmd string `json:"command-line"`
}

// DeviceDeletedEventData describes the properties of the DEVICE_DELETED event.
//
// Emitted whenever the device removal completion is acknowledged by the guest.
type DeviceDeletedEventData struct {
	Device string `json:"device"`
	Path   string `json:"path"`
}

// BlockJobErrorEventData describes the properties of the BLOCK_JOB_ERROR event.
//
// Emitted when a block job encounters an error.
type BlockJobErrorEventData struct {
	Device    string `json:"device"`
	Operation string `json:"operation"`
	Action    string `json:"acton"`
}

// BlockJobCompletedEventData describes the properties of the BLOCK_JOB_COMPLETED event.
//
// Emitted when a block job has completed.
type BlockJobCompletedEventData struct {
	Device     string `json:"device"`
	Type       string `json:"type"`
	ErrMessage string `json:"error"`
}

// JobStatusChangeEventData describes the properties of the JOB_STATUS_CHANGE event.
//
// Emitted when a job transitions to a different status.
type JobStatusChangeEventData struct {
	JobID  string `json:"id"`
	Status string `json:"status"`
}

// GenericError represents a common structure for the QMP errors
// that could be accurred. This type also used for errors that doesn't have
// a specific class (for most of them in fact).
type GenericError struct {
	Class string `json:"class"`
	Desc  string `json:"desc"`
}

func (err *GenericError) Error() string {
	return fmt.Sprintf("%s error: %s", err.Class, err.Desc)
}

// CommandNotFound occurs when a requested command has not been found.
type CommandNotFound interface {
	Error() string
}

// DeviceNotActive occurs when a device has failed to be become active.
type DeviceNotActive interface {
	Error() string
}

// DeviceNotFound occurs when a requested device has not been found.
type DeviceNotFound interface {
	Error() string
}

// KVMMissingCap occurs when a requested operation can't be
// fulfilled because a required KVM capability is missing.
type KVMMissingCap interface {
	Error() string
}
