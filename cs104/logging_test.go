// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"bytes"
	"log/slog"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/thinkgos/go-iecp5/asdu"
)

// syncBuffer is a bytes.Buffer safe to read while the connection goroutines
// are still writing records into it.
type syncBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (s *syncBuffer) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.Write(p)
}

func (s *syncBuffer) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.b.String()
}

func captureLogger(level slog.Level) (*slog.Logger, *syncBuffer) {
	buf := &syncBuffer{}
	return slog.New(slog.NewTextHandler(buf, &slog.HandlerOptions{Level: level})), buf
}

// A server logs without any setup: slog.Default() is the fallback, so a
// caller who never touches the logging API still gets warnings and errors.
// This is the property the old clog-based code did not have -- it defaulted
// to discarding everything until LogMode(true) was called.
func TestLogging_DefaultsToSlogDefault(t *testing.T) {
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	// No SetLogger call anywhere here.
	NewServer(stubServerHandler{}).SetConfig(Config{
		SendUnAckTimeout1: 15e9,
		RecvUnAckTimeout2: 20e9, // t₂ >= t₁, rejected
	})

	if !strings.Contains(buf.String(), "rejected config") {
		t.Fatalf("a rejected config produced no output with no logger configured; got %q", buf.String())
	}
}

// SetLogger(nil) means "go back to the default", not "be silent". Silencing
// is done by passing a logger whose handler discards, which is explicit at
// the call site.
func TestLogging_SetLoggerNilRestoresDefault(t *testing.T) {
	buf := &syncBuffer{}
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(buf, nil)))
	t.Cleanup(func() { slog.SetDefault(prev) })

	srv := NewServer(stubServerHandler{})
	srv.SetLogger(slog.New(slog.NewTextHandler(&syncBuffer{}, nil))) // elsewhere
	srv.SetLogger(nil)                                               // back to default
	srv.SetParams(&asdu.Params{})                                    // zero CauseSize etc: rejected

	if !strings.Contains(buf.String(), "rejected asdu params") {
		t.Fatalf("SetLogger(nil) did not restore the default logger; got %q", buf.String())
	}
}

// Every record from a session carries the peer it belongs to. Without this a
// multi-master server's sessions are indistinguishable in the log, and a
// line like "no acknowledgement within t₁" names no connection.
func TestLogging_SessionRecordsCarryRemoteAddr(t *testing.T) {
	logger, buf := captureLogger(slog.LevelDebug)
	srv := NewServer(stubServerHandler{}).SetLogger(logger)

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()

	client, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	accepted, err := ln.Accept()
	if err != nil {
		t.Fatal(err)
	}
	defer accepted.Close()

	sess := srv.acceptSession(accepted)
	if sess == nil {
		t.Fatal("acceptSession rejected the connection")
	}
	sess.log.Info("probe")

	want := "remote=" + client.LocalAddr().String()
	if got := buf.String(); !strings.Contains(got, want) {
		t.Fatalf("session record does not identify its peer: want %q in %q", want, got)
	}
}

// The per-frame trace sites are guarded by debugEnabled precisely so they
// cost nothing when debug is off; the guard is only correct if it still lets
// records through when debug is on.
func TestLogging_DebugEnabledTracksHandlerLevel(t *testing.T) {
	on, _ := captureLogger(slog.LevelDebug)
	off, _ := captureLogger(slog.LevelInfo)

	if !(&connection{log: on}).debugEnabled() {
		t.Error("debugEnabled() is false for a debug-level handler: the frame trace would never be emitted")
	}
	if (&connection{log: off}).debugEnabled() {
		t.Error("debugEnabled() is true for an info-level handler: the frame trace would allocate on every frame")
	}
}
