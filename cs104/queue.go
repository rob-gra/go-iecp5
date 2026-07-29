// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import "sync"

// messageQueue is a bounded, thread-safe FIFO of pending outbound ASDU
// payloads. When full, Push evicts the oldest entry to make room for the
// newest, rather than rejecting the newest and losing the most recent data.
//
// Unlike a plain channel, messageQueue is never drained just because the
// underlying connection was closed: run()'s cleanUp doesn't touch it, so
// whatever is still queued survives a reconnect (Client, ServerSpecial) or a
// redundancy-group hand-off (Server, see handleSessionActivated) instead of
// being silently dropped.
type messageQueue struct {
	mu    sync.Mutex
	items [][]byte
	max   int

	ready chan struct{} // buffered cap 1; signals "queue is non-empty"
}

// newMessageQueue creates a queue that holds at most max entries.
func newMessageQueue(max int) *messageQueue {
	if max < 1 {
		max = 1
	}
	return &messageQueue{
		max:   max,
		ready: make(chan struct{}, 1),
	}
}

// Push appends data to the queue, evicting the oldest entry first if the
// queue is already at capacity. It reports whether an entry was evicted.
func (q *messageQueue) Push(data []byte) (evicted bool) {
	q.mu.Lock()
	if len(q.items) >= q.max {
		q.items = q.items[1:]
		evicted = true
	}
	q.items = append(q.items, data)
	q.mu.Unlock()

	select {
	case q.ready <- struct{}{}:
	default:
	}
	return evicted
}

// Pop removes and returns the oldest entry, if any.
func (q *messageQueue) Pop() ([]byte, bool) {
	q.mu.Lock()
	defer q.mu.Unlock()

	if len(q.items) == 0 {
		return nil, false
	}
	data := q.items[0]
	q.items = q.items[1:]

	if len(q.items) > 0 {
		select {
		case q.ready <- struct{}{}:
		default:
		}
	}
	return data, true
}

// Ready returns a channel that receives a value when the queue transitions
// from empty to non-empty. It's a hint, not a guarantee: always safe (and
// necessary) to just try Pop after a receive, since another goroutine may
// have already drained the queue.
func (q *messageQueue) Ready() <-chan struct{} {
	return q.ready
}

// Len returns the current number of queued entries.
func (q *messageQueue) Len() int {
	q.mu.Lock()
	defer q.mu.Unlock()
	return len(q.items)
}

// DrainTo pops every entry off q, in order, and pushes it onto dst. Used to
// hand off a superseded session's undelivered queue to the session that
// replaced it, so activating a new connection in a redundancy group doesn't
// lose whatever the old one hadn't sent yet.
func (q *messageQueue) DrainTo(dst *messageQueue) {
	for {
		data, ok := q.Pop()
		if !ok {
			return
		}
		dst.Push(data)
	}
}
