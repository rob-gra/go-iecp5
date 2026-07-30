package asdu

import (
	"testing"
	"time"
)

// newTruncatedASDU builds an ASDU whose infoObj is shorter than a full
// CP56Time2a field (7 bytes), but whose backing array (bootstrap) has
// plenty of spare capacity beyond that -- the exact shape that used to let
// DecodeCP56Time2a's unconditional `sf.infoObj[7:]` reslice silently
// succeed with stale bytes instead of failing.
func newTruncatedASDU(t *testing.T, infoObjLen int) *ASDU {
	t.Helper()
	p := *ParamsWide // private copy: some subtests mutate InfoObjAddrSize
	a := NewEmptyASDU(&p)
	a.infoObj = a.bootstrap[:infoObjLen]
	for i := range a.infoObj {
		a.infoObj[i] = 0xff // recognizable stale/garbage marker bytes
	}
	return a
}

func mustPanic(t *testing.T, name string, f func()) {
	t.Helper()
	defer func() {
		r := recover()
		if r == nil {
			t.Fatalf("%s: expected a panic on truncated infoObj, got none", name)
		}
		if r != ErrInfoObjTruncated {
			t.Fatalf("%s: panic value = %v, want ErrInfoObjTruncated", name, r)
		}
	}()
	f()
}

func TestDecode_PanicsOnTruncatedInfoObj(t *testing.T) {
	tests := []struct {
		name string
		n    int // bytes actually present; always less than what's decoded
		call func(a *ASDU)
	}{
		{"DecodeByte", 0, func(a *ASDU) { a.DecodeByte() }},
		{"DecodeUint16", 1, func(a *ASDU) { a.DecodeUint16() }},
		{"DecodeNormalize", 1, func(a *ASDU) { a.DecodeNormalize() }},
		{"DecodeScaled", 1, func(a *ASDU) { a.DecodeScaled() }},
		{"DecodeFloat32", 3, func(a *ASDU) { a.DecodeFloat32() }},
		{"DecodeBinaryCounterReading", 4, func(a *ASDU) { a.DecodeBinaryCounterReading() }},
		{"DecodeBitsString32", 3, func(a *ASDU) { a.DecodeBitsString32() }},
		{"DecodeCP56Time2a", 6, func(a *ASDU) { a.DecodeCP56Time2a() }},
		{"DecodeCP24Time2a", 2, func(a *ASDU) { a.DecodeCP24Time2a() }},
		{"DecodeCP16Time2a", 1, func(a *ASDU) { a.DecodeCP16Time2a() }},
		{"DecodeStatusAndStatusChangeDetection", 3, func(a *ASDU) { a.DecodeStatusAndStatusChangeDetection() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTruncatedASDU(t, tt.n)
			mustPanic(t, tt.name, func() { tt.call(a) })
		})
	}

	t.Run("DecodeInfoObjAddr", func(t *testing.T) {
		for _, size := range []int{1, 2, 3} {
			a := newTruncatedASDU(t, size-1)
			a.InfoObjAddrSize = size
			mustPanic(t, "DecodeInfoObjAddr", func() { a.DecodeInfoObjAddr() })
		}
	})
}

// TestDecodeCP56Time2a_TruncatedDoesNotReadStaleBytes is a regression test:
// DecodeCP56Time2a used to reslice infoObj[7:] unconditionally, which is
// only bounds-checked against the backing array's capacity, not its
// length -- so a short-but-not-tiny infoObj (backed by ASDU's fixed-size
// bootstrap array, which almost always has spare capacity) would silently
// produce a reslice into stale/garbage bytes rather than failing. It must
// now panic instead.
func TestDecodeCP56Time2a_TruncatedDoesNotReadStaleBytes(t *testing.T) {
	a := newTruncatedASDU(t, 5) // fewer than the 7 bytes CP56Time2a needs
	defer func() {
		r := recover()
		if r != ErrInfoObjTruncated {
			t.Fatalf("panic value = %v, want ErrInfoObjTruncated", r)
		}
	}()
	got := a.DecodeCP56Time2a()
	t.Fatalf("DecodeCP56Time2a() = %v, want a panic instead of a silently-decoded value", got)
}

