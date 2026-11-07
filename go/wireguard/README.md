# WireGuard

**Specification:** WireGuard whitepaper, Noise Protocol Framework  
**Verify with:** wg, wg-quick in network namespaces, Wireshark

## What this is

A modern VPN: a small Noise-based handshake, then encrypted UDP packets carrying IP traffic through a TUN device.
Cryptographic primitives come from `golang.org/x/crypto`; the protocol and the tunnel are what you write.

## What to build

- Noise IKpsk2 handshake: initiation, response, cookie reply
- Transport packets with counters and replay protection
- Peers, allowed IPs, rekeying and keepalives
- A TUN device and a packet loop
- Interoperate with the real `wg` in two network namespaces
- Fuzz target for the message decoders

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
