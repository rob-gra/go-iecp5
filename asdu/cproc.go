// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package asdu

import (
	"time"
)

// ASDUs for process information in the control direction.

// SingleCommandInfo is a single-command information object.
type SingleCommandInfo struct {
	Ioa   InfoObjAddr
	Value bool
	Qoc   QualifierOfCommand
	Time  time.Time
}

// SingleCmd sends a type identification [C_SC_NA_1] or [C_SC_TA_1]: a single
// command. Only a single information object (SQ = 0).
// [C_SC_NA_1] See companion standard 101, subclass 7.3.2.1
// [C_SC_TA_1] See companion standard 101,
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
func SingleCmd(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr, cmd SingleCommandInfo) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})

	if err := u.AppendInfoObjAddr(cmd.Ioa); err != nil {
		return err
	}
	value := cmd.Qoc.Value()
	if cmd.Value {
		value |= 0x01
	}
	u.AppendBytes(value)
	switch typeID {
	case C_SC_NA_1:
	case C_SC_TA_1:
		u.AppendBytes(CP56Time2a(cmd.Time, u.InfoObjTimeZone)...)
	default:
		return ErrTypeIDNotMatch
	}
	return c.Send(u)
}

// DoubleCommandInfo is a double-command information object.
type DoubleCommandInfo struct {
	Ioa   InfoObjAddr
	Value DoubleCommand
	Qoc   QualifierOfCommand
	Time  time.Time
}

// DoubleCmd sends a type identification [C_DC_NA_1] or [C_DC_TA_1]: a double
// command. Only a single information object (SQ = 0).
// [C_DC_NA_1] See companion standard 101, subclass 7.3.2.2
// [C_DC_TA_1] See companion standard 101,
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
func DoubleCmd(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr,
	cmd DoubleCommandInfo) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}
	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})

	if err := u.AppendInfoObjAddr(cmd.Ioa); err != nil {
		return err
	}

	u.AppendBytes(cmd.Qoc.Value() | byte(cmd.Value&0x03))
	switch typeID {
	case C_DC_NA_1:
	case C_DC_TA_1:
		u.AppendBytes(CP56Time2a(cmd.Time, u.InfoObjTimeZone)...)
	default:
		return ErrTypeIDNotMatch
	}
	return c.Send(u)
}

// StepCommandInfo is a regulating-step-command information object.
type StepCommandInfo struct {
	Ioa   InfoObjAddr
	Value StepCommand
	Qoc   QualifierOfCommand
	Time  time.Time
}

// StepCmd sends a type [C_RC_NA_1] or [C_RC_TA_1]: a regulating step
// command. Only a single information object (SQ = 0).
// [C_RC_NA_1] See companion standard 101, subclass 7.3.2.3
// [C_RC_TA_1] See companion standard 101,
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
func StepCmd(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr, cmd StepCommandInfo) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}
	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})

	if err := u.AppendInfoObjAddr(cmd.Ioa); err != nil {
		return err
	}

	u.AppendBytes(cmd.Qoc.Value() | byte(cmd.Value&0x03))
	switch typeID {
	case C_RC_NA_1:
	case C_RC_TA_1:
		u.AppendBytes(CP56Time2a(cmd.Time, u.InfoObjTimeZone)...)
	default:
		return ErrTypeIDNotMatch
	}
	return c.Send(u)
}

// SetpointCommandNormalInfo is a set-point command information object,
// normalized value.
type SetpointCommandNormalInfo struct {
	Ioa   InfoObjAddr
	Value Normalize
	Qos   QualifierOfSetpointCmd
	Time  time.Time
}

// SetpointCmdNormal sends a type [C_SE_NA_1] or [C_SE_TA_1]: a set-point
// command, normalized value. Only a single information object (SQ = 0).
// [C_SE_NA_1] See companion standard 101, subclass 7.3.2.4
// [C_SE_TA_1] See companion standard 101,
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
func SetpointCmdNormal(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr, cmd SetpointCommandNormalInfo) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}
	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})

	if err := u.AppendInfoObjAddr(cmd.Ioa); err != nil {
		return err
	}
	u.AppendNormalize(cmd.Value).AppendBytes(cmd.Qos.Value())
	switch typeID {
	case C_SE_NA_1:
	case C_SE_TA_1:
		u.AppendBytes(CP56Time2a(cmd.Time, u.InfoObjTimeZone)...)
	default:
		return ErrTypeIDNotMatch
	}
	return c.Send(u)
}

