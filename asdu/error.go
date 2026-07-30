// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package asdu

import (
	"errors"
	"fmt"
)

// error defined
var (
	ErrTypeIdentifier = errors.New("asdu: type identification unknown")
	ErrCauseZero      = errors.New("asdu: cause of transmission 0 is not used")
	ErrCommonAddrZero = errors.New("asdu: common address 0 is not used")

	ErrParam           = errors.New("asdu: system parameter out of range")
	ErrInvalidTimeTag  = errors.New("asdu: invalid time tag")
	ErrOriginAddrFit   = errors.New("asdu: originator address not allowed with cause size 1 system parameter")
	ErrCommonAddrFit   = errors.New("asdu: common address exceeds size system parameter")
	ErrInfoObjAddrFit  = errors.New("asdu: information object address exceeds size system parameter")
	ErrInfoObjIndexFit = errors.New("asdu: information object index not in [1, 127]")
	ErrInroGroupNumFit = errors.New("asdu: interrogation group number exceeds 16")

	ErrLengthOutOfRange = fmt.Errorf("asdu: asdu filed length large than max %d", ASDUSizeMax)
	ErrNotAnyObjInfo    = errors.New("asdu: not any object information")
	ErrTypeIDNotMatch   = errors.New("asdu: type identifier doesn't match call or time tag")

	// ErrInfoObjTruncated is panicked by the Decode* methods when fewer
	// bytes remain in the information object than the field being decoded
	// needs. fixInfoObjSize validates the overall information object
	// length against the declared element count before any Decode* call
	// runs, so this should only be reachable for malformed/adversarial
	// input (e.g. a corrupted or truncated capture) rather than
	// well-formed traffic; callers decoding untrusted input should
	// recover() around Get*Cmd/GetXxx calls.
	ErrInfoObjTruncated = errors.New("asdu: information object truncated")

	ErrCmdCause = errors.New("asdu: cause of transmission for command not standard requirement")
)
