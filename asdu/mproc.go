// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package asdu

import (
	"time"
)

// ASDUs for process information in the monitor direction.

// checkValid check common parameter of request is valid
func checkValid(c Connect, typeID TypeID, isSequence bool, infosLen int) error {
	if infosLen == 0 {
		return ErrNotAnyObjInfo
	}
	objSize, err := GetInfoObjSize(typeID)
	if err != nil {
		return err
	}
	param := c.Params()
	if err := param.Valid(); err != nil {
		return err
	}

	var asduLen int
	if isSequence {
		asduLen = param.IdentifierSize() + infosLen*objSize + param.InfoObjAddrSize
	} else {
		asduLen = param.IdentifierSize() + infosLen*(objSize+param.InfoObjAddrSize)
	}

	if asduLen > ASDUSizeMax {
		return ErrLengthOutOfRange
	}
	return nil
}

// SinglePointInfo the measured value attributes.
type SinglePointInfo struct {
	Ioa InfoObjAddr
	// value of single point
	Value bool
	// Quality descriptor asdu.OK means no remarks.
	Qds QualityDescriptor
	// the type does not include timing will ignore
	Time time.Time
}

// single sends a type identification [M_SP_NA_1], [M_SP_TA_1] or [M_SP_TB_1]: single-point information.
// [M_SP_NA_1] See companion standard 101,subclass 7.3.1.1
// [M_SP_TA_1] See companion standard 101,subclass 7.3.1.2
// [M_SP_TB_1] See companion standard 101,subclass 7.3.1.22
func single(c Connect, typeID TypeID, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...SinglePointInfo) error {
	if err := checkValid(c, typeID, isSequence, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	once := false
	for _, v := range infos {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}

		value := byte(0)
		if v.Value {
			value = 0x01
		}
		u.AppendBytes(value | byte(v.Qds&0xf0))
		switch typeID {
		case M_SP_NA_1:
		case M_SP_TA_1:
			u.AppendBytes(CP24Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_SP_TB_1:
			u.AppendBytes(CP56Time2a(v.Time, u.InfoObjTimeZone)...)
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// Single sends a type identification [M_SP_NA_1]: single-point information without a time tag.
// [M_SP_NA_1] See companion standard 101,subclass 7.3.1.1
// Cause of transmission (COT) is used for:
// monitor direction:
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func Single(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...SinglePointInfo) error {
	if !(coa.Cause == Background || coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	return single(c, M_SP_NA_1, isSequence, coa, ca, infos...)
}

// SingleCP24Time2a sends a type identification [M_SP_TA_1]: single-point
// information with a CP24Time2a time tag. Only a single set of information
// elements (SQ = 0).
// [M_SP_TA_1] See companion standard 101,subclass 7.3.1.2
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
func SingleCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...SinglePointInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal) {
		return ErrCmdCause
	}
	return single(c, M_SP_TA_1, false, coa, ca, infos...)
}

// SingleCP56Time2a sends a type identification [M_SP_TB_1]: single-point
// information with a CP56Time2a time tag. Only a single set of information
// elements (SQ = 0).
// [M_SP_TB_1] See companion standard 101,subclass 7.3.1.22
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
func SingleCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...SinglePointInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal) {
		return ErrCmdCause
	}
	return single(c, M_SP_TB_1, false, coa, ca, infos...)
}

// DoublePointInfo the measured value attributes.
type DoublePointInfo struct {
	Ioa   InfoObjAddr
	Value DoublePoint
	// Quality descriptor asdu.QDSGood means no remarks.
	Qds QualityDescriptor
	// the type does not include timing will ignore
	Time time.Time
}

// double sends a type identification [M_DP_NA_1], [M_DP_TA_1] or [M_DP_TB_1]: double-point information.
// [M_DP_NA_1] See companion standard 101,subclass 7.3.1.3
// [M_DP_TA_1] See companion standard 101,subclass 7.3.1.4
// [M_DP_TB_1] See companion standard 101,subclass 7.3.1.23
func double(c Connect, typeID TypeID, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...DoublePointInfo) error {
	if err := checkValid(c, typeID, isSequence, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	once := false
	for _, v := range infos {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}

		u.AppendBytes(byte(v.Value&0x03) | byte(v.Qds&0xf0))
		switch typeID {
		case M_DP_NA_1:
		case M_DP_TA_1:
			u.AppendBytes(CP24Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_DP_TB_1:
			u.AppendBytes(CP56Time2a(v.Time, u.InfoObjTimeZone)...)
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// Double sends a type identification [M_DP_NA_1]: double-point information.
// [M_DP_NA_1] See companion standard 101,subclass 7.3.1.3
// Cause of transmission (COT) is used for:
// monitor direction:
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func Double(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...DoublePointInfo) error {
	if !(coa.Cause == Background || coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	return double(c, M_DP_NA_1, isSequence, coa, ca, infos...)
}

// DoubleCP24Time2a sends a type identification [M_DP_TA_1]: double-point
// information with a CP24Time2a time tag. Only a single set of information
// elements (SQ = 0).
// [M_DP_TA_1] See companion standard 101,subclass 7.3.1.4
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
func DoubleCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...DoublePointInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal) {
		return ErrCmdCause
	}
	return double(c, M_DP_TA_1, false, coa, ca, infos...)
}

// DoubleCP56Time2a sends a type identification [M_DP_TB_1]: double-point
// information with a CP56Time2a time tag. Only a single set of information
// elements (SQ = 0).
// [M_DP_TB_1] See companion standard 101,subclass 7.3.1.23
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
func DoubleCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...DoublePointInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal) {
		return ErrCmdCause
	}
	return double(c, M_DP_TB_1, false, coa, ca, infos...)
}

// StepPositionInfo the measured value attributes.
type StepPositionInfo struct {
	Ioa   InfoObjAddr
	Value StepPosition
	// Quality descriptor asdu.GOOD means no remarks.
	Qds QualityDescriptor
	// the type does not include timing will ignore
	Time time.Time
}

// step sends a type identification [M_ST_NA_1], [M_ST_TA_1] or [M_ST_TB_1]: step position information.
// [M_ST_NA_1] See companion standard 101, subclass 7.3.1.5
// [M_ST_TA_1] See companion standard 101, subclass 7.3.1.6
// [M_ST_TB_1] See companion standard 101, subclass 7.3.1.24
func step(c Connect, typeID TypeID, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...StepPositionInfo) error {
	if err := checkValid(c, typeID, isSequence, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	once := false
	for _, v := range infos {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}

		u.AppendBytes(v.Value.Value(), byte(v.Qds))
		switch typeID {
		case M_ST_NA_1:
		case M_ST_TA_1:
			u.AppendBytes(CP24Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_SP_TB_1:
			u.AppendBytes(CP56Time2a(v.Time, u.InfoObjTimeZone)...)
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// Step sends a type identification [M_ST_NA_1]: step position information.
// [M_ST_NA_1] See companion standard 101, subclass 7.3.1.5
// Cause of transmission (COT) is used for:
// monitor direction:
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func Step(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...StepPositionInfo) error {
	if !(coa.Cause == Background || coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	return step(c, M_ST_NA_1, isSequence, coa, ca, infos...)
}

// StepCP24Time2a sends a type identification [M_ST_TA_1]: step position
// information with a CP24Time2a time tag. Only a single set of information
// elements (SQ = 0).
// [M_ST_TA_1] See companion standard 101, subclass 7.3.1.5
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
func StepCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...StepPositionInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal) {
		return ErrCmdCause
	}
	return step(c, M_ST_TA_1, false, coa, ca, infos...)
}

// StepCP56Time2a sends a type identification [M_ST_TB_1]: step position
// information with a CP56Time2a time tag. Only a single set of information
// elements (SQ = 0).
// [M_ST_TB_1] See companion standard 101, subclass 7.3.1.24
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
func StepCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...StepPositionInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal) {
		return ErrCmdCause
	}
	return step(c, M_SP_TB_1, false, coa, ca, infos...)
}

// BitString32Info the measured value attributes.
type BitString32Info struct {
	Ioa   InfoObjAddr
	Value uint32
	// Quality descriptor asdu.GOOD means no remarks.
	Qds QualityDescriptor
	// the type does not include timing will ignore
	Time time.Time
}

// bitString32 sends a type identification [M_BO_NA_1], [M_BO_TA_1] or [M_BO_TB_1]: a bitstring of 32 bits.
// [M_ST_NA_1] See companion standard 101, subclass 7.3.1.7
// [M_ST_TA_1] See companion standard 101, subclass 7.3.1.8
// [M_ST_TB_1] See companion standard 101, subclass 7.3.1.25
func bitString32(c Connect, typeID TypeID, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...BitString32Info) error {
	if err := checkValid(c, typeID, isSequence, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	once := false
	for _, v := range infos {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}
		u.AppendBitsString32(v.Value).AppendBytes(byte(v.Qds))

		switch typeID {
		case M_BO_NA_1:
		case M_BO_TA_1:
			u.AppendBytes(CP24Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_BO_TB_1:
			u.AppendBytes(CP56Time2a(v.Time, u.InfoObjTimeZone)...)
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// BitString32 sends a type identification [M_BO_NA_1]: a bitstring of 32 bits.
// [M_ST_NA_1] See companion standard 101, subclass 7.3.1.7
// Cause of transmission (COT) is used for:
// monitor direction:
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func BitString32(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...BitString32Info) error {
	if !(coa.Cause == Background || coa.Cause == Spontaneous || coa.Cause == Request ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	return bitString32(c, M_BO_NA_1, isSequence, coa, ca, infos...)
}

// BitString32CP24Time2a sends a type identification [M_BO_TA_1]: a bitstring
// of 32 bits with a CP24Time2a time tag. Only a single set of information
// elements (SQ = 0).
// [M_ST_TA_1] See companion standard 101, subclass 7.3.1.8
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
func BitString32CP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...BitString32Info) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request) {
		return ErrCmdCause
	}
	return bitString32(c, M_BO_TA_1, false, coa, ca, infos...)
}

// BitString32CP56Time2a sends a type identification [M_BO_TB_1]: a bitstring
// of 32 bits with a CP56Time2a time tag. Only a single set of information
// elements (SQ = 0).
// [M_ST_TB_1] See companion standard 101, subclass 7.3.1.25
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
func BitString32CP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...BitString32Info) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request) {
		return ErrCmdCause
	}
	return bitString32(c, M_BO_TB_1, false, coa, ca, infos...)
}

// MeasuredValueNormalInfo the measured value attributes.
type MeasuredValueNormalInfo struct {
	Ioa   InfoObjAddr
	Value Normalize
	// Quality descriptor asdu.GOOD means no remarks.
	Qds QualityDescriptor
	// the type does not include timing will ignore
	Time time.Time
}

// measuredValueNormal sends a type identification [M_ME_NA_1], [M_ME_TA_1],
// [M_ME_TD_1] or [M_ME_ND_1]: a measured value, normalized.
// [M_ME_NA_1] See companion standard 101, subclass 7.3.1.9
// [M_ME_TA_1] See companion standard 101, subclass 7.3.1.10
// [M_ME_TD_1] See companion standard 101, subclass 7.3.1.26
// [M_ME_ND_1] See companion standard 101, subclass 7.3.1.21， The quality descriptor must default to asdu.GOOD
func measuredValueNormal(c Connect, typeID TypeID, isSequence bool, coa CauseOfTransmission, ca CommonAddr, attrs ...MeasuredValueNormalInfo) error {
	if err := checkValid(c, typeID, isSequence, len(attrs)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(attrs)); err != nil {
		return err
	}
	once := false
	for _, v := range attrs {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}
		u.AppendNormalize(v.Value)
		switch typeID {
		case M_ME_NA_1:
			u.AppendBytes(byte(v.Qds))
		case M_ME_TA_1:
			u.AppendBytes(byte(v.Qds)).AppendBytes(CP24Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_ME_TD_1:
			u.AppendBytes(byte(v.Qds)).AppendBytes(CP56Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_ME_ND_1: // without quality descriptor
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// MeasuredValueNormal sends a type identification [M_ME_NA_1]: a measured value, normalized.
// [M_ME_NA_1] See companion standard 101, subclass 7.3.1.9
// Cause of transmission (COT) is used for:
// monitor direction:
// <1> := periodic, cyclic
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func MeasuredValueNormal(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueNormalInfo) error {
	if !(coa.Cause == Periodic || coa.Cause == Background ||
		coa.Cause == Spontaneous || coa.Cause == Request ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	return measuredValueNormal(c, M_ME_NA_1, isSequence, coa, ca, infos...)
}

// MeasuredValueNormalCP24Time2a sends a type identification [M_ME_TA_1]: a
// measured value, normalized, with a CP24Time2a time tag. Only a single set
// of information elements (SQ = 0).
// [M_ME_TA_1] See companion standard 101, subclass 7.3.1.10
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
func MeasuredValueNormalCP24Time2a(c Connect, coa CauseOfTransmission,
	ca CommonAddr, infos ...MeasuredValueNormalInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request) {
		return ErrCmdCause
	}
	return measuredValueNormal(c, M_ME_TA_1, false, coa, ca, infos...)
}

// MeasuredValueNormalCP56Time2a sends a type identification [M_ME_TD_1]: a
// measured value, normalized, with a CP56Time2a time tag. Only a single set
// of information elements (SQ = 0).
// [M_ME_TD_1] See companion standard 101, subclass 7.3.1.26
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
func MeasuredValueNormalCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueNormalInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request) {
		return ErrCmdCause
	}
	return measuredValueNormal(c, M_ME_TD_1, false, coa, ca, infos...)
}

// MeasuredValueNormalNoQuality sends a type identification [M_ME_ND_1]: a
// measured value, normalized, without a quality descriptor.
// [M_ME_ND_1] See companion standard 101, subclass 7.3.1.21，
// The quality descriptor must default to asdu.GOOD
// Cause of transmission (COT) is used for:
// monitor direction:
// <1> := periodic, cyclic
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func MeasuredValueNormalNoQuality(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueNormalInfo) error {
	if !(coa.Cause == Periodic || coa.Cause == Background ||
		coa.Cause == Spontaneous || coa.Cause == Request ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	return measuredValueNormal(c, M_ME_ND_1, isSequence, coa, ca, infos...)
}

// MeasuredValueScaledInfo the measured value attributes.
type MeasuredValueScaledInfo struct {
	Ioa   InfoObjAddr
	Value int16
	// Quality descriptor asdu.GOOD means no remarks.
	Qds QualityDescriptor
	// the type does not include timing will ignore
	Time time.Time
}

// measuredValueScaled sends a type identification [M_ME_NB_1], [M_ME_TB_1] or
// [M_ME_TE_1]: a measured value, scaled.
// [M_ME_NB_1] See companion standard 101, subclass 7.3.1.11
// [M_ME_TB_1] See companion standard 101, subclass 7.3.1.12
// [M_ME_TE_1] See companion standard 101, subclass 7.3.1.27
func measuredValueScaled(c Connect, typeID TypeID, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueScaledInfo) error {
	if err := checkValid(c, typeID, isSequence, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	once := false
	for _, v := range infos {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}
		u.AppendScaled(v.Value).AppendBytes(byte(v.Qds))
		switch typeID {
		case M_ME_NB_1:
		case M_ME_TB_1:
			u.AppendBytes(CP24Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_ME_TE_1:
			u.AppendBytes(CP56Time2a(v.Time, u.InfoObjTimeZone)...)
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// MeasuredValueScaled sends a type identification [M_ME_NB_1]: a measured value, scaled.
// [M_ME_NB_1] See companion standard 101, subclass 7.3.1.11
// Cause of transmission (COT) is used for:
// monitor direction:
// <1> := periodic, cyclic
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func MeasuredValueScaled(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueScaledInfo) error {
	if !(coa.Cause == Periodic || coa.Cause == Background ||
		coa.Cause == Spontaneous || coa.Cause == Request ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	return measuredValueScaled(c, M_ME_NB_1, isSequence, coa, ca, infos...)
}

// MeasuredValueScaledCP24Time2a sends a type identification [M_ME_TB_1]: a
// measured value, scaled, with a CP24Time2a time tag. Only a single set of
// information elements (SQ = 0).
// [M_ME_TB_1] See companion standard 101, subclass 7.3.1.12
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
func MeasuredValueScaledCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueScaledInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request) {
		return ErrCmdCause
	}
	return measuredValueScaled(c, M_ME_TB_1, false, coa, ca, infos...)
}

// MeasuredValueScaledCP56Time2a sends a type identification [M_ME_TE_1]: a
// measured value, scaled, with a CP56Time2a time tag. Only a single set of
// information elements (SQ = 0).
// [M_ME_TE_1] See companion standard 101, subclass 7.3.1.27
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
func MeasuredValueScaledCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueScaledInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request) {
		return ErrCmdCause
	}
	return measuredValueScaled(c, M_ME_TE_1, false, coa, ca, infos...)
}

// MeasuredValueFloatInfo the measured value attributes.
type MeasuredValueFloatInfo struct {
	Ioa   InfoObjAddr
	Value float32
	// Quality descriptor asdu.GOOD means no remarks.
	Qds QualityDescriptor
	// the type does not include timing will ignore
	Time time.Time
}

// measuredValueFloat sends a type identification [M_ME_NC_1], [M_ME_TC_1] or
// [M_ME_TF_1]: a measured value as a short floating point number.
// [M_ME_NC_1] See companion standard 101, subclass 7.3.1.13
// [M_ME_TC_1] See companion standard 101, subclass 7.3.1.14
// [M_ME_TF_1] See companion standard 101, subclass 7.3.1.28
func measuredValueFloat(c Connect, typeID TypeID, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueFloatInfo) error {
	if err := checkValid(c, typeID, isSequence, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	once := false
	for _, v := range infos {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}

		u.AppendFloat32(v.Value).AppendBytes(byte(v.Qds & 0xf1))
		switch typeID {
		case M_ME_NC_1:
		case M_ME_TC_1:
			u.AppendBytes(CP24Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_ME_TF_1:
			u.AppendBytes(CP56Time2a(v.Time, u.InfoObjTimeZone)...)
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// MeasuredValueFloat sends a type identification [M_ME_TF_1]: a measured
// value as a short floating point number.
// [M_ME_NC_1] See companion standard 101, subclass 7.3.1.13
// Cause of transmission (COT) is used for:
// monitor direction:
// <1> := periodic, cyclic
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func MeasuredValueFloat(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueFloatInfo) error {
	if !(coa.Cause == Periodic || coa.Cause == Background ||
		coa.Cause == Spontaneous || coa.Cause == Request ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	return measuredValueFloat(c, M_ME_NC_1, isSequence, coa, ca, infos...)
}

// MeasuredValueFloatCP24Time2a sends a type identification [M_ME_TC_1]: a
// measured value as a short floating point number with a CP24Time2a time
// tag. Only a single set of information elements (SQ = 0).
// [M_ME_TC_1] See companion standard 101, subclass 7.3.1.14
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
func MeasuredValueFloatCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueFloatInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request) {
		return ErrCmdCause
	}
	return measuredValueFloat(c, M_ME_TC_1, false, coa, ca, infos...)
}

// MeasuredValueFloatCP56Time2a sends a type identification [M_ME_TF_1]: a
// measured value as a short floating point number with a CP56Time2a time
// tag. Only a single set of information elements (SQ = 0).
// [M_ME_TF_1] See companion standard 101, subclass 7.3.1.28
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <5> := request or requested
func MeasuredValueFloatCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...MeasuredValueFloatInfo) error {
	if !(coa.Cause == Spontaneous || coa.Cause == Request) {
		return ErrCmdCause
	}
	return measuredValueFloat(c, M_ME_TF_1, false, coa, ca, infos...)
}

// BinaryCounterReadingInfo holds the binary counter reading attributes.
type BinaryCounterReadingInfo struct {
	Ioa   InfoObjAddr
	Value BinaryCounterReading
	// the type does not include timing will ignore
	Time time.Time
}

// integratedTotals sends a type identification [M_IT_NA_1], [M_IT_TA_1] or
// [M_IT_TB_1]: integrated totals.
// [M_IT_NA_1] See companion standard 101, subclass 7.3.1.15
// [M_IT_TA_1] See companion standard 101, subclass 7.3.1.16
// [M_IT_TB_1] See companion standard 101, subclass 7.3.1.29
func integratedTotals(c Connect, typeID TypeID, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...BinaryCounterReadingInfo) error {
	if err := checkValid(c, typeID, isSequence, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	once := false
	for _, v := range infos {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}
		u.AppendBinaryCounterReading(v.Value)
		switch typeID {
		case M_IT_NA_1:
		case M_IT_TA_1:
			u.AppendBytes(CP24Time2a(v.Time, u.InfoObjTimeZone)...)
		case M_IT_TB_1:
			u.AppendBytes(CP56Time2a(v.Time, u.InfoObjTimeZone)...)
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// IntegratedTotals sends a type identification [M_IT_NA_1]: integrated totals.
// [M_IT_NA_1] See companion standard 101, subclass 7.3.1.15
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <37> := requested by general counter request
// <38> := requested by group 1 counter request
// <39> := requested by group 2 counter request
// <40> := requested by group 3 counter request
// <41> := requested by group 4 counter request
func IntegratedTotals(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...BinaryCounterReadingInfo) error {
	if !(coa.Cause == Spontaneous || (coa.Cause >= RequestByGeneralCounter && coa.Cause <= RequestByGroup4Counter)) {
		return ErrCmdCause
	}
	return integratedTotals(c, M_IT_NA_1, isSequence, coa, ca, infos...)
}

// IntegratedTotalsCP24Time2a sends a type identification [M_IT_TA_1]:
// integrated totals with a CP24Time2a time tag. Only a single set of
// information elements (SQ = 0).
// [M_IT_TA_1] See companion standard 101, subclass 7.3.1.16
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <37> := requested by general counter request
// <38> := requested by group 1 counter request
// <39> := requested by group 2 counter request
// <40> := requested by group 3 counter request
// <41> := requested by group 4 counter request
func IntegratedTotalsCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...BinaryCounterReadingInfo) error {
	if !(coa.Cause == Spontaneous || (coa.Cause >= RequestByGeneralCounter && coa.Cause <= RequestByGroup4Counter)) {
		return ErrCmdCause
	}
	return integratedTotals(c, M_IT_TA_1, false, coa, ca, infos...)
}

// IntegratedTotalsCP56Time2a sends a type identification [M_IT_TB_1]:
// integrated totals with a CP56Time2a time tag. Only a single set of
// information elements (SQ = 0).
// [M_IT_TB_1] See companion standard 101, subclass 7.3.1.29
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
// <37> := requested by general counter request
// <38> := requested by group 1 counter request
// <39> := requested by group 2 counter request
// <40> := requested by group 3 counter request
// <41> := requested by group 4 counter request
func IntegratedTotalsCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...BinaryCounterReadingInfo) error {
	if !(coa.Cause == Spontaneous || (coa.Cause >= RequestByGeneralCounter && coa.Cause <= RequestByGroup4Counter)) {
		return ErrCmdCause
	}
	return integratedTotals(c, M_IT_TB_1, false, coa, ca, infos...)
}

// EventOfProtectionEquipmentInfo holds an event of protection equipment.
type EventOfProtectionEquipmentInfo struct {
	Ioa   InfoObjAddr
	Event SingleEvent
	Qdp   QualityDescriptorProtection
	Msec  uint16
	// the type does not include timing will ignore
	Time time.Time
}

// eventOfProtectionEquipment sends a type identification [M_EP_TA_1] or
// [M_EP_TD_1]: an event of protection equipment.
// [M_EP_TA_1] See companion standard 101, subclass 7.3.1.17
// [M_EP_TD_1] See companion standard 101, subclass 7.3.1.30
func eventOfProtectionEquipment(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr, infos ...EventOfProtectionEquipmentInfo) error {
	if coa.Cause != Spontaneous {
		return ErrCmdCause
	}
	if err := checkValid(c, typeID, false, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	for _, v := range infos {
		if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
			return err
		}
		u.AppendBytes(byte(v.Event&0x03) | byte(v.Qdp&0xf8))
		u.AppendCP16Time2a(v.Msec)
		switch typeID {
		case M_EP_TA_1:
			u.AppendCP24Time2a(v.Time, u.InfoObjTimeZone)
		case M_EP_TD_1:
			u.AppendCP56Time2a(v.Time, u.InfoObjTimeZone)
		default:
			return ErrTypeIDNotMatch
		}
	}
	return c.Send(u)
}

// EventOfProtectionEquipmentCP24Time2a sends a type identification
// [M_EP_TA_1]: an event of protection equipment with a CP24Time2a time tag.
// [M_EP_TA_1] See companion standard 101, subclass 7.3.1.17
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
func EventOfProtectionEquipmentCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...EventOfProtectionEquipmentInfo) error {
	return eventOfProtectionEquipment(c, M_EP_TA_1, coa, ca, infos...)
}

// EventOfProtectionEquipmentCP56Time2a sends a type identification
// [M_EP_TD_1]: an event of protection equipment with a CP56Time2a time tag.
// [M_EP_TD_1] See companion standard 101, subclass 7.3.1.30
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
func EventOfProtectionEquipmentCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, infos ...EventOfProtectionEquipmentInfo) error {
	return eventOfProtectionEquipment(c, M_EP_TD_1, coa, ca, infos...)
}

// PackedStartEventsOfProtectionEquipmentInfo holds packed start events of
// protection equipment.
type PackedStartEventsOfProtectionEquipmentInfo struct {
	Ioa   InfoObjAddr
	Event StartEvent
	Qdp   QualityDescriptorProtection
	Msec  uint16
	// the type does not include timing will ignore
	Time time.Time
}

// packedStartEventsOfProtectionEquipment sends a type identification
// [M_EP_TB_1] or [M_EP_TE_1]: packed start events of protection equipment.
// [M_EP_TB_1] See companion standard 101, subclass 7.3.1.18
// [M_EP_TE_1] See companion standard 101, subclass 7.3.1.31
func packedStartEventsOfProtectionEquipment(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr, info PackedStartEventsOfProtectionEquipmentInfo) error {
	if coa.Cause != Spontaneous {
		return ErrCmdCause
	}
	if err := checkValid(c, typeID, false, 1); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})

	if err := u.AppendInfoObjAddr(info.Ioa); err != nil {
		return err
	}
	u.AppendBytes(byte(info.Event), byte(info.Qdp)&0xf1)
	u.AppendCP16Time2a(info.Msec)
	switch typeID {
	case M_EP_TB_1:
		u.AppendCP24Time2a(info.Time, u.InfoObjTimeZone)
	case M_EP_TE_1:
		u.AppendCP56Time2a(info.Time, u.InfoObjTimeZone)
	default:
		return ErrTypeIDNotMatch
	}

	return c.Send(u)
}

// PackedStartEventsOfProtectionEquipmentCP24Time2a sends a type
// identification [M_EP_TB_1]: packed start events of protection equipment
// with a CP24Time2a time tag.
// [M_EP_TB_1] See companion standard 101, subclass 7.3.1.18
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
func PackedStartEventsOfProtectionEquipmentCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, info PackedStartEventsOfProtectionEquipmentInfo) error {
	return packedStartEventsOfProtectionEquipment(c, M_EP_TB_1, coa, ca, info)
}

// PackedStartEventsOfProtectionEquipmentCP56Time2a sends a type
// identification [M_EP_TE_1]: packed start events of protection equipment
// with a CP56Time2a time tag.
// [M_EP_TE_1] See companion standard 101, subclass 7.3.1.31
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
func PackedStartEventsOfProtectionEquipmentCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, info PackedStartEventsOfProtectionEquipmentInfo) error {
	return packedStartEventsOfProtectionEquipment(c, M_EP_TE_1, coa, ca, info)
}

// PackedOutputCircuitInfoInfo holds packed output circuit information of
// protection equipment.
type PackedOutputCircuitInfoInfo struct {
	Ioa  InfoObjAddr
	Oci  OutputCircuitInfo
	Qdp  QualityDescriptorProtection
	Msec uint16
	// the type does not include timing will ignore
	Time time.Time
}

// packedOutputCircuitInfo sends a type identification [M_EP_TC_1] or
// [M_EP_TF_1]: packed output circuit information of protection equipment.
// [M_EP_TC_1] See companion standard 101, subclass 7.3.1.19
// [M_EP_TF_1] See companion standard 101, subclass 7.3.1.32
func packedOutputCircuitInfo(c Connect, typeID TypeID, coa CauseOfTransmission, ca CommonAddr, info PackedOutputCircuitInfoInfo) error {
	if coa.Cause != Spontaneous {
		return ErrCmdCause
	}
	if err := checkValid(c, typeID, false, 1); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		typeID,
		VariableStruct{IsSequence: false, Number: 1},
		coa,
		0,
		ca,
	})

	if err := u.AppendInfoObjAddr(info.Ioa); err != nil {
		return err
	}
	u.AppendBytes(byte(info.Oci), byte(info.Qdp)&0xf1)
	u.AppendCP16Time2a(info.Msec)
	switch typeID {
	case M_EP_TC_1:
		u.AppendCP24Time2a(info.Time, u.InfoObjTimeZone)
	case M_EP_TF_1:
		u.AppendCP56Time2a(info.Time, u.InfoObjTimeZone)
	default:
		return ErrTypeIDNotMatch
	}

	return c.Send(u)
}

// PackedOutputCircuitInfoCP24Time2a sends a type identification [M_EP_TC_1]:
// packed output circuit information of protection equipment with a
// CP24Time2a time tag.
// [M_EP_TC_1] See companion standard 101, subclass 7.3.1.19
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
func PackedOutputCircuitInfoCP24Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, info PackedOutputCircuitInfoInfo) error {
	return packedOutputCircuitInfo(c, M_EP_TC_1, coa, ca, info)
}

