// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"crypto/tls"
	"errors"
	"net"
	"net/url"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// DefaultReconnectInterval defined default value
const DefaultReconnectInterval = 1 * time.Minute

type seqPending struct {
	seq      uint16
	sendTime time.Time
}

// seqNoModulus is the wraparound modulus of the 15-bit I-frame sequence
// number space, see IEC 60870-5-104, subclass 5.5.
const seqNoModulus = 1 << 15

// 回绕机制, returns the count of sequence numbers between nextAckNo(inclusive)
// and nextSeqNo(exclusive), accounting for wraparound of the 15-bit sequence
// number space.
func seqNoCount(nextAckNo, nextSeqNo uint16) uint16 {
	if nextAckNo > nextSeqNo {
		nextSeqNo += seqNoModulus
	}
	return nextSeqNo - nextAckNo
}

// prevSeqNo returns (seq-1) mod 32768, the sequence number immediately
// preceding seq in the 15-bit I-frame sequence space. A plain "seq-1"
// underflows when seq is 0, since seq is stored in a 16-bit integer.
func prevSeqNo(seq uint16) uint16 {
	return (seq - 1) & (seqNoModulus - 1)
}

// nextSeqNo returns (seq+1) mod 32768, the sequence number immediately
// following seq in the 15-bit I-frame sequence space.
func nextSeqNo(seq uint16) uint16 {
	return (seq + 1) & (seqNoModulus - 1)
}

// confirmSeqNo validates an incoming ack (rcvSN) against the outstanding
// pending queue of sent I-frames identified by ackNoSend..seqNoSend. If
// ackNo is valid, it returns the pending queue trimmed of every entry the
// ack confirms; otherwise it returns pending unchanged and ok false.
func confirmSeqNo(pending []seqPending, ackNoSend, seqNoSend, ackNo uint16) (_ []seqPending, ok bool) {
	if ackNo == ackNoSend {
		return pending, true
	}
	// new acks validate, ack 不能在 req seq 前面,出错
	if seqNoCount(ackNoSend, seqNoSend) < seqNoCount(ackNo, seqNoSend) {
		return pending, false
	}

	// confirm reception
	want := prevSeqNo(ackNo)
	for i, v := range pending {
		if v.seq == want {
			pending = pending[i+1:]
			break
		}
	}
	return pending, true
}

// hostOnly strips the port from a net.Addr's string form, e.g.
// "192.168.1.10:2404" -> "192.168.1.10". Addresses that don't parse as
// host:port (unusual for TCP, but not guaranteed by the net.Addr interface)
// are returned unchanged.
func hostOnly(addr net.Addr) string {
	host := addr.String()
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	return host
}

// commonAddrSetFilter returns a filter function that accepts exactly the
// given common addresses. Used by AllowCommonAddrs on both Server and
// ClientOption as a convenience over SetCommonAddrFilter for the common
// case of a small, static set of owned common addresses.
func commonAddrSetFilter(cas []asdu.CommonAddr) func(asdu.CommonAddr) bool {
	allowed := make(map[asdu.CommonAddr]struct{}, len(cas))
	for _, ca := range cas {
		allowed[ca] = struct{}{}
	}
	return func(ca asdu.CommonAddr) bool {
		_, ok := allowed[ca]
		return ok
	}
}

func openConnection(uri *url.URL, tlsc *tls.Config, timeout time.Duration) (net.Conn, error) {
	switch uri.Scheme {
	case "tcp":
		return net.DialTimeout("tcp", uri.Host, timeout)
	case "ssl":
		fallthrough
	case "tls":
		fallthrough
	case "tcps":
		return tls.DialWithDialer(&net.Dialer{Timeout: timeout}, "tcp", uri.Host, tlsc)
	}
	return nil, errors.New("Unknown protocol")
}
