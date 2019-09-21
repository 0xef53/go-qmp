go-qmp
-----------

[![GoDoc](https://godoc.org/github.com/0xef53/go-qmp?status.svg)](https://godoc.org/github.com/0xef53/go-qmp)

Package go-qmp implements a [QEMU Machine Protocol](http://wiki.qemu.org/QMP) for the Go language.

### Installation

    go get github.com/0xef53/go-qmp

### Example

#### Waiting for a virtual machine completion

```go
mon, err := NewMonitor("/var/run/qemu/alice.qmp", 60*time.Second)
if err != nil {
	log.Fatalln(err)
}
defer mon.Close()

done := make(chan struct{})
go func() {
	ts := time.Now()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	got, err := mon.GetEvents(ctx, "SHUTDOWN", uint64(ts.Unix()))
	if err != nil {
		log.Printf("Timeout error (type=%T): %s\n", err, err)
	} else {
		log.Printf("OK, got a SHUTDOWN event: %#v\n", got)
	}
	close(done)
}()

log.Println("Sleeping for three seconds ...")

time.Sleep(3 * time.Second)

log.Println("... and sending a 'system_powerdown' command.")

if err := mon.Run(Command{"system_powerdown", nil}, nil); err != nil {
	log.Fatalln(err)
}

<-done
```

#### Executing a command via human monitor

```go
mon, err := NewMonitor("/var/run/qemu/alice.qmp", 60*time.Second)
if err != nil {
	log.Fatalln(err)
}

var out string

if err := mon.Run(Command{"human-monitor-command", &HumanCommand{"info vnc"}}, &out); err != nil {
	log.Fatalln(err)
}

fmt.Println(out)

```

#### Removing a device from a guest

Completion of the process is signaled with a `DEVICE_DELETED` event.

```go
mon, err := NewMonitor("/var/run/qemu/alice.qmp", 60*time.Second)
if err != nil {
	log.Fatalln(err)
}

deviceID := struct {
	Id string `json:"id"`
}{
	"blk_alice",
}

ts := time.Now()
if err := mon.Run(Command{"device_del", &deviceID}, nil); err != nil {
	log.Fatalln("device_del error:", err)
}

// ... and wait until the operation is completed
ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
defer cancel()

switch _, err := mon.WaitDeviceDeletedEvent(ctx, "blk_alice", uint64(ts.Unix())); {
case err == nil:
case err == context.DeadlineExceeded:
	log.Fatalln("device_del timeout error: failed to complete within 60 seconds")
default:
	log.Fatalln(err)
}
```

### Documentation

Use [Godoc documentation](https://godoc.org/github.com/0xef53/qmp-monitor) for reference and usage.
