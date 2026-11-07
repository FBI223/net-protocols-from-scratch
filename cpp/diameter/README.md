# Diameter

**Specification:** RFC 6733  
**Verify with:** freeDiameter, Wireshark

## What this is

The AAA protocol of mobile networks, successor of RADIUS. It carries authentication, charging and policy between
core network elements. A Go version lives in `go/diameter`; build one first and port only if time allows.

## What to build

- Message header and AVP encoding and decoding, with grouped AVPs and padding (`header`, `avp`, `message`)
- Dictionary of base-protocol AVPs (`dictionary`)
- Peer state machine: CER/CEA, DWR/DWA, DPR/DPA (`peer`)
- A peer that connects to freeDiameter over TCP (`diameter_peer`)

## Build and test

```bash
cd cpp
cmake -S . -B build && cmake --build build
ctest --test-dir build -R diameter --output-on-failure
```

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
