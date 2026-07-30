# Known issues / backlog

Tracking list for gaps and defects found during code review. GitHub Issues
are disabled on this repository, so these are recorded here instead. Each
entry is written as a standalone issue: summary, impact, and a suggested
fix.

- [x] [Server has no message queue: full send buffers silently drop ASDUs instead of buffering](#1-server-has-no-message-queue-full-send-buffers-silently-drop-asdus-instead-of-buffering) — implemented: `messageQueue` (`cs104/queue.go`)
- [x] [No redundancy-group / single-active-connection enforcement on the server](#2-no-redundancy-group--single-active-connection-enforcement-on-the-server) — implemented: `Server.SetServerMode` / `AddRedundancyGroup`
- [x] [No connection admission control (max connections, accept/reject hook, IP allow-list)](#3-no-connection-admission-control-max-connections-acceptreject-hook-ip-allow-list) — implemented: `Server.SetMaxConnections` / `SetConnectionRequestHandler` / `AllowClientIPs`
- [x] [No per-message common-address filtering hook](#4-no-per-message-common-address-filtering-hook) — implemented: `Server.SetCommonAddrFilter` / `AllowCommonAddrs`
- [~] [`Server.TLSConfig` is dead code — server-side TLS doesn't actually work](#5-servertlsconfig-is-dead-code--server-side-tls-doesnt-actually-work) — not planned, deemed irrelevant
- [~] [File transfer (F_xx ASDUs) is an empty stub](#6-file-transfer-f_xx-asdus-is-an-empty-stub) — not planned, deemed irrelevant
- [~] [Security/authentication ASDU types (S_xx) are declared but entirely unimplemented](#7-securityauthentication-asdu-types-s_xx-are-declared-but-entirely-unimplemented) — not planned, deemed irrelevant
- [~] [No threadless/tick-driven mode for embedded or single-threaded use](#8-no-threadlesstick-driven-mode-for-embedded-or-single-threaded-use) — not planned, deemed irrelevant
- [~] [No raw-message observation hook for applications](#9-no-raw-message-observation-hook-for-applications) — not planned, deemed irrelevant

---

## 1. Server has no message queue: full send buffers silently drop ASDUs instead of buffering

**Summary**: `Client.Send` and `SrvSession.Send` push onto a fixed-size, non-blocking channel (`sendASDU`) and immediately return `ErrBufferFulled` if it's full:

```go
// cs104/server_session.go
func (sf *SrvSession) Send(u *asdu.ASDU) error {
	if !sf.IsConnected() {
		return ErrUseClosedConnection
	}
	data, err := u.MarshalBinary()
	if err != nil {
		return err
	}
	select {
	case sf.sendASDU <- data:
	default:
		return ErrBufferFulled
	}
	return nil
}
```

There is no real message queue behind this: the channel's capacity is a fixed multiple of `k`/`w` from `Config`, it holds no persistent state, and everything in it is discarded on disconnect (see `cleanUp()`, which drains all channels on every new connection).

**Impact**: Any burst of spontaneous data (events, measured values, etc.) that exceeds the channel capacity is silently lost rather than buffered — the caller gets an error, but there's no queue to retry from, no eviction-of-oldest policy, and nothing is preserved across a reconnect. For a protocol whose whole point is reliable delivery of process data, losing data under load or during a brief disconnect is a significant reliability gap.

**Suggested fix**: Introduce an actual bounded queue (e.g. a ring buffer that evicts the oldest entry when full instead of rejecting the newest, or a persistent/replayable queue) between `Send()` and the low-level `sendRaw` transmission, so callers don't need to hand-roll retry/backoff logic around `ErrBufferFulled`, and so data isn't lost purely because of a transient burst or reconnect.

**Status: implemented.** `Client` and `SrvSession` now queue outbound ASDUs in a `messageQueue` (`cs104/queue.go`): a mutex-protected, bounded FIFO that evicts the oldest entry on overflow instead of rejecting the newest (`Send()` no longer returns `ErrBufferFulled`; it logs a warning via the existing `clog` debug logging when an eviction happens). Unlike the old channel, `cleanUp()` no longer drains it, so a message queued but not yet transmitted survives a `Client`/`ServerSpecial` reconnect. On the `Server` side, the connections in a redundancy group share one queue (`Server.queueFor`), so a connection superseded by failover doesn't lose whatever it hadn't sent yet -- the connection replacing it simply continues draining the same queue. `ErrBufferFulled` is kept declared (in `cs104/error.go`) for source compatibility, but this package no longer produces it.

---

## 2. No redundancy-group / single-active-connection enforcement on the server

**Summary**: `Server` treats every connected session as independently active. Once a session sends STARTDT_ACT it becomes active with no coordination against other sessions, and `Server.Send()` broadcasts to every session in the map at once:

```go
// cs104/server.go
func (sf *Server) Send(a *asdu.ASDU) error {
	sf.mux.Lock()
	for k := range sf.sessions {
		_ = k.Send(a.Clone())
	}
	sf.mux.Unlock()
	return nil
}
```

There is no concept of a "redundancy group" or of deactivating one connection when another activates.

**Impact**: In a deployment with more than one master/client talking to the same server (e.g. a primary and a standby SCADA master), both connections receive every spontaneous update simultaneously instead of only the currently-active one. This diverges from the intended failover model where only one master should be receiving live data at a time.

**Suggested fix**: Add an optional server mode that groups connections (e.g. by IP or an explicit group registration) and, on STARTDT_ACT from one connection in a group, closes/deactivates any other already-active connection in that group — mirroring the "single active master" semantics the protocol is designed around.

**Status: implemented.** `Server.SetServerMode` selects one of `ModeConnectionIsRedundancyGroup` (default, unchanged behavior), `ModeSingleRedundancyGroup` (every connection to the server is one group), or `ModeMultipleRedundancyGroups` (connections grouped by peer IP via `Server.AddRedundancyGroup`/`NewRedundancyGroup`/`RedundancyGroup.AddAllowedClient`). When a session completes STARTDT and belongs to a group, any other still-active session in the same group is **deactivated, not closed** (`cs104/redundancy.go`, `SrvSession.forceDeactivate`/`IsActive`): it falls back to inactive (an unsolicited STOPDT confirm) but its TCP connection stays open, matching IEC 60870-5-104's redundant-connection model where non-active connections remain established as warm standbys so they can be reactivated (STARTDT) without paying reconnection cost. (An earlier version of this feature force-closed the superseded connection instead of deactivating it, which didn't match the standard's intent and was corrected.) `Server.Send()` queues one copy per destination -- once per redundancy group, whose members share a queue and of which only the active one transmits, and once per ungrouped connection. It previously pushed a copy onto every session, which left a deactivated standby accumulating traffic it could never transmit and then flushing all of it, stale, on taking over.

---

## 3. No connection admission control (max connections, accept/reject hook, IP allow-list)

**Summary**: `Server.ListenAndServer` accepts every incoming TCP connection unconditionally:

```go
for {
	conn, err := listen.Accept()
	if err != nil {
		sf.Error("server run failed, %v", err)
		return
	}
	sf.wg.Add(1)
	go func() {
		sess := &SrvSession{ ... }
		...
	}()
}
```

There's no cap on the number of concurrent sessions, no hook to inspect/reject a peer before a session is created, and no IP allow-listing.

**Impact**: A misbehaving or malicious client can open unbounded connections against the server, each spinning up its own set of goroutines and channel buffers, with no way for the application to limit or filter this at the transport layer.

**Suggested fix**: Add a configurable maximum open-connection count and an optional `ConnectionRequestHandler`-style callback invoked right after `Accept()` (with the remote address available) so the application can reject a peer before any session state is allocated.

**Status: implemented.** `Server.SetMaxConnections(n)` caps concurrent sessions; once at capacity, additional connections are accepted at the TCP level then immediately closed, until an existing session ends and frees a slot. `Server.SetConnectionRequestHandler(func(net.Addr) bool)` is invoked right after `Accept()`, before any session state is allocated, and a `false` return closes the connection immediately. `Server.AllowClientIPs(ips...)` is sugar over the handler for a small static allow-list, mirroring `AllowCommonAddrs`. Both checks (and the `sessions` map insertion itself) happen synchronously in `Server.acceptSession`, called from `ListenAndServer`'s accept loop, so the max-connections check can't be overshot by a burst of near-simultaneous connections racing an asynchronous registration.

---

## 4. No per-message common-address filtering hook

**Summary**: `serverHandler` only checks that the incoming ASDU's common address isn't the invalid marker:

```go
if asduPack.CommonAddr == asdu.InvalidCommonAddr {
	return asduPack.SendReplyMirror(sf, asdu.UnknownCA)
}
```

It never checks whether the CA is actually one this server/station is responsible for — "present" and "mine" are conflated.

**Impact**: A server responsible for common address 5 will happily process a command addressed to common address 9999, rather than replying with `UnknownCA` as the protocol intends for addresses it doesn't own.

**Suggested fix**: Add an optional `IsCAAllowedHandler`-style callback (or a configured set of owned CAs) that `serverHandler` consults before dispatching to the type-specific handler, replying `UnknownCA` when the CA isn't one the server owns.

**Status: implemented.** `Server.SetCommonAddrFilter`/`AllowCommonAddrs` (and the equivalent on `ClientOption`, for `ServerSpecial`) let the application declare which CAs it's responsible for; `serverHandler` now checks this once, hoisted above the type-specific switch (replacing 7 duplicated `CommonAddr == InvalidCommonAddr` checks with one `commonAddrAllowed` call). `asdu.GlobalCommonAddr` is always accepted regardless of the filter, since it's the broadcast address and isn't something a single station owns. With no filter configured, behavior is unchanged from before (every CA other than the invalid marker is accepted).

---

## 5. `Server.TLSConfig` is dead code — server-side TLS doesn't actually work

**Summary**: `Server` declares a `TLSConfig *tls.Config` field, but nothing in `server.go` ever reads it:

```go
// cs104/server.go
type Server struct {
	config         Config
	params         asdu.Params
	handler        ServerHandlerInterface
	TLSConfig      *tls.Config // never used
	...
}

func (sf *Server) ListenAndServer(addr string) {
	listen, err := net.Listen("tcp", addr) // always plaintext
	...
}
```

There's also no setter for it (`ClientOption` has `SetTLSConfig`, `Server` does not), so a caller can only reach this field via a struct literal, and doing so has no effect.

**Impact**: The field's presence strongly implies server-side TLS is supported, but `ListenAndServer` never calls `tls.Listen` or wraps accepted connections in TLS — a caller who sets `TLSConfig` expecting an encrypted listener silently gets plaintext instead. Client-side TLS does work (`openConnection` handles the `tls://`/`ssl://` scheme).

**Suggested fix**: Either wire `TLSConfig` into `ListenAndServer` (use `tls.Listen` when set) and add a `SetTLSConfig` setter to match the client side, or remove the field entirely if server-side TLS isn't planned, so the API doesn't advertise a capability that doesn't exist.

**Status: not planned.** Deemed irrelevant to current priorities.

---

## 6. File transfer (F_xx ASDUs) is an empty stub

**Summary**: `asdu/filet.go` in its entirety:

```go
package asdu

// 文件传输的应用服务数据单元
// TODO:
```

The `F_FR_NA_1` … `F_SC_NB_1` type-ID constants exist in `identifier.go` (so parsing at least recognizes them), but there are no encode/decode helpers, no command constructors, and no server-side dispatch for any of them.

**Impact**: File transfer is part of the companion standard and the type IDs are already reserved in this codebase, but there's currently no way for a user of this library to send or receive a file over CS104 — the feature is entirely unimplemented despite looking present in the type catalog.

**Suggested fix**: Either implement the file-transfer ASDUs (F_FR_NA_1 file ready, F_SR_NA_1 section ready, F_SC_NA_1 select/call/request, F_LS_NA_1 last section/segment, F_AF_NA_1 ack file/section, F_SG_NA_1 segment, F_DR_TA_1 directory) following the pattern of `cproc.go`/`csys.go`, or clearly document that file transfer isn't supported so callers don't assume it works because the constants exist.

**Status: not planned.** Deemed irrelevant to current priorities.

---

## 7. Security/authentication ASDU types (S_xx) are declared but entirely unimplemented

**Summary**: `identifier.go` declares `S_CH_NA_1` through `S_UC_NA_1` (types 81–95, the authentication/session-key-management extension) with an entry in the info-object-size table, but no other file in the `asdu` package references these types — no constructors, no decoders, no handler dispatch.

**Impact**: Same shape of gap as file transfer: the type catalog suggests support, but there is no actual functionality behind it.

**Suggested fix**: Either implement the authentication/session-key ASDUs or remove/annotate them clearly as unsupported placeholders, so they aren't mistaken for working functionality.

**Status: not planned.** Deemed irrelevant to current priorities.

---

## 8. No threadless/tick-driven mode for embedded or single-threaded use

**Summary**: Every `Client`/`SrvSession` connection always spawns four goroutines (`recvLoop`, `sendLoop`, `handlerLoop`, and the `run` state machine). There is no alternative mode where the state machine is driven by an explicit `Tick()`/poll call from a single caller-controlled loop.

**Impact**: Applications that want tight control over scheduling (e.g. running on a constrained system, or integrating into an existing single-threaded event loop) have no way to avoid the goroutine-per-connection model.

**Suggested fix**: Not necessarily required for a Go library (goroutines are cheap relative to OS threads), but worth deciding deliberately and documenting rather than leaving unaddressed — e.g. an optional synchronous/tick-driven API for advanced users, or an explicit statement in the README that this is out of scope.

**Status: not planned.** Deemed irrelevant to current priorities.

---

## 9. No raw-message observation hook for applications

**Summary**: The only visibility into raw APDU bytes sent/received is the internal `clog` debug logging (`sf.Debug("RX Raw[% x]", apdu)` / `sf.Debug("TX Raw[% x]", apdu)`), which is a fixed log format gated by `LogMode`, not something an application can hook into programmatically.

**Impact**: Applications that want to record raw traffic for diagnostics, replay, or protocol-conformance testing have no supported way to observe it other than parsing debug log output.

**Suggested fix**: Add an optional callback (e.g. `SetRawMessageHandler(func(conn asdu.Connect, data []byte, isSend bool))`) invoked alongside the existing debug logging in `recvLoop`/`sendLoop`, so applications can tap raw traffic without scraping logs.

**Status: not planned.** Deemed irrelevant to current priorities.
