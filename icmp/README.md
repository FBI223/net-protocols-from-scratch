# ICMP

**Specification:** RFC 792, 4443  
**Verify with:** ping, traceroute, tcpdump, Wireshark

## What this is

The control protocol of IP: echo (ping), unreachable and time-exceeded messages. Your own `ping` and `traceroute`
show how the network reports errors back to the sender.

## What to build

- ICMPv4 and ICMPv6 message encoding and decoding, with checksum
- `ping`: echo requests with identifier, sequence and round-trip time
- `traceroute`: TTL stepping and reading time-exceeded replies
- Raw sockets (unprivileged ICMP sockets where the OS allows them)
- Fuzz target for the decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `../testdata`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
