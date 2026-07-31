// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// Client is an IEC104 master: the controlling station, which dials out and
// issues STARTDT/STOPDT. The connection state machine itself is shared with
// SrvSession, see connection.
type Client struct {
	connection

	option  ClientOption
	handler ClientHandlerInterface

	// startDtActiveSendSince and stopDtActiveSendSince are when a STARTDT or
	// STOPDT activation was sent and is still awaiting confirmation; nil
	// means none is outstanding. Atomic because SendStartDt/SendStopDt run
	// on the application's goroutine while run reads them from its own.
	//
	// A typed atomic.Pointer rather than an atomic.Value: Value hands back
	// any, and the assertion unwrapping it yields the zero time on failure
	// -- indistinguishable from "not waiting", so a missed confirmation
	// would go unnoticed rather than time out.
	//
	// And a *time.Time rather than Unix nanoseconds in an atomic.Int64,
	// which is what this was: time.Unix strips the monotonic reading, so
	// the elapsed-time comparison in roleTimedOut fell back to the wall
	// clock and a clock step (NTP, a resumed VM) could fire t₁ early or
	// late. Every other timer in the state machine keeps its monotonic
	// reading by holding a time.Time; this one now does too.
	startDtActiveSendSince atomic.Pointer[time.Time]
	stopDtActiveSendSince  atomic.Pointer[time.Time]

	closeCancel context.CancelFunc

	onConnect        func(c *Client)
	onConnectionLost func(c *Client)
}

// NewClient returns an IEC104 master, default config and default asdu.ParamsWide params
func NewClient(handler ClientHandlerInterface, o *ClientOption) *Client {
	c := &Client{
		connection: connection{
			// Sized as multiples of the protocol windows so a burst several
			// windows deep is absorbed rather than backpressured frame by
			// frame. The multipliers (16x, 32x) are headroom chosen for that
			// effect, not values the standard specifies -- w and k bound what
			// may be in flight unacknowledged, not what may be buffered
			// behind it.
			rcvASDU:   make(chan []byte, o.config.RecvUnAckLimitW<<4),
			sendQueue: newMessageQueue(int(o.config.SendUnAckLimitK) << 4),
			rcvRaw:    make(chan []byte, o.config.RecvUnAckLimitW<<5),
			sendRaw:   make(chan []byte, o.config.SendUnAckLimitK<<5),
			log:       o.logger().With("component", "cs104.client"),
		},
		option:           *o,
		handler:          handler,
		onConnect:        func(*Client) {},
		onConnectionLost: func(*Client) {},
	}
	c.role = c
	// Point at this Client's own copy of the option, not the caller's,
	// which it may reuse or mutate after NewClient returns.
	c.config = &c.option.config
	c.params = &c.option.params
	if o.server != nil {
		c.log = c.log.With("remote", o.server.Host)
	}
	c.option.logRejected(c.log)
	return c
}

// SetLogger directs this client's records to l. Records go to slog.Default()
// when unset. Passing nil restores that default rather than disabling
// logging -- to silence the library, give it a logger whose handler discards
// everything.
func (sf *Client) SetLogger(l *slog.Logger) *Client {
	if l == nil {
		l = slog.Default()
	}
	sf.log = l
	return sf
}

// SetOnConnectHandler set on connect handler
func (sf *Client) SetOnConnectHandler(f func(c *Client)) *Client {
	if f != nil {
		sf.onConnect = f
	}
	return sf
}

// SetConnectionLostHandler set connection lost handler
func (sf *Client) SetConnectionLostHandler(f func(c *Client)) *Client {
	if f != nil {
		sf.onConnectionLost = f
	}
	return sf
}

// Start begins connecting in the background and returns immediately. A nil
// return means the client was started, not that it connected.
//
// Connection outcomes are reported through the handlers, not from here:
// SetOnConnectHandler once a connection is up, SetConnectionLostHandler when
// one ends. A dial that fails is logged and retried per SetAutoReconnect; if
// auto-reconnect is disabled, the first failed dial stops the client for
// good, and since no connection was ever established the connection-lost
// handler does not run -- the error is only reported to the logger.
//
// Starting an already-running client returns ErrAlreadyStarted.
func (sf *Client) Start() error {
	if sf.option.server == nil {
		return errors.New("empty remote server")
	}

	// Claim the transition here rather than inside running: doing it in the
	// goroutine means Start has already returned nil by the time a duplicate
	// is detected, so the caller cannot be told. Taking closeCancel under the
	// same lock keeps Close working for a caller that calls it the instant
	// Start returns, before the goroutine has run at all.
	sf.connMu.Lock()
	if !sf.status.CompareAndSwap(initial, disconnected) {
		sf.connMu.Unlock()
		return ErrAlreadyStarted
	}
	ctx, cancel := context.WithCancel(context.Background())
	sf.closeCancel = cancel
	sf.connMu.Unlock()

	go sf.running(ctx)
	return nil
}

