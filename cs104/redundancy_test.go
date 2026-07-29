package cs104

import (
	"context"
	"io"
	"net"
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
	// now be forced closed.
	buf := make([]byte, 1)
	if _, err := peerA.Read(buf); err == nil {
		t.Fatal("expected connection A to be closed after B activated in the same redundancy group")
	}
	if sessA.IsConnected() {
		t.Fatal("session A should be disconnected")
	}
	if !sessB.IsConnected() {
		t.Fatal("session B should remain connected")
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
