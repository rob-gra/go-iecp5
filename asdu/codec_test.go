package asdu

import (
	"testing"
	"time"
)

// newTruncatedASDU builds an ASDU whose infoObj holds infoObjLen bytes of
// recognizable garbage: fewer than the fields decoded from it need, so
// every accessor over it must report ErrInfoObjTruncated rather than read
// past the end or invent a value.
func newTruncatedASDU(t *testing.T, infoObjLen int) *ASDU {
	t.Helper()
	p := *ParamsWide // private copy: some subtests mutate InfoObjAddrSize
	a := NewEmptyASDU(&p)
	a.infoObj = make([]byte, infoObjLen)
	for i := range a.infoObj {
		a.infoObj[i] = 0xff // recognizable stale/garbage marker bytes
	}
	return a
}

// TestGet_TruncatedReturnsErrInfoObjTruncated covers every accessor over a
// too-short information object. A truncated or corrupted frame is ordinary
// input for anything reading captured traffic, so it has to come back as an
// error the caller can act on -- not a panic they must recover() from, and
// certainly not a plausible-looking value decoded from whatever bytes
// happened to follow.
func TestGet_TruncatedReturnsErrInfoObjTruncated(t *testing.T) {
	tests := []struct {
		name string
		n    int // bytes present; always fewer than the accessor needs
		call func(a *ASDU) error
	}{
		{"GetInterrogationCmd", 3, func(a *ASDU) error { _, _, err := a.GetInterrogationCmd(); return err }},
		{"GetCounterInterrogationCmd", 3, func(a *ASDU) error { _, _, err := a.GetCounterInterrogationCmd(); return err }},
		{"GetReadCmd", 2, func(a *ASDU) error { _, err := a.GetReadCmd(); return err }},
		{"GetClockSynchronizationCmd", 5, func(a *ASDU) error { _, _, err := a.GetClockSynchronizationCmd(); return err }},
		{"GetTestCommand", 4, func(a *ASDU) error { _, _, err := a.GetTestCommand(); return err }},
		{"GetResetProcessCmd", 3, func(a *ASDU) error { _, _, err := a.GetResetProcessCmd(); return err }},
		{"GetDelayAcquireCommand", 4, func(a *ASDU) error { _, _, err := a.GetDelayAcquireCommand(); return err }},
		{"GetEndOfInitialization", 3, func(a *ASDU) error { _, _, err := a.GetEndOfInitialization(); return err }},
		{"GetParameterActivation", 3, func(a *ASDU) error { _, err := a.GetParameterActivation(); return err }},
		{"GetParameterNormal", 4, func(a *ASDU) error { _, err := a.GetParameterNormal(); return err }},
		{"GetParameterScaled", 4, func(a *ASDU) error { _, err := a.GetParameterScaled(); return err }},
		{"GetParameterFloat", 6, func(a *ASDU) error { _, err := a.GetParameterFloat(); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTruncatedASDU(t, tt.n)
			if err := tt.call(a); err != ErrInfoObjTruncated {
				t.Fatalf("error = %v, want ErrInfoObjTruncated", err)
			}
		})
	}
}

// TestGet_TruncatedSequenceReturnsError covers the accessors that decode a
// run of information elements: the variable structure qualifier promises
// more elements than the object actually carries.
func TestGet_TruncatedSequenceReturnsError(t *testing.T) {
	tests := []struct {
		name  string
		typ   TypeID
		count byte
		n     int
		call  func(a *ASDU) error
	}{
		{"GetSinglePoint", M_SP_NA_1, 4, 5, func(a *ASDU) error { _, err := a.GetSinglePoint(); return err }},
		{"GetDoublePoint", M_DP_NA_1, 4, 5, func(a *ASDU) error { _, err := a.GetDoublePoint(); return err }},
		{"GetMeasuredValueFloat", M_ME_NC_1, 4, 6, func(a *ASDU) error { _, err := a.GetMeasuredValueFloat(); return err }},
		{"GetIntegratedTotals", M_IT_NA_1, 4, 6, func(a *ASDU) error { _, err := a.GetIntegratedTotals(); return err }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			a := newTruncatedASDU(t, tt.n)
			a.Type = tt.typ
			a.Variable = VariableStruct{IsSequence: false, Number: tt.count}
			if err := tt.call(a); err != ErrInfoObjTruncated {
				t.Fatalf("error = %v, want ErrInfoObjTruncated", err)
			}
		})
	}
}

