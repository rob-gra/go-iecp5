package cs104

import (
	"context"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// stubAddr is a net.Addr with an arbitrary fixed string, used to drive
// groupKeyFor's IP matching without a real socket.
type stubAddr string

func (a stubAddr) Network() string { return "tcp" }
func (a stubAddr) String() string  { return string(a) }

// stubConn is a minimal net.Conn whose only real behavior is RemoteAddr,
// enough to exercise Server.groupKeyFor without a real connection.
type stubConn struct {
	remote net.Addr
}

func (c *stubConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (c *stubConn) Write(b []byte) (int, error)      { return len(b), nil }
func (c *stubConn) Close() error                     { return nil }
func (c *stubConn) LocalAddr() net.Addr              { return stubAddr("") }
func (c *stubConn) RemoteAddr() net.Addr             { return c.remote }
func (c *stubConn) SetDeadline(time.Time) error      { return nil }
func (c *stubConn) SetReadDeadline(time.Time) error  { return nil }
func (c *stubConn) SetWriteDeadline(time.Time) error { return nil }

func TestServer_groupKeyFor(t *testing.T) {
	conn := &stubConn{remote: stubAddr("192.168.1.10:2404")}

	t.Run("ModeConnectionIsRedundancyGroup is ungrouped", func(t *testing.T) {
		srv := NewServer(stubServerHandler{})
		if key := srv.groupKeyFor(conn); key != nil {
			t.Fatalf("groupKeyFor() = %v, want nil", key)
		}
	})

	t.Run("ModeSingleRedundancyGroup shares one key regardless of address", func(t *testing.T) {
		srv := NewServer(stubServerHandler{})
		srv.SetServerMode(ModeSingleRedundancyGroup)

		other := &stubConn{remote: stubAddr("10.0.0.1:2404")}
		key1 := srv.groupKeyFor(conn)
		key2 := srv.groupKeyFor(other)
		if key1 == nil || key1 != key2 {
			t.Fatalf("groupKeyFor() = %v, %v, want equal non-nil keys", key1, key2)
		}
	})

	t.Run("ModeMultipleRedundancyGroups matches by allowed client IP", func(t *testing.T) {
		srv := NewServer(stubServerHandler{})
		srv.SetServerMode(ModeMultipleRedundancyGroups)
		rgA := NewRedundancyGroup("A").AddAllowedClient("192.168.1.10")
		rgB := NewRedundancyGroup("B").AddAllowedClient("10.0.0.1")
		srv.AddRedundancyGroup(rgA)
		srv.AddRedundancyGroup(rgB)

		if key := srv.groupKeyFor(conn); key != rgA {
			t.Fatalf("groupKeyFor(%v) = %v, want group A", conn.remote, key)
		}
		unmatched := &stubConn{remote: stubAddr("172.16.0.1:2404")}
		if key := srv.groupKeyFor(unmatched); key != nil {
			t.Fatalf("groupKeyFor(%v) = %v, want nil (no matching group)", unmatched.remote, key)
		}
	})
}

// newPipeSession wires a Server-managed SrvSession to one end of an
// in-memory net.Pipe, mirroring what ListenAndServer's accept loop does, so
// redundancy-group enforcement can be tested without a real net.Listener.
func newPipeSession(t *testing.T, srv *Server) (*SrvSession, net.Conn) {
	t.Helper()

	serverConn, peerConn := net.Pipe()
	sess := srv.newSession(serverConn)

	srv.mux.Lock()
	srv.sessions[sess] = struct{}{}
	srv.mux.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = peerConn.Close()
	})
	go sess.run(ctx, serverConn)

	return sess, peerConn
}

func TestServer_SingleRedundancyGroup_ActivatingSupersedesPriorActive(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.SetServerMode(ModeSingleRedundancyGroup)

	sessA, peerA := newPipeSession(t, srv)
	_ = peerA.SetDeadline(time.Now().Add(2 * time.Second))
	writeFrame(t, peerA, newUFrame(uStartDtActive))
	if head, _ := readFrame(t, peerA); head.(uAPCI).function != uStartDtConfirm {
		t.Fatalf("expected A's StartDtConfirm")
	}
	if !sessA.IsActive() {
		t.Fatal("session A should be active")
	}

	sessB, peerB := newPipeSession(t, srv)
	_ = peerB.SetDeadline(time.Now().Add(2 * time.Second))
	writeFrame(t, peerB, newUFrame(uStartDtActive))
	if head, _ := readFrame(t, peerB); head.(uAPCI).function != uStartDtConfirm {
		t.Fatalf("expected B's StartDtConfirm")
	}
	if !sessB.IsActive() {
		t.Fatal("session B should be active")
	}

	// A shared a redundancy group with B, and B just activated, so A should
	// be deactivated (an unsolicited StopDtConfirm), but per
	// IEC 60870-5-104's redundant-connection model its TCP connection must
	// stay up as a warm standby, not be closed.
	head, _ := readFrame(t, peerA)
	if u, ok := head.(uAPCI); !ok || u.function != uStopDtConfirm {
		t.Fatalf("got %#v, want an unsolicited StopDtConfirm", head)
	}
	if sessA.IsActive() {
		t.Fatal("session A should be inactive after being superseded")
	}
	if !sessA.IsConnected() {
		t.Fatal("session A must remain connected (a standby), not be closed, when superseded")
	}
	if !sessB.IsConnected() || !sessB.IsActive() {
		t.Fatal("session B should be connected and active")
	}

	// A must still be usable as a standby: reactivating it should work
	// without reconnecting, and that in turn supersedes B.
	writeFrame(t, peerA, newUFrame(uStartDtActive))
	if head, _ := readFrame(t, peerA); head.(uAPCI).function != uStartDtConfirm {
		t.Fatalf("expected A's StartDtConfirm on reactivation")
	}
	if !sessA.IsActive() {
		t.Fatal("session A should be active again after reactivation")
	}
}

