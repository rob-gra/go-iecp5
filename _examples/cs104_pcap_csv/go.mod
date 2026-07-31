// A separate module on purpose: go-iecp5 itself has no dependencies, and a
// pcap reader is not a reason to give it one. Nothing here is imported by
// the library.
module github.com/thinkgos/go-iecp5/_examples/cs104_pcap_csv

go 1.22.0

toolchain go1.24.7

require (
	github.com/gopacket/gopacket v1.3.1
	github.com/thinkgos/go-iecp5 v0.0.0
)

require (
	golang.org/x/net v0.28.0 // indirect
	golang.org/x/sys v0.24.0 // indirect
)

replace github.com/thinkgos/go-iecp5 => ../..
