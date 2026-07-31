// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package main

import (
	"bytes"
	"encoding/csv"
	"io"
	"net"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/gopacket/gopacket/tcpassembly"
	"github.com/thinkgos/go-iecp5/asdu"
)

// iFrame wraps an encoded ASDU in an I-format APCI. cs104 builds these
// internally but does not export a constructor, and a capture-side test wants
// the bytes rather than a connection.
func iFrame(t *testing.T, sendSN, rcvSN uint16, payload []byte) []byte {
	t.Helper()
	if len(payload) > asdu.ASDUSizeMax {
		t.Fatalf("ASDU of %d bytes exceeds the %d maximum", len(payload), asdu.ASDUSizeMax)
	}
	return append([]byte{
		0x68, byte(len(payload) + 4),
		byte(sendSN << 1), byte(sendSN >> 7),
		byte(rcvSN << 1), byte(rcvSN >> 7),
	}, payload...)
}

// uFrame builds a U-format APCI, which carries no ASDU and must produce no row.
func uFrame(function byte) []byte {
	return []byte{0x68, 4, function | 0x03, 0x00, 0x00, 0x00}
}

// singlePointASDU encodes an M_SP_NA_1 with one information object per IOA
// (SQ = 0), which is the layout that puts an address in front of every element.
func singlePointASDU(t *testing.T, ca asdu.CommonAddr, ioas ...asdu.InfoObjAddr) []byte {
	t.Helper()
	a := asdu.NewASDU(asdu.ParamsWide, asdu.Identifier{
		Type:       asdu.M_SP_NA_1,
		Variable:   asdu.VariableStruct{IsSequence: false, Number: byte(len(ioas))},
		Coa:        asdu.CauseOfTransmission{Cause: asdu.Spontaneous},
		CommonAddr: ca,
	})
	for _, ioa := range ioas {
		if err := a.AppendInfoObjAddr(ioa); err != nil {
			t.Fatalf("AppendInfoObjAddr: %v", err)
		}
		a.AppendBytes(byte(asdu.QDSGood))
	}
	raw, err := a.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return raw
}

// floatSequenceASDU encodes an M_ME_NC_1 as a sequence (SQ = 1): a single
// base address followed by n elements at consecutive addresses.
func floatSequenceASDU(t *testing.T, ca asdu.CommonAddr, base asdu.InfoObjAddr, n int) []byte {
	t.Helper()
	a := asdu.NewASDU(asdu.ParamsWide, asdu.Identifier{
		Type:       asdu.M_ME_NC_1,
		Variable:   asdu.VariableStruct{IsSequence: true, Number: byte(n)},
		Coa:        asdu.CauseOfTransmission{Cause: asdu.Periodic},
		CommonAddr: ca,
	})
	if err := a.AppendInfoObjAddr(base); err != nil {
		t.Fatalf("AppendInfoObjAddr: %v", err)
	}
	for i := 0; i < n; i++ {
		a.AppendFloat32(float32(i) + 0.5)
		a.AppendBytes(byte(asdu.QDSGood))
	}
	raw, err := a.MarshalBinary()
	if err != nil {
		t.Fatalf("MarshalBinary: %v", err)
	}
	return raw
}

// writePcap builds a capture in which each element of payloads becomes one
// TCP segment, in order, on a single connection.
func writePcap(t *testing.T, path string, srcIP, dstIP string, srcPort, dstPort int, payloads [][]byte) {
	t.Helper()

	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65536, layers.LinkTypeEthernet); err != nil {
		t.Fatal(err)
	}

	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	seq := uint32(1)
	for i, payload := range payloads {
		eth := &layers.Ethernet{
			SrcMAC:       net.HardwareAddr{0x00, 0x11, 0x22, 0x33, 0x44, 0x55},
			DstMAC:       net.HardwareAddr{0x66, 0x77, 0x88, 0x99, 0xaa, 0xbb},
			EthernetType: layers.EthernetTypeIPv4,
		}
		ip := &layers.IPv4{
			Version: 4, IHL: 5, TTL: 64,
			Protocol: layers.IPProtocolTCP,
			SrcIP:    net.ParseIP(srcIP).To4(),
			DstIP:    net.ParseIP(dstIP).To4(),
		}
		tcp := &layers.TCP{
			SrcPort: layers.TCPPort(srcPort),
			DstPort: layers.TCPPort(dstPort),
			Seq:     seq,
			ACK:     true,
			PSH:     true,
			Window:  4096,
		}
		if err := tcp.SetNetworkLayerForChecksum(ip); err != nil {
			t.Fatal(err)
		}
		seq += uint32(len(payload))

		buf := gopacket.NewSerializeBuffer()
		opts := gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true}
		if err := gopacket.SerializeLayers(buf, opts, eth, ip, tcp, gopacket.Payload(payload)); err != nil {
			t.Fatal(err)
		}
		data := buf.Bytes()

		ci := gopacket.CaptureInfo{
			Timestamp:     base.Add(time.Duration(i) * time.Second),
			CaptureLength: len(data),
			Length:        len(data),
		}
		if err := w.WritePacket(ci, data); err != nil {
			t.Fatal(err)
		}
	}
}