func TestServer_ConnectionIsRedundancyGroup_BothStayActive(t *testing.T) {
	srv := NewServer(stubServerHandler{}) // default mode: no grouping

	sessA, peerA := newPipeSession(t, srv)
	_ = peerA.SetDeadline(time.Now().Add(2 * time.Second))
	writeFrame(t, peerA, newUFrame(uStartDtActive))
	readFrame(t, peerA)

	sessB, peerB := newPipeSession(t, srv)
	_ = peerB.SetDeadline(time.Now().Add(2 * time.Second))
	writeFrame(t, peerB, newUFrame(uStartDtActive))
	readFrame(t, peerB)

	time.Sleep(50 * time.Millisecond)
	if !sessA.IsConnected() || !sessB.IsConnected() {
		t.Fatal("without a redundancy group, both connections should remain independently active")
	}
}

// TestServer_HandleSessionActivated_HandsOffQueuedMessages verifies that
// deactivating a superseded connection doesn't lose whatever it hadn't sent
// yet: handleSessionActivated must drain the loser's sendQueue into the
// winner's before asking the loser to deactivate. Exercised directly (not
// through real run() goroutines over net.Pipe) so the outcome doesn't depend
// on winning a race against the loser's own send loop draining its queue
// first.
func TestServer_HandleSessionActivated_HandsOffQueuedMessages(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.SetServerMode(ModeSingleRedundancyGroup)

	groupKey := singleRedundancyGroupKey{}

	sessA := &SrvSession{sendQueue: newMessageQueue(10), redundancyGroupKey: groupKey, deactivateCh: make(chan struct{}, 1)}
	atomic.StoreUint32(&sessA.isActive, active)

	sessB := &SrvSession{sendQueue: newMessageQueue(10), redundancyGroupKey: groupKey}
	atomic.StoreUint32(&sessB.isActive, active)

	srv.mux.Lock()
	srv.sessions[sessA] = struct{}{}
	srv.sessions[sessB] = struct{}{}
	srv.mux.Unlock()

	// Record A as the group's active connection first (as its own real
	// activation would have done), then queue a message for it, before B
	// supersedes it.
	srv.handleSessionActivated(sessA)
	sessA.sendQueue.Push([]byte("queued-for-A"))

	srv.handleSessionActivated(sessB)

	got, ok := sessB.sendQueue.Pop()
	if !ok || string(got) != "queued-for-A" {
		t.Fatalf("sessB.sendQueue.Pop() = %q, %v, want the handed-off message", got, ok)
	}
	if n := sessA.sendQueue.Len(); n != 0 {
		t.Fatalf("sessA.sendQueue.Len() = %d, want 0 after hand-off", n)
	}

	select {
	case <-sessA.deactivateCh:
	default:
		t.Fatal("sessA.deactivateCh should have received a signal: forceDeactivate was expected")
	}
}

// TestServer_HandleSessionActivated_ConcurrentActivation_ExactlyOneSuperseded
// guards against a race where two sessions in the same redundancy group
// activate at nearly the same instant (e.g. two masters both issuing
// STARTDT within microseconds of each other during a failover). Naively
// re-deriving "who else is active" from each session's own IsActive() flag
// lets both concurrent calls see the other as the one to supersede, so both
// end up deactivated and the group ends up with no active connection at
// all. handleSessionActivated must instead guarantee exactly one of the two
// is ever told to deactivate, regardless of scheduling order.
func TestServer_HandleSessionActivated_ConcurrentActivation_ExactlyOneSuperseded(t *testing.T) {
	for i := 0; i < 50; i++ {
		srv := NewServer(stubServerHandler{})
		srv.SetServerMode(ModeSingleRedundancyGroup)
		groupKey := singleRedundancyGroupKey{}

		sessA := &SrvSession{sendQueue: newMessageQueue(10), redundancyGroupKey: groupKey, deactivateCh: make(chan struct{}, 1)}
		sessB := &SrvSession{sendQueue: newMessageQueue(10), redundancyGroupKey: groupKey, deactivateCh: make(chan struct{}, 1)}

		srv.mux.Lock()
		srv.sessions[sessA] = struct{}{}
		srv.sessions[sessB] = struct{}{}
		srv.mux.Unlock()

		// Simulate both sessions' run() goroutines completing their atomic
		// isActive swap (as happens on receiving STARTDT_ACT) before either
		// one's onActivate/handleSessionActivated call runs.
		atomic.StoreUint32(&sessA.isActive, active)
		atomic.StoreUint32(&sessB.isActive, active)

		var wg sync.WaitGroup
		wg.Add(2)
		go func() { defer wg.Done(); srv.handleSessionActivated(sessA) }()
		go func() { defer wg.Done(); srv.handleSessionActivated(sessB) }()
		wg.Wait()

		aSignaled := len(sessA.deactivateCh) == 1
		bSignaled := len(sessB.deactivateCh) == 1
		if aSignaled == bSignaled { // both true or both false: bug
			t.Fatalf("iteration %d: A deactivate-signaled=%v B deactivate-signaled=%v, want exactly one", i, aSignaled, bSignaled)
		}
	}
}