// running dials the remote server, runs the connection, and reconnects. The
// status transition and ctx are established by Start, which is what lets it
// report a duplicate start to the caller.
func (sf *Client) running(ctx context.Context) {
	defer sf.setConnectStatus(initial)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		sf.log.Debug("connecting")
		conn, err := openConnection(sf.option.server, sf.option.TLSConfig, sf.option.config.ConnectTimeout0)
		if err != nil {
			sf.log.Error("connect failed", "err", err)
			if !sf.option.autoReconnect {
				return
			}
			time.Sleep(sf.option.reconnectInterval)
			continue
		}
		sf.log.Info("connected")
		sf.run(ctx, conn)

		sf.log.Info("disconnected")
		select {
		case <-ctx.Done():
			return
		default:
			// Wait a random 500ms-1s before retrying, so a fast reconnect loop
			// does not leave the server with a pile of dead connections.
			time.Sleep(time.Millisecond * time.Duration(500+rand.Intn(500)))
		}
	}
}

// handleUFrame implements connRole. The controlling station issues the
// activations, so it is the confirmations it has to act on.
func (sf *Client) handleUFrame(function byte) {
	switch function {
	case UStartDtConfirm:
		sf.isActive.Store(true)
		sf.startDtActiveSendSince.Store(nil)
	case UStopDtConfirm:
		sf.isActive.Store(false)
		sf.stopDtActiveSendSince.Store(nil)
	case UTestFrActive:
		sf.sendUFrame(UTestFrConfirm)
	case UTestFrConfirm:
		sf.testFrAliveSendSince = time.Time{}
	default:
		sf.log.Error("ignoring illegal U-frame function", "function", fmt.Sprintf("0x%02x", function))
	}
}

// roleTimedOut implements connRole: t₁ also covers the STARTDT and STOPDT
// activations that only the controlling station sends.
func (sf *Client) roleTimedOut(now time.Time) bool {
	for _, v := range []*atomic.Pointer[time.Time]{&sf.startDtActiveSendSince, &sf.stopDtActiveSendSince} {
		if since := v.Load(); since != nil && now.Sub(*since) >= sf.option.config.SendUnAckTimeout1 {
			sf.log.Error("no STARTDT/STOPDT confirmation within t₁, closing",
				"t1", sf.option.config.SendUnAckTimeout1)
			return true
		}
	}
	return false
}

// roleCleanUp implements connRole, discarding any activation still awaiting
// confirmation when the previous connection ended.
//
// Without this the timestamps outlive the connection they belong to, and
// roleTimedOut measures them against the next one -- which never sent the
// activation being timed. An application that issues STARTDT once rather
// than on every reconnect then loses each fresh connection to its
// predecessor's expired timer, reconnecting in a loop.
func (sf *Client) roleCleanUp() {
	sf.startDtActiveSendSince.Store(nil)
	sf.stopDtActiveSendSince.Store(nil)
}

func (sf *Client) notifyUp()   { sf.onConnect(sf) }
func (sf *Client) notifyDown() { sf.onConnectionLost(sf) }