// PackedOutputCircuitInfoCP56Time2a sends a type identification [M_EP_TF_1]:
// packed output circuit information of protection equipment with a
// CP56Time2a time tag.
// [M_EP_TF_1] See companion standard 101, subclass 7.3.1.32
// Cause of transmission (COT) is used for:
// monitor direction:
// <3> := spontaneous
func PackedOutputCircuitInfoCP56Time2a(c Connect, coa CauseOfTransmission, ca CommonAddr, info PackedOutputCircuitInfoInfo) error {
	return packedOutputCircuitInfo(c, M_EP_TF_1, coa, ca, info)
}

// PackedSinglePointWithSCDInfo holds packed single-point information with
// status change detection.
type PackedSinglePointWithSCDInfo struct {
	Ioa InfoObjAddr
	Scd StatusAndStatusChangeDetection
	Qds QualityDescriptor
}

// PackedSinglePointWithSCD sends a type identification [M_PS_NA_1]: packed
// single-point information with status change detection.
// [M_PS_NA_1] See companion standard 101, subclass 7.3.1.20
// Cause of transmission (COT) is used for:
// monitor direction:
// <2> := background scan
// <3> := spontaneous
// <5> := request or requested
// <11> := return information caused by a remote command
// <12> := return information caused by a local command
// <20> := interrogated by station interrogation
// <21> := interrogated by group 1 interrogation
// through
// <36> := interrogated by group 16 interrogation
func PackedSinglePointWithSCD(c Connect, isSequence bool, coa CauseOfTransmission, ca CommonAddr, infos ...PackedSinglePointWithSCDInfo) error {
	if !(coa.Cause == Background || coa.Cause == Spontaneous || coa.Cause == Request ||
		coa.Cause == ReturnInfoRemote || coa.Cause == ReturnInfoLocal ||
		(coa.Cause >= InterrogatedByStation && coa.Cause <= InterrogatedByGroup16)) {
		return ErrCmdCause
	}
	if err := checkValid(c, M_PS_NA_1, isSequence, len(infos)); err != nil {
		return err
	}

	u := NewASDU(c.Params(), Identifier{
		M_PS_NA_1,
		VariableStruct{IsSequence: isSequence},
		coa,
		0,
		ca,
	})
	if err := u.SetVariableNumber(len(infos)); err != nil {
		return err
	}
	once := false
	for _, v := range infos {
		if !isSequence || !once {
			once = true
			if err := u.AppendInfoObjAddr(v.Ioa); err != nil {
				return err
			}
		}
		u.AppendStatusAndStatusChangeDetection(v.Scd)
		u.AppendBytes(byte(v.Qds))
	}
	return c.Send(u)
}

