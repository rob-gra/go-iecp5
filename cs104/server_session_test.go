package cs104

import (
	"context"
	"io"
	"log/slog"
	"net"
	"testing"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// stubServerHandler is a minimal ServerHandlerInterface implementation for tests.
type stubServerHandler struct{}

func (stubServerHandler) InterrogationHandler(asdu.Connect, *asdu.ASDU, asdu.QualifierOfInterrogation) error {
	return nil
}
func (stubServerHandler) CounterInterrogationHandler(asdu.Connect, *asdu.ASDU, asdu.QualifierCountCall) error {
	return nil
}
func (stubServerHandler) ReadHandler(asdu.Connect, *asdu.ASDU, asdu.InfoObjAddr) error { return nil }
func (stubServerHandler) ClockSyncHandler(asdu.Connect, *asdu.ASDU, time.Time) error   { return nil }
func (stubServerHandler) ResetProcessHandler(asdu.Connect, *asdu.ASDU, asdu.QualifierOfResetProcessCmd) error {
	return nil
}
func (stubServerHandler) DelayAcquisitionHandler(asdu.Connect, *asdu.ASDU, uint16) error { return nil }
func (stubServerHandler) ASDUHandler(asdu.Connect, *asdu.ASDU) error                     { return nil }

// discardLogger returns a logger that emits nothing, so the protocol trace
// doesn't drown the test output. Tests that care about a record assert on it
// via their own handler instead.
func discardLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// fastTestConfig returns a Config with short timeouts so tests don't have to
// wait on the (1s-255s) IEC-mandated minimums enforced by Config.Valid. It
// keeps SendUnAckTimeout1 < IdleTimeout3, mirroring the relative ordering of
// the IEC-recommended defaults (t1=15s < t3=20s): an unconfirmed TestFrActive
// must trip the t1 disconnect before the next t3 idle cycle re-triggers and
// resets the t1 clock.
func fastTestConfig() Config {
	return Config{
		ConnectTimeout0:   time.Second,
		SendUnAckLimitK:   10,
		SendUnAckTimeout1: 150 * time.Millisecond,
		RecvUnAckLimitW:   3,
		RecvUnAckTimeout2: 100 * time.Millisecond,
		IdleTimeout3:      300 * time.Millisecond,
	}
}

// newTestSrvSession wires an SrvSession to one end of an in-memory net.Pipe
// and starts its state machine, returning the session and the "remote peer"
// end of the pipe for the test to drive.
func newTestSrvSession(t *testing.T, handler ServerHandlerInterface, cfg Config) (*SrvSession, net.Conn) {
	t.Helper()

	serverConn, peerConn := net.Pipe()
	sess := &SrvSession{
		connection: connection{
			config:    &cfg,
			params:    asdu.ParamsWide,
			rcvASDU:   make(chan []byte, 1024),
			sendQueue: newMessageQueue(1024),
			rcvRaw:    make(chan []byte, 1024),
			sendRaw:   make(chan []byte, 1024),
			log:       discardLogger(),
		},
		handler: handler,
	}
	sess.role = sess

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(func() {
		cancel()
		_ = peerConn.Close()
	})

	go sess.run(ctx, serverConn)

	return sess, peerConn
}

// readFrame reads one raw APDU off conn and parses it.
func readFrame(t *testing.T, conn net.Conn) (any, []byte) {
	t.Helper()

	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		t.Fatalf("read APDU header: %v", err)
	}
	body := make([]byte, head[1])
	if _, err := io.ReadFull(conn, body); err != nil {
		t.Fatalf("read APDU body: %v", err)
	}
	return parse(append(head, body...))
}

func writeFrame(t *testing.T, conn net.Conn, frame []byte) {
	t.Helper()
	if _, err := conn.Write(frame); err != nil {
		t.Fatalf("write APDU: %v", err)
	}
}

// buildTestCommandASDU encodes a C_TS_NA_1 TestCommand activation, a
// convenient minimal ASDU to round-trip through a session as an I-frame
// payload.
func buildTestCommandASDU(t *testing.T) []byte {
	t.Helper()
	return buildTestCommandASDUWithCA(t, asdu.GlobalCommonAddr)
}

// buildTestCommandASDUWithCA is buildTestCommandASDU, addressed to ca
// instead of always asdu.GlobalCommonAddr.
func buildTestCommandASDUWithCA(t *testing.T, ca asdu.CommonAddr) []byte {
	t.Helper()

	u := asdu.NewASDU(asdu.ParamsWide, asdu.Identifier{
		Type:       asdu.C_TS_NA_1,
		Variable:   asdu.VariableStruct{IsSequence: false, Number: 1},
		Coa:        asdu.CauseOfTransmission{Cause: asdu.Activation},
		CommonAddr: ca,
	})
	if err := u.AppendInfoObjAddr(asdu.InfoObjAddrIrrelevant); err != nil {
		t.Fatalf("AppendInfoObjAddr: %v", err)
	}
	u.AppendBytes(byte(asdu.FBPTestWord&0xff), byte(asdu.FBPTestWord>>8))

	data, err := u.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return data
}

