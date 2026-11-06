# OSPFv2

**Specification:** RFC 2328  
**Verify with:** FRR, Wireshark

## What this is

A link-state routing protocol. An advanced item, useful for routing roles.

## What to build

- Hello packets and neighbor state machine
- Adjacency formation
- Type-1 router LSAs and the link-state database
- Shortest-path-first (Dijkstra) route calculation
- Test against FRR

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