// runExtractor runs the tool over path and returns the parsed CSV rows.
func runExtractor(t *testing.T, path string, configure func(*extractor)) [][]string {
	t.Helper()

	var out bytes.Buffer
	w := csv.NewWriter(&out)
	// Ragged rows are the point of the default output, so the reader must not
	// enforce a fixed field count.
	x := &extractor{
		params:  asdu.ParamsWide,
		port:    2404,
		timeFmt: time.RFC3339Nano,
		csv:     w,
	}
	if configure != nil {
		configure(x)
	}
	if err := x.run(path); err != nil {
		t.Fatalf("run: %v", err)
	}
	w.Flush()
	if err := w.Error(); err != nil {
		t.Fatal(err)
	}

	r := csv.NewReader(bytes.NewReader(out.Bytes()))
	r.FieldsPerRecord = -1
	rows, err := r.ReadAll()
	if err != nil {
		t.Fatalf("parse CSV: %v", err)
	}
	return rows
}

// The whole point of buffering per direction: a segment boundary is not a
// frame boundary. This capture puts two APDUs in one segment, splits a third
// across two, and mixes in a U-frame that carries no ASDU at all.
func TestExtract_SegmentBoundariesAreNotFrameBoundaries(t *testing.T) {
	first := iFrame(t, 0, 0, singlePointASDU(t, 1, 100, 101, 102))
	second := iFrame(t, 1, 0, singlePointASDU(t, 1, 200))
	third := iFrame(t, 2, 0, floatSequenceASDU(t, 7, 5000, 4))

	// Two APDUs in one segment; the third split mid-frame; a TESTFR U-frame
	// riding along in the same segment as the tail.
	split := len(third) / 2
	payloads := [][]byte{
		append(append([]byte{}, first...), second...),
		third[:split],
		append(append([]byte{}, third[split:]...), uFrame(0x40)...),
	}

	path := filepath.Join(t.TempDir(), "capture.pcap")
	writePcap(t, path, "10.0.0.1", "10.0.0.2", 2404, 50000, payloads)

	rows := runExtractor(t, path, nil)

	// runExtractor drives the extractor directly, which writes no header --
	// that belongs to main's flag handling.
	want := [][]string{
		{"2024-03-01T12:00:00Z", "10.0.0.1", "10.0.0.2", "M_SP_NA_1", "1", "100", "101", "102"},
		{"2024-03-01T12:00:00Z", "10.0.0.1", "10.0.0.2", "M_SP_NA_1", "1", "200"},
		// Reassembled across two segments, and timestamped with the segment
		// that completed it.
		{"2024-03-01T12:00:02Z", "10.0.0.1", "10.0.0.2", "M_ME_NC_1", "7", "5000", "5001", "5002", "5003"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows mismatch\n got: %q\nwant: %q", rows, want)
	}
}

// A capture that begins mid-frame must recover at the next start byte rather
// than being discarded: ReadAPDU resynchronizes on 0x68 for this case.
func TestExtract_RecoversFromCaptureStartingMidFrame(t *testing.T) {
	whole := iFrame(t, 0, 0, singlePointASDU(t, 1, 42))
	next := iFrame(t, 1, 0, singlePointASDU(t, 1, 43))

	// Start the capture in the middle of the first APDU.
	payloads := [][]byte{append(append([]byte{}, whole[4:]...), next...)}

	path := filepath.Join(t.TempDir(), "midframe.pcap")
	writePcap(t, path, "192.168.1.5", "192.168.1.9", 2404, 33000, payloads)

	rows := runExtractor(t, path, nil)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want only the frame after the truncated one: %q", len(rows), rows)
	}
	got := rows[0]
	want := []string{"2024-03-01T12:00:00Z", "192.168.1.5", "192.168.1.9", "M_SP_NA_1", "1", "43"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestExtract_FiltersByTypeAndCommonAddr(t *testing.T) {
	payloads := [][]byte{bytes.Join([][]byte{
		iFrame(t, 0, 0, singlePointASDU(t, 1, 10)),
		iFrame(t, 1, 0, floatSequenceASDU(t, 1, 20, 2)),
		iFrame(t, 2, 0, singlePointASDU(t, 9, 30)),
	}, nil)}

	path := filepath.Join(t.TempDir(), "filter.pcap")
	writePcap(t, path, "10.1.1.1", "10.1.1.2", 2404, 40000, payloads)

	t.Run("by type", func(t *testing.T) {
		rows := runExtractor(t, path, func(x *extractor) {
			x.types = map[asdu.TypeID]bool{asdu.M_ME_NC_1: true}
		})
		if len(rows) != 1 {
			t.Fatalf("got %d rows, want exactly the M_ME_NC_1: %q", len(rows), rows)
		}
		if rows[0][3] != "M_ME_NC_1" {
			t.Fatalf("kept %q, want M_ME_NC_1", rows[0][3])
		}
	})

	t.Run("by common address", func(t *testing.T) {
		rows := runExtractor(t, path, func(x *extractor) {
			x.commonAddrs = map[asdu.CommonAddr]bool{9: true}
		})
		if len(rows) != 1 || rows[0][4] != "9" {
			t.Fatalf("got %q, want the single CA 9 row", rows)
		}
	})
}

// -one-ioa-per-row trades the ragged rows for a rectangular file, repeating
// the ASDU's fields against each of its addresses.
func TestExtract_OneIOAPerRow(t *testing.T) {
	payloads := [][]byte{iFrame(t, 0, 0, singlePointASDU(t, 3, 7, 8, 9))}

	path := filepath.Join(t.TempDir(), "perioa.pcap")
	writePcap(t, path, "10.2.2.1", "10.2.2.2", 2404, 41000, payloads)

	rows := runExtractor(t, path, func(x *extractor) { x.perIOA = true })

	want := [][]string{
		{"2024-03-01T12:00:00Z", "10.2.2.1", "10.2.2.2", "M_SP_NA_1", "3", "7"},
		{"2024-03-01T12:00:00Z", "10.2.2.1", "10.2.2.2", "M_SP_NA_1", "3", "8"},
		{"2024-03-01T12:00:00Z", "10.2.2.1", "10.2.2.2", "M_SP_NA_1", "3", "9"},
	}
	if !reflect.DeepEqual(rows, want) {
		t.Fatalf("rows mismatch\n got: %q\nwant: %q", rows, want)
	}
}

// A non-104 flow must not accumulate without bound while waiting for a frame
// that will never arrive.
func TestStream_BufferIsCapped(t *testing.T) {
	x := &extractor{params: asdu.ParamsWide, csv: csv.NewWriter(io.Discard)}
	s := &stream{x: x}
	// No 0x68 anywhere, so nothing ever frames.
	s.Reassembled([]tcpassembly.Reassembly{
		{Bytes: bytes.Repeat([]byte{0xff}, maxStreamBuffer+1), Seen: time.Now()},
	})
	if x.rows != 0 {
		t.Fatalf("garbage produced %d rows", x.rows)
	}
	if len(s.buf) != 0 {
		t.Fatalf("buffer retained %d bytes past the %d cap", len(s.buf), maxStreamBuffer)
	}
}

// A gap in the capture must not be spliced over: joining the bytes on either
// side of a hole yields a frame that was never on the wire.
func TestStream_GapDiscardsThePartialFrame(t *testing.T) {
	x := &extractor{params: asdu.ParamsWide, csv: csv.NewWriter(io.Discard)}
	s := &stream{x: x}

	apdu := iFrame(t, 0, 0, singlePointASDU(t, 1, 500))
	half := len(apdu) / 2
	whole := iFrame(t, 1, 0, singlePointASDU(t, 1, 501))

	// First half, then a gap, then a complete frame. The complete one must
	// come through; the severed half must not be joined to it.
	s.Reassembled([]tcpassembly.Reassembly{{Bytes: apdu[:half], Seen: time.Now()}})
	s.Reassembled([]tcpassembly.Reassembly{{Bytes: whole, Skip: 12, Seen: time.Now()}})

	if x.rows != 1 {
		t.Fatalf("got %d rows, want exactly the frame that arrived intact", x.rows)
	}
}

func TestParseTypeIDs(t *testing.T) {
	got, err := parseTypeIDs("1, M_ME_NC_1 ,36")
	if err != nil {
		t.Fatal(err)
	}
	want := map[asdu.TypeID]bool{
		asdu.M_SP_NA_1: true, // 1, numeric
		asdu.M_ME_NC_1: true, // symbolic
		asdu.M_ME_TF_1: true, // 36, numeric
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v, want %v", got, want)
	}

	if _, err := parseTypeIDs("NOT_A_TYPE"); err == nil {
		t.Fatal("an unknown type name should be rejected, not silently ignored")
	}

	// Empty means "keep everything", which is nil rather than an empty set --
	// an empty set would keep nothing.
	if m, err := parseTypeIDs("  "); err != nil || m != nil {
		t.Fatalf("empty -types = (%v, %v), want (nil, nil)", m, err)
	}
}

type seg struct {
	seq     uint32
	payload []byte
}

// writeSegs emits segments with explicit sequence numbers, so a test can put
// them on the wire out of order or repeat one.
func writeSegs(t *testing.T, path string, segs []seg) {
	t.Helper()
	f, err := os.Create(path)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	w := pcapgo.NewWriter(f)
	if err := w.WriteFileHeader(65536, layers.LinkTypeEthernet); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2024, 3, 1, 12, 0, 0, 0, time.UTC)
	for i, s := range segs {
		eth := &layers.Ethernet{
			SrcMAC: net.HardwareAddr{0, 1, 2, 3, 4, 5}, DstMAC: net.HardwareAddr{6, 7, 8, 9, 10, 11},
			EthernetType: layers.EthernetTypeIPv4,
		}
		ip := &layers.IPv4{Version: 4, IHL: 5, TTL: 64, Protocol: layers.IPProtocolTCP,
			SrcIP: net.ParseIP("10.0.0.1").To4(), DstIP: net.ParseIP("10.0.0.2").To4()}
		tcp := &layers.TCP{SrcPort: 2404, DstPort: 50000, Seq: s.seq, ACK: true, PSH: true, Window: 4096}
		if err := tcp.SetNetworkLayerForChecksum(ip); err != nil {
			t.Fatal(err)
		}
		buf := gopacket.NewSerializeBuffer()
		if err := gopacket.SerializeLayers(buf, gopacket.SerializeOptions{FixLengths: true, ComputeChecksums: true},
			eth, ip, tcp, gopacket.Payload(s.payload)); err != nil {
			t.Fatal(err)
		}
		data := buf.Bytes()
		if err := w.WritePacket(gopacket.CaptureInfo{
			Timestamp: base.Add(time.Duration(i) * time.Second), CaptureLength: len(data), Length: len(data),
		}, data); err != nil {
			t.Fatal(err)
		}
	}
}

// One APDU split across two segments that arrive out of order -- routine in
// any real capture.
func TestOutOfOrderSegments(t *testing.T) {
	apdu := iFrame(t, 0, 0, singlePointASDU(t, 1, 4242))
	half := len(apdu) / 2

	path := filepath.Join(t.TempDir(), "ooo.pcap")
	// Second half on the wire first.
	writeSegs(t, path, []seg{
		{seq: 1 + uint32(half), payload: apdu[half:]},
		{seq: 1, payload: apdu[:half]},
	})

	rows := runExtractor(t, path, nil)
	t.Logf("rows: %q", rows)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: the APDU was not reassembled", len(rows))
	}
	if rows[0][5] != "4242" {
		t.Fatalf("got IOA %q, want 4242", rows[0][5])
	}
}

// A retransmitted segment must not be counted twice.
func TestRetransmission(t *testing.T) {
	apdu := iFrame(t, 0, 0, singlePointASDU(t, 1, 77))
	path := filepath.Join(t.TempDir(), "retx.pcap")
	writeSegs(t, path, []seg{
		{seq: 1, payload: apdu},
		{seq: 1, payload: apdu}, // same bytes, same seq: a retransmit
	})

	rows := runExtractor(t, path, nil)
	t.Logf("rows: %q", rows)
	if len(rows) != 1 {
		t.Fatalf("got %d rows, want 1: the retransmitted segment was counted again", len(rows))
	}
}
