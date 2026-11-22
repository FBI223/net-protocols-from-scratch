# TLS 1.3 ClientHello (parser only)

**Specification:** RFC 8446  
**Verify with:** openssl s_client, curl -v

## What this is

Parses the first message of a TLS handshake. No cryptography is written here.

## What to build

- Record layer and handshake header
- Extensions: SNI, ALPN, supported versions, supported groups
- JA3 fingerprint
- Tests on captured handshakes from different clients
- Fuzz target

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `../testdata`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
