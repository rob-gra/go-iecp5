// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package asdu

import (
	"time"
)

// ASDUs for system information in the control direction.

// InterrogationCmd sends an interrogation command [C_IC_NA_1]. Only a single
// information object (SQ = 0).
// [C_IC_NA_1] See companion standard 101, subclass 7.3.4.1
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// <8> := deactivation
// monitor direction:
// <7> := activation confirmation
// <9> := deactivation confirmation
// <10> := activation termination
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func InterrogationCmd(c Connect, coa CauseOfTransmission, ca CommonAddr, qoi QualifierOfInterrogation) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		C_IC_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(InfoObjAddrIrrelevant); err != nil {
		return err
	}
	u.AppendBytes(byte(qoi))
	return c.Send(u)
}

// CounterInterrogationCmd sends a counter interrogation command
// [C_CI_NA_1]. Only a single information object (SQ = 0).
// [C_CI_NA_1] See companion standard 101, subclass 7.3.4.2
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <10> := activation termination
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func CounterInterrogationCmd(c Connect, coa CauseOfTransmission, ca CommonAddr, qcc QualifierCountCall) error {
	if err := c.Params().Valid(); err != nil {
		return err
	}
	coa.Cause = Activation
	u := NewASDU(c.Params(), Identifier{
		C_CI_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(InfoObjAddrIrrelevant); err != nil {
		return err
	}
	u.AppendBytes(qcc.Value())
	return c.Send(u)
}

// ReadCmd sends a read command [C_RD_NA_1]. Only a single information object
// (SQ = 0).
// [C_RD_NA_1] See companion standard 101, subclass 7.3.4.3
// Cause of transmission (COT) is used for:
// control direction:
// <5> := request or requested
// monitor direction:
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func ReadCmd(c Connect, coa CauseOfTransmission, ca CommonAddr, ioa InfoObjAddr) error {
	if err := c.Params().Valid(); err != nil {
		return err
	}
	coa.Cause = Request
	u := NewASDU(c.Params(), Identifier{
		C_RD_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(ioa); err != nil {
		return err
	}
	return c.Send(u)
}

// ClockSynchronizationCmd sends a clock synchronization command
// [C_CS_NA_1]. Only a single information object (SQ = 0).
// [C_CS_NA_1] See companion standard 101, subclass 7.3.4.4
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <10> := activation termination
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func ClockSynchronizationCmd(c Connect, coa CauseOfTransmission, ca CommonAddr, t time.Time) error {
	if err := c.Params().Valid(); err != nil {
		return err
	}
	coa.Cause = Activation
	u := NewASDU(c.Params(), Identifier{
		C_CS_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(InfoObjAddrIrrelevant); err != nil {
		return err
	}
	u.AppendBytes(CP56Time2a(t, u.InfoObjTimeZone)...)
	return c.Send(u)
}

// TestCommand sends a test command [C_TS_NA_1]. Only a single information
// object (SQ = 0).
// [C_TS_NA_1] See companion standard 101, subclass 7.3.4.5
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func TestCommand(c Connect, coa CauseOfTransmission, ca CommonAddr) error {
	if err := c.Params().Valid(); err != nil {
		return err
	}
	coa.Cause = Activation
	u := NewASDU(c.Params(), Identifier{
		C_TS_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(InfoObjAddrIrrelevant); err != nil {
		return err
	}
	u.AppendBytes(byte(FBPTestWord&0xff), byte(FBPTestWord>>8))
	return c.Send(u)
}

// ResetProcessCmd sends a reset process command [C_RP_NA_1]. Only a single
// information object (SQ = 0).
// [C_RP_NA_1] See companion standard 101, subclass 7.3.4.6
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func ResetProcessCmd(c Connect, coa CauseOfTransmission, ca CommonAddr, qrp QualifierOfResetProcessCmd) error {
	if err := c.Params().Valid(); err != nil {
		return err
	}
	coa.Cause = Activation
	u := NewASDU(c.Params(), Identifier{
		C_RP_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(InfoObjAddrIrrelevant); err != nil {
		return err
	}
	u.AppendBytes(byte(qrp))
	return c.Send(u)
}

// DelayAcquireCommand sends a delay acquisition command [C_CD_NA_1]. Only a
// single information object (SQ = 0).
// [C_CD_NA_1] See companion standard 101, subclass 7.3.4.7
// Cause of transmission (COT) is used for:
// control direction:
// <3> := spontaneous
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func DelayAcquireCommand(c Connect, coa CauseOfTransmission, ca CommonAddr, msec uint16) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Activation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		C_CD_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(InfoObjAddrIrrelevant); err != nil {
		return err
	}
	u.AppendCP16Time2a(msec)
	return c.Send(u)
}

// TestCommandCP56Time2a sends a test command with a CP56Time2a time tag
// [C_TS_TA_1]. Only a single information object (SQ = 0).
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func TestCommandCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, t time.Time) error {
	if err := c.Params().Valid(); err != nil {
		return err
	}
	u := NewASDU(c.Params(), Identifier{
		C_TS_TA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(InfoObjAddrIrrelevant); err != nil {
		return err
	}
	u.AppendUint16(FBPTestWord)
	u.AppendCP56Time2a(t, u.InfoObjTimeZone)
	return c.Send(u)
}

// GetInterrogationCmd returns the interrogation command information object
// of a [C_IC_NA_1]: information object address and qualifier of
// interrogation.
func (sf *ASDU) GetInterrogationCmd() (InfoObjAddr, QualifierOfInterrogation, error) {
	d := sf.decoder()
	ioa := d.readInfoObjAddr()
	return ioa, QualifierOfInterrogation(d.readByte()), d.err
}

// GetCounterInterrogationCmd returns the counter interrogation information
// object of a [C_CI_NA_1]: information object address and qualifier of
// counter interrogation.
func (sf *ASDU) GetCounterInterrogationCmd() (InfoObjAddr, QualifierCountCall, error) {
	d := sf.decoder()
	ioa := d.readInfoObjAddr()
	return ioa, ParseQualifierCountCall(d.readByte()), d.err
}

// GetReadCmd returns the information object address of a read command
// [C_RD_NA_1].
func (sf *ASDU) GetReadCmd() (InfoObjAddr, error) {
	d := sf.decoder()
	return d.readInfoObjAddr(), d.err
}

// GetClockSynchronizationCmd returns the clock synchronization information
// object of a [C_CS_NA_1]: information object address and time.
func (sf *ASDU) GetClockSynchronizationCmd() (InfoObjAddr, time.Time, error) {
	d := sf.decoder()

	return d.readInfoObjAddr(), d.readCP56Time2a(), d.err
}

// GetTestCommand returns the test command information object of a
// [C_TS_NA_1]: information object address and whether the fixed test
// pattern matched.
func (sf *ASDU) GetTestCommand() (InfoObjAddr, bool, error) {
	d := sf.decoder()
	return d.readInfoObjAddr(), d.readUint16() == FBPTestWord, d.err
}

// GetResetProcessCmd returns the reset process command information object of
// a [C_RP_NA_1]: information object address and qualifier of reset process
// command.
func (sf *ASDU) GetResetProcessCmd() (InfoObjAddr, QualifierOfResetProcessCmd, error) {
	d := sf.decoder()
	ioa := d.readInfoObjAddr()
	return ioa, QualifierOfResetProcessCmd(d.readByte()), d.err
}

// GetDelayAcquireCommand returns the delay acquisition command information
// object of a [C_CD_NA_1]: information object address and delay in
// milliseconds.
func (sf *ASDU) GetDelayAcquireCommand() (InfoObjAddr, uint16, error) {
	d := sf.decoder()
	return d.readInfoObjAddr(), d.readUint16(), d.err
}

// GetTestCommandCP56Time2a returns the test command information object of a
// [C_TS_TA_1]: information object address and whether the fixed test
// pattern matched.
func (sf *ASDU) GetTestCommandCP56Time2a() (InfoObjAddr, bool, time.Time, error) {
	d := sf.decoder()
	return d.readInfoObjAddr(), d.readUint16() == FBPTestWord, d.readCP56Time2a(), d.err
}
