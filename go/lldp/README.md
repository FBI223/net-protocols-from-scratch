# LLDP

**Specification:** IEEE 802.1AB  
**Verify with:** lldpd (lldpcli), tcpdump, Wireshark

## What this is

Link Layer Discovery Protocol: how switches, routers and access points announce who they are to the device on the
other end of a cable. This is how a controller (UniFi, Cisco) builds its topology map.

## What to build

- TLV parser and builder: Chassis ID, Port ID, TTL, System Name, System Description, Management Address
- Frames sent to 01:80:c2:00:00:0e with EtherType 0x88cc, over a raw socket (AF_PACKET, Linux)
- Neighbour table with TTL expiry
- A sender that announces this host and a listener that prints its neighbours
- Test against lldpd in two network namespaces
- Fuzz target for the TLV decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