func TestSrvSession_StartStopDt(t *testing.T) {
	_, peer := newTestSrvSession(t, stubServerHandler{}, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	writeFrame(t, peer, newUFrame(UStartDtActive))
	if head, _ := readFrame(t, peer); head.(UAPCI).Function != UStartDtConfirm {
		t.Fatalf("got %#v, want StartDtConfirm", head)
	}

	writeFrame(t, peer, newUFrame(UStopDtActive))
	if head, _ := readFrame(t, peer); head.(UAPCI).Function != UStopDtConfirm {
		t.Fatalf("got %#v, want StopDtConfirm", head)
	}
}

func TestSrvSession_IFrameRoundTrip(t *testing.T) {
	sess, peer := newTestSrvSession(t, stubServerHandler{}, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	writeFrame(t, peer, newUFrame(UStartDtActive))
	if head, _ := readFrame(t, peer); head.(UAPCI).Function != UStartDtConfirm {
		t.Fatalf("expected StartDtConfirm")
	}

	iframe, err := newIFrame(0, 0, buildTestCommandASDU(t))
	if err != nil {
		t.Fatalf("newIFrame: %v", err)
	}
	writeFrame(t, peer, iframe)

	head, body := readFrame(t, peer)
	i, ok := head.(IAPCI)
	if !ok {
		t.Fatalf("got %#v, want IAPCI", head)
	}
	if i.SendSN != 0 || i.RcvSN != 1 {
		t.Fatalf("got %+v, want sendSN=0 rcvSN=1", i)
	}

	reply := asdu.NewEmptyASDU(asdu.ParamsWide)
	if err := reply.UnmarshalBinary(body); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if reply.Type != asdu.C_TS_NA_1 || reply.Coa.Cause != asdu.ActivationCon {
		t.Fatalf("got type=%v cause=%v, want C_TS_NA_1/ActivationCon", reply.Type, reply.Coa.Cause)
	}

	if !sess.IsConnected() {
		t.Fatal("session should stay connected after a valid I-frame exchange")
	}
}

// An I-frame before STARTDT closes the connection.
//
// This reverses what the session used to do, which was to discard the frame
// and stay connected. That looked lenient but was the one option that is
// wrong either way: subclass 5.3 permits no user data in the stopped state,
// and discarding without counting leaves the peer's send sequence number one
// ahead of ours, silently, so the divergence surfaces later as a sequence
// error on an unrelated frame. Either close (what the standard's controlled
// station does, and lib60870's) or count and process it -- not neither.
func TestSrvSession_IFrameBeforeStartDt_ClosesConnection(t *testing.T) {
	sess, peer := newTestSrvSession(t, stubServerHandler{}, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	iframe, err := newIFrame(0, 0, buildTestCommandASDU(t))
	if err != nil {
		t.Fatalf("newIFrame: %v", err)
	}
	writeFrame(t, peer, iframe)

	waitFor(t, time.Second, func() bool { return !sess.IsConnected() })
	if sess.IsConnected() {
		t.Fatal("an I-frame in the stopped state is a protocol violation and must close the connection")
	}
}

// STOPDT is confirmed only once the I-frames this end sent have been
// acknowledged. Confirming earlier says the connection is quiesced while data
// is still in flight, and against a peer that stops acknowledging once
// stopped those frames sit unconfirmed until t₁ takes the connection down.
// lib60870 withholds it the same way (M_CON_STATE_UNCONFIRMED_STOPPED).
func TestSrvSession_StopDtConfirmWaitsForOutstandingSends(t *testing.T) {
	cfg := fastTestConfig()
	// Nothing may time out mid-test; the subject is the confirmation order.
	cfg.SendUnAckTimeout1 = 5 * time.Second
	cfg.RecvUnAckTimeout2 = 4 * time.Second
	cfg.IdleTimeout3 = 10 * time.Second

	sess, peer := newTestSrvSession(t, stubServerHandler{}, cfg)
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))

	writeFrame(t, peer, newUFrame(UStartDtActive))
	if head, _ := readFrame(t, peer); head.(UAPCI).Function != UStartDtConfirm {
		t.Fatal("expected StartDtConfirm")
	}

	// Two I-frames out, neither acknowledged.
	payload := buildTestMonitorASDU(t)
	sess.enqueue(payload)
	sess.enqueue(payload)
	for i := 0; i < 2; i++ {
		if head, _ := readFrame(t, peer); !isIFrame(head) {
			t.Fatalf("frame %d: got %#v, want an I-frame", i, head)
		}
	}

	writeFrame(t, peer, newUFrame(UStopDtActive))

	// Data transfer stops immediately, but the confirmation must not.
	waitFor(t, time.Second, func() bool { return !sess.IsActive() })

	_ = peer.SetReadDeadline(time.Now().Add(300 * time.Millisecond))
	if head, _ := tryReadFrame(t, peer); head != nil {
		if u, ok := head.(UAPCI); ok && u.Function == UStopDtConfirm {
			t.Fatal("STOPDT confirmed while this end's I-frames were still unacknowledged")
		}
	}

	// Acknowledge both. Now the confirmation may go out.
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))
	writeFrame(t, peer, newSFrame(2))

	head, _ := readFrame(t, peer)
	u, ok := head.(UAPCI)
	if !ok || u.Function != UStopDtConfirm {
		t.Fatalf("got %#v, want StopDtConfirm once the outstanding frames were acknowledged", head)
	}
}

