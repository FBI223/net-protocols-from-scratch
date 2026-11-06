# DHCP

**Specification:** RFC 2131, 2132  
**Verify with:** dhclient in a network namespace, Wireshark

## What this is

A server that hands out addresses and options to clients on a network.

## What to build

- DISCOVER, OFFER, REQUEST, ACK and NAK handling
- Address pool, lease storage and expiry
- Common options: subnet mask, router, DNS servers, lease time
- A small REST API to list and manage leases
- Test with dhclient inside a network namespace

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
