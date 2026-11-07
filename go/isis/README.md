# IS-IS

**Specification:** ISO 10589, RFC 1195  
**Verify with:** FRR (isisd) in containerlab, Wireshark

## What this is

The second great link-state routing protocol, used inside most ISP backbones. It runs directly over Ethernet, not
over IP.

## What to build

- PDU encoding and decoding: IIH, LSP, CSNP, PSNP, with TLVs
- Adjacency state machine
- LSP database with flooding and aging
- Shortest-path calculation, reusing the Dijkstra code from `go/ospf`
- Fuzz target for the PDU decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
