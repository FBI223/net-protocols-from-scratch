# PFCP

**Specification:** 3GPP TS 29.244  
**Verify with:** Open5GS (SMF and UPF), Wireshark

## What this is

The control protocol between the session management function and the user plane function in 5G (and 4G CUPS).
It installs the rules that decide how each packet is forwarded.

## What to build

- Header encoding and decoding, with and without a SEID (`header`)
- Information elements, including grouped ones (`ie`)
- Messages: heartbeat, association setup, session establishment, modification and deletion (`message`)
- Session table (`session`)
- A server that answers an SMF (`pfcp_server`)

## Build and test

```bash
cd cpp
cmake -S . -B build && cmake --build build
ctest --test-dir build -R pfcp --output-on-failure
```

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