// GetSinglePoint returns the single-point information objects of an
// [M_SP_NA_1], [M_SP_TA_1] or [M_SP_TB_1].
func (sf *ASDU) GetSinglePoint() ([]SinglePointInfo, error) {
	d := sf.decoder()
	info := make([]SinglePointInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}
		value := d.readByte()

		var t time.Time
		switch sf.Type {
		case M_SP_NA_1:
		case M_SP_TA_1:
			t = d.readCP24Time2a()
		case M_SP_TB_1:
			t = d.readCP56Time2a()
		default:
			return nil, ErrTypeIDNotMatch
		}

		info = append(info, SinglePointInfo{
			Ioa:   infoObjAddr,
			Value: value&0x01 == 0x01,
			Qds:   QualityDescriptor(value & 0xf0),
			Time:  t})
	}
	return info, d.err
}

// GetDoublePoint returns the double-point information objects of an
// [M_DP_NA_1], [M_DP_TA_1] or [M_DP_TB_1].
func (sf *ASDU) GetDoublePoint() ([]DoublePointInfo, error) {
	d := sf.decoder()
	info := make([]DoublePointInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}
		value := d.readByte()

		var t time.Time
		switch sf.Type {
		case M_DP_NA_1:
		case M_DP_TA_1:
			t = d.readCP24Time2a()
		case M_DP_TB_1:
			t = d.readCP56Time2a()
		default:
			return nil, ErrTypeIDNotMatch
		}

		info = append(info, DoublePointInfo{
			Ioa:   infoObjAddr,
			Value: DoublePoint(value & 0x03),
			Qds:   QualityDescriptor(value & 0xf0),
			Time:  t})
	}
	return info, d.err
}

