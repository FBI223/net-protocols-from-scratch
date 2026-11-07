# NETCONF

**Specification:** RFC 6241, 6242, 7950 (YANG)  
**Verify with:** netopeer2, ncclient, Wireshark

## What this is

The standard way to configure routers and switches from software: XML RPCs over SSH. Network automation at Cisco,
Ericsson and Nokia is built on it. SSH itself comes from `golang.org/x/crypto/ssh`.

## What to build

- Chunked and end-of-message framing
- Hello and capability exchange
- RPCs: get, get-config, edit-config, lock, unlock, close-session
- A server with a small in-memory datastore
- A client that talks to netopeer2
- Fuzz target for the framing decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
