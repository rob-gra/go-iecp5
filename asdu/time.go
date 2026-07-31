// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package asdu

import (
	"encoding/binary"
	"time"
)

// CP56Time2a , CP24Time2a, CP16Time2a
// |         Milliseconds(D7--D0)        | Milliseconds = 0-59999
// |         Milliseconds(D15--D8)       |
// | IV(D7)   RES1(D6)  Minutes(D5--D0)  | Minutes = 1-59, IV = invalid,0 = valid, 1 = invalid
// | SU(D7)   RES2(D6-D5)  Hours(D4--D0) | Hours = 0-23, SU = summer Time,0 = standard time, 1 = summer time,
// | DayOfWeek(D7--D5) DayOfMonth(D4--D0)| DayOfMonth = 1-31  DayOfWeek = 1-7
// | RES3(D7--D4)        Months(D3--D0)  | Months = 1-12
// | RES4(D7)            Year(D6--D0)    | Year = 0-99

// CP56Time2a encodes a time as CP56Time2a, a seven-octet binary time.
// See companion standard 101, subclass 7.2.6.18.
func CP56Time2a(t time.Time, loc *time.Location) []byte {
	if loc == nil {
		loc = time.UTC
	}
	ts := t.In(loc)
	msec := ts.Nanosecond()/int(time.Millisecond) + ts.Second()*1000

	// Go numbers Sunday 0 and Saturday 6; the standard numbers Monday 1
	// through Sunday 7, reserving 0 for "day of week not used". Writing Go's
	// value straight through therefore encodes every Sunday as "not used" --
	// the other six days happen to coincide, which is why it went unnoticed.
	dayOfWeek := int(ts.Weekday())
	if dayOfWeek == 0 {
		dayOfWeek = 7
	}

	// SU marks the time as summer time. Encoding a summer local time with SU
	// clear tells the receiver it is standard time, which is an hour out; the
	// bit is also what disambiguates the repeated hour at the autumn
	// transition. Zones without DST report false here, so UTC is unaffected.
	hour := byte(ts.Hour())
	if ts.IsDST() {
		hour |= 0x80
	}

	// The year field is seven bits holding 0-99, so a century is simply not
	// representable and every implementation reduces modulo 100 -- lib60870's
	// CP56Time2a_setYear does the same. `Year() - 2000` did not: before 2000
	// it went negative and wrapped, and from 2100 it overflowed the field into
	// the reserved bit. ParseCP56Time2a reads the result back as 20xx, which
	// is the convention this encoding carries.
	year := ts.Year() % 100
	if year < 0 {
		year += 100
	}

	return []byte{byte(msec), byte(msec >> 8), byte(ts.Minute()), hour,
		byte(dayOfWeek<<5) | byte(ts.Day()), byte(ts.Month()), byte(year) & 0x7f}
}

// ParseCP56Time2a decodes CP56Time2a, a seven-octet binary time: it reads 7
// bytes and returns the time. UTC is recommended for all time tags.
// The year is assumed to be in the 20th century.
// See IEC 60870-5-4 § 6.8 and IEC 60870-5-101 second edition § 7.2.6.18.
func ParseCP56Time2a(bytes []byte, loc *time.Location) time.Time {
	if len(bytes) < 7 || bytes[2]&0x80 == 0x80 {
		return time.Time{}
	}

	x := int(binary.LittleEndian.Uint16(bytes))
	msec := x % 1000
	sec := x / 1000
	min := int(bytes[2] & 0x3f)
	hour := int(bytes[3] & 0x1f)
	day := int(bytes[4] & 0x1f)
	month := time.Month(bytes[5] & 0x0f)
	year := 2000 + int(bytes[6]&0x7f)

	nsec := msec * int(time.Millisecond)
	if loc == nil {
		loc = time.UTC
	}
	return time.Date(year, month, day, hour, min, sec, nsec, loc)
}

// CP24Time2a encodes a time as CP24Time2a, a three-octet binary time. UTC is
// recommended for all time tags.
// See companion standard 101, subclass 7.2.6.19.
func CP24Time2a(t time.Time, loc *time.Location) []byte {
	if loc == nil {
		loc = time.UTC
	}
	ts := t.In(loc)
	msec := ts.Nanosecond()/int(time.Millisecond) + ts.Second()*1000
	return []byte{byte(msec), byte(msec >> 8), byte(ts.Minute())}
}

// ParseCP24Time2a decodes CP24Time2a, a three-octet binary time.
// See companion standard 101, subclass 7.2.6.19.
//
// CP24Time2a carries milliseconds, seconds and minutes -- no hour, and no
// date. The returned value therefore sets only those fields, on the zero
// date: reading a field this encoding does not carry would be reading a
// value that was never sent. Completing it belongs to the application, which
// is the only party that knows which hour the frame refers to.
//
// This used to fill the missing fields from time.Now(), which made decoding
// non-deterministic, wrong for a captured or replayed frame, and liable to
// attribute a frame to the wrong hour when it arrived either side of an hour
// boundary. lib60870 exposes only getMillisecond and getMinute for the same
// reason, and never synthesizes a timestamp.
//
// A zero return means the frame carried the invalid flag; note that a valid
// frame reading zero milliseconds in minute zero is indistinguishable from
// it, as it is for ParseCP56Time2a.
func ParseCP24Time2a(bytes []byte, loc *time.Location) time.Time {
	if len(bytes) < 3 || bytes[2]&0x80 == 0x80 {
		return time.Time{}
	}
	if loc == nil {
		loc = time.UTC
	}
	x := int(binary.LittleEndian.Uint16(bytes))
	msec := x % 1000
	sec := x / 1000
	min := int(bytes[2] & 0x3f)

	return time.Date(1, time.January, 1, 0, min, sec, msec*int(time.Millisecond), loc)
}

// CP16Time2a encodes milliseconds as CP16Time2a, a two-octet binary time.
// See companion standard 101, subclass 7.2.6.20.
func CP16Time2a(msec uint16) []byte {
	return []byte{byte(msec), byte(msec >> 8)}
}

// ParseCP16Time2a decodes CP16Time2a, a two-octet binary time: it reads 2
// bytes and returns the value.
// See companion standard 101, subclass 7.2.6.20.
func ParseCP16Time2a(b []byte) uint16 {
	if len(b) < 2 {
		return 0
	}
	return binary.LittleEndian.Uint16(b)
}
