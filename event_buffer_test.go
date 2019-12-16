package qmp

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

func toStr(ee []Event) string {
	s := []string{}
	for _, e := range ee {
		s = append(s, fmt.Sprintf("{%s %d}", e.Type, e.Timestamp.Seconds))
	}
	return strings.Join(s, " ")
}

func TestBasic(t *testing.T) {
	names := []string{
		"TEST_EVENT_A", // 0
		"TEST_EVENT_B", // 1
		"TEST_EVENT_C", // 2
		"TEST_EVENT_D", // 3
		"TEST_EVENT_E", // 4
		"TEST_EVENT_F", // 5
		"TEST_EVENT_G", // 6
		"TEST_EVENT_H", // 7
		"TEST_EVENT_I", // 8
		"TEST_EVENT_J", // 9
		"TEST_EVENT_K", // 10
		"TEST_EVENT_L", // 11
		"TEST_EVENT_X", // 12
		"TEST_EVENT_N", // 13
		"TEST_EVENT_O", // 0
		"TEST_EVENT_X", // 1
		"TEST_EVENT_Q", // 2
		"TEST_EVENT_R", // 3
		"TEST_EVENT_S", // 4
	}

	eb := &eventBuffer{size: 14}

	for i := 0; i < len(names); i++ {
		ev := Event{Type: names[i]}
		ev.Timestamp.Seconds = uint64(i)
		eb.Put(ev)
	}

	// [1] Looking for TEST_EVENT_X with ts >= 13
	if got, err := eb.Get(context.Background(), "TEST_EVENT_X", 13); err == nil {
		if l := len(got); l != 1 {
			t.Fatalf("[1] got %d records instead of 1: %+v", l, got)
		}
		want := "{TEST_EVENT_X 15}"
		if s := toStr(got); s != want {
			t.Fatalf("[1] got invalid record:\n\twant:\t%s\n\tgot:\t%s", want, s)
		}
	} else {
		t.Fatal(err)
	}

	// [2] Looking for TEST_EVENT_X with any ts value
	if got, err := eb.Get(context.Background(), "TEST_EVENT_X", 0); err == nil {
		if l := len(got); l != 2 {
			t.Fatalf("[2] got %d records instead of 2: %+v", l, got)
		}
		want := "{TEST_EVENT_X 12} {TEST_EVENT_X 15}"
		if s := toStr(got); s != want {
			t.Fatalf("[2] got invalid record:\n\twant:\t%s\n\tgot:\t%s", want, s)
		}
	} else {
		t.Fatal(err)
	}

	// [3] Looking for TEST_EVENT_O with ts == 14
	if got, err := eb.Get(context.Background(), "TEST_EVENT_O", 14); err == nil {
		if l := len(got); l != 1 {
			t.Fatalf("[3] got %d records instead of 1: %+v", l, got)
		}
		want := "{TEST_EVENT_O 14}"
		if s := toStr(got); s != want {
			t.Fatalf("[3] got invalid record:\n\twant:\t%s\n\tgot:\t%s", want, s)
		}
	} else {
		t.Fatal(err)
	}

	// [4] Looking for TEST_EVENT_J with ts == 9
	if got, err := eb.Get(context.Background(), "TEST_EVENT_J", 9); err == nil {
		if l := len(got); l != 1 {
			t.Fatalf("[4] got %d records instead of 1: %+v", l, got)
		}
		want := "{TEST_EVENT_J 9}"
		if s := toStr(got); s != want {
			t.Fatalf("[4] got invalid record:\n\twant:\t%s\n\tgot:\t%s", want, s)
		}
	} else {
		t.Fatal(err)
	}

	// [5] Looking for any events with ts >= 12
	if got, err := eb.Get(context.Background(), "", 12); err == nil {
		if l := len(got); l != 7 {
			t.Fatalf("[5] got %d records instead of 7: %+v", l, got)
		}
		want := "{TEST_EVENT_X 12} {TEST_EVENT_N 13} {TEST_EVENT_O 14} {TEST_EVENT_X 15} {TEST_EVENT_Q 16} {TEST_EVENT_R 17} {TEST_EVENT_S 18}"
		if s := toStr(got); s != want {
			t.Fatalf("[5] got invalid record:\n\twant:\t%s\n\tgot:\t%s", want, s)
		}
	} else {
		t.Fatal(err)
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
