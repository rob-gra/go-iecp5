// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"context"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// connRole supplies the parts of a cs104 connection that genuinely differ
// between the two ends of the link. Everything else -- framing, sequence
// numbers, the k/w windows, the t1/t2/t3 timers, the send queue -- is
// identical for both and lives on connection.
//
// The split matters because the two ends previously carried their own
// near-identical copy of that machinery, and bugs in it had to be found and
// fixed twice: the sequence-number wraparound defect existed in both copies
// (see confirmSeqNo in common.go), as did the mirror-reply truncation.
type connRole interface {
	// handleUFrame processes a received U-frame function code. The
	// controlling station (Client) sees the confirmations; the controlled
	// station (SrvSession) sees the activations and answers them.
	handleUFrame(function byte)
	// dispatchASDU routes a decoded ASDU to the application's handler.
	dispatchASDU(*asdu.ASDU) error
	// roleTimedOut reports whether a timer only this role runs has expired,
	// which tears the connection down. Only the controlling station has any:
	// it waits for STARTDT/STOPDT confirmations that it alone sends.
	roleTimedOut(now time.Time) bool
	// roleCleanUp discards role-specific per-connection state before run
	// (re)starts, alongside connection.cleanUp's own. Whatever roleTimedOut
	// measures has to be reset here: a timer left running from the previous
	// connection would otherwise expire against the new one, which never
	// did the thing being timed.
	roleCleanUp()
	// notifyUp and notifyDown inform the application of connection state.
	// They are separate from the connRole's own bookkeeping because the two
	// roles hand the application different receiver types.
	//
	// notifyUp fires once the connection is established, before STARTDT has
	// completed -- deliberately, since issuing STARTDT is the usual thing to
	// do from it. Data transfer is not enabled yet at that point, and Send
	// reports ErrNotActive until it is.
	notifyUp()
	notifyDown()
}

// connection is the cs104 connection state machine shared by Client (the
// controlling station, which dials out) and SrvSession (the controlled
// station, which is dialled). Both embed it and supply a connRole.
type connection struct {
	config *Config
	params *asdu.Params
	role   connRole

	conn net.Conn

	rcvASDU chan []byte // decoded ASDU payloads awaiting the handler
	rcvRaw  chan []byte // raw frames from recvLoop
	sendRaw chan []byte // raw frames for sendLoop

	// sendQueue holds outbound ASDU payloads awaiting transmission. Unlike
	// the channels above it isn't cleared on cleanUp, so messages queued but
	// not yet sent survive a reconnect.
	sendQueue *messageQueue

	// see subclass 5.1 -- Protection against loss and duplication of messages
	seqNoSend uint16 // sequence number of next outbound I-frame
	ackNoSend uint16 // outbound sequence number yet to be confirmed
	seqNoRcv  uint16 // sequence number of next inbound I-frame
	ackNoRcv  uint16 // inbound sequence number yet to be confirmed
	pending   []seqPending

	// status and isActive are typed atomics: the type carries the
	// synchronisation requirement, so a plain read cannot be written by
	// accident the way it could when these were bare uint32s with a
	// comment asking for atomic access.
	status   atomic.Uint32
	isActive atomic.Bool
	// connMu guards the state that is rebound when a connection is
	// (re)established: conn here, and closeCancel on the types that
	// reconnect. status and isActive are atomics and need no lock.
	connMu sync.RWMutex

	// idleSince is when the link last carried traffic in either
	// direction, which is what t3 measures. Only run's goroutine touches
	// it (sendFrame runs there too); trySendFrame deliberately does not.
	idleSince time.Time

	// testFrAliveSendSince is when a TESTFR act was sent and is still
	// awaiting its confirmation; the zero time means none is outstanding.
	// Only touched from run's goroutine (handleUFrame runs there too).
	testFrAliveSendSince time.Time

	// deactivateCh asks the connection to fall back to inactive, as if it
	// had processed a STOPDT. Buffered 1 and sent to non-blockingly, so it
	// is safe to signal from any goroutine and a pending signal need not
	// queue twice.
	deactivateCh chan struct{}

	// log receives this connection's records. Server gives each session a
	// logger carrying the peer's address, so a line can be attributed to the
	// connection that produced it; see Server.newSession.
	log *slog.Logger

	wg     sync.WaitGroup
	ctx    context.Context
	cancel context.CancelFunc
}

// IsConnected reports whether the underlying connection is established.
func (sf *connection) IsConnected() bool { return sf.connectStatus() == connected }

// IsActive reports whether the STARTDT handshake has completed and no
// STOPDT has been sent or received since.
func (sf *connection) IsActive() bool { return sf.isActive.Load() }

