# Examples

Each directory is a standalone, runnable `package main`. Run with
`go run .` from inside the directory.

| Example | Type | Demonstrates |
|---|---|---|
| [`cs104_client`](./cs104_client) | `cs104.Client` | The IEC 60870-5-104 master: dials out to a slave, activates data transfer (STARTDT), and issues a general interrogation. |
| [`cs104_server`](./cs104_server) | `cs104.Server` | The IEC 60870-5-104 slave/RTU: listens for master connections and responds to a general interrogation with one point of data. |
| [`cs104_server_special`](./cs104_server_special) | `cs104.ServerSpecial` | A slave/RTU that dials *out* to the master instead of listening, e.g. because it sits behind NAT/firewall. Same protocol behavior as `cs104_server`, opposite TCP direction. |
| [`cs104_server_redundancy`](./cs104_server_redundancy) | `cs104.Server` | A slave/RTU configured for redundant masters: only one connection per redundancy group stays active at a time; connecting a second master supersedes and closes the first. |

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
