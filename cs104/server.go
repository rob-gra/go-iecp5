// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"context"
	"crypto/tls"
	"net"
	"sync"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
	"github.com/thinkgos/go-iecp5/clog"
)

// timeoutResolution is seconds according to companion standard 104,
// subclass 6.9, caption "Definition of time outs". However, then
// of a second make this system much more responsive i.c.w. S-frames.
const timeoutResolution = 100 * time.Millisecond

// Server the common server
type Server struct {
	config           Config
	params           asdu.Params
	handler          ServerHandlerInterface
	TLSConfig        *tls.Config
	mux              sync.Mutex
	sessions         map[*SrvSession]struct{}
	listen           net.Listener
	onConnection     func(asdu.Connect)
	connectionLost   func(asdu.Connect)
	serverMode       ServerMode
	redundancyGroups []*RedundancyGroup
	// activeByGroup tracks, per redundancy-group key, which session is
	// currently recorded as that group's active connection. See
	// handleSessionActivated for why this is authoritative state rather than
	// something re-derived from each session's IsActive() flag.
	activeByGroup    map[interface{}]*SrvSession
	commonAddrFilter func(asdu.CommonAddr) bool
	// maxConnections caps concurrent sessions; zero means unlimited. See
	// SetMaxConnections.
	maxConnections int
	// connectionRequest, if set, decides whether to accept a newly connected
	// peer before any session state is allocated for it. See
	// SetConnectionRequestHandler/AllowClientIPs.
	connectionRequest func(net.Addr) bool
	clog.Clog
	wg sync.WaitGroup
}

// NewServer new a server, default config and default asdu.ParamsWide params
func NewServer(handler ServerHandlerInterface) *Server {
	return &Server{
		config:        DefaultConfig(),
		params:        *asdu.ParamsWide,
		handler:       handler,
		sessions:      make(map[*SrvSession]struct{}),
		activeByGroup: make(map[interface{}]*SrvSession),
		Clog:          clog.NewLogger("cs104 server => "),
	}
}

// SetConfig set config if config is valid it will use DefaultConfig()
func (sf *Server) SetConfig(cfg Config) *Server {
	if err := cfg.Valid(); err != nil {
		sf.config = DefaultConfig()
	} else {
		sf.config = cfg
	}
	return sf
}

// SetParams set asdu params if params is valid it will use asdu.ParamsWide
func (sf *Server) SetParams(p *asdu.Params) *Server {
	if err := p.Valid(); err != nil {
		sf.params = *asdu.ParamsWide
	} else {
		sf.params = *p
	}
	return sf
}

// SetServerMode sets how connections are grouped for redundancy-group
// enforcement (see ServerMode). Must be called before ListenAndServer.
// Defaults to ModeConnectionIsRedundancyGroup, i.e. no grouping.
func (sf *Server) SetServerMode(mode ServerMode) *Server {
	sf.serverMode = mode
	return sf
}

// AddRedundancyGroup registers a redundancy group for use with
// ModeMultipleRedundancyGroups. Must be called before ListenAndServer.
func (sf *Server) AddRedundancyGroup(rg *RedundancyGroup) *Server {
	sf.redundancyGroups = append(sf.redundancyGroups, rg)
	return sf
}

// SetCommonAddrFilter sets the callback used to decide whether this server
// is responsible for a given common address (station address). An incoming
// ASDU addressed to a CA the filter rejects gets an UnknownCA reply instead
// of being dispatched to the handler. asdu.GlobalCommonAddr (the broadcast
// address) is always accepted regardless of the filter, since it isn't
// something a single station owns.
//
// With no filter set (the default), every CA other than the invalid marker
// (asdu.InvalidCommonAddr, 0) is accepted, matching the library's prior
// behavior. The filter is read once per accepted connection, so changing it
// takes effect for new connections; already-open sessions keep using
// whatever was set when they connected.
func (sf *Server) SetCommonAddrFilter(f func(asdu.CommonAddr) bool) *Server {
	sf.commonAddrFilter = f
	return sf
}

// AllowCommonAddrs is a convenience over SetCommonAddrFilter for the common
// case of a small, static set of common addresses this server is
// responsible for.
func (sf *Server) AllowCommonAddrs(cas ...asdu.CommonAddr) *Server {
	return sf.SetCommonAddrFilter(commonAddrSetFilter(cas))
}

// SetMaxConnections caps the number of concurrent sessions this server will
// accept. Once at capacity, additional incoming connections are accepted at
// the TCP level (so the peer sees a successful connect) then immediately
// closed, until an existing session ends and frees a slot. Zero (the
// default) means unlimited. Must be called before ListenAndServer.
func (sf *Server) SetMaxConnections(n int) *Server {
	sf.maxConnections = n
	return sf
}

// SetConnectionRequestHandler sets a callback invoked right after Accept(),
// before any session state is allocated, with the peer's address available.
// Returning false rejects the connection (it is closed immediately). Must
// be called before ListenAndServer.
func (sf *Server) SetConnectionRequestHandler(f func(remote net.Addr) bool) *Server {
	sf.connectionRequest = f
	return sf
}