// debugEnabled reports whether debug records are actually being emitted.
// The per-frame call sites check it before building their attributes: slog
// boxes a call's arguments into []any before the handler can discard them,
// so an unguarded Debug on the frame path allocates on every frame even
// with debug off.
func (sf *connection) debugEnabled() bool {
	return sf.log.Enabled(context.Background(), slog.LevelDebug)
}

// Params returns the ASDU parameters this connection encodes and decodes with.
func (sf *connection) Params() *asdu.Params { return sf.params }

// UnderlyingConn returns the underlying net.Conn.
func (sf *connection) UnderlyingConn() net.Conn { return sf.getConn() }

// forceDeactivate asks the connection to fall back to inactive, e.g.
// because it was superseded by another connection in the same redundancy
// group. The underlying connection is left alone: it stays connected as a
// standby, ready to be reactivated (STARTDT) later. Safe to call from any
// goroutine, and safe to call repeatedly.
func (sf *connection) forceDeactivate() {
	select {
	case sf.deactivateCh <- struct{}{}:
	default:
	}
}

func (sf *connection) setConnectStatus(status uint32) {
	sf.status.Store(status)
}

func (sf *connection) connectStatus() uint32 {
	return sf.status.Load()
}

// setConn stores conn for UnderlyingConn to read. Client and ServerSpecial
// rebind the same value to a new conn on every reconnect, so reads and
// writes must be synchronized.
func (sf *connection) setConn(conn net.Conn) {
	sf.connMu.Lock()
	sf.conn = conn
	sf.connMu.Unlock()
}

func (sf *connection) getConn() net.Conn {
	sf.connMu.RLock()
	conn := sf.conn
	sf.connMu.RUnlock()
	return conn
}

// Send queues an ASDU for transmission.
func (sf *connection) Send(u *asdu.ASDU) error {
	if !sf.IsConnected() {
		return ErrUseClosedConnection
	}
	data, err := u.MarshalBinary()
	if err != nil {
		return err
	}
	// Checked here so the caller hears about it. Past this point the ASDU
	// is just bytes on a queue, and the only thing left to do with one too
	// long to frame is drop it.
	if len(data) > asdu.ASDUSizeMax {
		return asdu.ErrLengthOutOfRange
	}
	sf.enqueue(data)
	return nil
}

// enqueue puts an already-marshalled ASDU on the send queue, logging an
// eviction if the queue was full.
func (sf *connection) enqueue(data []byte) {
	if sf.sendQueue.Push(data) {
		sf.log.Warn("send queue full, dropped oldest unsent message")
	}
}

// recvLoop feeds rcvRaw with frames read off the wire.
func (sf *connection) recvLoop(conn net.Conn) {
	sf.log.Debug("recv loop started")
	defer func() {
		sf.cancel()
		sf.wg.Done()
		sf.log.Debug("recv loop stopped")
	}()

	for {
		apdu, err := ReadAPDU(conn)
		if err != nil {
			// Neither of the first two cases is a fault, and now that
			// records are visible by default, logging them as one would
			// report every routine disconnect as an error.
			//
			// A peer hanging up (EOF) is how connections normally end. And
			// run's defer closes conn to stop this loop, so net.ErrClosed
			// here is our own teardown arriving as a read failure -- always
			// self-inflicted, since it is only returned for a descriptor
			// this side closed. Anything else genuinely failed.
			switch {
			case errors.Is(err, io.EOF):
				sf.log.Info("remote closed the connection")
			case errors.Is(err, net.ErrClosed):
				sf.log.Debug("read stopped: connection closed locally")
			default:
				sf.log.Error("receive failed", "err", err)
			}
			return
		}
		if sf.debugEnabled() {
			sf.log.Debug("rx raw", "apdu", fmt.Sprintf("% x", apdu))
		}
		select {
		case sf.rcvRaw <- apdu:
		case <-sf.ctx.Done():
			return
		}
	}
}