// GetStepPosition returns the step position information objects of an
// [M_ST_NA_1], [M_ST_TA_1] or [M_ST_TB_1].
func (sf *ASDU) GetStepPosition() ([]StepPositionInfo, error) {
	d := sf.decoder()
	info := make([]StepPositionInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}
		value := ParseStepPosition(d.readByte())
		qds := QualityDescriptor(d.readByte())

		var t time.Time
		switch sf.Type {
		case M_ST_NA_1:
		case M_ST_TA_1:
			t = d.readCP24Time2a()
		case M_ST_TB_1:
			t = d.readCP56Time2a()
		default:
			return nil, ErrTypeIDNotMatch
		}

		info = append(info, StepPositionInfo{
			Ioa:   infoObjAddr,
			Value: value,
			Qds:   qds,
			Time:  t})
	}
	return info, d.err
}

// GetBitString32 returns the 32-bit bitstring information objects of an
// [M_BO_NA_1], [M_BO_TA_1] or [M_BO_TB_1].
func (sf *ASDU) GetBitString32() ([]BitString32Info, error) {
	d := sf.decoder()
	info := make([]BitString32Info, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}

		value := d.readBitsString32()
		qds := QualityDescriptor(d.readByte())

		var t time.Time
		switch sf.Type {
		case M_BO_NA_1:
		case M_BO_TA_1:
			t = d.readCP24Time2a()
		case M_BO_TB_1:
			t = d.readCP56Time2a()
		default:
			return nil, ErrTypeIDNotMatch
		}

		info = append(info, BitString32Info{
			Ioa:   infoObjAddr,
			Value: value,
			Qds:   qds,
			Time:  t})
	}
	return info, d.err
}

