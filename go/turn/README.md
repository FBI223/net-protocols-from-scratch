# TURN

**Specification:** RFC 8656, 8489  
**Verify with:** coturn (turnutils_uclient), Wireshark, a WebRTC sample page

## What this is

A relay for clients behind NAT that cannot reach each other directly. It completes `go/stun` for WebRTC and VoIP.

## What to build

- Message and attribute encoding, reusing the codec from `go/stun`
- Allocate, Refresh, CreatePermission, ChannelBind, Send and Data
- Long-term credential authentication
- A relay server with UDP allocations and channel data
- Fuzz target for the message decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
