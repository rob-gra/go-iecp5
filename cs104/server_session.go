// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"fmt"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

const (
	initial uint32 = iota
	disconnected
	connected
)

// SrvSession is one cs104 controlled station (slave) connection: the end
// that answers STARTDT/STOPDT rather than issuing it. The connection state
// machine itself is shared with Client, see connection.
type SrvSession struct {
	connection

	handler ServerHandlerInterface

	onConnection   func(asdu.Connect)
	connectionLost func(asdu.Connect)

	// onActivate, if set, is called (with no lock held) whenever this
	// session transitions from inactive to active, i.e. it just confirmed
	// STARTDT. Server uses it to enforce redundancy groups: see
	// Server.handleSessionActivated.
	onActivate func(*SrvSession)
	// redundancyGroupKey groups this session together with other sessions
	// sharing the same non-nil key: see Server.groupKeyFor. A nil key means
	// this session isn't part of any redundancy group.
	redundancyGroupKey any
	// commonAddrFilter, if set, decides whether this session is responsible
	// for a given common address (station address); see
	// Server.SetCommonAddrFilter/AllowCommonAddrs. Nil means every CA other
	// than the invalid marker (0) is accepted.
	commonAddrFilter func(asdu.CommonAddr) bool
}

// handleUFrame implements connRole. The controlled station answers the
// activations its peer sends; it never issues them itself.
func (sf *SrvSession) handleUFrame(function byte) {
	switch function {
	case uStartDtActive:
		sf.sendUFrame(uStartDtConfirm)
		if !sf.isActive.Swap(true) && sf.onActivate != nil {
			sf.onActivate(sf)
		}
	case uStopDtActive:
		sf.sendUFrame(uStopDtConfirm)
		sf.isActive.Store(false)
	case uTestFrActive:
		sf.sendUFrame(uTestFrConfirm)
	case uTestFrConfirm:
		sf.testFrAliveSendSince = time.Time{}
	default:
		sf.log.Error("ignoring illegal U-frame function", "function", fmt.Sprintf("0x%02x", function))
	}
}

// roleTimedOut implements connRole. The controlled station sends no
// activation of its own, so it has no confirmation of its own to wait for.
func (sf *SrvSession) roleTimedOut(time.Time) bool { return false }

func (sf *SrvSession) notifyUp() {
	if sf.onConnection != nil {
		sf.onConnection(sf)
	}
}

func (sf *SrvSession) notifyDown() {
	if sf.connectionLost != nil {
		sf.connectionLost(sf)
	}
}

// commonAddrAllowed reports whether ca should be processed by this session:
// never the invalid marker (0), and either the broadcast address
// (asdu.GlobalCommonAddr, always accepted since it isn't something a single
// station owns) or accepted by the configured commonAddrFilter. With no
// filter configured, every CA other than the invalid marker is accepted.
func (sf *SrvSession) commonAddrAllowed(ca asdu.CommonAddr) bool {
	if ca == asdu.InvalidCommonAddr {
		return false
	}
	if ca == asdu.GlobalCommonAddr {
		return true
	}
	if sf.commonAddrFilter == nil {
		return true
	}
	return sf.commonAddrFilter(ca)
}