// AllowClientIPs is a convenience over SetConnectionRequestHandler for the
// common case of a small, static set of client IP addresses allowed to
// connect (the port is ignored; only the host portion of the peer address
// is compared).
func (sf *Server) AllowClientIPs(ips ...string) *Server {
	allowed := make(map[string]struct{}, len(ips))
	for _, ip := range ips {
		allowed[ip] = struct{}{}
	}
	return sf.SetConnectionRequestHandler(func(remote net.Addr) bool {
		_, ok := allowed[hostOnly(remote)]
		return ok
	})
}

// newSession builds a SrvSession bound to conn, wired with this Server's
// handlers and redundancy-group configuration. Split out from
// ListenAndServer's accept loop so it can be driven directly in tests
// without a real net.Listener.
func (sf *Server) newSession(conn net.Conn) *SrvSession {
	return &SrvSession{
		config:    &sf.config,
		params:    &sf.params,
		handler:   sf.handler,
		rcvASDU:   make(chan []byte, sf.config.RecvUnAckLimitW<<4),
		sendQueue: newMessageQueue(int(sf.config.SendUnAckLimitK) << 4),
		rcvRaw:    make(chan []byte, sf.config.RecvUnAckLimitW<<5),
		sendRaw:   make(chan []byte, sf.config.SendUnAckLimitK<<5), // may not block!

		onConnection:       sf.onConnection,
		connectionLost:     sf.connectionLost,
		onActivate:         sf.handleSessionActivated,
		redundancyGroupKey: sf.groupKeyFor(conn),
		commonAddrFilter:   sf.commonAddrFilter,
		Clog:               sf.Clog,
	}
}

// ListenAndServer run the server
func (sf *Server) ListenAndServer(addr string) {
	listen, err := net.Listen("tcp", addr)
	if err != nil {
		sf.Error("server run failed, %v", err)
		return
	}
	sf.mux.Lock()
	sf.listen = listen
	sf.mux.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = sf.Close()
		sf.Debug("server stop")
	}()
	sf.Debug("server run")
	for {
		conn, err := listen.Accept()
		if err != nil {
			sf.Error("server run failed, %v", err)
			return
		}

		sess := sf.acceptSession(conn)
		if sess == nil {
			continue
		}

		sf.wg.Add(1)
		go func() {
			sess.run(ctx, conn)
			sf.mux.Lock()
			delete(sf.sessions, sess)
			sf.mux.Unlock()
			sf.wg.Done()
		}()
	}
}

// acceptSession decides whether to admit conn as a new session: it's
// rejected (and closed immediately) if SetConnectionRequestHandler declines
// it or SetMaxConnections' cap is already reached, in which case
// acceptSession returns nil. Otherwise a SrvSession is built, registered in
// sf.sessions, and returned. Split out from ListenAndServer's accept loop
// so admission control can be tested without a real net.Listener.
func (sf *Server) acceptSession(conn net.Conn) *SrvSession {
	if sf.connectionRequest != nil && !sf.connectionRequest(conn.RemoteAddr()) {
		sf.Warn("rejected connection from %v: declined by connection request handler", conn.RemoteAddr())
		_ = conn.Close()
		return nil
	}

	sess := sf.newSession(conn)

	sf.mux.Lock()
	if sf.maxConnections > 0 && len(sf.sessions) >= sf.maxConnections {
		sf.mux.Unlock()
		sf.Warn("rejected connection from %v: max connections (%d) reached", conn.RemoteAddr(), sf.maxConnections)
		_ = conn.Close()
		return nil
	}
	sf.sessions[sess] = struct{}{}
	sf.mux.Unlock()

	return sess
}

// Close close the server
func (sf *Server) Close() error {
	var err error

	sf.mux.Lock()
	if sf.listen != nil {
		err = sf.listen.Close()
		sf.listen = nil
	}
	sf.mux.Unlock()
	sf.wg.Wait()
	return err
}

// Send imp interface Connect
func (sf *Server) Send(a *asdu.ASDU) error {
	sf.mux.Lock()
	for k := range sf.sessions {
		_ = k.Send(a.Clone())
	}
	sf.mux.Unlock()
	return nil
}

// Params imp interface Connect
func (sf *Server) Params() *asdu.Params { return &sf.params }

// UnderlyingConn imp interface Connect
func (sf *Server) UnderlyingConn() net.Conn { return nil }

// SetInfoObjTimeZone set info object time zone
func (sf *Server) SetInfoObjTimeZone(zone *time.Location) {
	sf.params.InfoObjTimeZone = zone
}

// SetOnConnectionHandler set on connect handler
func (sf *Server) SetOnConnectionHandler(f func(asdu.Connect)) {
	sf.onConnection = f
}

// SetConnectionLostHandler set connect lost handler
func (sf *Server) SetConnectionLostHandler(f func(asdu.Connect)) {
	sf.connectionLost = f
}
