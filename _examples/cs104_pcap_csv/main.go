// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

// Command cs104_pcap_csv extracts IEC 60870-5-104 information objects from a
// packet capture and writes one CSV row per ASDU.
//
//	cs104_pcap_csv -in capture.pcap -out objects.csv -types 1,3,9,13,30,36
//
// Each row is:
//
//	frame_time,src_ip,dst_ip,typeid,addr,ioa,ioa,...
//
// The IOA columns are variable in number: an ASDU carries one information
// object per IOA, and a single one may carry many. Rows are therefore ragged
// -- every consumer of this file has to read it by position, not by a fixed
// header width. Pass -one-ioa-per-row for the rectangular alternative.
//
// # Reading a capture is not the same as reading a connection
//
// Two things make a capture harder than the live socket the library normally
// reads, and both are handled here:
//
// A TCP segment is not an APDU. One segment routinely carries several APDUs
// (they are at most 255 bytes, and a busy link fills the MSS), and an APDU
// may equally be split across two segments. So payload is accumulated per
// direction of each TCP connection and framed out of that byte stream, not
// out of individual packets.
//
// A capture starts wherever the capture started. It may begin mid-frame, and
// it may be missing segments entirely. cs104.ReadAPDU resynchronizes on the
// 0x68 start byte for exactly this reason, so a stream that begins in the
// middle of a frame recovers at the next one rather than being discarded.
package main

import (
	"bytes"
	"encoding/binary"
	"encoding/csv"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/gopacket/gopacket"
	"github.com/gopacket/gopacket/layers"
	"github.com/gopacket/gopacket/pcapgo"
	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/cs104"
)

// maxStreamBuffer caps what is held per direction while waiting for the rest
// of an APDU. A frame is at most 255 bytes, so anything beyond a few of them
// means the stream is not IEC 104 (or its start was never captured) and the
// bytes will never resolve. Without the cap, one such flow grows until the
// process dies.
const maxStreamBuffer = 1 << 16

func main() {
	var (
		inPath   = flag.String("in", "", "pcap or pcapng file to read (required)")
		outPath  = flag.String("out", "", "CSV file to write (default stdout)")
		typeList = flag.String("types", "", "comma-separated type IDs to keep, numeric (13) or symbolic (M_ME_NC_1); empty keeps all")
		caList   = flag.String("ca", "", "comma-separated common addresses to keep; empty keeps all")
		port     = flag.Int("port", cs104.Port, "TCP port carrying IEC 104; 0 matches any port")
		narrow   = flag.Bool("narrow", false, "decode with asdu.ParamsNarrow instead of ParamsWide")
		timeFmt  = flag.String("time-format", time.RFC3339Nano, "Go layout for the frame_time column")
		perIOA   = flag.Bool("one-ioa-per-row", false, "emit one row per information object instead of one row per ASDU")
		header   = flag.Bool("header", true, "write a header row")
	)
	flag.Parse()

	if *inPath == "" {
		flag.Usage()
		log.Fatal("-in is required")
	}

	types, err := parseTypeIDs(*typeList)
	if err != nil {
		log.Fatalf("-types: %v", err)
	}
	commonAddrs, err := parseCommonAddrs(*caList)
	if err != nil {
		log.Fatalf("-ca: %v", err)
	}

	params := asdu.ParamsWide
	if *narrow {
		params = asdu.ParamsNarrow
	}

	out := io.Writer(os.Stdout)
	if *outPath != "" {
		f, err := os.Create(*outPath)
		if err != nil {
			log.Fatalf("create %s: %v", *outPath, err)
		}
		defer func() {
			if err := f.Close(); err != nil {
				log.Fatalf("close %s: %v", *outPath, err)
			}
		}()
		out = f
	}

	w := csv.NewWriter(out)
	if *header {
		if *perIOA {
			err = w.Write([]string{"frame_time", "src_ip", "dst_ip", "typeid", "addr", "ioa"})
		} else {
			// The IOA columns are ragged, so the header names only the fixed
			// prefix; everything after "addr" is an IOA.
			err = w.Write([]string{"frame_time", "src_ip", "dst_ip", "typeid", "addr", "ioa..."})
		}
		if err != nil {
			log.Fatalf("write header: %v", err)
		}
	}

	x := &extractor{
		params:      params,
		types:       types,
		commonAddrs: commonAddrs,
		port:        *port,
		timeFmt:     *timeFmt,
		perIOA:      *perIOA,
		csv:         w,
		streams:     make(map[streamKey]*stream),
	}

	if err := x.run(*inPath); err != nil {
		log.Fatalf("%s: %v", *inPath, err)
	}

	w.Flush()
	if err := w.Error(); err != nil {
		log.Fatalf("write CSV: %v", err)
	}

	log.Printf("%d packets, %d APDUs, %d ASDUs, %d rows (%d skipped by filter, %d undecodable)",
		x.packets, x.apdus, x.asdus, x.rows, x.filtered, x.undecodable)
}