// sendLoop drains sendRaw onto the wire.
func (sf *connection) sendLoop(conn net.Conn) {
	sf.log.Debug("send loop started")
	defer func() {
		sf.cancel()
		sf.wg.Done()
		sf.log.Debug("send loop stopped")
	}()

	for {
		select {
		case <-sf.ctx.Done():
			return
		case apdu := <-sf.sendRaw:
			if sf.debugEnabled() {
				sf.log.Debug("tx raw", "apdu", fmt.Sprintf("% x", apdu))
			}
			// Bound how long one frame may take to reach the peer. Without
			// a deadline, a peer that stops reading blocks Write forever:
			// sendRaw fills, and run() -- the one that would notice via t1
			// and tear the connection down -- ends up queued behind it
			// instead. t1 is the protocol's own limit on an unresponsive
			// peer, so it is the right bound here too. A missed deadline
			// surfaces as a write error below and ends the connection.
			if err := conn.SetWriteDeadline(time.Now().Add(sf.config.SendUnAckTimeout1)); err != nil {
				sf.log.Error("set write deadline failed", "err", err)
				return
			}
			// Any write failure ends the connection. This is a stateful
			// protocol carrying sequence numbers, so a partially written
			// or failed frame leaves the stream unusable -- there is
			// nothing to recover to, and the peer reconnects and
			// re-runs STARTDT. (The retry this replaced was unreachable
			// anyway: it could only be entered for io.EOF or
			// io.ErrClosedPipe, neither of which is a net.Error, so its
			// net.Error.Temporary check -- deprecated since Go 1.18 --
			// always fell through to the same return.)
			for wrCnt := 0; len(apdu) > wrCnt; {
				byteCount, err := conn.Write(apdu[wrCnt:])
				if err != nil {
					sf.log.Error("frame write failed", "err", err)
					return
				}
				wrCnt += byteCount
			}
		}
	}
}

// handlerLoop decodes received ASDUs and hands them to the application.
func (sf *connection) handlerLoop() {
	sf.log.Debug("handler loop started")
	defer func() {
		sf.wg.Done()
		sf.log.Debug("handler loop stopped")
	}()

	for {
		select {
		case <-sf.ctx.Done():
			return
		case rawAsdu := <-sf.rcvASDU:
			asduPack := asdu.NewEmptyASDU(sf.params)
			if err := asduPack.UnmarshalBinary(rawAsdu); err != nil {
				sf.log.Warn("discarding undecodable ASDU", "err", err)
				continue
			}
			if err := sf.role.dispatchASDU(asduPack); err != nil {
				sf.log.Warn("handling I-frame failed", "err", err)
			}
		}
	}
}

// sendFrame hands a raw frame to sendLoop. Only run's own goroutine may
// call it, since it reads the per-connection context run installs.
//
// It waits for room rather than dropping: an I-frame has already taken a
// sequence number by the time it gets here, so discarding it would leave a
// hole the peer answers by dropping the connection. Waiting is safe because
// the wait ends as soon as the connection is torn down -- and sendLoop
// bounds how long a stalled peer can hold that up, by giving each write a
// deadline. So run() cannot be wedged by a peer that stops reading, which
// is what would otherwise stop it servicing its own t1 timer.
func (sf *connection) sendFrame(apdu []byte) {
	// Anything we put on the link is link activity, so it pushes back t3
	// -- the standard measures idleness in either direction. Doing it here
	// rather than at each call site is why an S-frame acknowledgement or a
	// STOPDT confirm counts, and not just the I-frames that used to.
	//
	// This marks when the frame was queued, not when it reached the wire;
	// the two differ only when sendLoop is backed up, which its write
	// deadline already bounds to t1.
	sf.idleSince = time.Now()
	select {
	case sf.sendRaw <- apdu:
	case <-sf.ctx.Done():
	}
}

// trySendFrame is sendFrame for callers outside run's goroutine: the
// application issuing STARTDT/STOPDT. It neither blocks nor touches the
// context run owns, and drops the frame if the buffer is somehow full --
// these are rare, un-sequenced control frames, so losing one costs a
// retry rather than the connection.
func (sf *connection) trySendFrame(apdu []byte) {
	select {
	case sf.sendRaw <- apdu:
	default:
		sf.log.Warn("send buffer full, dropped an outbound frame: peer is not reading")
	}
}

func (sf *connection) sendSFrame(rcvSN uint16) {
	if sf.debugEnabled() {
		sf.log.Debug("tx S-frame", "rcvSN", rcvSN)
	}
	sf.sendFrame(newSFrame(rcvSN))
}

func (sf *connection) sendUFrame(which byte) {
	if sf.debugEnabled() {
		sf.log.Debug("tx U-frame", "function", UAPCI{which})
	}
	sf.sendFrame(newUFrame(which))
}

// trySendUFrame is sendUFrame for callers outside run's goroutine.
func (sf *connection) trySendUFrame(which byte) {
	if sf.debugEnabled() {
		sf.log.Debug("tx U-frame", "function", UAPCI{which})
	}
	sf.trySendFrame(newUFrame(which))
}

