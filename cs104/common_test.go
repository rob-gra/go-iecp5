package cs104

import (
	"reflect"
	"testing"
	"time"
)

func TestConfirmSeqNo(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name        string
		pending     []seqPending
		ackNoSend   uint16
		seqNoSend   uint16
		ackNo       uint16
		wantOK      bool
		wantPending []seqPending
		wantAckNo   uint16
	}{
		{
			name:        "ack equals current ackNoSend is a no-op",
			pending:     []seqPending{{seq: 3, sendTime: now}},
			ackNoSend:   3,
			seqNoSend:   5,
			ackNo:       3,
			wantOK:      true,
			wantPending: []seqPending{{seq: 3, sendTime: now}},
		},
		{
			name:      "ack ahead of what has been sent is rejected",
			pending:   []seqPending{{seq: 0, sendTime: now}},
			ackNoSend: 0,
			seqNoSend: 1,
			ackNo:     5,
			wantOK:    false,
			wantPending: []seqPending{
				{seq: 0, sendTime: now},
			},
		},
		{
			name: "normal ack trims confirmed entries",
			pending: []seqPending{
				{seq: 3, sendTime: now},
				{seq: 4, sendTime: now},
				{seq: 5, sendTime: now},
			},
			ackNoSend:   3,
			seqNoSend:   6,
			ackNo:       5,
			wantOK:      true,
			wantPending: []seqPending{{seq: 5, sendTime: now}},
			wantAckNo:   5,
		},
		{
			// Regression test: the 15-bit sequence number space wraps at
			// 32768. Before the fix, `ackNo - 1` underflowed a uint16 to
			// 65535 when ackNo was 0, so the pending entry for the last
			// frame sent before the wrap (seq 32767) was never matched and
			// never trimmed.
			name:        "ack confirming the wrapped sequence number 32767->0",
			pending:     []seqPending{{seq: 32767, sendTime: now}},
			ackNoSend:   32767,
			seqNoSend:   0,
			ackNo:       0,
			wantOK:      true,
			wantPending: []seqPending{},
			wantAckNo:   0,
		},
		{
			name: "ack confirming a run of frames spanning the wraparound",
			pending: []seqPending{
				{seq: 32766, sendTime: now},
				{seq: 32767, sendTime: now},
				{seq: 0, sendTime: now},
				{seq: 1, sendTime: now},
			},
			ackNoSend:   32766,
			seqNoSend:   2,
			ackNo:       1,
			wantOK:      true,
			wantPending: []seqPending{{seq: 1, sendTime: now}},
			wantAckNo:   1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := confirmSeqNo(tt.pending, tt.ackNoSend, tt.seqNoSend, tt.ackNo)
			if ok != tt.wantOK {
				t.Fatalf("confirmSeqNo() ok = %v, want %v", ok, tt.wantOK)
			}
			if !ok {
				return
			}
			if !reflect.DeepEqual(got, tt.wantPending) {
				t.Errorf("confirmSeqNo() pending = %v, want %v", got, tt.wantPending)
			}
		})
	}
}

func TestPrevSeqNo(t *testing.T) {
	tests := []struct {
		seq  uint16
		want uint16
	}{
		{0, 32767},
		{1, 0},
		{32767, 32766},
		{100, 99},
	}
	for _, tt := range tests {
		if got := prevSeqNo(tt.seq); got != tt.want {
			t.Errorf("prevSeqNo(%d) = %d, want %d", tt.seq, got, tt.want)
		}
	}
}
