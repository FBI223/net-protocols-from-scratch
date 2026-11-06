# GRE and VXLAN

**Specification:** RFC 2784, 7348  
**Verify with:** ip, tcpdump

## What this is

Encapsulation protocols behind VPNs, SD-WAN and cloud overlay networks.

## What to build

- GRE and VXLAN header encoding and decoding
- Encapsulation and decapsulation over a TUN device
- Two hosts in separate network namespaces
- Test with ip and tcpdump

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
