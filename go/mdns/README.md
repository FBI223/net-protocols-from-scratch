# mDNS and DNS-SD

**Specification:** RFC 6762, 6763  
**Verify with:** avahi-browse, dns-sd, dig -p 5353 @224.0.0.251, Wireshark

## What this is

DNS without a server: devices on a local network answer queries sent to a multicast address. This is how a
controller finds UniFi devices, printers and Apple gear.

## What to build

- Message encoding and decoding, reusing the codec from `go/dns`
- Multicast socket on 224.0.0.251:5353, with the QU bit and known-answer suppression
- A responder that announces and defends its records
- A browser for DNS-SD service types and instances
- Record cache with TTL
- Fuzz target for the decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
