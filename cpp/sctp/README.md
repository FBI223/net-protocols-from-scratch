# SCTP

**Specification:** RFC 9260  
**Verify with:** the Linux kernel SCTP stack (`ncat --sctp`, `sctp_darn`), Wireshark

## What this is

A message-oriented transport with multi-streaming and multi-homing. It carries signalling in mobile networks
(S1AP, NGAP, Diameter over SCTP).

## What to build

- Packet and chunk encoding and decoding: INIT, INIT-ACK, COOKIE-ECHO, DATA, SACK, HEARTBEAT (`packet`, `chunk`)
- CRC32c checksum (`checksum`)
- Association state machine and the four-way handshake with cookie (`association`)
- Independent streams with ordered and unordered delivery (`stream`)
- An echo program (`sctp_echo`)

## Build and test

```bash
cd cpp
cmake -S . -B build && cmake --build build
ctest --test-dir build -R sctp --output-on-failure
```

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
