# STUN

**Specification:** RFC 8489  
**Verify with:** Wireshark, a public STUN server

## What this is

The protocol a device uses to discover its public address behind NAT. A first step toward WebRTC and VoIP.

## What to build

- Binding request and response
- XOR-MAPPED-ADDRESS attribute
- Transaction IDs and the magic cookie
- Client and a small server
- Basic NAT type detection

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
