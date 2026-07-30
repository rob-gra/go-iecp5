// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"net"
	"sync"
	"testing"
)

// trackingConn wraps stubConn to record whether Close was called, so tests
// can verify a rejected connection is actually closed rather than merely not
// registered as a session.
type trackingConn struct {
	stubConn
	mu     sync.Mutex
	closed bool
}

func (c *trackingConn) Close() error {
	c.mu.Lock()
	c.closed = true
	c.mu.Unlock()
	return c.stubConn.Close()
}

func (c *trackingConn) wasClosed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.closed
}

func TestServer_acceptSession_NoLimits_Accepts(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	conn := &trackingConn{stubConn: stubConn{remote: stubAddr("192.168.1.10:2404")}}

	sess := srv.acceptSession(conn)
	if sess == nil {
		t.Fatal("acceptSession() = nil, want a session with no limits configured")
	}
	if conn.wasClosed() {
		t.Fatal("accepted connection should not be closed")
	}
	srv.mux.Lock()
	_, registered := srv.sessions[sess]
	srv.mux.Unlock()
	if !registered {
		t.Fatal("accepted session should be registered in srv.sessions")
	}
}

func TestServer_SetConnectionRequestHandler_Rejects(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.SetConnectionRequestHandler(func(remote net.Addr) bool {
		return remote.String() == "192.168.1.10:2404"
	})

	allowed := &trackingConn{stubConn: stubConn{remote: stubAddr("192.168.1.10:2404")}}
	if sess := srv.acceptSession(allowed); sess == nil {
		t.Fatal("acceptSession() = nil, want a session for an address the handler allows")
	}

	rejected := &trackingConn{stubConn: stubConn{remote: stubAddr("10.0.0.1:2404")}}
	if sess := srv.acceptSession(rejected); sess != nil {
		t.Fatal("acceptSession() should reject an address the handler declines")
	}
	if !rejected.wasClosed() {
		t.Fatal("rejected connection should be closed")
	}
}

func TestServer_AllowClientIPs_RejectsUnlistedIP(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.AllowClientIPs("192.168.1.10")

	conn := &trackingConn{stubConn: stubConn{remote: stubAddr("10.0.0.1:2404")}}
	sess := srv.acceptSession(conn)
	if sess != nil {
		t.Fatal("acceptSession() should reject an IP not in the allow-list")
	}
	if !conn.wasClosed() {
		t.Fatal("rejected connection should be closed")
	}
	srv.mux.Lock()
	n := len(srv.sessions)
	srv.mux.Unlock()
	if n != 0 {
		t.Fatalf("srv.sessions has %d entries, want 0: rejected connection must not be registered", n)
	}
}

func TestServer_AllowClientIPs_AcceptsListedIP(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.AllowClientIPs("192.168.1.10", "192.168.1.11")

	conn := &trackingConn{stubConn: stubConn{remote: stubAddr("192.168.1.10:2404")}}
	sess := srv.acceptSession(conn)
	if sess == nil {
		t.Fatal("acceptSession() = nil, want a session for an allow-listed IP")
	}
	if conn.wasClosed() {
		t.Fatal("accepted connection should not be closed")
	}
}

func TestServer_SetMaxConnections_RejectsBeyondCap(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.SetMaxConnections(2)

	var sessions []*SrvSession
	var conns []*trackingConn
	for i := 0; i < 2; i++ {
		conn := &trackingConn{stubConn: stubConn{remote: stubAddr("192.168.1.10:2404")}}
		conns = append(conns, conn)
		sess := srv.acceptSession(conn)
		if sess == nil {
			t.Fatalf("connection %d: acceptSession() = nil, want a session (under the cap)", i)
		}
		sessions = append(sessions, sess)
	}

	// A third connection should be rejected: the cap is already reached.
	third := &trackingConn{stubConn: stubConn{remote: stubAddr("192.168.1.10:2404")}}
	if sess := srv.acceptSession(third); sess != nil {
		t.Fatal("acceptSession() should reject a connection beyond SetMaxConnections' cap")
	}
	if !third.wasClosed() {
		t.Fatal("rejected connection should be closed")
	}

	// Freeing a slot (simulating a session ending, as ListenAndServer's
	// accept loop does on session exit) should let a new connection in.
	srv.mux.Lock()
	delete(srv.sessions, sessions[0])
	srv.mux.Unlock()

	fourth := &trackingConn{stubConn: stubConn{remote: stubAddr("192.168.1.10:2404")}}
	if sess := srv.acceptSession(fourth); sess == nil {
		t.Fatal("acceptSession() should accept once a slot has freed up")
	}
}