// SetpointCommandScaledInfo is a set-point command information object,
// scaled value.
type SetpointCommandScaledInfo struct {
	Ioa   InfoObjAddr
	Value int16
	Qos   QualifierOfSetpointCmd
	Time  time.Time
}

// SetpointCmdScaled sends a type [C_SE_NB_1] or [C_SE_TB_1]: a set-point
// command, scaled value. Only a single information object (SQ = 0).
// [C_SE_NB_1] See companion standard 101, subclass 7.3.2.5
// [C_SE_TB_1] See companion standard 101,
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
func SetpointCmdScaled(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr, cmd SetpointCommandScaledInfo) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}
	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})

	if err := u.AppendInfoObjAddr(cmd.Ioa); err != nil {
		return err
	}
	u.AppendScaled(cmd.Value).AppendBytes(cmd.Qos.Value())
	switch typeID {
	case C_SE_NB_1:
	case C_SE_TB_1:
		u.AppendBytes(CP56Time2a(cmd.Time, u.InfoObjTimeZone)...)
	default:
		return ErrTypeIDNotMatch
	}
	return c.Send(u)
}

// SetpointCommandFloatInfo is a set-point command information object, short
// floating point number.
type SetpointCommandFloatInfo struct {
	Ioa   InfoObjAddr
	Value float32
	Qos   QualifierOfSetpointCmd
	Time  time.Time
}

// SetpointCmdFloat sends a type [C_SE_NC_1] or [C_SE_TC_1]: a set-point
// command, short floating point number. Only a single information object
// (SQ = 0).
// [C_SE_NC_1] See companion standard 101, subclass 7.3.2.6
// [C_SE_TC_1] See companion standard 101,
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
func SetpointCmdFloat(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr, cmd SetpointCommandFloatInfo) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}
	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(cmd.Ioa); err != nil {
		return err
	}

	u.AppendFloat32(cmd.Value).AppendBytes(cmd.Qos.Value())

	switch typeID {
	case C_SE_NC_1:
	case C_SE_TC_1:
		u.AppendBytes(CP56Time2a(cmd.Time, u.InfoObjTimeZone)...)
	default:
		return ErrTypeIDNotMatch
	}

	return c.Send(u)
}

// BitsString32CommandInfo is a 32-bit bitstring command information object.
type BitsString32CommandInfo struct {
	Ioa   InfoObjAddr
	Value uint32
	Time  time.Time
}

// BitsString32Cmd sends a type [C_BO_NA_1] or [C_BO_TA_1]: a 32-bit
// bitstring command. Only a single information object (SQ = 0).
// [C_BO_NA_1] See companion standard 101, subclass 7.3.2.7
// [C_BO_TA_1] See companion standard 101,
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
func BitsString32Cmd(c Connect, typeID TypeID, coa CauseOfTransmission, commonAddr CommonAddr,
	cmd BitsString32CommandInfo) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}
	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		commonAddr,
	})
	if err := u.AppendInfoObjAddr(cmd.Ioa); err != nil {
		return err
	}

	u.AppendBitsString32(cmd.Value)

	switch typeID {
	case C_BO_NA_1:
	case C_BO_TA_1:
		u.AppendBytes(CP56Time2a(cmd.Time, u.InfoObjTimeZone)...)
	default:
		return ErrTypeIDNotMatch
	}

	return c.Send(u)
}

// GetSingleCmd returns the single-command information object of a
// [C_SC_NA_1] or [C_SC_TA_1].
func (sf *ASDU) GetSingleCmd() (SingleCommandInfo, error) {
	d := sf.decoder()
	var s SingleCommandInfo

	s.Ioa = d.readInfoObjAddr()
	value := d.readByte()
	s.Value = value&0x01 == 0x01
	s.Qoc = ParseQualifierOfCommand(value & 0xfe)

	switch sf.Type {
	case C_SC_NA_1:
	case C_SC_TA_1:
		s.Time = d.readCP56Time2a()
	default:
		return SingleCommandInfo{}, ErrTypeIDNotMatch
	}

	return s, d.err
}

