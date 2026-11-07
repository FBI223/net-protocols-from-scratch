# BFD

**Specification:** RFC 5880, 5881  
**Verify with:** FRR (bfdd), BIRD, Wireshark

## What this is

Bidirectional Forwarding Detection: tiny hello packets that let routers notice a dead link in milliseconds instead
of seconds. BGP and OSPF use it as a fast failure detector.

## What to build

- Control packet encoding and decoding (24-byte mandatory section)
- Session state machine: AdminDown, Down, Init, Up
- Timer negotiation: transmit interval, detection time and jitter
- Single-hop sessions over UDP port 3784
- Peer with FRR in containerlab and tell the BGP implementation when a session goes down
- Fuzz target for the packet decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
