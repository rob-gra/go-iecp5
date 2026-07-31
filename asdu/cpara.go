// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package asdu

// ASDUs for parameters in the control direction.

// ParameterNormalInfo is a parameter of measured value information object,
// normalized value.
type ParameterNormalInfo struct {
	Ioa   InfoObjAddr
	Value Normalize
	Qpm   QualifierOfParameterMV
}

// ParameterNormal sends a parameter of measured value, normalized value
// [P_ME_NA_1]. Only a single information object (SQ = 0).
// [P_ME_NA_1], See companion standard 101, subclass 7.3.5.1
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// <22> := interrogated by group 2 interrogation
// through
// <36> := interrogated by group 16 interrogation
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func ParameterNormal(c Connect, coa CauseOfTransmission, ca CommonAddr, p ParameterNormalInfo) error {
	if coa.Cause != Activation {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		P_ME_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(p.Ioa); err != nil {
		return err
	}
	u.AppendNormalize(p.Value)
	u.AppendBytes(p.Qpm.Value())
	return c.Send(u)
}

// ParameterScaledInfo is a parameter of measured value information object,
// scaled value.
type ParameterScaledInfo struct {
	Ioa   InfoObjAddr
	Value int16
	Qpm   QualifierOfParameterMV
}

// ParameterScaled sends a parameter of measured value, scaled value
// [P_ME_NB_1]. Only a single information object (SQ = 0).
// [P_ME_NB_1], See companion standard 101, subclass 7.3.5.2
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// <22> := interrogated by group 2 interrogation
// through
// <36> := interrogated by group 16 interrogation
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func ParameterScaled(c Connect, coa CauseOfTransmission, ca CommonAddr, p ParameterScaledInfo) error {
	if coa.Cause != Activation {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		P_ME_NB_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(p.Ioa); err != nil {
		return err
	}
	u.AppendScaled(p.Value).AppendBytes(p.Qpm.Value())
	return c.Send(u)
}

// ParameterFloatInfo is a parameter of measured value information object,
// short floating point number.
type ParameterFloatInfo struct {
	Ioa   InfoObjAddr
	Value float32
	Qpm   QualifierOfParameterMV
}

// ParameterFloat sends a parameter of measured value, short floating point
// number [P_ME_NC_1]. Only a single information object (SQ = 0).
// [P_ME_NC_1], See companion standard 101, subclass 7.3.5.3
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// monitor direction:
// <7> := activation confirmation
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// <22> := interrogated by group 2 interrogation
// through
// <36> := interrogated by group 16 interrogation
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func ParameterFloat(c Connect, coa CauseOfTransmission, ca CommonAddr, p ParameterFloatInfo) error {
	if coa.Cause != Activation {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		P_ME_NC_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(p.Ioa); err != nil {
		return err
	}
	u.AppendFloat32(p.Value).AppendBytes(p.Qpm.Value())
	return c.Send(u)
}

// ParameterActivationInfo is a parameter activation information object.
type ParameterActivationInfo struct {
	Ioa InfoObjAddr
	Qpa QualifierOfParameterAct
}

// ParameterActivation sends a parameter activation [P_AC_NA_1]. Only a
// single information object (SQ = 0).
// [P_AC_NA_1], See companion standard 101, subclass 7.3.5.4
// Cause of transmission (COT) is used for:
// control direction:
// <6> := activation
// <8> := deactivation
// monitor direction:
// <7> := activation confirmation
// <9> := deactivation confirmation
// <44> := unknown type identification
// <45> := unknown cause of transmission
// <46> := unknown common address of ASDU
// <47> := unknown information object address
func ParameterActivation(c Connect, coa CauseOfTransmission, ca CommonAddr, p ParameterActivationInfo) error {
	if !(coa.Cause == Activation || coa.Cause == Deactivation) {
		return ErrCmdCause
	}
	if err := c.Params().Valid(); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		P_AC_NA_1,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})
	if err := u.AppendInfoObjAddr(p.Ioa); err != nil {
		return err
	}
	u.AppendBytes(byte(p.Qpa))
	return c.Send(u)
}

// GetParameterNormal returns the parameter of measured value information
// object, normalized value, of a [P_ME_NA_1].
func (sf *ASDU) GetParameterNormal() (ParameterNormalInfo, error) {
	d := sf.decoder()
	// Decoded in explicit statements rather than inline in the composite
	// literal: each call advances the infoObj cursor, and Go only orders
	// function calls within an expression -- an index expression like
	// sf.infoObj[0] is unordered relative to them, so reading the trailing
	// qualifier that way could observe the cursor either before or after
	// the fields ahead of it were consumed.
	ioa := d.readInfoObjAddr()
	value := d.readNormalize()
	return ParameterNormalInfo{
		Ioa:   ioa,
		Value: value,
		Qpm:   ParseQualifierOfParamMV(d.readByte()),
	}, d.err
}

// GetParameterScaled returns the parameter of measured value information
// object, scaled value, of a [P_ME_NB_1].
func (sf *ASDU) GetParameterScaled() (ParameterScaledInfo, error) {
	d := sf.decoder()
	ioa := d.readInfoObjAddr()
	value := d.readScaled()
	return ParameterScaledInfo{
		Ioa:   ioa,
		Value: value,
		Qpm:   ParseQualifierOfParamMV(d.readByte()),
	}, d.err
}

// GetParameterFloat returns the parameter of measured value information
// object, short floating point number, of a [P_ME_NC_1].
func (sf *ASDU) GetParameterFloat() (ParameterFloatInfo, error) {
	d := sf.decoder()
	ioa := d.readInfoObjAddr()
	value := d.readFloat32()
	return ParameterFloatInfo{
		Ioa:   ioa,
		Value: value,
		Qpm:   ParseQualifierOfParamMV(d.readByte()),
	}, d.err
}

// GetParameterActivation returns the parameter activation information object
// of a [P_AC_NA_1].
func (sf *ASDU) GetParameterActivation() (ParameterActivationInfo, error) {
	d := sf.decoder()
	ioa := d.readInfoObjAddr()
	return ParameterActivationInfo{
		Ioa: ioa,
		Qpa: QualifierOfParameterAct(d.readByte()),
	}, d.err
}
