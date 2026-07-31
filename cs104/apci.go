// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"fmt"
	"io"

	"github.com/thinkgos/go-iecp5/asdu"
)

const startFrame byte = 0x68 // start byte

// APDU form Max size 255
//
//	|              APCI                   |       ASDU         |
//	| start | APDU length | control field |       ASDU         |
//	                 |          APDU field size(253)           |
//
// bytes|    1  |    1   |        4           |                    |
const (
	APCICtlFiledSize = 4 // control filed(4)

	APDUSizeMax      = 255                                 // start(1) + length(1) + control field(4) + ASDU
	APDUFieldSizeMax = APCICtlFiledSize + asdu.ASDUSizeMax // control field(4) + ASDU
)

// U-frame control-field functions, as carried in UAPCI.Function.
const (
	UStartDtActive  byte = 4 << iota // STARTDT activation 0x04
	UStartDtConfirm                  // STARTDT confirmation 0x08
	UStopDtActive                    // STOPDT activation 0x10
	UStopDtConfirm                   // STOPDT confirmation 0x20
	UTestFrActive                    // TESTFR activation 0x40
	UTestFrConfirm                   // TESTFR confirmation 0x80
)

// IAPCI is an I-format APDU's control information (information transfer):
// APCI plus ASDU, carrying numbered information transfer.
//
// The three APCI types are exported, along with their fields, because
// ParseAPCI hands one back as an any: a caller decoding APDUs from a capture
// has to type-switch on what it gets, and cannot do that against unexported
// types.
type IAPCI struct {
	SendSN, RcvSN uint16
}

func (sf IAPCI) String() string {
	return fmt.Sprintf("I[sendNO: %d, recvNO: %d]", sf.SendSN, sf.RcvSN)
}

// SAPCI is an S-format APDU's control information (supervisory): APCI only,
// acknowledging correct receipt of numbered frames.
type SAPCI struct {
	RcvSN uint16
}

func (sf SAPCI) String() string {
	return fmt.Sprintf("S[recvNO: %d]", sf.RcvSN)
}

// UAPCI is a U-format APDU's control information (unnumbered): APCI only,
// carrying control information. Compare Function against the U* constants.
type UAPCI struct {
	Function byte // one of UStartDtActive, UStartDtConfirm, ...
}

func (sf UAPCI) String() string {
	var s string
	switch sf.Function {
	case UStartDtActive:
		s = "StartDtActive"
	case UStartDtConfirm:
		s = "StartDtConfirm"
	case UStopDtActive:
		s = "StopDtActive"
	case UStopDtConfirm:
		s = "StopDtConfirm"
	case UTestFrActive:
		s = "TestFrActive"
	case UTestFrConfirm:
		s = "TestFrConfirm"
	default:
		s = "Unknown"
	}
	return fmt.Sprintf("U[function: %s]", s)
}

// newIFrame builds an I-frame and returns the APDU.
func newIFrame(sendSN, RcvSN uint16, asdus []byte) ([]byte, error) {
	if len(asdus) > asdu.ASDUSizeMax {
		return nil, fmt.Errorf("ASDU filed large than max %d", asdu.ASDUSizeMax)
	}

	b := make([]byte, len(asdus)+6)

	b[0] = startFrame
	b[1] = byte(len(asdus) + 4)
	b[2] = byte(sendSN << 1)
	b[3] = byte(sendSN >> 7)
	b[4] = byte(RcvSN << 1)
	b[5] = byte(RcvSN >> 7)
	copy(b[6:], asdus)

	return b, nil
}

// newSFrame builds an S-frame and returns the APDU.
func newSFrame(RcvSN uint16) []byte {
	return []byte{startFrame, 4, 0x01, 0x00, byte(RcvSN << 1), byte(RcvSN >> 7)}
}

// newUFrame builds a U-frame and returns the APDU.
func newUFrame(which byte) []byte {
	return []byte{startFrame, 4, which | 0x03, 0x00, 0x00, 0x00}
}

// APCI is the application protocol control information.
type APCI struct {
	start                  byte
	apduFiledLen           byte // length of control field + ASDU
	ctr1, ctr2, ctr3, ctr4 byte
}

// parse returns the frame type as an IAPCI, SAPCI or UAPCI, plus the
// remaining data. apdu must be at
// least 6 bytes (APCICtlFiledSize+2); callers are expected to have already
// validated the frame length, e.g. via ReadAPDU or recvLoop's own framing.
// See ParseAPCI for a bounds-checked, exported equivalent suitable for
// arbitrary/untrusted input.
func parse(apdu []byte) (any, []byte) {
	apci := APCI{apdu[0], apdu[1], apdu[2], apdu[3], apdu[4], apdu[5]}
	if apci.ctr1&0x01 == 0 {
		return IAPCI{
			SendSN: uint16(apci.ctr1)>>1 + uint16(apci.ctr2)<<7,
			RcvSN:  uint16(apci.ctr3)>>1 + uint16(apci.ctr4)<<7,
		}, apdu[6:]
	}
	if apci.ctr1&0x03 == 0x01 {
		return SAPCI{
			RcvSN: uint16(apci.ctr3)>>1 + uint16(apci.ctr4)<<7,
		}, apdu[6:]
	}
	// apci.ctrl&0x03 == 0x03
	return UAPCI{
		Function: apci.ctr1 & 0xfc,
	}, apdu[6:]
}

// ParseAPCI parses a raw APDU frame's 6-byte control field into its APCI
// form (an iAPCI, sAPCI, or uAPCI) and returns the remaining ASDU payload
// bytes. apdu is normally a complete frame as returned by ReadAPDU (a start
// byte, a length byte, and the declared number of following bytes);
// ParseAPCI does not itself validate the start byte or the declared length
// against len(apdu), only that apdu is long enough to contain a control
// field at all.
//
// Unlike the package's internal frame handling, ParseAPCI is exported and
// bounds-checked so it can be used to decode APDUs from sources other than
// a live connection, e.g. frames extracted from a pcap capture.
func ParseAPCI(apdu []byte) (apci any, asduPayload []byte, err error) {
	if len(apdu) < APCICtlFiledSize+2 {
		return nil, nil, io.ErrUnexpectedEOF
	}
	apci, asduPayload = parse(apdu)
	return apci, asduPayload, nil
}
