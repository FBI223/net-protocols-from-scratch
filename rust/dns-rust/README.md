# DNS (port from Go)

**Specification:** RFC 1034, 1035, 6891  
**Verify with:** dig, kdig, Wireshark

## What this is

The DNS implementation from go/dns, rewritten in Rust to learn the language on a protocol already understood.

## What to build

- Message parser and builder with name compression
- Authoritative server over UDP and TCP
- TTL cache
- EDNS(0)
- Fuzz target and round-trip property tests
- Behaviour compared with the Go version

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
