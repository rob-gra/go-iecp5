# Examples

Each directory is a standalone, runnable `package main`. Run with
`go run .` from inside the directory.

| Example | Type | Demonstrates |
|---|---|---|
| [`cs104_client`](./cs104_client) | `cs104.Client` | The IEC 60870-5-104 master: dials out to a slave, activates data transfer (STARTDT), and issues a general interrogation. |
| [`cs104_server`](./cs104_server) | `cs104.Server` | The IEC 60870-5-104 slave/RTU: listens for master connections, responds to a general interrogation with one point of data, and periodically reports Go runtime statistics (heap in use, goroutine count, whether a GC ran) as spontaneous process data. |
| [`cs104_server_special`](./cs104_server_special) | `cs104.ServerSpecial` | A slave/RTU that dials *out* to the master instead of listening, e.g. because it sits behind NAT/firewall. Same protocol behavior as `cs104_server`, opposite TCP direction. |
| [`cs104_server_redundancy`](./cs104_server_redundancy) | `cs104.Server` | A slave/RTU configured for redundant masters: only one connection per redundancy group is active at a time; connecting a second master deactivates (not closes) the first, which stays as a warm standby. |
| [`cs104_powerstation_server`](./cs104_powerstation_server) / [`cs104_powerstation_client`](./cs104_powerstation_client) | `cs104.Server` / `cs104.Client` | A more complete, end-to-end demo: a simulated power station (generator + reservoir) driven by a client that interrogates, sets an output setpoint, and starts/stops the station, watching the simulation respond in real time. |

## Trying them out

`cs104_server` and `cs104_client` talk to each other directly:

```sh
# terminal 1
cd cs104_server && go run .

# terminal 2
cd cs104_client && go run .
```

`cs104_server_special` connects outward, so start `cs104_server` (or a
generic IEC 104 test master listening on `:2404`) first, then run it:

```sh
cd cs104_server_special && go run .
```

`cs104_server_redundancy` speaks the same wire protocol as `cs104_server`;
run it and connect two masters (e.g. two instances of `cs104_client`, or
any IEC 104 test client) to see the second one supersede the first.

`cs104_powerstation_server` and `cs104_powerstation_client` are a matched
pair:

```sh
# terminal 1
cd cs104_powerstation_server && go run .

# terminal 2
cd cs104_powerstation_client && go run .
```

The client interrogates the station, sets an output setpoint of 75%, starts
the station, waits 30s watching it ramp up and the reservoir drain, then
stops it again. Watch either log to see the full command/response exchange,
or the server's spontaneous measurement reports in between.

## Runtime metrics as process data

`cs104_server`, `cs104_server_special`, and `cs104_server_redundancy` all
run a `reportRuntimeMetrics` goroutine publishing a few Go runtime
statistics every 5s as spontaneous process data, as a small example of
using continuous and boolean information types together:

| IOA | Type | Value |
|---|---|---|
| 10 | 36 (`M_ME_TF_1`, short floating point, CP56Time2a) | Heap memory in use, in MiB |
| 11 | 36 (`M_ME_TF_1`, short floating point, CP56Time2a) | Number of goroutines |
| 12 | 30 (`M_SP_TB_1`, single point information, CP56Time2a) | Whether a GC cycle ran since the last report |
