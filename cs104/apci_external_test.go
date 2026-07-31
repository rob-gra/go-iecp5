// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

// This file is deliberately in package cs104_test rather than cs104: it
// exists to prove the decoding API works for a caller outside the package,
// which a test compiled into the package itself cannot demonstrate -- it can
// reach unexported identifiers whether or not an external caller could.
package cs104_test

import (
	"bytes"
	"testing"

	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/cs104"
)

// ParseAPCI returns an any, so identifying a frame means type-switching on
// what comes back. That is only possible if the APCI types are exported --
// they were not, which made the function unusable for the offline decoding
// its own documentation recommends it for.
func TestParseAPCI_TypesAreUsableFromOutsideThePackage(t *testing.T) {
	iFrame := []byte{0x68, 0x0a, 0x04, 0x00, 0x06, 0x00, 0x01, 0x01, 0x03, 0x00, 0x01, 0x00}
	sFrame := []byte{0x68, 0x04, 0x01, 0x00, 0x08, 0x00}
	uFrame := []byte{0x68, 0x04, cs104.UStartDtActive | 0x03, 0x00, 0x00, 0x00}

	t.Run("I-format", func(t *testing.T) {
		apci, payload, err := cs104.ParseAPCI(iFrame)
		if err != nil {
			t.Fatal(err)
		}
		i, ok := apci.(cs104.IAPCI)
		if !ok {
			t.Fatalf("got %T, want cs104.IAPCI", apci)
		}
		// Sequence numbers are the reason a caller wants the concrete type
		// and not just the frame class.
		if i.SendSN != 2 || i.RcvSN != 3 {
			t.Fatalf("got %+v, want SendSN=2 RcvSN=3", i)
		}
		if !bytes.Equal(payload, iFrame[6:]) {
			t.Fatalf("payload = % x, want % x", payload, iFrame[6:])
		}
	})

	t.Run("S-format", func(t *testing.T) {
		apci, _, err := cs104.ParseAPCI(sFrame)
		if err != nil {
			t.Fatal(err)
		}
		s, ok := apci.(cs104.SAPCI)
		if !ok {
			t.Fatalf("got %T, want cs104.SAPCI", apci)
		}
		if s.RcvSN != 4 {
			t.Fatalf("got RcvSN=%d, want 4", s.RcvSN)
		}
	})

	t.Run("U-format", func(t *testing.T) {
		apci, _, err := cs104.ParseAPCI(uFrame)
		if err != nil {
			t.Fatal(err)
		}
		u, ok := apci.(cs104.UAPCI)
		if !ok {
			t.Fatalf("got %T, want cs104.UAPCI", apci)
		}
		// The function constants have to be exported too, or an external
		// caller has a Function byte and nothing to compare it against.
		if u.Function != cs104.UStartDtActive {
			t.Fatalf("got Function=%#x, want UStartDtActive (%#x)", u.Function, cs104.UStartDtActive)
		}
	})
}

// The decoding path the package documentation points at: frame a byte stream
// with ReadAPDU, split each frame with ParseAPCI, and decode the I-format
// payloads. All of it from outside the package.
func TestDecodeStreamFromOutsideThePackage(t *testing.T) {
	a := asdu.NewASDU(asdu.ParamsWide, asdu.Identifier{
		Type:       asdu.M_SP_NA_1,
		Variable:   asdu.VariableStruct{IsSequence: false, Number: 1},
		Coa:        asdu.CauseOfTransmission{Cause: asdu.Spontaneous},
		CommonAddr: 5,
	})
	if err := a.AppendInfoObjAddr(77); err != nil {
		t.Fatal(err)
	}
	a.AppendBytes(byte(asdu.QDSGood))
	payload, err := a.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}

	// One I-format APDU, then a TESTFR that carries no ASDU, concatenated
	// into a single stream the way a TCP segment would deliver them.
	stream := append([]byte{0x68, byte(len(payload) + 4), 0x00, 0x00, 0x00, 0x00}, payload...)
	stream = append(stream, 0x68, 0x04, cs104.UTestFrActive|0x03, 0x00, 0x00, 0x00)

	r := bytes.NewReader(stream)
	var decoded int
	for {
		apdu, err := cs104.ReadAPDU(r)
		if err != nil {
			break
		}
		apci, body, err := cs104.ParseAPCI(apdu)
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := apci.(cs104.IAPCI); !ok {
			continue // S- and U-format carry no ASDU
		}
		got := asdu.NewEmptyASDU(asdu.ParamsWide)
		if err := got.UnmarshalBinary(body); err != nil {
			t.Fatalf("UnmarshalBinary: %v", err)
		}
		if got.Type != asdu.M_SP_NA_1 || got.CommonAddr != 5 {
			t.Fatalf("got %v, want M_SP_NA_1 @5", got.Identifier)
		}
		decoded++
	}
	if decoded != 1 {
		t.Fatalf("decoded %d ASDUs, want 1", decoded)
	}
}
