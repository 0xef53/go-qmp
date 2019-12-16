package qmp

import (
	"context"
	"sort"
	"sync"
)

// This is a ring buffer with a given size where all occurred QMP events stored.
type eventBuffer struct {
	mu      sync.Mutex
	events  []Event
	size    int
	cur     int
	waiters map[string][]chan Event
	ctx     context.Context
}

func (eb *eventBuffer) find(t string, after uint64) ([]Event, bool) {
	i := sort.Search(len(eb.events), func(i int) bool {
		offset := (i + eb.cur) % len(eb.events)
		return eb.events[offset].Timestamp.Seconds >= after
	})

	if i < len(eb.events) {
		offset := (i + eb.cur) % len(eb.events)

		out := make([]Event, 0)

		right := len(eb.events)
		if i+eb.cur >= right {
			// The buffer border is exceeded
			right = offset
		}

		for _, e := range eb.events[offset:right] {
			if e.Type == t || t == "" {
				out = append(out, e)
			}
		}

		left := offset
		if left >= eb.cur {
			left = 0
		}

		for _, e := range eb.events[left:eb.cur] {
			if e.Type == t || t == "" {
				out = append(out, e)
			}
		}

		if len(out) > 0 {
			return out, true
		}
	}

	// No matches found
	return nil, false
}

// Find tries to find at least one event of the specified type
// that occurred after the specified Unix time (in seconds).
// If no matches found, the second return value will be false.
func (eb *eventBuffer) Find(t string, after uint64) ([]Event, bool) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	return eb.find(t, after)
}

// Get returns an event list of the specified type from the buffer.
// If no events are found, the function subscribes and waits for the first new event
// until the context is closed (manually or using context.WithTimeout).
func (eb *eventBuffer) Get(ctx context.Context, t string, after uint64) ([]Event, error) {
	eb.mu.Lock()

	// Check existing events
	if ee, found := eb.find(t, after); found {
		eb.mu.Unlock()
		return ee, nil
	}

	// No matches found, subscribe and wait
	if eb.waiters == nil {
		eb.waiters = make(map[string][]chan Event)
	}
	ch := make(chan Event, 1)
	eb.waiters[t] = append(eb.waiters[t], ch)

	eb.mu.Unlock()

	defer func() {
		eb.mu.Lock()
		defer eb.mu.Unlock()
		if w := eb.waiters[t]; len(w) == 1 && w[0] == ch { // shortcut for 1-element slice
			delete(eb.waiters, t)
			return
		}
		for i, c := range eb.waiters[t] {
			if c != ch {
				continue
			}
			// remove eb.waiters[t][i]
			w := eb.waiters[t]
			w = append(w[:i], w[i+1:]...)
			eb.waiters[t] = w
			return
		}
	}()

	for {
		select {
		case <-ctx.Done():
			// timeout
			return nil, ctx.Err()
		case <-eb.ctx.Done():
			// global context
			return nil, ErrOperationCanceled
		case ev := <-ch:
			if ev.Timestamp.Seconds >= after {
				return []Event{ev}, nil
			}
		}
	}
}

// Put appends events to the buffer, and also sends them to all subscribers.
//
// Put assumes that events are added with non-decreasing timestamps (each next
// has timestamp larger or equal to previous)
func (eb *eventBuffer) Put(ee ...Event) {
	eb.mu.Lock()
	defer eb.mu.Unlock()

	append := func(e *Event) {
		if len(eb.events) < eb.size {
			eb.events = append(eb.events, *e)
		} else {
			eb.events[eb.cur] = *e
		}
		eb.cur++
		if eb.cur == eb.size {
			eb.cur = 0
		}
	}

	for _, e := range ee {
		append(&e)
	}

	for _, e := range ee {
		for _, ch := range eb.waiters[e.Type] {
			select {
			case ch <- e:
			default:
			}
		}
	}
}
