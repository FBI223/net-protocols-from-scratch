# WebSocket

**Specification:** RFC 6455  
**Verify with:** websocat, a browser

## What this is

The upgrade handshake and framing layer behind live dashboards and notifications.

## What to build

- HTTP Upgrade handshake with Sec-WebSocket-Accept
- Frame parser and writer: opcodes, payload lengths, masking, fragmentation
- Ping/pong and the close handshake
- Echo server tested from a browser and from websocat

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
