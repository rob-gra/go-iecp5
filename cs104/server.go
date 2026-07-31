// Copyright 2020 thinkgos (thinkgo@aliyun.com).  All rights reserved.
// Use of this source code is governed by a version 3 of the GNU General
// Public License, license that can be found in the LICENSE file.

package cs104

import (
	"context"
	"crypto/tls"
	"errors"
	"log/slog"
	"net"
	"sync"
	"time"

	"github.com/thinkgos/go-iecp5/asdu"
)

// timeoutResolution is how often the connection state machine re-checks
// its timers. The companion standard defines t0-t3 in whole seconds (104,
// subclass 6.9, "Definition of time outs"); this only bounds how late a
// timer may be noticed, so it is finer than a second.
const timeoutResolution = 100 * time.Millisecond

// Server the common server
type Server struct {
	config  Config
	params  asdu.Params
	handler ServerHandlerInterface
	// TLSConfig, when set, makes ListenAndServer serve TLS. See
	// SetTLSConfig.
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
	activeByGroup map[any]*SrvSession
	// groupQueues holds the outbound queue shared by all connections in a
	// redundancy group. The data belongs to the group, not to whichever
	// connection happens to be carrying it, so members share one queue and
	// only the active one drains it -- a standby that takes over simply
	// continues where its predecessor left off. Ungrouped connections are
	// absent here and own a private queue instead.
	groupQueues      map[any]*messageQueue
	commonAddrFilter func(asdu.CommonAddr) bool
	// maxConnections caps concurrent sessions; zero means unlimited. See
	// SetMaxConnections.
	maxConnections int
	// connectionRequest, if set, decides whether to accept a newly connected
	// peer before any session state is allocated for it. See
	// SetConnectionRequestHandler/AllowClientIPs.
	connectionRequest func(net.Addr) bool
	// log receives this server's records, and is the base each session's
	// logger is derived from. See SetLogger.
	log *slog.Logger
	wg  sync.WaitGroup
}

// NewServer new a server, default config and default asdu.ParamsWide params
func NewServer(handler ServerHandlerInterface) *Server {
	return &Server{
		config:        DefaultConfig(),
		params:        *asdu.ParamsWide,
		handler:       handler,
		sessions:      make(map[*SrvSession]struct{}),
		activeByGroup: make(map[any]*SrvSession),
		groupQueues:   make(map[any]*messageQueue),
		log:           slog.Default().With("component", "cs104.server"),
	}
}

// SetLogger directs this server's records (and those of every session it
// accepts) to l. Records go to slog.Default() when unset. Passing nil
// restores that default rather than disabling logging -- to silence the
// library, give it a logger whose handler discards everything.
//
// Must be called before ListenAndServer. A session's logger is derived when
// the connection is accepted, so changing this later affects only sessions
// accepted afterwards.
func (sf *Server) SetLogger(l *slog.Logger) *Server {
	if l == nil {
		l = slog.Default()
	}
	sf.log = l
	return sf
}

// SetConfig set config if config is valid it will use DefaultConfig()
func (sf *Server) SetConfig(cfg Config) *Server {
	if err := cfg.Valid(); err != nil {
		sf.log.Warn("rejected config, falling back to DefaultConfig()", "err", err)
		sf.config = DefaultConfig()
	} else {
		sf.config = cfg
	}
	return sf
}

