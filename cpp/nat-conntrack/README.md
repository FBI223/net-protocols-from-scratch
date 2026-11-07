# NAT and connection tracking

**Specification:** RFC 3022, 4787, 5382  
**Verify with:** `conntrack -L`, `iptables` MASQUERADE in network namespaces, Wireshark

## What this is

How a home router or a carrier-grade NAT shares one public address: a table of flows, address and port
translation, and checksum updates on every rewritten packet.

## What to build

- Flow key and hashing (`flow_key`)
- Connection tracking table with TCP state and timeouts (`conntrack`)
- Source NAT with port allocation (`nat`, `port_pool`)
- Incremental checksum update (RFC 1624) for IP, TCP and UDP (`checksum`)
- A simulator that translates a stream of packets (`nat_sim`)

## Build and test

```bash
cd cpp
cmake -S . -B build && cmake --build build
ctest --test-dir build -R nat-conntrack --output-on-failure
```

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
