# DNS

**Specification:** RFC 1034, 1035, 6891  
**Verify with:** dig, kdig, Wireshark

## What this is

Message parser and builder with name compression, then a stub resolver and an authoritative server.

## What to build

- Header, question and resource-record parser and builder
- Name compression, with protection against pointer loops
- Stub resolver over UDP with TCP fallback
- Authoritative server for zones loaded from a file, with a TTL cache
- EDNS(0) OPT record
- Fuzz target for the parser

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
