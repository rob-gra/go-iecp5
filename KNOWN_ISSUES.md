# Known issues / backlog

Tracking list for gaps and defects found during code review. GitHub Issues
are disabled on this repository, so these are recorded here instead. Each
entry is written as a standalone issue: summary, impact, and a suggested
fix.

- [x] [Server has no message queue: full send buffers silently drop ASDUs instead of buffering](#1-server-has-no-message-queue-full-send-buffers-silently-drop-asdus-instead-of-buffering) — implemented: `messageQueue` (`cs104/queue.go`)
- [x] [No redundancy-group / single-active-connection enforcement on the server](#2-no-redundancy-group--single-active-connection-enforcement-on-the-server) — implemented: `Server.SetServerMode` / `AddRedundancyGroup`
- [x] [No connection admission control (max connections, accept/reject hook, IP allow-list)](#3-no-connection-admission-control-max-connections-acceptreject-hook-ip-allow-list) — implemented: `Server.SetMaxConnections` / `SetConnectionRequestHandler` / `AllowClientIPs`
- [x] [No per-message common-address filtering hook](#4-no-per-message-common-address-filtering-hook) — implemented: `Server.SetCommonAddrFilter` / `AllowCommonAddrs`
- [x] [`Server.TLSConfig` is dead code — server-side TLS doesn't actually work](#5-servertlsconfig-is-dead-code--server-side-tls-doesnt-actually-work) — implemented: `Server.SetTLSConfig`, wired into `ListenAndServer`
- [~] [File transfer (F_xx ASDUs) is an empty stub](#6-file-transfer-f_xx-asdus-is-an-empty-stub) — not planned, deemed irrelevant
- [~] [Security/authentication ASDU types (S_xx) are declared but entirely unimplemented](#7-securityauthentication-asdu-types-s_xx-are-declared-but-entirely-unimplemented) — not planned, deemed irrelevant
- [~] [No threadless/tick-driven mode for embedded or single-threaded use](#8-no-threadlesstick-driven-mode-for-embedded-or-single-threaded-use) — not planned, deemed irrelevant
- [~] [No raw-message observation hook for applications](#9-no-raw-message-observation-hook-for-applications) — not planned, deemed irrelevant
- [x] [Send window allowed k+1 unacknowledged I-frames instead of k](#10-send-window-allowed-k1-unacknowledged-i-frames-instead-of-k) — fixed in `connection.run`
- [x] [`Config.Valid` did not enforce the cross-field constraints it documents](#11-configvalid-did-not-enforce-the-cross-field-constraints-it-documents) — implemented: `w <= 2/3 k` and `t₂ < t₁`
- [x] [Invalid config/params were replaced with defaults silently](#12-invalid-configparams-were-replaced-with-defaults-silently) — implemented: warnings on `Server` and `ClientOption`
- [x] [Logging is disabled by default, so every warning and error is silent](#13-logging-is-disabled-by-default-so-every-warning-and-error-is-silent) — resolved by moving to `log/slog`
- [x] [A client's activation timer outlives the connection that started it](#14-a-clients-activation-timer-outlives-the-connection-that-started-it) — implemented: `connRole.roleCleanUp`
- [x] [Recovering a panicking handler erased the failure](#15-recovering-a-panicking-handler-erased-the-failure) — implemented: named returns on both `dispatchASDU`
- [x] [`Start` could not report a duplicate start, and lost the monotonic clock](#15b-start-could-not-report-a-duplicate-start-and-activation-timers-lost-the-monotonic-clock) — implemented: `ErrAlreadyStarted`, `atomic.Pointer[time.Time]`
- [x] [`Server.Send` accepted an ASDU too long to frame](#16-serversend-accepted-an-asdu-too-long-to-frame) — implemented: same length check as `connection.Send`
- [x] [`ParseAPCI` returned unexported types, so nothing could use it](#17-parseapci-returned-unexported-types-so-nothing-could-use-it) — implemented: `IAPCI`/`SAPCI`/`UAPCI` and the `U*` constants are exported
- [x] [`TypeID.String` decorated the mnemonic as `TID<...>`](#18-typeidstring-decorated-the-mnemonic-as-tid) — implemented: returns the bare mnemonic
- [x] [An I-frame in the stopped state was discarded without being counted](#19-an-i-frame-in-the-stopped-state-was-discarded-without-being-counted) — implemented: closes the connection
- [x] [STOPDT was confirmed while sent I-frames were still unacknowledged](#20-stopdt-was-confirmed-while-sent-i-frames-were-still-unacknowledged) — implemented: `confirmStopDtIfSettled`
- [x] [CP56Time2a encoded Sunday as "not used" and never set the SU bit](#21-cp56time2a-encoded-sunday-as-not-used-and-never-set-the-su-bit) — implemented
- [ ] [Smaller divergences from lib60870, not addressed](#22-smaller-divergences-from-lib60870-not-addressed)

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

**Status: implemented.** `Client` and `SrvSession` now queue outbound ASDUs in a `messageQueue` (`cs104/queue.go`): a mutex-protected, bounded FIFO that evicts the oldest entry on overflow instead of rejecting the newest (`Send()` no longer returns `ErrBufferFulled`; it logs a warning when an eviction happens). Unlike the old channel, `cleanUp()` no longer drains it, so a message queued but not yet transmitted survives a `Client`/`ServerSpecial` reconnect. On the `Server` side, the connections in a redundancy group share one queue (`Server.queueFor`), so a connection superseded by failover doesn't lose whatever it hadn't sent yet -- the connection replacing it simply continues draining the same queue. `ErrBufferFulled` has been removed: nothing produced it any more.

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

**Status: implemented.** `ListenAndServer` wraps its listener with `tls.NewListener` when `TLSConfig` is set, and `Server.SetTLSConfig` mirrors `ClientOption.SetTLSConfig` on the dial-out side. The field no longer advertises a capability the code doesn't have.

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

**Status: not planned.** Deemed irrelevant to current priorities. Note that
the premise has shifted since this was written: the frame trace now goes
through `log/slog` (issue 13), so an application can capture raw traffic by
installing its own `slog.Handler` and matching on the `rx raw`/`tx raw`
records, without a dedicated hook or any log-scraping.

---

## 10. Send window allowed k+1 unacknowledged I-frames instead of k

**Summary**: `connection.run`'s send gate compared the number of *already
outstanding* I-frames against `k` with `<=`, then sent one more on top of it:

```go
// cs104/connection.go
if sf.IsActive() && seqNoCount(sf.ackNoSend, sf.seqNoSend) <= sf.config.SendUnAckLimitK {
    if o, ok := sf.sendQueue.Pop(); ok {
        sf.sendIFrame(o) // seqNoSend++, so the count becomes k+1
        continue
    }
}
```

With `k=3` the connection emitted 4 unacknowledged I-frames before stopping.

**Impact**: IEC 60870-5-104 subclass 5.5 caps the difference between the send
state variable and the acknowledged sequence number at `k`. A peer that
enforces `k` — lib60870 does — sees the (k+1)th I-frame as a protocol
violation and drops the connection. Against a lenient peer nothing visibly
breaks, which is why this survived: it only manifests against a strict
implementation, and then only under enough load to fill the window.

**Status: fixed.** The comparison is now `<` against the outstanding count, so
at most `k` I-frames are ever unacknowledged. Covered by
`TestConnection_KWindowStopsAtK`, which counts the I-frames emitted with no
acknowledgement and fails at `k+1`.

---

## 11. `Config.Valid` did not enforce the cross-field constraints it documents

**Summary**: `Config`'s own field comments state two relationships — `w` must
not exceed 2/3 `k` ("w不超过2/3k"), and `t₂` must be less than `t₁` ("必须小于
t₁") — but `Valid()` only range-checked each field independently. A config
with `t₂=20s, t₁=15s`, or with `w=k`, was accepted.

**Impact**: Both misconfigurations fail in ways that point away from their
cause. `t₂ >= t₁` means this end acknowledges received frames only after the
peer has already given up waiting and dropped the link, so a quiet connection
cycles on a timer with no error attributable to configuration. `w` close to
`k` stalls the sender waiting for an acknowledgement the receiver is not yet
obliged to send, so throughput collapses to whatever `t₂` paces and the link
looks slow rather than misconfigured.

**Status: implemented.** `Valid()` now rejects both. Additionally, an
unspecified `w` defaults to `2/3 k` rather than to a fixed 8, so lowering `k`
alone still yields a valid pair; at the default `k=12` this is 8, unchanged.
Covered by `cs104/config_test.go`.

---

## 12. Invalid config/params were replaced with defaults silently

**Summary**: `Server.SetConfig`/`SetParams` and
`ClientOption.SetConfig`/`SetParams` discarded a rejected value and
substituted `DefaultConfig()`/`asdu.ParamsWide` with no output, so a caller
could not tell their settings had not been applied.

**Impact**: The caller runs with timing parameters they did not choose. In a
SCADA deployment the symptom is connection cycling or throughput loss that
looks like a network fault, and the configuration is the last place anyone
looks because it appears to have been accepted.

**Status: implemented.** All four call sites now emit a warning naming the
validation error and the fallback. `ClientOption` has no logger of its own, so
it records the message and `NewClient`/`NewServerSpecial` emit it once a
logger exists. Note this is only visible if the application has enabled
logging — see issue 13.

---

## 13. Logging is disabled by default, so every warning and error is silent

**Summary**: `clog.NewLogger` returns a `Clog` with its output flag clear, and
every level checks that flag:

```go
// clog/clog.go
func NewLogger(prefix string) Clog {
    return Clog{defaultLogger{log.New(os.Stdout, prefix, log.LstdFlags)}, 0}
}                                                                     // ^ off

func (sf Clog) Warn(format string, v ...interface{}) {
    if atomic.LoadUint32(&sf.has) == 1 { // never true until LogMode(true)
        sf.provider.Warn(format, v...)
    }
}
```

Nothing in the library enables it, so unless the application calls
`LogMode(true)` the entire package is silent.

**Impact**: This is not only about debug tracing. `Critical`, `Error` and
`Warn` are gated by the same flag, so by default a deployment gets no output
for `fatal transmission timeout t₁`, `remote connect closed`, `send queue
full, dropped the oldest unsent message`, a panicking application handler
caught by `dispatchASDU`'s `recover`, or a rejected configuration (issue 12).
An operator sees connections dropping and data missing with nothing in the
logs to explain either. `LogMode` is also not documented as something a
production deployment must call.

**Suggested fix**: Separate severity from verbosity — have `LogMode` gate
`Debug` only (the per-frame `RX Raw`/`TX Raw` tracing it exists for) and let
`Warn`/`Error`/`Critical` always reach the provider. Applications that want
true silence can install a no-op `LogProvider` via `SetLogProvider`. This is a
behavior change for existing callers, who would begin seeing warnings and
errors they currently do not, which is the point; it should be a deliberate
decision rather than a drive-by fix.

**Status: resolved, by replacing `clog` with `log/slog` rather than by the
fix suggested above.** The `clog` package is deleted. Records now go to
`slog.Default()` when nothing is configured, so the "off until `LogMode(true)`"
gate no longer exists — the defect disappears rather than being patched.
`LogMode(bool)` and `SetLogProvider(clog.LogProvider)` are replaced by
`SetLogger(*slog.Logger)` on `Server`, `Client` and `ClientOption`, and on the
`ServerSpecial` interface. Silencing the library is now explicit: pass a
logger whose handler discards.

Two things came out of the migration beyond the swap itself:

- **Per-connection attribution.** `Server.newSession` derives each session's
  logger with the peer address attached, so every record names the connection
  that produced it. Previously all sessions shared one static prefix and a
  line like `fatal transmission timeout t₁` could not be tied to a
  connection — the practical reason this migration was worth doing on a
  multi-master server.
- **Levels revisited, now that records are visible.** Routine lifecycle events
  that were logged as errors would, under the new default, report every clean
  shutdown as a fault: a peer hanging up (`io.EOF`), this side closing the
  socket during teardown (`net.ErrClosed` from `recvLoop`), and `Accept`
  failing because `Close` closed the listener. Each is now reported at the
  level it deserves.

The per-frame trace sites (`rx raw`, `tx raw`, the I/S/U frame records) are
guarded by `connection.debugEnabled`. slog boxes a call's arguments into
`[]any` before the handler can discard them, so an unguarded `Debug` on the
frame path allocates on every frame even with debug off: measured at 2
allocs/94ns per call unguarded versus 0 allocs/5.9ns guarded.

---

## 14. A client's activation timer outlives the connection that started it

**Summary**: `Client.SendStartDt`/`SendStopDt` record when the activation was
sent, and `roleTimedOut` tears the connection down if no confirmation arrives
within t₁. `connection.cleanUp` resets the state machine before `run`
(re)starts — sequence numbers, `testFrAliveSendSince`, the frame buffers —
but nothing cleared these two fields, and `cleanUp` had no way to reach them:
they live on `Client`, not on `connection`.

**Impact**: The timestamp survives into the next connection, which never sent
the activation being timed. An application that issues STARTDT from its
connect handler overwrites it and never notices. One that issues STARTDT once
— a reasonable reading of an API where `SendStartDt` is a separate call —
loses every reconnect to its predecessor's expired timer, in a loop, with the
log blaming a STARTDT confirmation that the failing connection never asked
for. Reproduced: with a peer that answers TESTFR but not STARTDT, the client
opened 4 connections in 3s instead of 2.

**Status: implemented.** `connRole` gains `roleCleanUp`, called from
`connection.cleanUp` alongside its own reset. `Client` clears both
timestamps; `SrvSession` is a no-op, matching `roleTimedOut`, which it also
implements trivially. Covered by
`TestClient_StaleActivationTimerDoesNotOutliveItsConnection`, whose peer
answers TESTFR so the connection under test can only be ended by the stale
timer — a peer that answers nothing loses every connection to an unconfirmed
TESTFR and the test would pass either way.

---

## 15. Recovering a panicking handler erased the failure

**Summary**: Both `dispatchASDU` implementations recover a panicking
application handler so it cannot take the connection down. Neither named its
return value, so recovery left the function returning `nil` — success.

**Impact**: Currently bounded: the caller (`connection.handlerLoop`) only
logs the error, and the panic itself is already logged with more detail. It
is a latent trap rather than a live defect — the moment anything acts on that
return (a negative confirmation to the peer, a metric, a retry), a handler
that blew up will read as one that succeeded.

**Status: implemented.** Both use a named return and set it from the
`recover` block, carrying what the handler panicked with. Covered by
`TestClient_HandlerPanicSurfacesAsError` and
`TestSrvSession_HandlerPanicSurfacesAsError`.

---

## 15b. `Start` could not report a duplicate start, and activation timers lost the monotonic clock

**Summary**: Two smaller defects on the same call path.

`Client.Start`/`serverSpec.Start` spawned `running`, which claimed the
`initial -> disconnected` transition with a CAS. Since the CAS ran in the new
goroutine, `Start` had already returned `nil`: a second `Start` on a running
client reported success and did nothing.

Separately, the activation timestamps were stored as Unix nanoseconds in an
`atomic.Int64`. `time.Unix` yields a `time.Time` with no monotonic reading,
so the `now.Sub(...)` in `roleTimedOut` fell back to the wall clock — the one
timer in the state machine that did, every other one holding a `time.Time`
throughout. A clock step (NTP correction, a resumed VM) could fire t₁ early
or skip it.

**Impact**: The duplicate start hides a caller bug. The monotonic loss is
narrow but real: t₁ is 15s by default, so any step larger than a few seconds
during an outstanding STARTDT/STOPDT distorts it.

**Status: implemented.** `Start` now performs the CAS itself and installs
`closeCancel` under the same lock, returning `ErrAlreadyStarted` on a
duplicate; `running` takes the `ctx` as a parameter. Holding the lock across
both keeps `Close` working for a caller that calls it the instant `Start`
returns. The timestamps are `atomic.Pointer[time.Time]`, which preserves the
monotonic reading and stays typed — the reason the original avoided
`atomic.Value`, whose `any` unwraps to the zero time on a failed assertion,
indistinguishable from "not waiting". Covered by
`TestClient_StartTwiceReportsAlreadyStarted` and
`TestClient_CloseImmediatelyAfterStart`.

---

## 16. `Server.Send` accepted an ASDU too long to frame

**Summary**: `connection.Send` rejects an ASDU whose marshalled form exceeds
`asdu.ASDUSizeMax`, deliberately and with a comment saying why: past that
point it is bytes on a queue, and the only thing left to do with one too long
to frame is drop it. `Server.Send` — the broadcast path — marshalled and
enqueued without the check.

```go
func (sf *Server) Send(a *asdu.ASDU) error {
	data, err := a.MarshalBinary()   // no length check follows
	...
	for _, q := range sf.groupQueues { q.Push(data) }
```

`MarshalBinary` does not bound the total either; it validates the identifier
fields and allocates whatever the information object needs.

**Impact**: The caller is told `nil` for a message that can never be
delivered. A copy is queued to every redundancy group and every ungrouped
session, where it occupies a slot until it reaches the front and
`sendIFrame` fails to frame it — logged, then discarded. Because the queues
evict oldest-first, an over-long ASDU can displace data that would otherwise
have been sent. The two send paths disagreed on the same input: with a
connected session, `Server.Send` returned `nil` and queued it while
`SrvSession.Send` returned `asdu: asdu filed length large than max 249`.

**Status: implemented.** `Server.Send` performs the same check, immediately
after marshalling, and returns `asdu.ErrLengthOutOfRange`. Covered by
`TestServer_SendRejectsOverlongASDU`, which asserts both the error and that
the group queue did not grow.

---

## 17. `ParseAPCI` returned unexported types, so nothing could use it

**Summary**: `ParseAPCI` is documented as the bounds-checked, exported entry
point for decoding APDUs "from sources other than a live connection, e.g.
frames extracted from a pcap capture". It returns the APCI as an `any`:

```go
func ParseAPCI(apdu []byte) (apci any, asduPayload []byte, err error)
```

The concrete types behind that `any` were `iAPCI`, `sAPCI` and `uAPCI` — all
unexported. A caller outside the package therefore had no type to assert
against, and no way to tell an I-format APDU (the only one carrying an ASDU)
from an S- or U-format one. The U-frame function constants were unexported
too, so even a caller who reached a `uAPCI` had nothing to compare its
function byte with.

**Impact**: The function could not perform the task its own documentation
recommends it for. Writing the pcap example against the public API meant
reading the I-format bit off the control field by hand — reimplementing, at
the call site, the classification `ParseAPCI` exists to do.

**Status: implemented.** `IAPCI`, `SAPCI` and `UAPCI` are exported with
exported fields (`SendSN`, `RcvSN`, `Function`), as are the six U-format
function constants (`UStartDtActive` … `UTestFrConfirm`). Covered by
`cs104/apci_external_test.go`, which is in package `cs104_test` on purpose: a
test compiled into `cs104` reaches unexported identifiers regardless, so only
an external test can demonstrate the property. `_examples/cs104_pcap_csv` now
type-switches on `cs104.IAPCI` instead of inspecting the control byte.

---

## 18. `TypeID.String` decorated the mnemonic as `TID<...>`

**Summary**: `TypeID.String()` returned `"TID<M_ME_NC_1>"` rather than
`"M_ME_NC_1"`.

**Impact**: The decoration reads acceptably in a log line, but `String` is
what every `%v`, every CSV column and every serialized field gets. Anything
downstream matching on a type identification had to strip the wrapper first,
and `Identifier.String()` inherited it too. It is also unidiomatic: a
`Stringer` on a named integer conventionally returns the name of the value,
the way `time.Month` returns `"January"`.

**Status: implemented.** Known ids return the bare mnemonic. An id this build
has no name for returns `"TypeID(200)"` rather than a bare `"200"`, so it
stays distinguishable from a mnemonic wherever it is recorded — the form
`stringer` generates for an out-of-range value.

---

## 19. An I-frame in the stopped state was discarded without being counted

**Summary**: `connection.run` dropped an I-frame received while data transfer
was stopped, logged a warning, and carried on:

```go
if !sf.IsActive() {
    sf.log.Warn("discarding I-frame: data transfer is stopped")
    break // discard: data transfer is stopped
}
```

The `break` came before the sequence-number check, so the frame was neither
validated nor counted.

**Impact**: This is the one option that is wrong whichever way the standard is
read. Subclass 5.3 permits no user data in the stopped state, so the frame is
a protocol violation — but discarding it without counting leaves the peer's
send sequence number one ahead of ours, silently. Nothing reports it at the
time. The divergence surfaces later, on an unrelated frame after the next
STARTDT, as `I-frame sequence out of step` — naming the wrong cause, or never
surfacing at all if the peer stops sending. Verified against the previous
build: a session received two illegal I-frames in the stopped state, stayed
connected, and reported nothing.

The two coherent choices are to close (what lib60870's controlled station
does: *"Received I message while connection not active -> close
connection"*) or to count and process it (what lib60870's *controlling*
station does — it validates and acknowledges I-frames regardless of state).
Doing neither is what produced the silent divergence.

**Status: implemented.** The connection closes, matching the controlled
station in both the standard's state machine and lib60870.

This reverses `TestSrvSession_IFrameBeforeStartDt_Discarded`, which asserted
the discard. That test arrived with a sequence-wraparound refactor and pinned
the behavior that existed rather than deciding it; it is now
`TestSrvSession_IFrameBeforeStartDt_ClosesConnection`.

---

## 20. STOPDT was confirmed while sent I-frames were still unacknowledged

**Summary**: The controlled station answered STOPDT_ACT immediately:

```go
case UStopDtActive:
    sf.sendUFrame(UStopDtConfirm)
    sf.isActive.Store(false)
```

regardless of how many I-frames it had sent that the peer had not yet
acknowledged.

**Impact**: The confirmation says the connection is quiesced. Sending it with
data still in flight states something untrue, and against a peer that stops
acknowledging once stopped those frames then sit unconfirmed until t₁ expires
and takes the connection down — a clean shutdown turning into a timeout
roughly 15s later. Verified: with two unacknowledged I-frames outstanding, the
old build answered STOPDT_ACT with STOPDT_CON immediately.

lib60870 models this as a third connection state,
`M_CON_STATE_UNCONFIRMED_STOPPED` (*"only U, S frames allowed"*), and holds
the confirmation until the peer's S-frame clears the outstanding messages.
This library had only the active/inactive pair.

**Status: implemented.** On STOPDT_ACT the session stops data transfer at
once, acknowledges anything received but not yet confirmed (as lib60870 does,
rather than leaving it to t₂), and withholds the confirmation if its own sends
are outstanding. `connection.confirmStopDtIfSettled` releases it when the
acknowledgement arrives, from either an S-frame or an I-frame's piggybacked
N(R). A fresh STARTDT clears the withheld confirmation, since the peer has
moved on from the request. Covered by
`TestSrvSession_StopDtConfirmWaitsForOutstandingSends`.

---

## 21. CP56Time2a encoded Sunday as "not used" and never set the SU bit

**Summary**: Two defects in the seven-octet time encoder.

Day of week was `byte(ts.Weekday()<<5)`. Go numbers Sunday 0 through Saturday
6; subclass 7.2.6.18 numbers Monday 1 through Sunday 7 and reserves 0 for "day
of week not used". Monday through Saturday coincide, so only Sunday was
affected — encoded as 0, which a conforming receiver reads as "not used".

The SU (summer time) bit was never set. `SetInfoObjTimeZone` accepts any
location, so a time encoded in a DST-observing zone during summer was marked
standard time, an hour out to a receiver that honours the bit. SU is also what
disambiguates the repeated local hour at the autumn transition.

**Impact**: The day-of-week defect degrades rather than corrupts — "not used"
is a legal value — so it is the milder of the two. The SU defect misstates the
time by an hour for anyone using a DST zone. lib60870 sidesteps both by
building from a UTC timestamp and explicitly setting day-of-week to 0.

**Status: implemented.** Sunday encodes as 7, and SU is set from
`time.Time.IsDST()`. UTC reports false there, so UTC output is unchanged apart
from Sundays. Covered by `TestCP56Time2a_DayOfWeek` and
`TestCP56Time2a_SummerTimeBit`; the existing 2019-12-15 vector was a Sunday
and carried the old value.

Not addressed: `ParseCP56Time2a` ignores SU on the way in. Decoding into the
same location gives the right instant, but the repeated hour at the autumn
transition stays ambiguous.

---

## 22. Smaller divergences from lib60870, not addressed

Found while comparing against the C reference implementation. None is a
correctness defect on its own; all are places where the two libraries would
behave differently against a hostile or broken peer.

- **An I-frame carrying no ASDU.** lib60870 requires `msgSize >= 7` and closes
  otherwise. Here the payload fails `UnmarshalBinary`, is logged as
  `discarding undecodable ASDU`, and the connection continues.
- **An S-frame in the stopped state.** lib60870 actively closes
  (*"S message in stopped state -> active close"*). Here it is processed
  normally.
- **`ParseCP24Time2a` fills the date from `time.Now()`.** CP24Time2a carries
  only milliseconds and minutes, so something has to supply the rest, but
  taking it from the wall clock makes decoding non-deterministic and wrong for
  a captured or replayed frame.
- **Year handling in `CP56Time2a`.** `byte(ts.Year() - 2000)` is unguarded, so
  a year outside 2000-2099 silently encodes as some other year.
