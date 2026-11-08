# Ethernet, ARP, IPv4, ICMP, UDP, TCP

**Specification:** RFC 826, 791, 792, 768, 9293  
**Verify with:** Wireshark, tcpdump

## What this is

A parser that works on raw bytes. This is the core of packet processing.

## What to build

- Read pcap files
- Zero-copy parsers for Ethernet, ARP, IPv4, ICMP, UDP and TCP headers
- Checksum verification
- Your own ping and traceroute
- ARP spoofing detection
- Fuzz target and property tests

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `../testdata`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