// streamKey identifies one direction of one TCP connection. Direction
// matters: each carries its own independent byte stream and its own frame
// boundaries, so the two must never share a buffer.
type streamKey struct {
	net, transport gopacket.Flow
}

// stream holds the not-yet-framed tail of one direction's payload.
type stream struct {
	buf []byte
	// src and dst are cached so a row can be written without re-deriving
	// them from the flow on every ASDU.
	src, dst string
}

type extractor struct {
	params      *asdu.Params
	types       map[asdu.TypeID]bool
	commonAddrs map[asdu.CommonAddr]bool
	port        int
	timeFmt     string
	perIOA      bool
	csv         *csv.Writer
	streams     map[streamKey]*stream

	packets, apdus, asdus, rows, filtered, undecodable int
}

func (x *extractor) run(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	source, err := packetSource(f)
	if err != nil {
		return err
	}

	for {
		data, ci, err := source.ReadPacketData()
		if errors.Is(err, io.EOF) {
			return nil
		}
		if err != nil {
			// A truncated final packet is common in captures cut short; it
			// is not a reason to discard everything already extracted.
			log.Printf("stopping early: %v", err)
			return nil
		}
		x.packets++

		packet := gopacket.NewPacket(data, source.LinkType(), gopacket.DecodeOptions{
			Lazy:   true,
			NoCopy: true,
		})
		x.handlePacket(packet, ci.Timestamp)
	}
}

// packetReader is the intersection of pcapgo's classic and pcapng readers.
type packetReader interface {
	ReadPacketData() ([]byte, gopacket.CaptureInfo, error)
	LinkType() layers.LinkType
}

// packetSource opens f as either classic pcap or pcapng, chosen by the magic
// number rather than by the file extension -- ".pcap" is routinely a pcapng
// file in practice.
func packetSource(f *os.File) (packetReader, error) {
	var magic [4]byte
	if _, err := io.ReadFull(f, magic[:]); err != nil {
		return nil, fmt.Errorf("read magic: %w", err)
	}
	if _, err := f.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}

	// 0x0a0d0d0a is the pcapng Section Header Block type, and is byte-order
	// independent. Classic pcap is 0xa1b2c3d4 (or 0xd4c3b2a1 byte-swapped,
	// and the ...4d3c variants for nanosecond precision), all of which
	// pcapgo.NewReader handles itself.
	if binary.BigEndian.Uint32(magic[:]) == 0x0a0d0d0a {
		r, err := pcapgo.NewNgReader(f, pcapgo.DefaultNgReaderOptions)
		if err != nil {
			return nil, fmt.Errorf("pcapng: %w", err)
		}
		return &ngReader{r}, nil
	}
	r, err := pcapgo.NewReader(f)
	if err != nil {
		return nil, fmt.Errorf("pcap: %w", err)
	}
	return r, nil
}

// ngReader adapts pcapgo.NgReader, whose LinkType is per-interface, to the
// single-link-type shape the classic reader has.
type ngReader struct{ *pcapgo.NgReader }

func (r *ngReader) LinkType() layers.LinkType { return r.NgReader.LinkType() }