// TestGet_TruncatedInfoObjAddrReturnsError checks the address decode at each
// configured width, not just the default.
func TestGet_TruncatedInfoObjAddrReturnsError(t *testing.T) {
	for _, size := range []int{1, 2, 3} {
		a := newTruncatedASDU(t, size-1)
		a.InfoObjAddrSize = size
		if _, err := a.GetReadCmd(); err != ErrInfoObjTruncated {
			t.Fatalf("InfoObjAddrSize=%d: error = %v, want ErrInfoObjTruncated", size, err)
		}
	}
}

// TestGet_DoesNotConsumeInfoObj is the guarantee that makes Clone()
// unnecessary before decoding: reading an ASDU must leave it byte-for-byte
// intact, so the same value can be read again and, more to the point,
// echoed back by SendReplyMirror afterwards. Under the old cursor-on-the-
// ASDU design, decoding emptied the information object, and a caller who
// forgot to Clone() first mirrored an empty one back onto the wire.
func TestGet_DoesNotConsumeInfoObj(t *testing.T) {
	p := *ParamsWide
	a := NewASDU(&p, Identifier{
		Type:       C_SC_NA_1,
		Variable:   VariableStruct{IsSequence: false, Number: 1},
		Coa:        CauseOfTransmission{Cause: Activation},
		CommonAddr: GlobalCommonAddr,
	})
	if err := a.AppendInfoObjAddr(0x010203); err != nil {
		t.Fatalf("AppendInfoObjAddr: %v", err)
	}
	a.AppendBytes(0x01)

	before := append([]byte(nil), a.infoObj...)

	first, err := a.GetSingleCmd()
	if err != nil {
		t.Fatalf("first GetSingleCmd: %v", err)
	}
	if string(a.infoObj) != string(before) {
		t.Fatalf("infoObj = % x after decoding, want % x unchanged", a.infoObj, before)
	}

	// Reading again must yield the same thing, not a decode of leftovers.
	second, err := a.GetSingleCmd()
	if err != nil {
		t.Fatalf("second GetSingleCmd: %v", err)
	}
	if first != second {
		t.Fatalf("second read = %+v, want the same as the first %+v", second, first)
	}
	if first.Ioa != 0x010203 || !first.Value {
		t.Fatalf("decoded %+v, want Ioa=0x010203 Value=true", first)
	}
}

// TestGet_QualifierReadAfterAddress pins the decode order: the trailing
// qualifier must come from the byte after the information object address,
// never from the address's own first byte.
func TestGet_QualifierReadAfterAddress(t *testing.T) {
	p := *ParamsWide
	a := NewEmptyASDU(&p)
	// 3-byte IOA 0x010203, then the qualifier byte.
	a.infoObj = append(a.infoObj, 0x03, 0x02, 0x01, byte(QOIStation))

	ioa, qoi, err := a.GetInterrogationCmd()
	if err != nil {
		t.Fatalf("GetInterrogationCmd: %v", err)
	}
	if ioa != 0x010203 {
		t.Errorf("InfoObjAddr = %#x, want 0x010203", ioa)
	}
	if qoi != QOIStation {
		t.Errorf("QualifierOfInterrogation = %d, want %d (the byte after the address, not inside it)", qoi, QOIStation)
	}
}

// TestDecoder_FirstErrorSticks checks the sticky-error contract: once a read
// fails, later reads are no-ops returning zero values and the original error
// is what surfaces, so a caller can decode a whole object and check once.
func TestDecoder_FirstErrorSticks(t *testing.T) {
	p := *ParamsWide
	a := NewEmptyASDU(&p)
	a.infoObj = []byte{0x01}

	d := a.decoder()
	if got := d.readUint16(); got != 0 { // needs 2 bytes, only 1 present
		t.Fatalf("readUint16() = %d, want 0 on truncation", got)
	}
	if d.err != ErrInfoObjTruncated {
		t.Fatalf("err = %v, want ErrInfoObjTruncated", d.err)
	}
	// The single byte that *is* present must not be handed out now.
	if got := d.readByte(); got != 0 {
		t.Fatalf("readByte() after an error = %#x, want 0", got)
	}
	if d.err != ErrInfoObjTruncated {
		t.Fatalf("err = %v, want the first error preserved", d.err)
	}
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
