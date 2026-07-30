package cs104

import (
	"bytes"
	"io"
	"testing"
)

func TestReadAPDU_WellFormedFrame(t *testing.T) {
	want := newUFrame(uStartDtActive)
	got, err := ReadAPDU(bytes.NewReader(want))
	if err != nil {
		t.Fatalf("ReadAPDU() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadAPDU() = % x, want % x", got, want)
	}
}

func TestReadAPDU_BackToBackFrames(t *testing.T) {
	f1 := newUFrame(uStartDtActive)
	f2 := newSFrame(5)
	f3, err := newIFrame(0, 0, []byte{0x01, 0x02})
	if err != nil {
		t.Fatalf("newIFrame: %v", err)
	}

	r := bytes.NewReader(append(append(append([]byte{}, f1...), f2...), f3...))
	for i, want := range [][]byte{f1, f2, f3} {
		got, err := ReadAPDU(r)
		if err != nil {
			t.Fatalf("ReadAPDU() #%d error = %v", i, err)
		}
		if !bytes.Equal(got, want) {
			t.Fatalf("ReadAPDU() #%d = % x, want % x", i, got, want)
		}
	}
	if _, err := ReadAPDU(r); err != io.EOF {
		t.Fatalf("ReadAPDU() after last frame error = %v, want io.EOF", err)
	}
}

func TestReadAPDU_SkipsGarbageAndResyncs(t *testing.T) {
	want := newUFrame(uStartDtActive)

	tests := []struct {
		name    string
		garbage []byte
	}{
		{"single garbage byte", []byte{0x00}},
		{"even number of garbage bytes", []byte{0xaa, 0xbb}},
		{"odd number of garbage bytes", []byte{0xaa, 0xbb, 0xcc}},
		// The byte after the embedded startFrame must not itself look like
		// a plausible length (>= APCICtlFiledSize+2-2), or ReadAPDU will
		// reasonably (if wrongly, in this synthetic case) treat it as a
		// real frame start and try to read a body that isn't there.
		{"garbage containing a false start byte", []byte{0x11, startFrame, 0x00, 0x22}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stream := append(append([]byte{}, tt.garbage...), want...)
			got, err := ReadAPDU(bytes.NewReader(stream))
			if err != nil {
				t.Fatalf("ReadAPDU() error = %v", err)
			}
			if !bytes.Equal(got, want) {
				t.Fatalf("ReadAPDU() = % x, want % x", got, want)
			}
		})
	}
}

func TestReadAPDU_InvalidLengthByteResyncs(t *testing.T) {
	want := newUFrame(uStartDtActive)

	// A start byte immediately followed by a length byte that can't form a
	// valid frame (too large: claims an APDU bigger than APDUSizeMax) must
	// be treated as a false match, not a fatal error.
	falseStart := []byte{startFrame, 0xff}
	stream := append(append([]byte{}, falseStart...), want...)

	got, err := ReadAPDU(bytes.NewReader(stream))
	if err != nil {
		t.Fatalf("ReadAPDU() error = %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("ReadAPDU() = % x, want % x", got, want)
	}
}

func TestReadAPDU_TruncatedHeaderReturnsError(t *testing.T) {
	_, err := ReadAPDU(bytes.NewReader([]byte{startFrame}))
	if err == nil {
		t.Fatal("ReadAPDU() error = nil, want an error for a truncated header")
	}
}

func TestReadAPDU_TruncatedBodyReturnsError(t *testing.T) {
	full := newUFrame(uStartDtActive)
	_, err := ReadAPDU(bytes.NewReader(full[:len(full)-1]))
	if err == nil {
		t.Fatal("ReadAPDU() error = nil, want an error for a truncated body")
	}
}

func TestReadAPDU_EmptyReaderReturnsEOF(t *testing.T) {
	_, err := ReadAPDU(bytes.NewReader(nil))
	if err != io.EOF {
		t.Fatalf("ReadAPDU() error = %v, want io.EOF", err)
	}
}

func TestParseAPCI_TooShortReturnsError(t *testing.T) {
	_, _, err := ParseAPCI([]byte{startFrame, 0x04, 0x07, 0x00, 0x00})
	if err == nil {
		t.Fatal("ParseAPCI() error = nil, want an error for a too-short frame")
	}
}

func TestParseAPCI_DelegatesToParse(t *testing.T) {
	frame := newUFrame(uStartDtActive)
	apci, asduPayload, err := ParseAPCI(frame)
	if err != nil {
		t.Fatalf("ParseAPCI() error = %v", err)
	}
	u, ok := apci.(uAPCI)
	if !ok || u.function != uStartDtActive {
		t.Fatalf("ParseAPCI() apci = %#v, want uAPCI{function: StartDtActive}", apci)
	}
	if len(asduPayload) != 0 {
		t.Fatalf("ParseAPCI() asduPayload = % x, want empty (U-frames carry no ASDU)", asduPayload)
	}
}