// dispatchASDU implements connRole, routing a received ASDU to the
// application's handler.
func (sf *Client) dispatchASDU(asduPack *asdu.ASDU) (err error) {
	defer func() {
		if r := recover(); r != nil {
			sf.log.Error("client handler panicked", "panic", r, "type", asduPack.Identifier.Type)
			// Named return, so the panic surfaces as a failure rather than
			// as the nil an unnamed return would leave behind -- which would
			// make a handler that blew up indistinguishable from one that
			// succeeded.
			err = fmt.Errorf("client handler panicked: %v", r)
		}
	}()

	if sf.debugEnabled() {
		sf.log.Debug("dispatching ASDU", "asdu", asduPack.String())
	}

	switch asduPack.Identifier.Type {
	case asdu.C_IC_NA_1: // InterrogationCmd
		return sf.handler.InterrogationHandler(sf, asduPack)

	case asdu.C_CI_NA_1: // CounterInterrogationCmd
		return sf.handler.CounterInterrogationHandler(sf, asduPack)

	case asdu.C_RD_NA_1: // ReadCmd
		return sf.handler.ReadHandler(sf, asduPack)

	case asdu.C_CS_NA_1: // ClockSynchronizationCmd
		return sf.handler.ClockSyncHandler(sf, asduPack)

	case asdu.C_TS_NA_1: // TestCommand
		return sf.handler.TestCommandHandler(sf, asduPack)

	case asdu.C_RP_NA_1: // ResetProcessCmd
		return sf.handler.ResetProcessHandler(sf, asduPack)

	case asdu.C_CD_NA_1: // DelayAcquireCommand
		return sf.handler.DelayAcquisitionHandler(sf, asduPack)
	}

	return sf.handler.ASDUHandler(sf, asduPack)
}

// Send send asdu
func (sf *Client) Send(a *asdu.ASDU) error {
	// Connection state first, then activation: a disconnected client is
	// also inactive, and "the connection is gone" is the more useful of the
	// two answers.
	if !sf.IsConnected() {
		return ErrUseClosedConnection
	}
	if !sf.IsActive() {
		return ErrNotActive
	}
	return sf.connection.Send(a)
}

// Close close all
func (sf *Client) Close() error {
	sf.connMu.Lock()
	if sf.closeCancel != nil {
		sf.closeCancel()
	}
	sf.connMu.Unlock()
	return nil
}

// SendStartDt start data transmission on this connection
func (sf *Client) SendStartDt() {
	now := time.Now()
	sf.startDtActiveSendSince.Store(&now)
	sf.trySendUFrame(UStartDtActive)
}

// SendStopDt stop data transmission on this connection
func (sf *Client) SendStopDt() {
	now := time.Now()
	sf.stopDtActiveSendSince.Store(&now)
	sf.trySendUFrame(UStopDtActive)
}

// InterrogationCmd wrap asdu.InterrogationCmd
func (sf *Client) InterrogationCmd(coa asdu.CauseOfTransmission, ca asdu.CommonAddr, qoi asdu.QualifierOfInterrogation) error {
	return asdu.InterrogationCmd(sf, coa, ca, qoi)
}

// CounterInterrogationCmd wrap asdu.CounterInterrogationCmd
func (sf *Client) CounterInterrogationCmd(coa asdu.CauseOfTransmission, ca asdu.CommonAddr, qcc asdu.QualifierCountCall) error {
	return asdu.CounterInterrogationCmd(sf, coa, ca, qcc)
}

// ReadCmd wrap asdu.ReadCmd
func (sf *Client) ReadCmd(coa asdu.CauseOfTransmission, ca asdu.CommonAddr, ioa asdu.InfoObjAddr) error {
	return asdu.ReadCmd(sf, coa, ca, ioa)
}

// ClockSynchronizationCmd wrap asdu.ClockSynchronizationCmd
func (sf *Client) ClockSynchronizationCmd(coa asdu.CauseOfTransmission, ca asdu.CommonAddr, t time.Time) error {
	return asdu.ClockSynchronizationCmd(sf, coa, ca, t)
}

// ResetProcessCmd wrap asdu.ResetProcessCmd
func (sf *Client) ResetProcessCmd(coa asdu.CauseOfTransmission, ca asdu.CommonAddr, qrp asdu.QualifierOfResetProcessCmd) error {
	return asdu.ResetProcessCmd(sf, coa, ca, qrp)
}

// DelayAcquireCommand wrap asdu.DelayAcquireCommand
func (sf *Client) DelayAcquireCommand(coa asdu.CauseOfTransmission, ca asdu.CommonAddr, msec uint16) error {
	return asdu.DelayAcquireCommand(sf, coa, ca, msec)
}

// TestCommand  wrap asdu.TestCommand
func (sf *Client) TestCommand(coa asdu.CauseOfTransmission, ca asdu.CommonAddr) error {
	return asdu.TestCommand(sf, coa, ca)
}