func (x *extractor) handlePacket(packet gopacket.Packet, ts time.Time) {
	netLayer := packet.NetworkLayer()
	if netLayer == nil {
		return // not IP: ARP, LLDP, and so on
	}
	tcpLayer := packet.Layer(layers.LayerTypeTCP)
	if tcpLayer == nil {
		return
	}
	tcp, _ := tcpLayer.(*layers.TCP)
	if len(tcp.Payload) == 0 {
		return // pure ACK, or a handshake/teardown segment
	}
	if x.port != 0 && int(tcp.SrcPort) != x.port && int(tcp.DstPort) != x.port {
		return
	}

	key := streamKey{net: netLayer.NetworkFlow(), transport: tcp.TransportFlow()}
	s := x.streams[key]
	if s == nil {
		src, dst := netLayer.NetworkFlow().Endpoints()
		s = &stream{src: src.String(), dst: dst.String()}
		x.streams[key] = s
	}
	s.buf = append(s.buf, tcp.Payload...)

	for _, apdu := range s.frames() {
		x.apdus++
		x.handleAPDU(apdu, s, ts)
	}
}

// frames pulls every complete APDU out of the buffered stream, leaving any
// partial trailing frame for the next segment to complete.
func (s *stream) frames() [][]byte {
	var out [][]byte
	for {
		r := bytes.NewReader(s.buf)
		before := r.Len()
		apdu, err := cs104.ReadAPDU(r)
		if err != nil {
			// Out of data mid-frame. Keep the buffer as it stands: the next
			// segment appends to it and the same scan runs again, which is
			// why this must not consume on failure.
			break
		}
		s.buf = s.buf[before-r.Len():]
		out = append(out, apdu)
	}

	if len(s.buf) > maxStreamBuffer {
		// Not IEC 104, or its beginning was never captured. Either way these
		// bytes will not resolve into a frame.
		s.buf = nil
	}
	return out
}

func (x *extractor) handleAPDU(apdu []byte, s *stream, ts time.Time) {
	// Only I-frames carry an ASDU; S- and U-frames are pure APCI.
	//
	// This reads the frame type off the control field directly rather than
	// type-switching on what cs104.ParseAPCI returns. ParseAPCI hands back an
	// `any` holding one of iAPCI/sAPCI/uAPCI, and those types are unexported,
	// so a caller outside the package has nothing to assert against. The bit
	// itself is stable and specified: subclass 5.1 defines the I-format APDU
	// as the one whose first control octet has bit 1 clear.
	if apdu[2]&0x01 != 0 {
		return
	}

	_, payload, err := cs104.ParseAPCI(apdu)
	if err != nil {
		x.undecodable++
		return
	}

	a := asdu.NewEmptyASDU(x.params)
	if err := a.UnmarshalBinary(payload); err != nil {
		// Malformed, truncated, or a type this build does not know. Ordinary
		// for captured traffic -- count it and move on.
		x.undecodable++
		return
	}
	x.asdus++

	if x.types != nil && !x.types[a.Type] {
		x.filtered++
		return
	}
	if x.commonAddrs != nil && !x.commonAddrs[a.CommonAddr] {
		x.filtered++
		return
	}

	ioas, err := infoObjAddrs(a, payload, x.params)
	if err != nil {
		x.undecodable++
		return
	}

	when := ts.Format(x.timeFmt)
	typeID := typeName(a.Type)
	addr := strconv.FormatUint(uint64(a.CommonAddr), 10)

	if x.perIOA {
		for _, ioa := range ioas {
			x.writeRow([]string{when, s.src, s.dst, typeID, addr, strconv.FormatUint(uint64(ioa), 10)})
		}
		return
	}

	row := make([]string, 0, 5+len(ioas))
	row = append(row, when, s.src, s.dst, typeID, addr)
	for _, ioa := range ioas {
		row = append(row, strconv.FormatUint(uint64(ioa), 10))
	}
	x.writeRow(row)
}

func (x *extractor) writeRow(row []string) {
	if err := x.csv.Write(row); err != nil {
		log.Fatalf("write CSV: %v", err)
	}
	x.rows++
}

