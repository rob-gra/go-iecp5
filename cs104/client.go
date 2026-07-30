// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"context"
	"errors"
	"math/rand"
	"sync/atomic"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/clog"
)

const (
	inactive = iota
	active
)

// Client is an IEC104 master: the controlling station, which dials out and
// issues STARTDT/STOPDT. The connection state machine itself is shared with
// SrvSession, see connection.
type Client struct {
	connection

	option  ClientOption
	handler ClientHandlerInterface

	// startDtActiveSendSince and stopDtActiveSendSince are when a STARTDT or
	// STOPDT activation was sent and is still awaiting confirmation; the
	// zero time means none is outstanding. Held in an atomic.Value because
	// SendStartDt/SendStopDt are called from the application's goroutine
	// while run reads them from its own.
	startDtActiveSendSince atomic.Value
	stopDtActiveSendSince  atomic.Value

	closeCancel context.CancelFunc

	onConnect        func(c *Client)
	onConnectionLost func(c *Client)
}

// NewClient returns an IEC104 master, default config and default asdu.ParamsWide params
func NewClient(handler ClientHandlerInterface, o *ClientOption) *Client {
	c := &Client{
		connection: connection{
			config:    &o.config,
			params:    &o.params,
			rcvASDU:   make(chan []byte, o.config.RecvUnAckLimitW<<4),
			sendQueue: newMessageQueue(int(o.config.SendUnAckLimitK) << 4),
			rcvRaw:    make(chan []byte, o.config.RecvUnAckLimitW<<5),
			sendRaw:   make(chan []byte, o.config.SendUnAckLimitK<<5),
			Clog:      clog.NewLogger("cs104 client => "),
		},
		option:           *o,
		handler:          handler,
		onConnect:        func(*Client) {},
		onConnectionLost: func(*Client) {},
	}
	c.role = c
	// The config and params the connection reads must be the Client's own
	// copy of the option, not the caller's, which it may reuse or mutate.
	c.connection.config = &c.option.config
	c.connection.params = &c.option.params
	c.startDtActiveSendSince.Store(time.Time{})
	c.stopDtActiveSendSince.Store(time.Time{})
	return c
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

// Start start the server,and return quickly,if it nil,the server will disconnected background,other failed
func (sf *Client) Start() error {
	if sf.option.server == nil {
		return errors.New("empty remote server")
	}

	go sf.running()
	return nil
}

// running dials the remote server, runs the connection, and reconnects.
func (sf *Client) running() {
	var ctx context.Context

	sf.rwMux.Lock()
	if !atomic.CompareAndSwapUint32(&sf.status, initial, disconnected) {
		sf.rwMux.Unlock()
		return
	}
	ctx, sf.closeCancel = context.WithCancel(context.Background())
	sf.rwMux.Unlock()
	defer sf.setConnectStatus(initial)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		sf.Debug("connecting server %+v", sf.option.server)
		conn, err := openConnection(sf.option.server, sf.option.TLSConfig, sf.option.config.ConnectTimeout0)
		if err != nil {
			sf.Error("connect failed, %v", err)
			if !sf.option.autoReconnect {
				return
			}
			time.Sleep(sf.option.reconnectInterval)
			continue
		}
		sf.Debug("connect success")
		sf.run(ctx, conn)

		sf.Debug("disconnected server %+v", sf.option.server)
		select {
		case <-ctx.Done():
			return
		default:
			// 随机500ms-1s的重试，避免快速重试造成服务器许多无效连接
			time.Sleep(time.Millisecond * time.Duration(500+rand.Intn(500)))
		}
	}
}

// handleUFrame implements connRole. The controlling station issues the
// activations, so it is the confirmations it has to act on.
func (sf *Client) handleUFrame(function byte) {
	switch function {
	case uStartDtConfirm:
		atomic.StoreUint32(&sf.isActive, active)
		sf.startDtActiveSendSince.Store(time.Time{})
	case uStopDtConfirm:
		atomic.StoreUint32(&sf.isActive, inactive)
		sf.stopDtActiveSendSince.Store(time.Time{})
	case uTestFrActive:
		sf.sendUFrame(uTestFrConfirm)
	case uTestFrConfirm:
		sf.testFrAliveSendSince = time.Time{}
	default:
		sf.Error("illegal U-Frame functions[0x%02x] ignored", function)
	}
}

// roleTimedOut implements connRole: t₁ also covers the STARTDT and STOPDT
// activations that only the controlling station sends.
func (sf *Client) roleTimedOut(now time.Time) bool {
	for _, v := range []*atomic.Value{&sf.startDtActiveSendSince, &sf.stopDtActiveSendSince} {
		since, _ := v.Load().(time.Time)
		if !since.IsZero() && now.Sub(since) >= sf.option.config.SendUnAckTimeout1 {
			sf.Error("start/stop data transfer confirm timeout t₁")
			return true
		}
	}
	return false
}

func (sf *Client) notifyUp()   { sf.onConnect(sf) }
func (sf *Client) notifyDown() { sf.onConnectionLost(sf) }

// dispatchASDU implements connRole, routing a received ASDU to the
// application's handler.
func (sf *Client) dispatchASDU(asduPack *asdu.ASDU) error {
	defer func() {
		if err := recover(); err != nil {
			sf.Critical("client handler %+v", err)
		}
	}()

	sf.Debug("ASDU %+v", asduPack)

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
	if !sf.IsActive() {
		return ErrNotActive
	}
	return sf.connection.Send(a)
}

// Close close all
func (sf *Client) Close() error {
	sf.rwMux.Lock()
	if sf.closeCancel != nil {
		sf.closeCancel()
	}
	sf.rwMux.Unlock()
	return nil
}

// SendStartDt start data transmission on this connection
func (sf *Client) SendStartDt() {
	sf.startDtActiveSendSince.Store(time.Now())
	sf.sendUFrame(uStartDtActive)
}

// SendStopDt stop data transmission on this connection
func (sf *Client) SendStopDt() {
	sf.stopDtActiveSendSince.Store(time.Now())
	sf.sendUFrame(uStopDtActive)
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