// SetParams set asdu params if params is valid it will use asdu.ParamsWide
func (sf *Server) SetParams(p *asdu.Params) *Server {
	if err := p.Valid(); err != nil {
		sf.log.Warn("rejected asdu params, falling back to asdu.ParamsWide", "err", err)
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
	sess := &SrvSession{
		connection: connection{
			config:  &sf.config,
			params:  &sf.params,
			rcvASDU: make(chan []byte, sf.config.RecvUnAckLimitW<<4),
			rcvRaw:  make(chan []byte, sf.config.RecvUnAckLimitW<<5),
			sendRaw: make(chan []byte, sf.config.SendUnAckLimitK<<5),
			// Every record from this session carries the peer it belongs to.
			// Without it a multi-master server's sessions are indistinguishable
			// in the log, and a line like "no acknowledgement within t₁" names
			// no connection.
			log: sf.log.With("remote", conn.RemoteAddr().String()),
		},
		handler:            sf.handler,
		onConnection:       sf.onConnection,
		connectionLost:     sf.connectionLost,
		onActivate:         sf.handleSessionActivated,
		redundancyGroupKey: sf.groupKeyFor(conn),
		commonAddrFilter:   sf.commonAddrFilter,
	}
	sess.role = sess
	sess.sendQueue = sf.queueFor(sess.redundancyGroupKey)
	return sess
}

// SetTLSConfig makes ListenAndServer serve TLS instead of plaintext, using
// the given configuration. It mirrors ClientOption.SetTLSConfig on the
// dial-out side. Must be called before ListenAndServer.
func (sf *Server) SetTLSConfig(t *tls.Config) *Server {
	sf.TLSConfig = t
	return sf
}

// ListenAndServer run the server
func (sf *Server) ListenAndServer(addr string) {
	listen, err := net.Listen("tcp", addr)
	if err != nil {
		sf.log.Error("listen failed", "addr", addr, "err", err)
		return
	}
	if sf.TLSConfig != nil {
		listen = tls.NewListener(listen, sf.TLSConfig)
	}
	sf.mux.Lock()
	sf.listen = listen
	sf.mux.Unlock()

	ctx, cancel := context.WithCancel(context.Background())
	defer func() {
		cancel()
		_ = sf.Close()
		sf.log.Info("server stopped")
	}()
	sf.log.Info("server listening", "addr", listen.Addr().String(), "tls", sf.TLSConfig != nil)
	for {
		conn, err := listen.Accept()
		if err != nil {
			// Close() closes the listener to stop this loop, so the resulting
			// Accept failure is the shutdown working, not a fault. Reporting
			// it as an error would make every clean stop look like one.
			if errors.Is(err, net.ErrClosed) {
				return
			}
			sf.log.Error("accept failed", "err", err)
			return
		}

		sess := sf.acceptSession(conn)
		if sess == nil {
			continue
		}

		sf.wg.Add(1)
		go func() {
			sess.run(ctx, conn)
			sf.releaseSession(sess)
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
		sf.log.Warn("rejected connection: declined by connection request handler",
			"remote", conn.RemoteAddr().String())
		_ = conn.Close()
		return nil
	}

	// Check the cap before building the session, not after: newSession
	// allocates several channels sized from Config (with a large k/w those
	// run to megabytes each), and a peer hammering a server that's already
	// at capacity is exactly the case the cap exists to contain -- building
	// and immediately discarding that state per rejected connection would
	// hand the flood back much of the cost the cap is meant to deny it.
	//
	// A single check suffices: ListenAndServer calls this synchronously from
	// its one accept-loop goroutine, so no other admission can race this
	// one, and the only concurrent mutation of sf.sessions is releaseSession
	// removing an entry -- which can only free capacity, never consume it.
	sf.mux.Lock()
	atCapacity := sf.maxConnections > 0 && len(sf.sessions) >= sf.maxConnections
	sf.mux.Unlock()
	if atCapacity {
		sf.log.Warn("rejected connection: at max connections",
			"remote", conn.RemoteAddr().String(), "maxConnections", sf.maxConnections)
		_ = conn.Close()
		return nil
	}

	sess := sf.newSession(conn)

	sf.mux.Lock()
	sf.sessions[sess] = struct{}{}
	sf.mux.Unlock()

	return sess
}

// releaseSession removes a finished session's server-side state, freeing a
// slot against SetMaxConnections' cap. Clearing the redundancy-group entry
// matters beyond bookkeeping: activeByGroup holds the session by pointer, so
// leaving a disconnected session recorded there would keep it (and its
// buffers) reachable for as long as the server runs.
func (sf *Server) releaseSession(sess *SrvSession) {
	sf.mux.Lock()
	delete(sf.sessions, sess)
	if sess.redundancyGroupKey != nil && sf.activeByGroup[sess.redundancyGroupKey] == sess {
		delete(sf.activeByGroup, sess.redundancyGroupKey)
	}
	sf.mux.Unlock()
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

// queueFor returns the outbound queue a session with the given
// redundancy-group key should use: the group's shared queue, created on
// first use, or a private queue for an ungrouped connection.
//
// A group's queue outlives its connections on purpose. It is where data for
// that group accumulates, so a group whose members are all momentarily
// disconnected still buffers (bounded, oldest-evicted) until one of them
// reconnects, rather than dropping everything on the floor.
func (sf *Server) queueFor(key any) *messageQueue {
	size := int(sf.config.SendUnAckLimitK) << 4
	if key == nil {
		return newMessageQueue(size)
	}

	sf.mux.Lock()
	defer sf.mux.Unlock()
	if q, ok := sf.groupQueues[key]; ok {
		return q
	}
	q := newMessageQueue(size)
	sf.groupQueues[key] = q
	return q
}

// Send imp interface Connect. It queues one copy of a per destination:
// once per redundancy group (whose members share a queue and of which only
// the active one transmits) and once per ungrouped connection.
//
// Sending to a group rather than to each of its connections is what keeps a
// standby from accumulating traffic it can never transmit and then flushing
// it, stale, when it takes over.
func (sf *Server) Send(a *asdu.ASDU) error {
	data, err := a.MarshalBinary()
	if err != nil {
		return err
	}
	// Same check connection.Send makes, for the same reason: past this point
	// the ASDU is bytes on a queue, and an over-long one can only be dropped
	// when sendIFrame fails to frame it. Without the check here the caller is
	// told nil for a message that can never be delivered, and a copy of it
	// occupies a slot in every group queue and every ungrouped session --
	// where, the queues being evict-oldest, it can displace data that would
	// have been sent.
	if len(data) > asdu.ASDUSizeMax {
		return asdu.ErrLengthOutOfRange
	}

	sf.mux.Lock()
	defer sf.mux.Unlock()

	for _, q := range sf.groupQueues {
		if q.Push(data) {
			sf.log.Warn("send queue full, dropped oldest unsent message")
		}
	}
	for sess := range sf.sessions {
		if sess.redundancyGroupKey == nil && sess.IsConnected() {
			sess.enqueue(data)
		}
	}
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