// infoObjAddrs returns the address of every information object in the ASDU.
//
// It walks the raw information-object bytes rather than going through the
// typed Get* accessors, so it covers every type identification with a known
// object size instead of needing a case per type. The layout is set by the
// variable structure qualifier (subclass 7.2.2): with SQ set, one address is
// followed by N elements at consecutive addresses; with SQ clear, each of the
// N elements carries its own address.
func infoObjAddrs(a *asdu.ASDU, rawASDU []byte, params *asdu.Params) ([]asdu.InfoObjAddr, error) {
	objSize, err := asdu.GetInfoObjSize(a.Type)
	if err != nil {
		return nil, err
	}
	infoObj := rawASDU[params.IdentifierSize():]
	addrSize := params.InfoObjAddrSize
	n := int(a.Variable.Number)
	if n == 0 {
		return nil, nil
	}

	out := make([]asdu.InfoObjAddr, 0, n)
	if a.Variable.IsSequence {
		if len(infoObj) < addrSize {
			return nil, io.ErrUnexpectedEOF
		}
		base := parseInfoObjAddr(infoObj[:addrSize])
		for i := 0; i < n; i++ {
			out = append(out, base+asdu.InfoObjAddr(i))
		}
		return out, nil
	}

	for i := 0; i < n; i++ {
		off := i * (addrSize + objSize)
		if off+addrSize > len(infoObj) {
			return nil, io.ErrUnexpectedEOF
		}
		out = append(out, parseInfoObjAddr(infoObj[off:off+addrSize]))
	}
	return out, nil
}

// parseInfoObjAddr decodes a 1-, 2-, or 3-octet information object address,
// which is little-endian (subclass 7.2.5).
func parseInfoObjAddr(b []byte) asdu.InfoObjAddr {
	var v uint32
	for i := len(b) - 1; i >= 0; i-- {
		v = v<<8 | uint32(b[i])
	}
	return asdu.InfoObjAddr(v)
}

// parseTypeIDs accepts numeric ids ("13") and symbolic names ("M_ME_NC_1"),
// mixed. An empty list returns nil, meaning "keep everything" -- distinct
// from an empty set, which would keep nothing.
func parseTypeIDs(s string) (map[asdu.TypeID]bool, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	out := make(map[asdu.TypeID]bool)
	for _, field := range strings.Split(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		if n, err := strconv.ParseUint(field, 10, 8); err == nil {
			out[asdu.TypeID(n)] = true
			continue
		}
		id, ok := typeIDByName(field)
		if !ok {
			return nil, fmt.Errorf("unknown type identification %q", field)
		}
		out[id] = true
	}
	return out, nil
}

// typeName returns the bare mnemonic, e.g. "M_ME_NC_1".
//
// asdu.TypeID.String decorates it as "TID<M_ME_NC_1>", which reads well in a
// log line but not in a CSV column that something downstream has to match on.
// An unrecognized id stringifies to its decimal value, which passes through
// here unchanged and is the right thing to record: the capture said it, even
// if this build has no name for it.
func typeName(id asdu.TypeID) string {
	s := id.String()
	s = strings.TrimPrefix(s, "TID<")
	s = strings.TrimSuffix(s, ">")
	return strings.TrimSpace(s)
}

// typeIDByName resolves a symbolic name by asking every type id what it is
// called. asdu exposes TypeID.String but no reverse lookup, and 256 string
// comparisons once at startup is not worth a generated table.
func typeIDByName(name string) (asdu.TypeID, bool) {
	for i := 0; i <= 255; i++ {
		id := asdu.TypeID(i)
		if strings.EqualFold(typeName(id), name) {
			return id, true
		}
	}
	return 0, false
}

func parseCommonAddrs(s string) (map[asdu.CommonAddr]bool, error) {
	if strings.TrimSpace(s) == "" {
		return nil, nil
	}
	out := make(map[asdu.CommonAddr]bool)
	for _, field := range strings.Split(s, ",") {
		field = strings.TrimSpace(field)
		if field == "" {
			continue
		}
		n, err := strconv.ParseUint(field, 10, 16)
		if err != nil {
			return nil, fmt.Errorf("bad common address %q", field)
		}
		out[asdu.CommonAddr(n)] = true
	}
	return out, nil
}