// GetMeasuredValueNormal returns the normalized measured-value information
// objects of an [M_ME_NA_1], [M_ME_TA_1], [M_ME_TD_1] or [M_ME_ND_1].
func (sf *ASDU) GetMeasuredValueNormal() ([]MeasuredValueNormalInfo, error) {
	d := sf.decoder()
	info := make([]MeasuredValueNormalInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}

		value := d.readNormalize()

		var t time.Time
		var qds QualityDescriptor
		switch sf.Type {
		case M_ME_NA_1:
			qds = QualityDescriptor(d.readByte())
		case M_ME_TA_1:
			qds = QualityDescriptor(d.readByte())
			t = d.readCP24Time2a()
		case M_ME_TD_1:
			qds = QualityDescriptor(d.readByte())
			t = d.readCP56Time2a()
		case M_ME_ND_1: // without quality descriptor
		default:
			return nil, ErrTypeIDNotMatch
		}

		info = append(info, MeasuredValueNormalInfo{
			Ioa:   infoObjAddr,
			Value: value,
			Qds:   qds,
			Time:  t})
	}
	return info, d.err
}

// GetMeasuredValueScaled returns the scaled measured-value information
// objects of an [M_ME_NB_1], [M_ME_TB_1] or [M_ME_TE_1].
func (sf *ASDU) GetMeasuredValueScaled() ([]MeasuredValueScaledInfo, error) {
	d := sf.decoder()
	info := make([]MeasuredValueScaledInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}

		value := d.readScaled()
		qds := QualityDescriptor(d.readByte())

		var t time.Time
		switch sf.Type {
		case M_ME_NB_1:
		case M_ME_TB_1:
			t = d.readCP24Time2a()
		case M_ME_TE_1:
			t = d.readCP56Time2a()
		default:
			return nil, ErrTypeIDNotMatch
		}

		info = append(info, MeasuredValueScaledInfo{
			Ioa:   infoObjAddr,
			Value: value,
			Qds:   qds,
			Time:  t})
	}
	return info, d.err
}

