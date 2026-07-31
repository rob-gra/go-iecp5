// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// stubClientHandler is a minimal ClientHandlerInterface implementation for
// tests.
type stubClientHandler struct{}

func (stubClientHandler) InterrogationHandler(asdu.Connect, *asdu.ASDU) error        { return nil }
func (stubClientHandler) CounterInterrogationHandler(asdu.Connect, *asdu.ASDU) error { return nil }
func (stubClientHandler) ReadHandler(asdu.Connect, *asdu.ASDU) error                 { return nil }
func (stubClientHandler) TestCommandHandler(asdu.Connect, *asdu.ASDU) error          { return nil }
func (stubClientHandler) ClockSyncHandler(asdu.Connect, *asdu.ASDU) error            { return nil }
func (stubClientHandler) ResetProcessHandler(asdu.Connect, *asdu.ASDU) error         { return nil }
func (stubClientHandler) DelayAcquisitionHandler(asdu.Connect, *asdu.ASDU) error     { return nil }
func (stubClientHandler) ASDUHandler(asdu.Connect, *asdu.ASDU) error                 { return nil }

// newTestClient wires a Client to one end of an in-memory net.Pipe and
// starts its state machine, returning the client and the "remote station"
// end of the pipe for the test to drive. It is the master-side counterpart
// of newTestSrvSession.
func newTestClient(t *testing.T, cfg Config) (*Client, net.Conn) {
	t.Helper()

	clientConn, peerConn := net.Pipe()
	o := NewOption()
	// Assigned rather than passed through SetConfig, which rejects
	// sub-second timeouts (Config.Valid enforces the IEC minimums) and
	// would silently substitute the defaults. newTestSrvSession bypasses
	// the same validation for the same reason.
	o.config = cfg

	c := NewClient(stubClientHandler{}, o)

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = peerConn.Close()
	})

	go c.run(ctx, clientConn)

	// run installs the per-connection context before it starts exchanging
	// frames; wait for it so SendStartDt and friends aren't racing setup.
	waitFor(t, time.Second, c.IsConnected)

	return c, peerConn
}

// waitFor polls cond until it holds or the timeout expires.
func waitFor(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met within timeout")
}