func isIFrame(head any) bool {
	_, ok := head.(IAPCI)
	return ok
}

// tryReadFrame is readFrame for a caller that expects nothing to arrive: it
// returns a nil APCI on a read deadline instead of failing the test.
func tryReadFrame(t *testing.T, conn net.Conn) (any, []byte) {
	t.Helper()

	head := make([]byte, 2)
	if _, err := io.ReadFull(conn, head); err != nil {
		return nil, nil
	}
	body := make([]byte, head[1])
	if _, err := io.ReadFull(conn, body); err != nil {
		return nil, nil
	}
	return parse(append(head, body...))
}

func TestSrvSession_TestFrKeepAlive(t *testing.T) {
	cfg := fastTestConfig()
	sess, peer := newTestSrvSession(t, stubServerHandler{}, cfg)
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	writeFrame(t, peer, newUFrame(UStartDtActive))
	readFrame(t, peer) // StartDtConfirm

	head, _ := readFrame(t, peer) // idle timeout should trigger TestFrActive
	if u, ok := head.(UAPCI); !ok || u.Function != UTestFrActive {
		t.Fatalf("got %#v, want TestFrActive", head)
	}
	writeFrame(t, peer, newUFrame(UTestFrConfirm))

	time.Sleep(cfg.SendUnAckTimeout1 + 100*time.Millisecond)
	if !sess.IsConnected() {
		t.Fatal("session should remain connected once TestFrActive is confirmed")
	}
}

func TestSrvSession_TestFrTimeout_Disconnects(t *testing.T) {
	cfg := fastTestConfig()
	sess, peer := newTestSrvSession(t, stubServerHandler{}, cfg)
	_ = peer.SetDeadline(time.Now().Add(3 * time.Second))

	writeFrame(t, peer, newUFrame(UStartDtActive))
	readFrame(t, peer) // StartDtConfirm
	readFrame(t, peer) // TestFrActive - deliberately never confirmed

	deadline := time.Now().Add(cfg.IdleTimeout3 + cfg.SendUnAckTimeout1 + 500*time.Millisecond)
	for time.Now().Before(deadline) {
		if !sess.IsConnected() {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("session should have disconnected after an unconfirmed TestFrActive")
}

func TestSrvSession_InvalidAck_Disconnects(t *testing.T) {
	sess, peer := newTestSrvSession(t, stubServerHandler{}, fastTestConfig())
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))

	writeFrame(t, peer, newUFrame(UStartDtActive))
	readFrame(t, peer) // StartDtConfirm

	writeFrame(t, peer, newSFrame(5)) // ack for a frame never sent

	buf := make([]byte, 1)
	if _, err := peer.Read(buf); err == nil {
		t.Fatal("expected the connection to be closed after an out-of-range ack")
	}
	if sess.IsConnected() {
		t.Fatal("session should be disconnected after an out-of-range ack")
	}
}

// TestSrvSession_updateAckNoOut_Wraparound is a regression test for the
// updateAckNoOut sequence-number-wraparound bug: acknowledging the frame
// sent immediately before the 15-bit sequence counter wraps from 32767
// back to 0 must trim it from the pending queue like any other ack.
func TestSrvSession_updateAckNoOut_Wraparound(t *testing.T) {
	sess := &SrvSession{connection: connection{
		ackNoSend: 32767,
		seqNoSend: 0,
		pending:   []seqPending{{seq: 32767, sendTime: time.Now()}},
	}}

	if !sess.updateAckNoOut(0) {
		t.Fatal("updateAckNoOut(0) = false, want true")
	}
	if len(sess.pending) != 0 {
		t.Fatalf("pending = %v, want empty: the wrapped ack should confirm seq 32767", sess.pending)
	}
	if sess.ackNoSend != 0 {
		t.Fatalf("ackNoSend = %d, want 0", sess.ackNoSend)
	}
}

// TestSrvSession_CleanUp_PreservesSendQueue is a regression test: cleanUp
// used to drain every channel including outbound sends, so a message queued
// but not yet transmitted was silently discarded on every reconnect
// (ServerSpecial reuses one SrvSession across reconnects, calling cleanUp
// at the top of every run()). sendQueue must survive it.
func TestSrvSession_CleanUp_PreservesSendQueue(t *testing.T) {
	sess := &SrvSession{connection: connection{
		sendQueue: newMessageQueue(10),
		rcvASDU:   make(chan []byte, 1),
		rcvRaw:    make(chan []byte, 1),
		sendRaw:   make(chan []byte, 1),
	}}
	sess.role = sess // as every real construction does; cleanUp calls into it
	sess.sendQueue.Push([]byte("pending"))

	sess.cleanUp()

	got, ok := sess.sendQueue.Pop()
	if !ok || string(got) != "pending" {
		t.Fatalf("cleanUp must not discard queued outbound messages: got %q, ok=%v", got, ok)
	}
}