// GetMeasuredValueFloat returns the short-floating-point measured-value
// information objects of an [M_ME_NC_1], [M_ME_TC_1] or [M_ME_TF_1].
func (sf *ASDU) GetMeasuredValueFloat() ([]MeasuredValueFloatInfo, error) {
	d := sf.decoder()
	info := make([]MeasuredValueFloatInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}

		value := d.readFloat32()
		qua := d.readByte() & 0xf1

		var t time.Time
		switch sf.Type {
		case M_ME_NC_1:
		case M_ME_TC_1:
			t = d.readCP24Time2a()
		case M_ME_TF_1:
			t = d.readCP56Time2a()
		default:
			return nil, ErrTypeIDNotMatch
		}
		info = append(info, MeasuredValueFloatInfo{
			Ioa:   infoObjAddr,
			Value: value,
			Qds:   QualityDescriptor(qua),
			Time:  t})
	}
	return info, d.err
}

// GetIntegratedTotals returns the integrated-totals information objects of
// an [M_IT_NA_1], [M_IT_TA_1] or [M_IT_TB_1].
func (sf *ASDU) GetIntegratedTotals() ([]BinaryCounterReadingInfo, error) {
	d := sf.decoder()
	info := make([]BinaryCounterReadingInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}

		value := d.readBinaryCounterReading()

		var t time.Time
		switch sf.Type {
		case M_IT_NA_1:
		case M_IT_TA_1:
			t = d.readCP24Time2a()
		case M_IT_TB_1:
			t = d.readCP56Time2a()
		default:
			return nil, ErrTypeIDNotMatch
		}
		info = append(info, BinaryCounterReadingInfo{
			Ioa:   infoObjAddr,
			Value: value,
			Time:  t})
	}
	return info, d.err
}

