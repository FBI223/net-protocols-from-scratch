# TFTP

**Specification:** RFC 1350  
**Verify with:** tftp-hpa, Wireshark

## What this is

A tiny file-transfer protocol over UDP. Small enough to finish quickly, and it teaches UDP sockets, request/ACK exchanges, retransmission and timeouts.

## What to build

- Client and server for RRQ and WRQ
- 512-byte DATA blocks acknowledged one by one
- Retransmission and timeouts
- ERROR packets and the end-of-transfer rule (a block shorter than 512 bytes)
- Tests on captured packets

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