// GetDoubleCmd returns the double-command information object of a
// [C_DC_NA_1] or [C_DC_TA_1].
func (sf *ASDU) GetDoubleCmd() (DoubleCommandInfo, error) {
	d := sf.decoder()
	var cmd DoubleCommandInfo

	cmd.Ioa = d.readInfoObjAddr()
	value := d.readByte()
	cmd.Value = DoubleCommand(value & 0x03)
	cmd.Qoc = ParseQualifierOfCommand(value & 0xfc)

	switch sf.Type {
	case C_DC_NA_1:
	case C_DC_TA_1:
		cmd.Time = d.readCP56Time2a()
	default:
		return DoubleCommandInfo{}, ErrTypeIDNotMatch
	}

	return cmd, d.err
}

// GetStepCmd returns the regulating-step-command information object of a
// [C_RC_NA_1] or [C_RC_TA_1].
func (sf *ASDU) GetStepCmd() (StepCommandInfo, error) {
	d := sf.decoder()
	var cmd StepCommandInfo

	cmd.Ioa = d.readInfoObjAddr()
	value := d.readByte()
	cmd.Value = StepCommand(value & 0x03)
	cmd.Qoc = ParseQualifierOfCommand(value & 0xfc)

	switch sf.Type {
	case C_RC_NA_1:
	case C_RC_TA_1:
		cmd.Time = d.readCP56Time2a()
	default:
		return StepCommandInfo{}, ErrTypeIDNotMatch
	}

	return cmd, d.err
}

// GetSetpointNormalCmd returns the set-point command information object,
// normalized value, of a [C_SE_NA_1] or [C_SE_TA_1].
func (sf *ASDU) GetSetpointNormalCmd() (SetpointCommandNormalInfo, error) {
	d := sf.decoder()
	var cmd SetpointCommandNormalInfo

	cmd.Ioa = d.readInfoObjAddr()
	cmd.Value = d.readNormalize()
	cmd.Qos = ParseQualifierOfSetpointCmd(d.readByte())

	switch sf.Type {
	case C_SE_NA_1:
	case C_SE_TA_1:
		cmd.Time = d.readCP56Time2a()
	default:
		return SetpointCommandNormalInfo{}, ErrTypeIDNotMatch
	}

	return cmd, d.err
}

// GetSetpointCmdScaled returns the set-point command information object,
// scaled value, of a [C_SE_NB_1] or [C_SE_TB_1].
func (sf *ASDU) GetSetpointCmdScaled() (SetpointCommandScaledInfo, error) {
	d := sf.decoder()
	var cmd SetpointCommandScaledInfo

	cmd.Ioa = d.readInfoObjAddr()
	cmd.Value = d.readScaled()
	cmd.Qos = ParseQualifierOfSetpointCmd(d.readByte())

	switch sf.Type {
	case C_SE_NB_1:
	case C_SE_TB_1:
		cmd.Time = d.readCP56Time2a()
	default:
		return SetpointCommandScaledInfo{}, ErrTypeIDNotMatch
	}

	return cmd, d.err
}

// GetSetpointFloatCmd returns the set-point command information object,
// short floating point number, of a [C_SE_NC_1] or [C_SE_TC_1].
func (sf *ASDU) GetSetpointFloatCmd() (SetpointCommandFloatInfo, error) {
	d := sf.decoder()
	var cmd SetpointCommandFloatInfo

	cmd.Ioa = d.readInfoObjAddr()
	cmd.Value = d.readFloat32()
	cmd.Qos = ParseQualifierOfSetpointCmd(d.readByte())

	switch sf.Type {
	case C_SE_NC_1:
	case C_SE_TC_1:
		cmd.Time = d.readCP56Time2a()
	default:
		return SetpointCommandFloatInfo{}, ErrTypeIDNotMatch
	}

	return cmd, d.err
}

// GetBitsString32Cmd returns the 32-bit bitstring command information object
// of a [C_BO_NA_1] or [C_BO_TA_1].
func (sf *ASDU) GetBitsString32Cmd() (BitsString32CommandInfo, error) {
	d := sf.decoder()
	var cmd BitsString32CommandInfo

	cmd.Ioa = d.readInfoObjAddr()
	cmd.Value = d.readBitsString32()
	switch sf.Type {
	case C_BO_NA_1:
	case C_BO_TA_1:
		cmd.Time = d.readCP56Time2a()
	default:
		return BitsString32CommandInfo{}, ErrTypeIDNotMatch
	}

	return cmd, d.err
}
