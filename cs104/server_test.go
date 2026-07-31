// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"context"
	"errors"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// Server.Send must reject an ASDU too long to frame, exactly as
// connection.Send does. It used to marshal and enqueue one regardless: the
// caller was told nil for a message that could never be delivered, and a
// copy occupied a slot in every group queue and every ungrouped session --
// where, the queues being evict-oldest, it could displace data that would
// have been sent.
func TestServer_SendRejectsOverlongASDU(t *testing.T) {
	srv := NewServer(stubServerHandler{}).SetLogger(discardLogger()).
		SetServerMode(ModeSingleRedundancyGroup)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	peer, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer peer.Close()
	accepted, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}

	sess := srv.acceptSession(accepted)
	if sess == nil {
		t.Fatal("acceptSession rejected the connection")
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go sess.run(ctx, accepted)
	waitFor(t, time.Second, sess.IsConnected)

	q := srv.queueFor(sess.redundancyGroupKey)
	before := q.Len()

	over := asdu.NewASDU(asdu.ParamsWide, asdu.Identifier{
		Type:       asdu.M_SP_NA_1,
		Variable:   asdu.VariableStruct{IsSequence: false, Number: 1},
		Coa:        asdu.CauseOfTransmission{Cause: asdu.Spontaneous},
		CommonAddr: 1,
	})
	if err := over.AppendInfoObjAddr(1); err != nil {
		t.Fatal(err)
	}
	over.AppendBytes(make([]byte, asdu.ASDUSizeMax+1)...)

	if err := srv.Send(over); !errors.Is(err, asdu.ErrLengthOutOfRange) {
		t.Fatalf("Server.Send(over-long) = %v, want asdu.ErrLengthOutOfRange", err)
	}
	if got := q.Len(); got != before {
		t.Fatalf("queue grew from %d to %d: an undeliverable ASDU took a slot", before, got)
	}
}

// Close must terminate while sessions are live. It waits on the same
// WaitGroup those session goroutines are in, and only ListenAndServer's
// defer cancels their context -- so the two have to interleave correctly or
// Close hangs for good.
func TestServer_CloseWithActiveSessions(t *testing.T) {
	srv := NewServer(stubServerHandler{}).SetLogger(discardLogger())

	// Take a port, release it, and hand it to ListenAndServer, which owns
	// its own listener.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	go srv.ListenAndServer(addr)

	const want = 5
	var peers []net.Conn
	deadline := time.Now().Add(3 * time.Second)
	for len(peers) < want && time.Now().Before(deadline) {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			time.Sleep(20 * time.Millisecond)
			continue
		}
		peers = append(peers, c)
	}
	defer func() {
		for _, c := range peers {
			_ = c.Close()
		}
	}()
	if len(peers) != want {
		t.Fatalf("connected %d of %d peers", len(peers), want)
	}
	waitFor(t, 2*time.Second, func() bool {
		srv.mux.Lock()
		defer srv.mux.Unlock()
		return len(srv.sessions) == want
	})

	done := make(chan error, 1)
	go func() { done <- srv.Close() }()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("Close() hung while sessions were live")
	}

	srv.mux.Lock()
	remaining := len(srv.sessions)
	srv.mux.Unlock()
	if remaining != 0 {
		t.Fatalf("%d sessions still registered after Close", remaining)
	}
}

// Sessions must not leak goroutines as connections come and go: a server
// running for weeks churns through far more than this.
func TestServer_SessionChurnLeaksNothing(t *testing.T) {
	settle := func() int {
		for i := 0; i < 5; i++ {
			runtime.GC()
			time.Sleep(20 * time.Millisecond)
		}
		return runtime.NumGoroutine()
	}
	before := settle()

	srv := NewServer(stubServerHandler{}).SetLogger(discardLogger())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	var wg sync.WaitGroup
	for i := 0; i < 40; i++ {
		peer, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		accepted, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		sess := srv.acceptSession(accepted)
		if sess == nil {
			t.Fatal("acceptSession rejected the connection")
		}

		ctx, cancel := context.WithCancel(context.Background())
		wg.Add(1)
		go func() {
			defer wg.Done()
			sess.run(ctx, accepted)
			srv.releaseSession(sess)
		}()

		_, _ = peer.Write(newUFrame(UStartDtActive))
		time.Sleep(2 * time.Millisecond)
		cancel()
		_ = peer.Close()
	}
	wg.Wait()

	if got := settle() - before; got > 5 {
		t.Fatalf("leaked ~%d goroutines across 40 sessions", got)
	}
	srv.mux.Lock()
	remaining := len(srv.sessions)
	srv.mux.Unlock()
	if remaining != 0 {
		t.Fatalf("sessions map retained %d entries after every session ended", remaining)
	}
}

// Exactly one connection in a redundancy group may be active, including when
// several activate at once. activeByGroup is swapped under one lock for this
// reason: re-deriving "who is active" from each session's own IsActive() lets
// two concurrent activations each see the other as the one to supersede, and
// deactivate both, leaving the group with no active connection at all.
func TestServer_ConcurrentActivationLeavesExactlyOneActive(t *testing.T) {
	const sessions = 8

	srv := NewServer(stubServerHandler{}).SetLogger(discardLogger()).
		SetServerMode(ModeSingleRedundancyGroup)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var (
		wg    sync.WaitGroup
		socks []*SrvSession
		peers []net.Conn
	)
	for i := 0; i < sessions; i++ {
		peer, err := net.Dial("tcp", ln.Addr().String())
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		accepted, err := ln.Accept()
		if err != nil {
			t.Fatal(err)
		}
		sess := srv.acceptSession(accepted)
		if sess == nil {
			t.Fatal("acceptSession rejected the connection")
		}
		socks = append(socks, sess)
		peers = append(peers, peer)

		wg.Add(1)
		go func() { defer wg.Done(); sess.run(ctx, accepted) }()
	}
	for _, s := range socks {
		waitFor(t, time.Second, s.IsConnected)
	}

	// Release every STARTDT at once, so the activations genuinely race.
	var gate sync.WaitGroup
	gate.Add(1)
	var writers sync.WaitGroup
	for _, peer := range peers {
		writers.Add(1)
		go func(c net.Conn) {
			defer writers.Done()
			gate.Wait()
			_, _ = c.Write(newUFrame(UStartDtActive))
		}(peer)
	}
	gate.Done()
	writers.Wait()

	waitFor(t, 2*time.Second, func() bool {
		active := 0
		for _, s := range socks {
			if s.IsActive() {
				active++
			}
		}
		return active == 1
	})

	active := 0
	for _, s := range socks {
		if s.IsActive() {
			active++
		}
	}
	if active != 1 {
		t.Fatalf("%d of %d sessions active in one redundancy group, want exactly 1", active, sessions)
	}

	cancel()
	wg.Wait()
}
