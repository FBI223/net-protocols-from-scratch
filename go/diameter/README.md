# Diameter

**Specification:** RFC 6733  
**Verify with:** freeDiameter, Wireshark

## What this is

The AAA protocol of mobile networks, the successor of RADIUS. It carries authentication, charging and policy
between core network elements.

## What to build

- Message header and AVP encoding and decoding, including grouped AVPs and padding
- A dictionary of base-protocol AVPs
- Peer state machine: CER/CEA, DWR/DWA, DPR/DPA
- A peer that connects to freeDiameter over TCP
- Fuzz target for the message decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
