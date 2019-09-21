package qmp

import (
	"context"
	"sync"
	"testing"
	"time"
)

func TestBasic(t *testing.T) {
	eb := &eventBuffer{size: 10}

	for i := 0; i < 3; i++ {
		ev := Event{Type: "TEST_EVENT"}
		ev.Timestamp.Seconds = uint64(i)
		eb.Put(ev)
	}

	got, err := eb.Get(context.Background(), "TEST_EVENT", 2)
	if err != nil {
		t.Fatal(err)
	}

	t.Logf("got %+v", got)

	if l := len(got); l != 1 {
		t.Fatalf("got %d records instead of 1: %+v", l, got)
	}

	if got[0].Timestamp.Seconds != 2 {
		t.Fatalf("got record with wrong timestamp (want TS=2): %+v", got[0])
	}
}

func TestWaiting(t *testing.T) {
	eb := &eventBuffer{size: 10, ctx: context.Background()}

	var wg sync.WaitGroup

	ready := make(chan struct{})

	wg.Add(1)

	go func() {
		defer wg.Done()

		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
		defer cancel()

		close(ready)

		got, err := eb.Get(ctx, "TEST_EVENT", 1) // will wait for single Event{Type:"TEST", Timestamp:1}
		if err != nil {
			t.Error(err)
			return
		}

		t.Logf("got %+v", got)

		if l := len(got); l != 1 {
			t.Errorf("got %d records instead of 1: %+v", l, got)
		}

		if got[0].Timestamp.Seconds != 1 {
			t.Errorf("got record with wrong timestamp (want TS=1): %+v", got[0])
		}
	}()

	<-ready

	for i := 0; i < 3; i++ {
		ev := Event{Type: "TEST_EVENT"}
		ev.Timestamp.Seconds = uint64(i)
		eb.Put(ev)
	}

	wg.Wait()
}
