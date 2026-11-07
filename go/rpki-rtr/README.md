# RPKI-to-Router

**Specification:** RFC 8210, 6811  
**Verify with:** Routinator, StayRTR, Wireshark

## What this is

How a router learns which origin AS is allowed to announce which prefix. It protects the internet from route
hijacks, and it is what Cloudflare and other big networks use for origin validation.

## What to build

- PDU encoding and decoding: Serial Notify, Reset Query, Cache Response, IPv4 and IPv6 Prefix, End of Data
- A client that syncs a validated ROA table from Routinator
- Origin validation of a route: valid, invalid, not-found (RFC 6811)
- Hook it into `go/bgp` to mark received routes
- Fuzz target for the PDU decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