func TestClient_StartDtConfirmActivates(t *testing.T) {
	c, peer := newTestClient(t, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	if c.IsActive() {
		t.Fatal("client should start inactive: data transfer is not enabled until STARTDT is confirmed")
	}

	c.SendStartDt()
	head, _ := readFrame(t, peer)
	if u, ok := head.(UAPCI); !ok || u.Function != UStartDtActive {
		t.Fatalf("got %#v, want StartDtActive", head)
	}

	writeFrame(t, peer, newUFrame(UStartDtConfirm))
	waitFor(t, time.Second, c.IsActive)
}

func TestClient_StopDtConfirmDeactivates(t *testing.T) {
	c, peer := newTestClient(t, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	c.SendStartDt()
	readFrame(t, peer)
	writeFrame(t, peer, newUFrame(UStartDtConfirm))
	waitFor(t, time.Second, c.IsActive)

	c.SendStopDt()
	head, _ := readFrame(t, peer)
	if u, ok := head.(UAPCI); !ok || u.Function != UStopDtActive {
		t.Fatalf("got %#v, want StopDtActive", head)
	}

	writeFrame(t, peer, newUFrame(UStopDtConfirm))
	waitFor(t, time.Second, func() bool { return !c.IsActive() })
}

// TestClient_StartDtUnconfirmedDisconnects covers the t₁ timer that only the
// controlling station runs: it alone issues STARTDT, so it alone has to
// notice the confirmation never arriving.
func TestClient_StartDtUnconfirmedDisconnects(t *testing.T) {
	c, peer := newTestClient(t, fastTestConfig()) // t₁ = 150ms
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	c.SendStartDt()
	readFrame(t, peer)
	// Deliberately never confirm.

	waitFor(t, time.Second, func() bool { return !c.IsConnected() })
}

func TestClient_RespondsToTestFrActive(t *testing.T) {
	c, peer := newTestClient(t, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	_ = c

	writeFrame(t, peer, newUFrame(UTestFrActive))
	head, _ := readFrame(t, peer)
	if u, ok := head.(UAPCI); !ok || u.Function != UTestFrConfirm {
		t.Fatalf("got %#v, want TestFrConfirm", head)
	}
}

// TestClient_SendRequiresConnectedAndActive pins which error a caller gets,
// and in which order: a disconnected client is also inactive, and the more
// useful of the two answers is that the connection is gone.
func TestClient_SendRequiresConnectedAndActive(t *testing.T) {
	o := NewOption()
	notConnected := NewClient(stubClientHandler{}, o)
	u := asdu.NewASDU(asdu.ParamsWide, asdu.Identifier{
		Type:       asdu.C_IC_NA_1,
		Variable:   asdu.VariableStruct{IsSequence: false, Number: 1},
		Coa:        asdu.CauseOfTransmission{Cause: asdu.Activation},
		CommonAddr: asdu.GlobalCommonAddr,
	})
	if err := notConnected.Send(u); err != ErrUseClosedConnection {
		t.Fatalf("Send() on a never-connected client = %v, want ErrUseClosedConnection", err)
	}

	c, peer := newTestClient(t, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	// Connected but STARTDT not yet confirmed.
	if err := c.Send(u); err != ErrNotActive {
		t.Fatalf("Send() before STARTDT = %v, want ErrNotActive", err)
	}

	c.SendStartDt()
	readFrame(t, peer)
	writeFrame(t, peer, newUFrame(UStartDtConfirm))
	waitFor(t, time.Second, c.IsActive)

	if err := c.Send(u); err != nil {
		t.Fatalf("Send() once active = %v, want nil", err)
	}
	head, _ := readFrame(t, peer)
	if _, ok := head.(IAPCI); !ok {
		t.Fatalf("got %#v, want the queued ASDU as an I-frame", head)
	}
}

// TestClient_IFrameRoundTrip walks a full exchange: the client sends an
// ASDU, the station acknowledges it and sends one back.
func TestClient_IFrameRoundTrip(t *testing.T) {
	c, peer := newTestClient(t, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	c.SendStartDt()
	readFrame(t, peer)
	writeFrame(t, peer, newUFrame(UStartDtConfirm))
	waitFor(t, time.Second, c.IsActive)

	if err := c.InterrogationCmd(asdu.CauseOfTransmission{Cause: asdu.Activation},
		asdu.GlobalCommonAddr, asdu.QOIStation); err != nil {
		t.Fatalf("InterrogationCmd() = %v", err)
	}

	head, _ := readFrame(t, peer)
	i, ok := head.(IAPCI)
	if !ok {
		t.Fatalf("got %#v, want an I-frame carrying the interrogation", head)
	}
	if i.SendSN != 0 {
		t.Fatalf("sendSN = %d, want 0 for the first I-frame", i.SendSN)
	}

	// Acknowledge it, then send one the other way.
	writeFrame(t, peer, newSFrame(1))
	iframe, err := newIFrame(0, 1, buildTestMonitorASDU(t))
	if err != nil {
		t.Fatalf("newIFrame failed: %v", err)
	}
	writeFrame(t, peer, iframe)

	// The client must stay up and eventually acknowledge what it received.
	head, _ = readFrame(t, peer)
	if s, ok := head.(SAPCI); !ok || s.RcvSN != 1 {
		t.Fatalf("got %#v, want an S-frame acknowledging 1", head)
	}
	if !c.IsConnected() {
		t.Fatal("client should still be connected after a clean round trip")
	}
}

// TestClient_SendStartDtBeforeRun must not panic or block. STARTDT is
// issued from the application's goroutine, which may call it before the
// state machine has installed its context -- so that path cannot depend on
// the context existing.
func TestClient_SendStartDtBeforeRun(t *testing.T) {
	c := NewClient(stubClientHandler{}, NewOption())

	done := make(chan struct{})
	go func() {
		defer close(done)
		c.SendStartDt()
		c.SendStopDt()
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("SendStartDt/SendStopDt blocked before the connection was running")
	}
}
