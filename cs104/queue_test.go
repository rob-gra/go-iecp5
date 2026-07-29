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
