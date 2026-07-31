# go-iecp5(Archived)
## NOTE: Archived, not maintain. 
## NOTE: 已归档, 不再维护, 放弃License. 有需要的可以自由分发

go-iecp5 library for IEC 60870-5 based protocols in pure go.
The current implementation contains code for IEC 60870-5-104 (protocool over TCP/IP) specifications.



[![Go.Dev reference](https://img.shields.io/badge/go.dev-reference-blue?logo=go&logoColor=white)](https://pkg.go.dev/github.com/thinkgos/go-iecp5?tab=doc)
[![Tests](https://github.com/thinkgos/go-iecp5/actions/workflows/ci.yml/badge.svg)](https://github.com/thinkgos/go-iecp5/actions/workflows/ci.yml)
[![codecov](https://codecov.io/gh/thinkgos/go-iecp5/branch/master/graph/badge.svg)](https://codecov.io/gh/thinkgos/go-iecp5)
[![Go Report Card](https://goreportcard.com/badge/github.com/thinkgos/go-iecp5)](https://goreportcard.com/report/github.com/thinkgos/go-iecp5)
[![License](https://img.shields.io/github/license/thinkgos/go-iecp5)](https://github.com/thinkgos/go-iecp5/raw/master/LICENSE)
[![Tag](https://img.shields.io/github/v/tag/thinkgos/go-iecp5)](https://github.com/thinkgos/go-iecp5/tags)
[![Sourcegraph](https://sourcegraph.com/github.com/thinkgos/go-iecp5/-/badge.svg)](https://sourcegraph.com/github.com/thinkgos/go-iecp5?badge)


asdu package: [![GoDoc](https://godoc.org/github.com/thinkgos/go-iecp5/asdu?status.svg)](https://godoc.org/github.com/thinkgos/go-iecp5/asdu)  
cs104 package: [![GoDoc](https://godoc.org/github.com/thinkgos/go-iecp5/cs104?status.svg)](https://godoc.org/github.com/thinkgos/go-iecp5/cs104)  

## Feature:

- client/server for CS 104 TCP/IP communication
- support for much application layer(except file object) message types,

## Logging

The library logs through `log/slog`. With no setup it writes to
`slog.Default()`, so warnings and errors — a dropped connection, a t₁
timeout, a rejected configuration — are visible without opting in:

```go
srv := cs104.NewServer(&handler{}) // logs to slog.Default()
```

Pass your own logger to route records anywhere slog can go, and to control
the level. The per-frame protocol trace is at `Debug`; everything a running
system normally needs is `Info` and above:

```go
srv.SetLogger(slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
    Level: slog.LevelDebug, // include the RX/TX frame trace
})))
```

`Client` and `ClientOption` (for `ServerSpecial`) take the same `SetLogger`.
To silence the library, give it a logger whose handler discards — passing
`nil` restores `slog.Default()` rather than disabling output.

Server records carry the connection they came from, so sessions stay
distinguishable when several masters are connected:

```
level=ERROR msg="no acknowledgement within t₁, closing" component=cs104.server remote=10.0.0.7:41230 t1=15s unacked=12
```

### Migrating from `clog`

The `clog` package is gone. `LogMode(bool)` and
`SetLogProvider(clog.LogProvider)` are replaced by `SetLogger(*slog.Logger)`.
`LogMode(true)` has no direct equivalent because output is no longer off by
default; to get the old full trace, set a `Debug`-level handler as above.

## Examples

See [`_examples`](./_examples) for runnable master (`cs104.Client`), slave
(`cs104.Server`), dial-out slave (`cs104.ServerSpecial`), and
redundant-master server setups.

## Decoding ASDUs

`Append*` builds an information object; the `Get*` accessors read one back.
Reading is non-destructive and reports failure as a value:

```go
cmd, err := a.GetSingleCmd()
if err != nil {
    // truncated or malformed: err is asdu.ErrInfoObjTruncated or
    // asdu.ErrTypeIDNotMatch. The ASDU is untouched and still safe to
    // echo back with a.SendReplyMirror(...).
}
```

This matters most when decoding traffic you did not produce -- frames
lifted out of a capture, or anything from an untrusted peer -- where a
malformed object is ordinary input rather than a programming error.

### Migrating from the cursor-based API

Decoding used to consume the information object as it read, and signalled
truncation by panicking. If you are upgrading:

- **`Get*` now returns an extra `error`.** Check it instead of wrapping
  calls in `recover()`.
- **Drop `Clone()` before decoding.** `a.Clone().GetSingleCmd()` was
  required because decoding emptied `a`, which broke a later
  `SendReplyMirror`. Reading no longer modifies the ASDU, so
  `a.GetSingleCmd()` is correct on its own.
- **The exported `Decode*` methods are gone.** They were the cursor
  plumbing behind `Get*`; use the `Get*` accessor for the type you want.

# Reference
lib60870 c library [lib60870](https://github.com/mz-automation/lib60870)  
lib60870 c library doc [lib60870 doc](https://support.mz-automation.de/doc/lib60870/latest/group__CS104__MASTER.html)

## Donation

if package help you a lot,you can support us by:

**Alipay**

![alipay](https://github.com/thinkgos/thinkgos/blob/master/asserts/alipay.jpg)

**WeChat Pay**

![wxpay](https://github.com/thinkgos/thinkgos/blob/master/asserts/wxpay.jpg)
