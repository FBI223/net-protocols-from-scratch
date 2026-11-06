# BGP-4

**Specification:** RFC 4271, 7854  
**Verify with:** FRR (vtysh), Wireshark

## What this is

The routing protocol of the internet. This implementation speaks BGP with a real router.

## What to build

- Session state machine: Idle, Connect, Active, OpenSent, OpenConfirm, Established
- OPEN, KEEPALIVE, UPDATE and NOTIFICATION messages
- Path attributes and a simple route table
- Hold and keepalive timers
- Peering with FRR in containerlab, checked in vtysh

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
