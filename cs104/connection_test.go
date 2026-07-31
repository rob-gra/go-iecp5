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

	writeFrame(t, peer, newUFrame(UStartDtActive))
	if head, _ := readFrame(t, peer); head.(UAPCI).Function != UStartDtConfirm {
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
	s, ok := head.(SAPCI)
	if !ok {
		t.Fatalf("got %#v, want an S-frame acknowledgement driven by t₂", head)
	}
	if s.RcvSN != 1 {
		t.Fatalf("S-frame acknowledges %d, want 1", s.RcvSN)
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

// TestConnection_TrySendFrame_DoesNotBlockWhenBufferFull covers the path used
// by callers outside run's goroutine (the application issuing
// STARTDT/STOPDT): it must never block them, and must not touch the
// context run owns.
func TestConnection_TrySendFrame_DoesNotBlockWhenBufferFull(t *testing.T) {
	sf := &connection{
		sendRaw: make(chan []byte, 2),
		log:     discardLogger(),
	}

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			sf.trySendUFrame(UStartDtActive)
		}
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("trySendFrame blocked once sendRaw filled")
	}

	if got := len(sf.sendRaw); got != 2 {
		t.Fatalf("sendRaw holds %d frames, want 2 (its capacity): excess must be dropped, not buffered", got)
	}
}

// TestConnection_SendFrame_UnblocksOnTeardown pins the property that keeps
// a blocking send safe. sendFrame waits for room rather than dropping --
// an I-frame has already taken a sequence number by the time it gets
// there, and discarding it would leave a hole the peer answers by dropping
// the connection. Waiting is only acceptable because the wait ends the
// moment the connection is torn down; otherwise run() could sit in a send
// forever and never service its own t1 timer.
func TestConnection_SendFrame_UnblocksOnTeardown(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	sf := &connection{
		sendRaw: make(chan []byte, 1),
		ctx:     ctx,
		log:     discardLogger(),
	}
	sf.sendRaw <- []byte{0} // buffer now full; nothing drains it

	blocked := make(chan struct{})
	go func() {
		defer close(blocked)
		sf.sendFrame([]byte{1})
	}()

	select {
	case <-blocked:
		t.Fatal("sendFrame returned while the buffer was full and the connection was live: the frame was dropped, which loses its sequence number")
	case <-time.After(50 * time.Millisecond):
	}

	cancel()
	select {
	case <-blocked:
	case <-time.After(2 * time.Second):
		t.Fatal("sendFrame stayed blocked after teardown: run() can be wedged by a peer that stops reading")
	}
}

// TestConnection_SendLoop_WriteDeadlineEndsStalledWrite covers where the
// bound on a stalled peer actually lives. sendLoop gives each write t1 --
// the protocol's own limit on an unresponsive peer -- so a peer that stops
// reading the socket surfaces as a write error that ends the connection,
// rather than as unbounded backpressure into the state machine.
func TestConnection_SendLoop_WriteDeadlineEndsStalledWrite(t *testing.T) {
	cfg := fastTestConfig() // t1 = 150ms
	conn, peer := net.Pipe()
	t.Cleanup(func() { _ = peer.Close() })

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	sf := &connection{
		config:  &cfg,
		sendRaw: make(chan []byte, 4),
		ctx:     ctx,
		cancel:  cancel,
		log:     discardLogger(),
	}

	// net.Pipe is unbuffered, so this write blocks until the peer reads --
	// and the peer never does.
	sf.sendRaw <- newUFrame(UTestFrActive)

	sf.wg.Add(1)
	stopped := make(chan struct{})
	go func() {
		defer close(stopped)
		sf.sendLoop(conn)
	}()

	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("sendLoop never gave up on a peer that stopped reading: the write has no deadline")
	}

	// Giving up must also tear the connection down, not just exit the loop.
	select {
	case <-ctx.Done():
	default:
		t.Fatal("sendLoop exited without cancelling the connection")
	}
}

// TestConnection_OutboundFrameDefersT3 covers what t₃ measures. The
// standard treats idleness as no traffic in *either* direction, so a frame
// we put on the link is activity and must push the idle timer back. The
// timer used to be reset only by inbound frames and by outbound I-frames,
// so a station that was acknowledging steadily but sending no data of its
// own still asked "are you alive?" on a link that plainly was.
func TestConnection_OutboundFrameDefersT3(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sf := &connection{
		sendRaw: make(chan []byte, 4),
		ctx:     ctx,
		log:     discardLogger(),
	}

	sf.idleSince = time.Now().Add(-time.Hour) // long idle
	sf.sendSFrame(1)                          // an acknowledgement, not an I-frame

	if time.Since(sf.idleSince) > time.Second {
		t.Fatal("an outbound S-frame did not push back the t₃ idle timer")
	}
}

