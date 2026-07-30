package cs104

import (
	"testing"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

func TestSrvSession_commonAddrAllowed(t *testing.T) {
	tests := []struct {
		name   string
		filter func(asdu.CommonAddr) bool
		ca     asdu.CommonAddr
		want   bool
	}{
		{"invalid CA always rejected, even with no filter", nil, asdu.InvalidCommonAddr, false},
		{"no filter accepts any non-invalid CA", nil, 42, true},
		{"global CA accepted despite a rejecting filter", func(asdu.CommonAddr) bool { return false }, asdu.GlobalCommonAddr, true},
		{"filter rejects a CA it doesn't own", func(ca asdu.CommonAddr) bool { return ca == 5 }, 42, false},
		{"filter accepts a CA it owns", func(ca asdu.CommonAddr) bool { return ca == 5 }, 5, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sess := &SrvSession{commonAddrFilter: tt.filter}
			if got := sess.commonAddrAllowed(tt.ca); got != tt.want {
				t.Errorf("commonAddrAllowed(%d) = %v, want %v", tt.ca, got, tt.want)
			}
		})
	}
}

func TestCommonAddrSetFilter(t *testing.T) {
	f := commonAddrSetFilter([]asdu.CommonAddr{5, 7})
	for _, ca := range []asdu.CommonAddr{5, 7} {
		if !f(ca) {
			t.Errorf("commonAddrSetFilter(5,7)(%d) = false, want true", ca)
		}
	}
	if f(6) {
		t.Error("commonAddrSetFilter(5,7)(6) = true, want false")
	}
}

func TestServer_AllowCommonAddrs_RejectsUnownedCA(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.AllowCommonAddrs(5)

	sess, peer := newPipeSession(t, srv)
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	writeFrame(t, peer, newUFrame(uStartDtActive))
	readFrame(t, peer) // StartDtConfirm

	iframe, err := newIFrame(0, 0, buildTestCommandASDUWithCA(t, 42)) // not in the allow-list
	if err != nil {
		t.Fatalf("newIFrame: %v", err)
	}
	writeFrame(t, peer, iframe)

	_, body := readFrame(t, peer)
	reply := asdu.NewEmptyASDU(asdu.ParamsWide)
	if err := reply.UnmarshalBinary(body); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if reply.Coa.Cause != asdu.UnknownCA {
		t.Fatalf("got cause=%v, want UnknownCA for a CA outside the allow-list", reply.Coa.Cause)
	}
	if !sess.IsConnected() {
		t.Fatal("session should remain connected after an UnknownCA reply")
	}
}

func TestServer_AllowCommonAddrs_AcceptsOwnedCA(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.AllowCommonAddrs(5)

	_, peer := newPipeSession(t, srv)
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	writeFrame(t, peer, newUFrame(uStartDtActive))
	readFrame(t, peer) // StartDtConfirm

	iframe, err := newIFrame(0, 0, buildTestCommandASDUWithCA(t, 5)) // in the allow-list
	if err != nil {
		t.Fatalf("newIFrame: %v", err)
	}
	writeFrame(t, peer, iframe)

	_, body := readFrame(t, peer)
	reply := asdu.NewEmptyASDU(asdu.ParamsWide)
	if err := reply.UnmarshalBinary(body); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if reply.Coa.Cause != asdu.ActivationCon {
		t.Fatalf("got cause=%v, want ActivationCon for an owned CA", reply.Coa.Cause)
	}
}

func TestServer_AllowCommonAddrs_GlobalAlwaysAccepted(t *testing.T) {
	srv := NewServer(stubServerHandler{})
	srv.AllowCommonAddrs(5) // GlobalCommonAddr is not in this list

	_, peer := newPipeSession(t, srv)
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	writeFrame(t, peer, newUFrame(uStartDtActive))
	readFrame(t, peer) // StartDtConfirm

	iframe, err := newIFrame(0, 0, buildTestCommandASDUWithCA(t, asdu.GlobalCommonAddr))
	if err != nil {
		t.Fatalf("newIFrame: %v", err)
	}
	writeFrame(t, peer, iframe)

	_, body := readFrame(t, peer)
	reply := asdu.NewEmptyASDU(asdu.ParamsWide)
	if err := reply.UnmarshalBinary(body); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if reply.Coa.Cause != asdu.ActivationCon {
		t.Fatalf("got cause=%v, want ActivationCon: GlobalCommonAddr must always be accepted", reply.Coa.Cause)
	}
}

func TestServer_NoCommonAddrFilter_AcceptsAnyNonInvalidCA(t *testing.T) {
	srv := NewServer(stubServerHandler{}) // no filter configured: default behavior

	_, peer := newPipeSession(t, srv)
	_ = peer.SetDeadline(time.Now().Add(2 * time.Second))
	writeFrame(t, peer, newUFrame(uStartDtActive))
	readFrame(t, peer) // StartDtConfirm

	iframe, err := newIFrame(0, 0, buildTestCommandASDUWithCA(t, 9999))
	if err != nil {
		t.Fatalf("newIFrame: %v", err)
	}
	writeFrame(t, peer, iframe)

	_, body := readFrame(t, peer)
	reply := asdu.NewEmptyASDU(asdu.ParamsWide)
	if err := reply.UnmarshalBinary(body); err != nil {
		t.Fatalf("UnmarshalBinary: %v", err)
	}
	if reply.Coa.Cause != asdu.ActivationCon {
		t.Fatalf("got cause=%v, want ActivationCon: with no filter configured, any non-invalid CA should be accepted", reply.Coa.Cause)
	}
}
