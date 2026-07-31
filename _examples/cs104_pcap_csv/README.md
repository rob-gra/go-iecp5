# cs104_pcap_csv

Extracts IEC 60870-5-104 information objects from a packet capture and writes
them to CSV.

```sh
go run . -in capture.pcap -out objects.csv -types M_SP_NA_1,M_ME_NC_1
```

```
frame_time,src_ip,dst_ip,typeid,addr,ioa...
2024-03-01T12:00:00Z,10.20.0.10,10.20.0.44,M_SP_NA_1,1,100,101,102
2024-03-01T12:00:00Z,10.20.0.10,10.20.0.44,M_ME_NC_1,1,5000,5001,5002,5003
2024-03-01T12:00:02Z,10.20.0.10,10.20.0.44,M_SP_NA_1,9,700
```

A runnable capture is included:

```sh
go run . -in testdata/sample.pcap
```

## Flags

| Flag | Default | |
|---|---|---|
| `-in` | | pcap or pcapng file (required) |
| `-out` | stdout | CSV output |
| `-types` | all | type IDs to keep, numeric (`13`) or symbolic (`M_ME_NC_1`), comma-separated |
| `-ca` | all | common addresses to keep |
| `-port` | 2404 | TCP port carrying IEC 104; `0` matches any |
| `-narrow` | false | decode with `asdu.ParamsNarrow` instead of `ParamsWide` |
| `-one-ioa-per-row` | false | one row per information object instead of per ASDU |
| `-time-format` | RFC3339Nano | Go layout for `frame_time` |
| `-header` | true | write a header row |

## Ragged rows

One ASDU carries one information object per IOA, and may carry many, so the
default output has a variable number of trailing columns. Read it by position,
not by a fixed header width — in Go, set `csv.Reader.FieldsPerRecord = -1`;
in pandas, `pd.read_csv(f, names=range(N), engine="python")`.

`-one-ioa-per-row` gives a rectangular file instead, repeating the ASDU's
fields against each of its addresses. Use it if the consumer wants uniform
columns.

## What it handles

**A TCP segment is not an APDU.** One segment routinely carries several (a
frame is at most 255 bytes, and a busy link fills the MSS), and one APDU may
be split across two. Payload is accumulated per *direction* of each TCP
connection and framed out of that byte stream, so neither case loses data.
Each direction is buffered separately — they are independent byte streams
with independent frame boundaries.

**Captures start mid-stream.** `cs104.ReadAPDU` resynchronizes on the `0x68`
start byte, so a capture that begins partway through a frame recovers at the
next one instead of being discarded.

**Sequence ASDUs.** With the SQ bit set, one base address is followed by N
elements at consecutive addresses; the expansion to `base, base+1, …` happens
here, so the CSV always lists every address explicitly.

Addresses are read straight from the information-object bytes using
`asdu.GetInfoObjSize`, so every type identification with a known object size
works without a case per type.

## Why a separate module

`go-iecp5` has no dependencies, and a pcap reader is not a reason to give it
one. This directory has its own `go.mod` requiring gopacket, with a `replace`
pointing at the parent. Nothing here is imported by the library.

The consequence is that `go build ./...` and `go test ./...` at the repository
root do not cover it — run them in this directory.

## Note on frame types

Only I-format APDUs carry an ASDU. This checks the control field directly
rather than type-switching on what `cs104.ParseAPCI` returns, because
`ParseAPCI` hands back an `any` holding one of `iAPCI`/`sAPCI`/`uAPCI` and
those types are unexported — there is nothing for an outside caller to assert
against. Subclass 5.1 defines the I-format APDU as the one whose first
control octet has bit 1 clear, which is what the check reads.