// GetEventOfProtectionEquipment returns the protection-equipment event
// information object of an [M_EP_TA_1] or [M_EP_TD_1].
func (sf *ASDU) GetEventOfProtectionEquipment() ([]EventOfProtectionEquipmentInfo, error) {
	d := sf.decoder()
	info := make([]EventOfProtectionEquipmentInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}

		value := d.readByte()
		msec := d.readCP16Time2a()
		var t time.Time
		switch sf.Type {
		case M_EP_TA_1:
			t = d.readCP24Time2a()
		case M_EP_TD_1:
			t = d.readCP56Time2a()
		default:
			return nil, ErrTypeIDNotMatch
		}
		info = append(info, EventOfProtectionEquipmentInfo{
			Ioa:   infoObjAddr,
			Event: SingleEvent(value & 0x03),
			Qdp:   QualityDescriptorProtection(value & 0xf1),
			Msec:  msec,
			Time:  t})
	}
	return info, d.err
}

// GetPackedStartEventsOfProtectionEquipment returns the packed start events
// information object of an [M_EP_TB_1] or [M_EP_TE_1].
func (sf *ASDU) GetPackedStartEventsOfProtectionEquipment() (PackedStartEventsOfProtectionEquipmentInfo, error) {
	d := sf.decoder()
	info := PackedStartEventsOfProtectionEquipmentInfo{}

	if sf.Variable.IsSequence || sf.Variable.Number != 1 {
		return info, ErrInfoObjIndexFit
	}

	info.Ioa = d.readInfoObjAddr()
	info.Event = StartEvent(d.readByte())
	info.Qdp = QualityDescriptorProtection(d.readByte() & 0xf1)
	info.Msec = d.readCP16Time2a()
	switch sf.Type {
	case M_EP_TB_1:
		info.Time = d.readCP24Time2a()
	case M_EP_TE_1:
		info.Time = d.readCP56Time2a()
	default:
		return PackedStartEventsOfProtectionEquipmentInfo{}, ErrTypeIDNotMatch
	}
	return info, d.err
}