func (sf *connection) sendIFrame(asdu1 []byte) {
	seqNo := sf.seqNoSend

	iframe, err := newIFrame(seqNo, sf.seqNoRcv, asdu1)
	if err != nil {
		// Only reachable for an over-long ASDU, which Send rejects up
		// front -- but say so rather than dropping it in silence, since a
		// message that vanishes with no trace is the worst thing to have
		// to diagnose from the far end of a link. Nothing has been
		// committed yet: the sequence number is still unspent.
		sf.log.Error("dropping outbound ASDU", "err", err)
		return
	}
	sf.ackNoRcv = sf.seqNoRcv
	sf.seqNoSend = nextSeqNo(seqNo)
	sf.pending = append(sf.pending, seqPending{seqNo, time.Now()})

	if sf.debugEnabled() {
		sf.log.Debug("tx I-frame", "sendSN", seqNo, "rcvSN", sf.seqNoRcv)
	}
	sf.sendFrame(iframe)
}

func (sf *connection) updateAckNoOut(ackNo uint16) (ok bool) {
	pending, ok := confirmSeqNo(sf.pending, sf.ackNoSend, sf.seqNoSend, ackNo)
	if !ok {
		return false
	}
	sf.pending = pending
	sf.ackNoSend = ackNo
	return true
}

// cleanUp resets the per-connection state before (re)starting run.
func (sf *connection) cleanUp() {
	sf.ackNoRcv = 0
	sf.ackNoSend = 0
	sf.seqNoRcv = 0
	sf.seqNoSend = 0
	sf.pending = nil
	sf.testFrAliveSendSince = time.Time{}
	sf.role.roleCleanUp()
	sf.isActive.Store(false)
	// Client and ServerSpecial reuse one value across reconnects, so re-arm
	// the deactivate signal for the new connection's lifetime.
	sf.deactivateCh = make(chan struct{}, 1)
	// Clear the raw-frame and inbound-ASDU buffers: they belong to the
	// sequence-number state of the connection that just ended, so replaying
	// them makes no sense. sendQueue is deliberately left untouched, so
	// outbound messages the caller queued survive the reconnect.
	drain(sf.sendRaw)
	drain(sf.rcvRaw)
	drain(sf.rcvASDU)
}

// drain empties ch without blocking. Safe here because run's goroutines
// have all finished by the time cleanUp runs, so nothing is refilling it.
func drain[T any](ch chan T) {
	for {
		select {
		case <-ch:
		default:
			return
		}
	}
}