func TestParseCP16Time2a_ShortSliceReturnsZero(t *testing.T) {
	if got := ParseCP16Time2a([]byte{0x01}); got != 0 {
		t.Errorf("ParseCP16Time2a(short) = %d, want 0", got)
	}
	if got := ParseCP16Time2a(nil); got != 0 {
		t.Errorf("ParseCP16Time2a(nil) = %d, want 0", got)
	}
}

func TestParseCP56Time2a_ShortSliceReturnsZeroTime(t *testing.T) {
	if got := ParseCP56Time2a([]byte{1, 2, 3}, time.UTC); !got.IsZero() {
		t.Errorf("ParseCP56Time2a(short) = %v, want zero time", got)
	}
}

func TestParseCP24Time2a_ShortSliceReturnsZeroTime(t *testing.T) {
	if got := ParseCP24Time2a([]byte{1}, time.UTC); !got.IsZero() {
		t.Errorf("ParseCP24Time2a(short) = %v, want zero time", got)
	}
}

// TestGetCmd_PanicsWithErrInfoObjTruncated covers the Get* accessors that
// read a trailing qualifier byte after the information object address.
// These used to index sf.infoObj[0] directly, which on a truncated frame
// panicked with a raw runtime bounds error instead of the package's
// documented ErrInfoObjTruncated -- indistinguishable, from a caller
// decoding untrusted captures, from a genuine bug in this library.
func TestGetCmd_PanicsWithErrInfoObjTruncated(t *testing.T) {
	tests := []struct {
		name string
		call func(a *ASDU)
	}{
		{"GetInterrogationCmd", func(a *ASDU) { a.GetInterrogationCmd() }},
		{"GetCounterInterrogationCmd", func(a *ASDU) { a.GetCounterInterrogationCmd() }},
		{"GetResetProcessCmd", func(a *ASDU) { a.GetResetProcessCmd() }},
		{"GetEndOfInitialization", func(a *ASDU) { a.GetEndOfInitialization() }},
		{"GetParameterActivation", func(a *ASDU) { a.GetParameterActivation() }},
		{"GetParameterNormal", func(a *ASDU) { a.GetParameterNormal() }},
		{"GetParameterScaled", func(a *ASDU) { a.GetParameterScaled() }},
		{"GetParameterFloat", func(a *ASDU) { a.GetParameterFloat() }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// ParamsWide addresses are 3 bytes, so an infoObj holding only
			// the address is truncated for every accessor here: each needs
			// at least one more byte for its trailing qualifier.
			a := newTruncatedASDU(t, 3)
			mustPanic(t, tt.name, func() { tt.call(a) })
		})
	}
}

// TestGetCmd_QualifierReadAfterAddress pins the decode order: the trailing
// qualifier must come from the byte *after* the information object address,
// never from the address's own first byte. Go orders function calls within
// an expression but leaves index expressions unordered relative to them, so
// reading the qualifier as sf.infoObj[0] inline alongside a cursor-advancing
// call left this to the compiler rather than to the code.
func TestGetCmd_QualifierReadAfterAddress(t *testing.T) {
	p := *ParamsWide
	a := NewEmptyASDU(&p)
	// 3-byte IOA 0x010203, then the qualifier byte.
	a.infoObj = append(a.infoObj, 0x03, 0x02, 0x01, byte(QOIStation))

	ioa, qoi := a.GetInterrogationCmd()
	if ioa != 0x010203 {
		t.Errorf("InfoObjAddr = %#x, want 0x010203", ioa)
	}
	if qoi != QOIStation {
		t.Errorf("QualifierOfInterrogation = %d, want %d (the byte after the address, not inside it)", qoi, QOIStation)
	}
}
