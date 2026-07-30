// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"testing"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/clog"
)

// TestConnection_T2_AcknowledgesBeforeW covers t₂: a peer that stops
// sending before w I-frames have accumulated must still get its data
// acknowledged, within t₂ of the oldest unacknowledged frame.
//
// This also guards the shape of the check. It used to be OR'd with "100ms
// since the last frame arrived", which fired first in every realistic case
// and made the configured t₂ unreachable -- a documented knob that did
// nothing.
func TestConnection_T2_AcknowledgesBeforeW(t *testing.T) {
	cfg := fastTestConfig() // w=3
	// t₂ deliberately well above timeoutResolution, so "acknowledged at
	// t₂" and "acknowledged as soon as the link went quiet" are
	// distinguishable outcomes. t₃ stays clear of both, so an idle-timer
	// TESTFR can't be mistaken for the acknowledgement under test.
	cfg.RecvUnAckTimeout2 = 500 * time.Millisecond
	cfg.IdleTimeout3 = 3 * time.Second
	_, peer := newTestSrvSession(t, stubServerHandler{}, cfg)
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	writeFrame(t, peer, newUFrame(uStartDtActive))
	if head, _ := readFrame(t, peer); head.(uAPCI).function != uStartDtConfirm {
		t.Fatal("expected StartDtConfirm")
	}

	// One I-frame is short of w=3, so only t₂ can trigger the
	// acknowledgement. It carries monitoring-direction data, which the stub
	// handler accepts silently -- an ASDU that drew a reply would have its
	// acknowledgement piggybacked on that outbound I-frame instead, and
	// never exercise t₂ at all.
	iframe, err := newIFrame(0, 0, buildTestMonitorASDU(t))
	if err != nil {
		t.Fatalf("newIFrame failed: %v", err)
	}
	writeFrame(t, peer, iframe)

	// Nothing may be acknowledged yet: the link has been quiet since the
	// frame arrived, but t₂ has not elapsed. This is the regression guard.
	// The check used to be OR'd with "timeoutResolution since the last
	// frame", which fired here and made the configured t₂ unreachable.
	_ = peer.SetReadDeadline(time.Now().Add(timeoutResolution * 3))
	if _, err := peer.Read(make([]byte, 1)); err == nil {
		t.Fatal("acknowledged before t₂ elapsed: t₂ is not what governs the acknowledgement")
	}

	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	head, _ := readFrame(t, peer)
	s, ok := head.(sAPCI)
	if !ok {
		t.Fatalf("got %#v, want an S-frame acknowledgement driven by t₂", head)
	}
	if s.rcvSN != 1 {
		t.Fatalf("S-frame acknowledges %d, want 1", s.rcvSN)
	}
}

// buildTestMonitorASDU encodes an M_SP_NA_1 single point in the monitoring
// direction: an ASDU a server accepts without generating any reply.
func buildTestMonitorASDU(t *testing.T) []byte {
	t.Helper()

	u := asdu.NewASDU(asdu.ParamsWide, asdu.Identifier{
		Type:       asdu.M_SP_NA_1,
		Variable:   asdu.VariableStruct{IsSequence: false, Number: 1},
		Coa:        asdu.CauseOfTransmission{Cause: asdu.Spontaneous},
		CommonAddr: asdu.GlobalCommonAddr,
	})
	if err := u.AppendInfoObjAddr(1); err != nil {
		t.Fatalf("AppendInfoObjAddr: %v", err)
	}
	u.AppendBytes(byte(asdu.QDSGood))

	data, err := u.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return data
}

// TestConnection_SendFrame_DoesNotBlockWhenPeerStalls covers the send path:
// run() emits frames into sendRaw, and a peer that stops reading the socket
// must not be able to wedge the state machine. If the send were blocking,
// run() would stop servicing its own t₁ timer and could never tear the
// failed connection down.
func TestConnection_SendFrame_DoesNotBlockWhenPeerStalls(t *testing.T) {
	sf := &connection{
		sendRaw: make(chan []byte, 2),
		Clog:    clog.NewLogger("test cs104 => "),
	}

	// Fill sendRaw, then push well past its capacity.
	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			sf.sendUFrame(uTestFrActive)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("sendFrame blocked once sendRaw filled: a stalled peer can wedge the state machine")
	}

	if got := len(sf.sendRaw); got != 2 {
		t.Fatalf("sendRaw holds %d frames, want 2 (its capacity): excess must be dropped, not buffered", got)
	}
}
