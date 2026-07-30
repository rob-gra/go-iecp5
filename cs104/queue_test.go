package cs104

import (
	"reflect"
	"testing"
	"time"
)

func TestMessageQueue_FIFOOrder(t *testing.T) {
	q := newMessageQueue(10)
	q.Push([]byte("a"))
	q.Push([]byte("b"))
	q.Push([]byte("c"))

	for _, want := range []string{"a", "b", "c"} {
		got, ok := q.Pop()
		if !ok {
			t.Fatalf("Pop() ok = false, want true (want %q)", want)
		}
		if string(got) != want {
			t.Fatalf("Pop() = %q, want %q", got, want)
		}
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("Pop() on empty queue: ok = true, want false")
	}
}

func TestMessageQueue_EvictsOldestWhenFull(t *testing.T) {
	q := newMessageQueue(2)

	if evicted := q.Push([]byte("1")); evicted {
		t.Fatal("Push() into empty slot reported eviction")
	}
	if evicted := q.Push([]byte("2")); evicted {
		t.Fatal("Push() into empty slot reported eviction")
	}
	if evicted := q.Push([]byte("3")); !evicted {
		t.Fatal("Push() into full queue should report eviction")
	}

	// "1" should have been evicted; "2" and "3" remain, in order.
	got, ok := q.Pop()
	if !ok || string(got) != "2" {
		t.Fatalf("Pop() = %q, %v, want \"2\", true", got, ok)
	}
	got, ok = q.Pop()
	if !ok || string(got) != "3" {
		t.Fatalf("Pop() = %q, %v, want \"3\", true", got, ok)
	}
	if _, ok := q.Pop(); ok {
		t.Fatal("queue should be empty after draining both surviving entries")
	}
}

// TestMessageQueue_PopAndEvict_ClearBackingSlot guards against a memory
// retention bug: q.items[1:] alone only moves the slice header forward, so
// without explicitly nil-ing the vacated slot first, the backing array
// keeps referencing an already-popped/evicted payload until some later
// append happens to grow and reallocate the array -- delaying GC of
// payloads that are logically gone from the queue.
func TestMessageQueue_PopAndEvict_ClearBackingSlot(t *testing.T) {
	t.Run("Pop", func(t *testing.T) {
		q := newMessageQueue(10)
		q.Push([]byte("a"))
		q.Push([]byte("b"))

		// Captured before Pop: shares the same backing array, so index 0
		// lets us observe the slot Pop vacates even after q.items itself
		// has been resliced past it.
		beforePop := q.items

		if _, ok := q.Pop(); !ok {
			t.Fatal("Pop() ok = false, want true")
		}
		if beforePop[0] != nil {
			t.Fatal("Pop() left the vacated backing-array slot non-nil")
		}
	})

	t.Run("Push eviction", func(t *testing.T) {
		q := newMessageQueue(1)
		q.Push([]byte("a"))
		beforeEvict := q.items

		if evicted := q.Push([]byte("b")); !evicted {
			t.Fatal("Push() into full queue should report eviction")
		}
		if beforeEvict[0] != nil {
			t.Fatal("Push() eviction left the vacated backing-array slot non-nil")
		}
	})
}

func TestMessageQueue_Ready(t *testing.T) {
	q := newMessageQueue(10)

	select {
	case <-q.Ready():
		t.Fatal("Ready() fired before anything was pushed")
	default:
	}

	q.Push([]byte("x"))
	select {
	case <-q.Ready():
	case <-time.After(time.Second):
		t.Fatal("Ready() did not fire after Push")
	}

	if _, ok := q.Pop(); !ok {
		t.Fatal("Pop() after Ready should succeed")
	}
}

func TestMessageQueue_DrainTo(t *testing.T) {
	src := newMessageQueue(10)
	dst := newMessageQueue(10)

	src.Push([]byte("a"))
	src.Push([]byte("b"))
	dst.Push([]byte("existing"))

	src.DrainTo(dst)

	if src.Len() != 0 {
		t.Fatalf("src.Len() = %d, want 0 after DrainTo", src.Len())
	}

	var got [][]byte
	for {
		v, ok := dst.Pop()
		if !ok {
			break
		}
		got = append(got, v)
	}
	want := [][]byte{[]byte("existing"), []byte("a"), []byte("b")}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("dst contents = %q, want %q", got, want)
	}
}
