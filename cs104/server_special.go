// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"context"
	"errors"
	"log/slog"
	"math/rand"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// ServerSpecial server special interface
type ServerSpecial interface {
	asdu.Connect

	IsConnected() bool
	IsClosed() bool
	Start() error
	Close() error

	SetOnConnectHandler(f func(c asdu.Connect))
	SetConnectionLostHandler(f func(c asdu.Connect))

	// SetLogger directs this station's records to l, or to slog.Default()
	// when l is nil. To silence the library, pass a logger whose handler
	// discards everything.
	SetLogger(l *slog.Logger)
}

type serverSpec struct {
	SrvSession
	option      ClientOption
	closeCancel context.CancelFunc
}

// NewServerSpecial new special server
func NewServerSpecial(handler ServerHandlerInterface, o *ClientOption) ServerSpecial {
	s := &serverSpec{
		SrvSession: SrvSession{
			connection: connection{
				rcvASDU:   make(chan []byte, 1024),
				sendQueue: newMessageQueue(1024),
				rcvRaw:    make(chan []byte, 1024),
				sendRaw:   make(chan []byte, 1024),
				log:       o.logger().With("component", "cs104.serverSpecial"),
			},
			handler:          handler,
			commonAddrFilter: o.commonAddrFilter,
		},
		option: *o,
	}
	// Point at this value's own copy of the option, not the caller's, which
	// it may reuse or mutate after NewServerSpecial returns.
	s.connection.config = &s.option.config
	s.connection.params = &s.option.params
	s.role = &s.SrvSession
	if o.server != nil {
		s.log = s.log.With("remote", o.server.Host)
	}
	s.option.logRejected(s.log)
	return s
}

// SetLogger implements ServerSpecial.
func (sf *serverSpec) SetLogger(l *slog.Logger) {
	if l == nil {
		l = slog.Default()
	}
	sf.log = l
}

// SetOnConnectHandler set on connect handler
func (sf *serverSpec) SetOnConnectHandler(f func(conn asdu.Connect)) {
	sf.onConnection = f
}

// SetConnectionLostHandler set connection lost handler
func (sf *serverSpec) SetConnectionLostHandler(f func(c asdu.Connect)) {
	sf.connectionLost = f
}

// Start begins connecting in the background and returns immediately. A nil
// return means the station was started, not that it connected. See
// Client.Start, which this mirrors, for how outcomes are reported.
//
// Starting an already-running station returns ErrAlreadyStarted.
func (sf *serverSpec) Start() error {
	if sf.option.server == nil {
		return errors.New("empty remote server")
	}

	// Claimed here rather than inside running, for the reasons given on
	// Client.Start.
	sf.connMu.Lock()
	if !sf.status.CompareAndSwap(initial, disconnected) {
		sf.connMu.Unlock()
		return ErrAlreadyStarted
	}
	ctx, cancel := context.WithCancel(context.Background())
	sf.closeCancel = cancel
	sf.connMu.Unlock()

	go sf.running(ctx)
	return nil
}

// running dials the remote server, runs the connection, and reconnects.
func (sf *serverSpec) running(ctx context.Context) {
	defer sf.setConnectStatus(initial)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}

		sf.log.Debug("connecting")
		conn, err := openConnection(sf.option.server, sf.option.TLSConfig, sf.config.ConnectTimeout0)
		if err != nil {
			sf.log.Error("connect failed", "err", err)
			if !sf.option.autoReconnect {
				return
			}
			time.Sleep(sf.option.reconnectInterval)
			continue
		}
		sf.log.Info("connected")
		sf.run(ctx, conn)
		sf.log.Info("disconnected")
		select {
		case <-ctx.Done():
			return
		default:
			// Wait a random 500ms-1s before retrying, so a fast reconnect loop
			// does not leave the server with a pile of dead connections.
			time.Sleep(time.Millisecond * time.Duration(500+rand.Intn(500)))
		}
	}
}

func (sf *serverSpec) IsClosed() bool {
	return sf.connectStatus() == initial
}

func (sf *serverSpec) Close() error {
	sf.connMu.Lock()
	if sf.closeCancel != nil {
		sf.closeCancel()
	}
	sf.connMu.Unlock()
	return nil
}
