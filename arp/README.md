# ARP

**Specification:** RFC 826  
**Verify with:** arping, tcpdump, Wireshark

## What this is

The protocol that maps an IPv4 address to a MAC address on a local network. Every packet on Ethernet depends on it.

## What to build

- Request and reply encoding and decoding for Ethernet and IPv4
- A cache with entry timeouts
- A responder that answers for its own address, and gratuitous ARP
- An `arping` tool over a raw socket (AF_PACKET, Linux)
- Fuzz target for the decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `../testdata`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
