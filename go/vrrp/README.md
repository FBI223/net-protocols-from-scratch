# VRRP

**Specification:** RFC 5798  
**Verify with:** keepalived, Wireshark

## What this is

Two or more routers share one virtual gateway address, and a backup takes over within seconds when the master
dies.

## What to build

- VRRPv3 advertisement encoding and decoding, with checksum
- Master and backup state machine with priorities and preemption
- Timers: advertisement interval and master-down interval
- IP protocol 112 over multicast 224.0.0.18
- Interoperate with keepalived in network namespaces
- Fuzz target for the decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
