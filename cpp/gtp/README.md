# GTP

**Specification:** 3GPP TS 29.281 (GTP-U), 3GPP TS 29.274 (GTP-C)  
**Verify with:** Open5GS, Wireshark

## What this is

The tunnelling protocol of mobile networks: GTP-U carries user traffic between a base station and the core
network, GTP-C sets the tunnels up. Every packet of a mobile data session passes through it.

## What to build

- GTP-U header encoding and decoding, with optional fields and extension headers (`header`, `gtpu`)
- GTP-C messages and information elements (`gtpc`, `ie`)
- Tunnel table keyed by TEID (`tunnel`)
- A relay that decapsulates and forwards user packets (`gtpu_relay`)

## Build and test

```bash
cd cpp
cmake -S . -B build && cmake --build build
ctest --test-dir build -R gtp --output-on-failure
```

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
