// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package asdu

import (
	"encoding/binary"
	"math"
	"time"
)

// decoder reads an information object field by field, over a buffer it does
// not modify. It is the reading half of the ASDU codec; Append* on ASDU is
// the writing half.
//
// Two properties matter, both of which the previous cursor-on-the-ASDU
// design could not offer:
//
// Decoding is non-destructive. The cursor lives here, not on the ASDU, so
// reading an ASDU leaves it byte-for-byte intact and it can be read again
// or echoed back afterwards. Under the old design, decoding consumed the
// information object, and any caller that still needed the original bytes
// -- SendReplyMirror echoing a command back, most of all -- had to know to
// Clone() first. Forgetting that produced wire-malformed replies, and it
// happened more than once.
//
// Errors are values. A truncated or malformed object sets err and every
// later read becomes a no-op returning a zero value, so a whole object can
// be decoded and checked once at the end rather than guarded field by
// field. The old design signalled truncation by panicking, which meant any
// caller decoding untrusted input -- a capture file, say -- had to wrap
// every call in recover() to tell a bad frame from a bug in this library.
type decoder struct {
	params *Params
	buf    []byte
	pos    int
	err    error
}

// decoder returns a decoder positioned at the start of this ASDU's
// information object. The ASDU is not modified by anything the decoder does.
func (sf *ASDU) decoder() *decoder {
	return &decoder{params: sf.Params, buf: sf.infoObj}
}

// fail records err, unless an earlier error is already recorded.
func (d *decoder) fail(err error) {
	if d.err == nil {
		d.err = err
	}
}

// next returns the next n bytes and advances, or nil if they aren't
// available or an error was already recorded.
func (d *decoder) next(n int) []byte {
	if d.err != nil {
		return nil
	}
	if d.pos+n > len(d.buf) {
		d.fail(ErrInfoObjTruncated)
		return nil
	}
	b := d.buf[d.pos : d.pos+n]
	d.pos += n
	return b
}

// remaining reports how many bytes are left unread.
func (d *decoder) remaining() int {
	if d.err != nil {
		return 0
	}
	return len(d.buf) - d.pos
}

func (d *decoder) readByte() byte {
	b := d.next(1)
	if b == nil {
		return 0
	}
	return b[0]
}

func (d *decoder) readUint16() uint16 {
	b := d.next(2)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}

// readInfoObjAddr reads an information object address, whose width is
// controlled by Params.InfoObjAddrSize.
func (d *decoder) readInfoObjAddr() InfoObjAddr {
	switch d.params.InfoObjAddrSize {
	case 1:
		b := d.next(1)
		if b == nil {
			return 0
		}
		return InfoObjAddr(b[0])
	case 2:
		b := d.next(2)
		if b == nil {
			return 0
		}
		return InfoObjAddr(b[0]) | (InfoObjAddr(b[1]) << 8)
	case 3:
		b := d.next(3)
		if b == nil {
			return 0
		}
		return InfoObjAddr(b[0]) | (InfoObjAddr(b[1]) << 8) | (InfoObjAddr(b[2]) << 16)
	default:
		d.fail(ErrParam)
		return 0
	}
}

func (d *decoder) readNormalize() Normalize {
	return Normalize(d.readUint16())
}

func (d *decoder) readScaled() int16 {
	return int16(d.readUint16())
}

func (d *decoder) readFloat32() float32 {
	b := d.next(4)
	if b == nil {
		return 0
	}
	return math.Float32frombits(binary.LittleEndian.Uint32(b))
}

func (d *decoder) readBinaryCounterReading() BinaryCounterReading {
	b := d.next(5)
	if b == nil {
		return BinaryCounterReading{}
	}
	return BinaryCounterReading{
		int32(binary.LittleEndian.Uint32(b)),
		b[4] & 0x1f,
		b[4]&0x20 == 0x20,
		b[4]&0x40 == 0x40,
		b[4]&0x80 == 0x80,
	}
}

func (d *decoder) readBitsString32() uint32 {
	b := d.next(4)
	if b == nil {
		return 0
	}
	return binary.LittleEndian.Uint32(b)
}

func (d *decoder) readCP56Time2a() time.Time {
	b := d.next(7)
	if b == nil {
		return time.Time{}
	}
	return ParseCP56Time2a(b, d.params.InfoObjTimeZone)
}

func (d *decoder) readCP24Time2a() time.Time {
	b := d.next(3)
	if b == nil {
		return time.Time{}
	}
	return ParseCP24Time2a(b, d.params.InfoObjTimeZone)
}

func (d *decoder) readCP16Time2a() uint16 {
	b := d.next(2)
	if b == nil {
		return 0
	}
	return ParseCP16Time2a(b)
}

func (d *decoder) readStatusAndStatusChangeDetection() StatusAndStatusChangeDetection {
	b := d.next(4)
	if b == nil {
		return 0
	}
	return StatusAndStatusChangeDetection(binary.LittleEndian.Uint32(b))
}