// dispatchASDU implements connRole: it validates the ASDU against what this
// station accepts, then routes it to the application handler.
//
// A malformed information object gets an UnknownIOA reply rather than
// being passed to the handler: the peer sent something this station cannot
// act on, and saying so is more use to it than silence.
func (sf *SrvSession) dispatchASDU(asduPack *asdu.ASDU) error {
	defer func() {
		if err := recover(); err != nil {
			sf.log.Error("server handler panicked", "panic", err, "type", asduPack.Identifier.Type)
		}
	}()

	if sf.debugEnabled() {
		sf.log.Debug("dispatching ASDU", "asdu", asduPack.String())
	}

	if !sf.commonAddrAllowed(asduPack.CommonAddr) {
		return asduPack.SendReplyMirror(sf, asdu.UnknownCA)
	}

	switch asduPack.Identifier.Type {
	case asdu.C_IC_NA_1: // InterrogationCmd
		if !(asduPack.Identifier.Coa.Cause == asdu.Activation ||
			asduPack.Identifier.Coa.Cause == asdu.Deactivation) {
			return asduPack.SendReplyMirror(sf, asdu.UnknownCOT)
		}
		ioa, qoi, err := asduPack.GetInterrogationCmd()
		if err != nil || ioa != asdu.InfoObjAddrIrrelevant {
			return asduPack.SendReplyMirror(sf, asdu.UnknownIOA)
		}
		return sf.handler.InterrogationHandler(sf, asduPack, qoi)

	case asdu.C_CI_NA_1: // CounterInterrogationCmd
		if asduPack.Identifier.Coa.Cause != asdu.Activation {
			return asduPack.SendReplyMirror(sf, asdu.UnknownCOT)
		}
		ioa, qcc, err := asduPack.GetCounterInterrogationCmd()
		if err != nil || ioa != asdu.InfoObjAddrIrrelevant {
			return asduPack.SendReplyMirror(sf, asdu.UnknownIOA)
		}
		return sf.handler.CounterInterrogationHandler(sf, asduPack, qcc)

	case asdu.C_RD_NA_1: // ReadCmd
		if asduPack.Identifier.Coa.Cause != asdu.Request {
			return asduPack.SendReplyMirror(sf, asdu.UnknownCOT)
		}
		ioa, err := asduPack.GetReadCmd()
		if err != nil {
			return asduPack.SendReplyMirror(sf, asdu.UnknownIOA)
		}
		return sf.handler.ReadHandler(sf, asduPack, ioa)

	case asdu.C_CS_NA_1: // ClockSynchronizationCmd
		if asduPack.Identifier.Coa.Cause != asdu.Activation {
			return asduPack.SendReplyMirror(sf, asdu.UnknownCOT)
		}
		ioa, tm, err := asduPack.GetClockSynchronizationCmd()
		if err != nil || ioa != asdu.InfoObjAddrIrrelevant {
			return asduPack.SendReplyMirror(sf, asdu.UnknownIOA)
		}
		return sf.handler.ClockSyncHandler(sf, asduPack, tm)

	case asdu.C_TS_NA_1: // TestCommand
		if asduPack.Identifier.Coa.Cause != asdu.Activation {
			return asduPack.SendReplyMirror(sf, asdu.UnknownCOT)
		}
		ioa, _, err := asduPack.GetTestCommand()
		if err != nil || ioa != asdu.InfoObjAddrIrrelevant {
			return asduPack.SendReplyMirror(sf, asdu.UnknownIOA)
		}
		return asduPack.SendReplyMirror(sf, asdu.ActivationCon)

	case asdu.C_RP_NA_1: // ResetProcessCmd
		if asduPack.Identifier.Coa.Cause != asdu.Activation {
			return asduPack.SendReplyMirror(sf, asdu.UnknownCOT)
		}
		ioa, qrp, err := asduPack.GetResetProcessCmd()
		if err != nil || ioa != asdu.InfoObjAddrIrrelevant {
			return asduPack.SendReplyMirror(sf, asdu.UnknownIOA)
		}
		return sf.handler.ResetProcessHandler(sf, asduPack, qrp)

	case asdu.C_CD_NA_1: // DelayAcquireCommand
		if !(asduPack.Identifier.Coa.Cause == asdu.Activation ||
			asduPack.Identifier.Coa.Cause == asdu.Spontaneous) {
			return asduPack.SendReplyMirror(sf, asdu.UnknownCOT)
		}
		ioa, msec, err := asduPack.GetDelayAcquireCommand()
		if err != nil || ioa != asdu.InfoObjAddrIrrelevant {
			return asduPack.SendReplyMirror(sf, asdu.UnknownIOA)
		}
		return sf.handler.DelayAcquisitionHandler(sf, asduPack, msec)
	}

	if err := sf.handler.ASDUHandler(sf, asduPack); err != nil {
		return asduPack.SendReplyMirror(sf, asdu.UnknownTypeID)
	}
	return nil
}