// TestConnection_CloseDoesNotFreeBlockedRecvLoop is why run's teardown
// cancels outright instead of relying on closing the connection to get
// there by way of recvLoop failing its read.
//
// That reasoning only holds while recvLoop is actually reading. Parked
// handing a frame to a full rcvRaw it never touches the socket, so closing
// the connection tells it nothing; an idle sendLoop is no different. With
// neither reaching cancel, the wg.Wait() in run's defer would have nothing
// left to wake it.
//
// This pins the mechanism rather than the whole race: driving run() into
// exiting at the exact moment recvLoop is parked is not something a test
// can arrange reliably, so what is checked here is the property the
// teardown path depends on.
func TestConnection_CloseDoesNotFreeBlockedRecvLoop(t *testing.T) {
	conn, peer := net.Pipe()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	sf := &connection{
		rcvRaw: make(chan []byte, 1), // fills immediately, nothing drains it
		ctx:    ctx,
		cancel: cancel,
		log:    discardLogger(),
	}
	sf.wg.Add(1)
	stopped := make(chan struct{})
	go func() { defer close(stopped); sf.recvLoop(conn) }()

	go func() {
		for i := 0; i < 2; i++ { // first fills the buffer, second parks recvLoop
			if _, err := peer.Write(newUFrame(UTestFrActive)); err != nil {
				return
			}
		}
	}()
	waitFor(t, time.Second, func() bool { return len(sf.rcvRaw) == 1 })

	_ = conn.Close()
	select {
	case <-stopped:
		t.Fatal("recvLoop exited on conn.Close() alone: this test no longer describes the code, re-check whether teardown still needs its own cancel")
	case <-time.After(200 * time.Millisecond):
	}

	cancel() // what run's defer now does
	select {
	case <-stopped:
	case <-time.After(2 * time.Second):
		t.Fatal("recvLoop stayed parked even after cancel")
	}
}

// TestConnection_SendRejectsOverlongASDU covers the one path that used to
// lose a message without a word. ASDU.MarshalBinary does not bound the
// information object, so an application can build one too long to frame;
// newIFrame then refused it inside the state machine and sendIFrame
// returned, dropping it silently. The caller is told now, while it still
// has the ASDU and can do something about it.
func TestConnection_SendRejectsOverlongASDU(t *testing.T) {
	c, peer := newTestClient(t, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	c.SendStartDt()
	readFrame(t, peer)
	writeFrame(t, peer, newUFrame(UStartDtConfirm))
	waitFor(t, time.Second, c.IsActive)

	u := asdu.NewASDU(asdu.ParamsWide, asdu.Identifier{
		Type:       asdu.M_SP_NA_1,
		Variable:   asdu.VariableStruct{IsSequence: false, Number: 1},
		Coa:        asdu.CauseOfTransmission{Cause: asdu.Spontaneous},
		CommonAddr: asdu.GlobalCommonAddr,
	})
	u.AppendBytes(make([]byte, asdu.ASDUSizeMax+1)...)

	if err := c.Send(u); err != asdu.ErrLengthOutOfRange {
		t.Fatalf("Send() of an over-long ASDU = %v, want asdu.ErrLengthOutOfRange", err)
	}

	// Rejecting it must not have disturbed the connection or the sequence.
	if !c.IsConnected() || !c.IsActive() {
		t.Fatal("rejecting an over-long ASDU should leave the connection untouched")
	}
}

// TestConnection_KWindowStopsAtK covers the k send window: with k
// unacknowledged I-frames outstanding, transmission must stop until the peer
// acknowledges (IEC 60870-5-104, subclass 5.5).
//
// The bound used to be "<= k" against the count of frames already
// outstanding, which permitted one more send on top of it and left k+1
// unacknowledged -- one past what the standard allows, and enough for a peer
// enforcing k to tear the link down.
func TestConnection_KWindowStopsAtK(t *testing.T) {
	cfg := fastTestConfig()
	cfg.SendUnAckLimitK = 3
	// Nothing may time out mid-test: the subject here is the window, not the
	// timers, and a t₁ teardown would end the connection before the count is
	// meaningful.
	cfg.SendUnAckTimeout1 = 10 * time.Second
	cfg.RecvUnAckTimeout2 = 5 * time.Second
	cfg.IdleTimeout3 = 20 * time.Second

	sess, peer := newTestSrvSession(t, stubServerHandler{}, cfg)

	writeFrame(t, peer, newUFrame(UStartDtActive))
	if head, _ := readFrame(t, peer); head.(UAPCI).Function != UStartDtConfirm {
		t.Fatal("expected StartDtConfirm")
	}

	// Queue well past the window, and never acknowledge any of it.
	payload := buildTestMonitorASDU(t)
	for i := 0; i < int(cfg.SendUnAckLimitK)*3; i++ {
		sess.enqueue(payload)
	}

	// Count the I-frames that arrive before the session goes quiet.
	iFrames := 0
	for {
		_ = peer.SetReadDeadline(time.Now().Add(timeoutResolution * 3))
		head := make([]byte, 2)
		if _, err := peer.Read(head); err != nil {
			break // gone quiet: the window is closed
		}
		body := make([]byte, head[1])
		if _, err := peer.Read(body); err != nil {
			break
		}
		if _, ok := mustParse(append(head, body...)).(IAPCI); ok {
			iFrames++
		}
	}

	if iFrames != int(cfg.SendUnAckLimitK) {
		t.Fatalf("sent %d unacknowledged I-frames, want exactly k=%d",
			iFrames, cfg.SendUnAckLimitK)
	}
}

// mustParse is parse for tests that have already read a whole frame.
func mustParse(frame []byte) any {
	apci, _ := parse(frame)
	return apci
}