// GetPackedOutputCircuitInfo returns the packed output circuit information
// object of an [M_EP_TC_1] or [M_EP_TF_1].
func (sf *ASDU) GetPackedOutputCircuitInfo() (PackedOutputCircuitInfoInfo, error) {
	d := sf.decoder()
	info := PackedOutputCircuitInfoInfo{}

	if sf.Variable.IsSequence || sf.Variable.Number != 1 {
		return info, ErrInfoObjIndexFit
	}

	info.Ioa = d.readInfoObjAddr()
	info.Oci = OutputCircuitInfo(d.readByte())
	info.Qdp = QualityDescriptorProtection(d.readByte() & 0xf1)
	info.Msec = d.readCP16Time2a()
	switch sf.Type {
	case M_EP_TC_1:
		info.Time = d.readCP24Time2a()
	case M_EP_TF_1:
		info.Time = d.readCP56Time2a()
	default:
		return PackedOutputCircuitInfoInfo{}, ErrTypeIDNotMatch
	}
	return info, d.err
}

// GetPackedSinglePointWithSCD returns the packed single-point information
// with status change detection of an [M_PS_NA_1].
func (sf *ASDU) GetPackedSinglePointWithSCD() ([]PackedSinglePointWithSCDInfo, error) {
	d := sf.decoder()
	info := make([]PackedSinglePointWithSCDInfo, 0, sf.Variable.Number)
	infoObjAddr := InfoObjAddr(0)
	for i, once := 0, false; i < int(sf.Variable.Number); i++ {
		if !sf.Variable.IsSequence || !once {
			once = true
			infoObjAddr = d.readInfoObjAddr()
		} else {
			infoObjAddr++
		}
		scd := d.readStatusAndStatusChangeDetection()
		qds := QualityDescriptor(d.readByte())
		info = append(info, PackedSinglePointWithSCDInfo{
			Ioa: infoObjAddr,
			Scd: scd,
			Qds: qds})
	}
	return info, d.err
}
