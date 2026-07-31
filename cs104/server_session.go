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
	case UStartDtActive:
		// A peer that restarts data transfer is no longer waiting to be told
		// it stopped; releasing the withheld confirmation now would answer a
		// request it has already moved on from.
		sf.stopDtPending = false
		sf.sendUFrame(UStartDtConfirm)
		if !sf.isActive.Swap(true) && sf.onActivate != nil {
			sf.onActivate(sf)
		}
	case UStopDtActive:
		// Data transfer stops at once, but the confirmation may have to wait.
		sf.isActive.Store(false)

		// Acknowledge what has been received but not yet confirmed. The w
		// window and t₂ would get to it eventually, but the peer is entitled
		// to see the connection settle promptly once it has asked it to stop,
		// and t₂ can be seconds away. lib60870 sends this S-frame here too.
		if sf.ackNoRcv != sf.seqNoRcv {
			sf.sendSFrame(sf.seqNoRcv)
			sf.ackNoRcv = sf.seqNoRcv
		}

		// Withhold the confirmation while I-frames this end sent are still
		// unacknowledged: it would tell the peer the connection is quiesced
		// when it is not. confirmStopDtIfSettled releases it once the peer's
		// acknowledgement arrives.
		if sf.ackNoSend != sf.seqNoSend {
			sf.stopDtPending = true
			sf.log.Debug("STOPDT confirmation withheld: I-frames still unacknowledged",
				"unacked", seqNoCount(sf.ackNoSend, sf.seqNoSend))
			return
		}
		sf.sendUFrame(UStopDtConfirm)
	case UTestFrActive:
		sf.sendUFrame(UTestFrConfirm)
	case UTestFrConfirm:
		sf.testFrAliveSendSince = time.Time{}
	default:
		sf.log.Error("ignoring illegal U-frame function", "function", fmt.Sprintf("0x%02x", function))
	}
}

// roleTimedOut implements connRole. The controlled station sends no
// activation of its own, so it has no confirmation of its own to wait for.
func (sf *SrvSession) roleTimedOut(time.Time) bool { return false }

// roleCleanUp implements connRole. Nothing to reset: the controlled station
// runs no timer of its own, per roleTimedOut.
func (sf *SrvSession) roleCleanUp() {}

// sFrameWhileStoppedIsFatal implements connRole. The controlled station is
// the one the standard's state machine constrains here, and lib60870's does
// the same.
func (sf *SrvSession) sFrameWhileStoppedIsFatal() bool { return true }

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
func (sf *SrvSession) dispatchASDU(asduPack *asdu.ASDU) (err error) {
	defer func() {
		if r := recover(); r != nil {
			sf.log.Error("server handler panicked", "panic", r, "type", asduPack.Identifier.Type)
			// Named return, so the panic surfaces as a failure rather than
			// as the nil an unnamed return would leave behind -- which would
			// make a handler that blew up indistinguishable from one that
			// succeeded.
			err = fmt.Errorf("server handler panicked: %v", r)
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
