// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"errors"
	"net"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// A STARTDT awaiting confirmation belongs to the connection that sent it.
// Left set when that connection ends, roleTimedOut measures it against the
// next one -- which never sent an activation, so there is nothing to time
// out. An application that issues STARTDT once rather than on every
// reconnect then loses each fresh connection to its predecessor's expired
// timer, reconnecting in a loop.
func TestClient_StaleActivationTimerDoesNotOutliveItsConnection(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var accepted atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			// Answer TESTFR but never STARTDT. Keeping the link alive is what
			// isolates the timer under test: a peer that answers nothing
			// loses every connection to an unconfirmed TESTFR instead, and
			// the reconnects would look identical either way.
			go func(c net.Conn) {
				defer c.Close()
				for {
					apdu, err := ReadAPDU(c)
					if err != nil {
						return
					}
					if u, ok := mustParse(apdu).(UAPCI); ok && u.Function == UTestFrActive {
						if _, err := c.Write(newUFrame(UTestFrConfirm)); err != nil {
							return
						}
					}
				}
			}(conn)
		}
	}()

	cfg := fastTestConfig() // t₁ = 150ms
	opt := NewOption().SetLogger(discardLogger())
	opt.config = cfg
	if err := opt.AddRemoteServer(ln.Addr().String()); err != nil {
		t.Fatal(err)
	}
	c := NewClient(&stubClientHandler{}, opt)

	// STARTDT on the first connection only, modelling an application that
	// starts data transfer once rather than on every reconnect.
	var connects atomic.Int32
	c.SetOnConnectHandler(func(cl *Client) {
		if connects.Add(1) == 1 {
			cl.SendStartDt()
		}
	})

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	defer c.Close()

	// The first connection is torn down by its own t₁ and one reconnect
	// follows; that second connection sends no activation and has its TESTFR
	// answered, so nothing should end it. Any further reconnect means the
	// first connection's timestamp is still firing. The client waits 0.5-1s
	// between attempts, so 3s leaves room for several.
	time.Sleep(3 * time.Second)

	if got := accepted.Load(); got > 2 {
		t.Fatalf("opened %d connections in 3s: the previous connection's activation timer is tearing down its successors", got)
	}
}

// Start reports a duplicate rather than silently doing nothing. The
// transition has to be claimed by Start itself for this to work: done inside
// the goroutine, Start has already returned nil by the time the duplicate is
// detected.
func TestClient_StartTwiceReportsAlreadyStarted(t *testing.T) {
	opt := NewOption().SetAutoReconnect(false).SetLogger(discardLogger())
	if err := opt.AddRemoteServer("127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	c := NewClient(&stubClientHandler{}, opt)
	defer c.Close()

	if err := c.Start(); err != nil {
		t.Fatalf("first Start() = %v, want nil", err)
	}
	if err := c.Start(); !errors.Is(err, ErrAlreadyStarted) {
		t.Fatalf("second Start() = %v, want ErrAlreadyStarted", err)
	}
}

// Close must work for a caller that calls it the instant Start returns,
// before the connection goroutine has run at all -- which is only true
// because Start installs closeCancel under the same lock as the transition.
func TestClient_CloseImmediatelyAfterStart(t *testing.T) {
	opt := NewOption().SetLogger(discardLogger())
	if err := opt.AddRemoteServer("127.0.0.1:1"); err != nil {
		t.Fatal(err)
	}
	c := NewClient(&stubClientHandler{}, opt)

	if err := c.Start(); err != nil {
		t.Fatal(err)
	}
	if err := c.Close(); err != nil {
		t.Fatalf("Close() = %v", err)
	}

	// The running loop must actually stop, which it only does if it saw the
	// cancellation. IsClosed is spelled as the status returning to initial.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if c.connectStatus() == initial {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("client still running after Close() called immediately after Start()")
}

// panicClientHandler blows up in the catch-all handler.
type panicClientHandler struct{ stubClientHandler }

func (panicClientHandler) ASDUHandler(asdu.Connect, *asdu.ASDU) error {
	panic("handler exploded")
}

// A handler that panics is recovered so it cannot take the connection down,
// but the recovery must not also erase the failure: with an unnamed return
// the function yields nil, making a handler that blew up indistinguishable
// from one that succeeded.
func TestClient_HandlerPanicSurfacesAsError(t *testing.T) {
	c, _ := newTestClient(t, fastTestConfig())
	c.handler = panicClientHandler{}

	err := c.dispatchASDU(buildMonitorASDU(t))
	if err == nil {
		t.Fatal("dispatchASDU returned nil after the handler panicked: the failure is indistinguishable from success")
	}
	if !strings.Contains(err.Error(), "handler exploded") {
		t.Fatalf("error %q does not carry what the handler panicked with", err)
	}
}

// The same guarantee on the controlled-station side.
type panicSrvHandler struct{ stubServerHandler }

func (panicSrvHandler) ASDUHandler(asdu.Connect, *asdu.ASDU) error {
	panic("handler exploded")
}

func TestSrvSession_HandlerPanicSurfacesAsError(t *testing.T) {
	sess, _ := newTestSrvSession(t, panicSrvHandler{}, fastTestConfig())

	err := sess.dispatchASDU(buildMonitorASDU(t))
	if err == nil {
		t.Fatal("dispatchASDU returned nil after the handler panicked: the failure is indistinguishable from success")
	}
	if !strings.Contains(err.Error(), "handler exploded") {
		t.Fatalf("error %q does not carry what the handler panicked with", err)
	}
}

// buildMonitorASDU returns a decoded monitoring-direction ASDU, which
// dispatch routes to the catch-all ASDUHandler.
func buildMonitorASDU(t *testing.T) *asdu.ASDU {
	t.Helper()

	a := asdu.NewEmptyASDU(asdu.ParamsWide)
	if err := a.UnmarshalBinary(buildTestMonitorASDU(t)); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	return a
}
