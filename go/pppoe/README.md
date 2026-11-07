# PPPoE

**Specification:** RFC 2516, 1661, 1332, 1994  
**Verify with:** pppd with the rp-pppoe plugin in a network namespace, Wireshark

## What this is

How a home customer connects to an ISP over Ethernet. The access concentrator authenticates the subscriber and
hands out an address.

## What to build

- Discovery stage: PADI, PADO, PADR, PADS, PADT
- Session stage and PPP framing
- LCP negotiation, PAP and CHAP authentication, IPCP address assignment
- Authentication against `go/radius`
- Fuzz target for the discovery and PPP decoders

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
