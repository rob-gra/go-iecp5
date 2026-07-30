// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import "io"

// ReadAPDU reads one complete IEC 60870-5-104 APDU frame from r: a start
// byte (0x68), a length byte, and that many following bytes (control field
// + ASDU). The returned slice is exactly one frame, ready for ParseAPCI.
//
// Bytes preceding a valid start byte are skipped one at a time, so ReadAPDU
// resynchronizes with the stream after corrupted or misaligned data -- both
// useful for a noisy live link and for a byte stream that starts mid-frame,
// e.g. a TCP conversation reassembled from a pcap capture that begins
// partway through.
//
// ReadAPDU works with any io.Reader: a live net.Conn, or a
// bytes.Reader/bufio.Reader over TCP payload bytes already reassembled by a
// packet-capture tool. It returns the error from the underlying read
// unmodified (typically io.EOF or io.ErrUnexpectedEOF once r is exhausted)
// and does not itself interpret connection-lifecycle semantics (temporary
// vs. fatal network errors, "connection closed" vs. "end of capture", ...)
// -- that distinction belongs to the caller, who knows what r actually is.
func ReadAPDU(r io.Reader) ([]byte, error) {
	rawData := make([]byte, APDUSizeMax)

	// Fast path: a well-formed stream always has a frame starting at the
	// very next byte, so read the 2-byte header (start + length) in one
	// call rather than one byte at a time.
	if _, err := io.ReadFull(r, rawData[:2]); err != nil {
		return nil, err
	}

	for {
		if rawData[0] == startFrame {
			length := int(rawData[1]) + 2
			if length >= APCICtlFiledSize+2 && length <= APDUSizeMax {
				if _, err := io.ReadFull(r, rawData[2:length]); err != nil {
					return nil, err
				}
				return rawData[:length], nil
			}
		}

		// Not a real frame at this offset (bad start byte, or a start byte
		// whose declared length doesn't fit APCI bounds): resynchronize by
		// shifting the window forward one byte and reading a single fresh
		// byte, rather than discarding both buffered bytes -- a valid start
		// byte could otherwise sit at the offset we'd skip past.
		rawData[0] = rawData[1]
		if _, err := io.ReadFull(r, rawData[1:2]); err != nil {
			return nil, err
		}
	}
}
