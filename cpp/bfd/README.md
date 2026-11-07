# BFD

**Specification:** RFC 5880, 5881  
**Verify with:** FRR (`bfdd`), BIRD, Wireshark

## What this is

Bidirectional Forwarding Detection: hello packets that detect a dead link in milliseconds. A port of `go/bfd`
to C++, where timer precision and low overhead matter most.

## What to build

- Control packet encoding and decoding, 24-byte mandatory section (`packet`)
- Session state machine: AdminDown, Down, Init, Up (`session`)
- Timer negotiation, detection time and jitter (`timers`)
- Single-hop sessions over UDP port 3784 (`bfd_daemon`)
- Peer with FRR and compare detection time with the Go version

## Build and test

```bash
cd cpp
cmake -S . -B build && cmake --build build
ctest --test-dir build -R bfd --output-on-failure
```

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