// run is the connection state machine: it drives the k/w send window, the
// t1/t2/t3 timers and the I/S/U frame exchange, delegating to the connRole
// wherever the two ends of the link differ.
func (sf *connection) run(ctx context.Context, conn net.Conn) {
	sf.log.Debug("connection state machine started")
	sf.cleanUp()
	sf.setConn(conn)

	sf.ctx, sf.cancel = context.WithCancel(ctx)
	sf.setConnectStatus(connected)
	sf.wg.Add(3)
	go sf.recvLoop(conn)
	go sf.sendLoop(conn)
	go sf.handlerLoop()

	checkTicker := time.NewTicker(timeoutResolution)
	defer func() {
		sf.isActive.Store(false)
		sf.setConnectStatus(disconnected)
		checkTicker.Stop()
		// Cancel before waiting. Closing the connection is not enough on
		// its own: recvLoop may be handing a frame to a full rcvRaw rather
		// than reading, and sendLoop may be idle -- neither notices a
		// closed conn, so without this wg.Wait() below could block for
		// good.
		sf.cancel()
		_ = conn.Close()
		sf.wg.Wait()
		sf.role.notifyDown()
		sf.log.Debug("connection state machine stopped")
	}()
	sf.role.notifyUp()

	// unAckRcvSince is when the oldest not-yet-acknowledged inbound I-frame
	// arrived; it is zero when nothing is outstanding.
	var unAckRcvSince time.Time
	sf.idleSince = time.Now()

	for {
		// Strictly less than k: seqNoCount is the number of I-frames already
		// outstanding, and sendIFrame below adds one more. Allowing the send
		// at == k would leave k+1 unacknowledged, one past what subclass 5.5
		// permits -- enough for a peer that enforces k to drop the link.
		if sf.IsActive() && seqNoCount(sf.ackNoSend, sf.seqNoSend) < sf.config.SendUnAckLimitK {
			if o, ok := sf.sendQueue.Pop(); ok {
				sf.sendIFrame(o) // pushes back t3 via sendFrame
				continue
			}
		}

		select {
		case <-sf.ctx.Done():
			return

		case <-sf.deactivateCh:
			// Superseded by another connection in the same redundancy group:
			// fall back to inactive, same as processing a STOPDT, but stay
			// connected as a standby so we can be reactivated later without
			// the peer having to reconnect.
			sf.log.Info("deactivating: superseded by another connection in the redundancy group")
			sf.sendUFrame(UStopDtConfirm)
			sf.isActive.Store(false)

		case <-sf.sendQueue.Ready():
			continue

		case now := <-checkTicker.C:
			// t1: a sent TESTFR (or, for the controlling station, a sent
			// STARTDT/STOPDT) was never confirmed.
			if !sf.testFrAliveSendSince.IsZero() &&
				now.Sub(sf.testFrAliveSendSince) >= sf.config.SendUnAckTimeout1 {
				sf.log.Error("no TESTFR confirmation within t₁, closing", "t1", sf.config.SendUnAckTimeout1)
				return
			}
			if sf.role.roleTimedOut(now) {
				return
			}
			// t1: the oldest unacknowledged outbound I-frame went
			// unanswered. pending holds exactly the frames between
			// ackNoSend and seqNoSend, so it is non-empty whenever those
			// two differ -- which is what makes pending[0] safe here.
			if sf.ackNoSend != sf.seqNoSend &&
				now.Sub(sf.pending[0].sendTime) >= sf.config.SendUnAckTimeout1 {
				sf.log.Error("no acknowledgement within t₁, closing",
					"t1", sf.config.SendUnAckTimeout1, "unacked", seqNoCount(sf.ackNoSend, sf.seqNoSend))
				return
			}

			// t2: acknowledge inbound I-frames left outstanding too long.
			// The w window (below, on receipt) does the acknowledging under
			// sustained traffic; t2 covers the tail, where the peer stops
			// sending before w frames have accumulated. t2 < t1 by default,
			// so the acknowledgement lands before the sender's own t1 fires.
			if sf.ackNoRcv != sf.seqNoRcv && now.Sub(unAckRcvSince) >= sf.config.RecvUnAckTimeout2 {
				sf.sendSFrame(sf.seqNoRcv)
				sf.ackNoRcv = sf.seqNoRcv
			}

			// t3: the link has been idle, send a TESTFR to prove it is alive.
			if now.Sub(sf.idleSince) >= sf.config.IdleTimeout3 {
				sf.sendUFrame(UTestFrActive) // pushes back t3 via sendFrame
				sf.testFrAliveSendSince = time.Now()
			}

		case apdu := <-sf.rcvRaw:
			sf.idleSince = time.Now() // any inbound I/S/U frame is activity too
			apci, asduVal := parse(apdu)
			switch head := apci.(type) {
			case SAPCI:
				if sf.debugEnabled() {
					sf.log.Debug("rx S-frame", "rcvSN", head.RcvSN)
				}
				if !sf.updateAckNoOut(head.RcvSN) {
					sf.log.Error("acknowledgement outside the outstanding window, closing",
						"rcvSN", head.RcvSN, "ackNoSend", sf.ackNoSend, "seqNoSend", sf.seqNoSend)
					return
				}

			case IAPCI:
				if sf.debugEnabled() {
					sf.log.Debug("rx I-frame", "sendSN", head.SendSN, "rcvSN", head.RcvSN)
				}
				if !sf.IsActive() {
					sf.log.Warn("discarding I-frame: data transfer is stopped")
					break // discard: data transfer is stopped
				}
				if !sf.updateAckNoOut(head.RcvSN) || head.SendSN != sf.seqNoRcv {
					sf.log.Error("I-frame sequence out of step, closing",
						"sendSN", head.SendSN, "wantSendSN", sf.seqNoRcv,
						"rcvSN", head.RcvSN, "ackNoSend", sf.ackNoSend, "seqNoSend", sf.seqNoSend)
					return
				}

				select {
				case sf.rcvASDU <- asduVal:
				case <-sf.ctx.Done():
					return
				}
				if sf.ackNoRcv == sf.seqNoRcv { // first unacknowledged
					unAckRcvSince = time.Now()
				}

				sf.seqNoRcv = nextSeqNo(sf.seqNoRcv)
				if seqNoCount(sf.ackNoRcv, sf.seqNoRcv) >= sf.config.RecvUnAckLimitW {
					sf.sendSFrame(sf.seqNoRcv)
					sf.ackNoRcv = sf.seqNoRcv
				}

			case UAPCI:
				if sf.debugEnabled() {
					sf.log.Debug("rx U-frame", "function", head)
				}
				sf.role.handleUFrame(head.Function)
			}
		}
	}
}
