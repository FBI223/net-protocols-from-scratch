# ASN.1 PER

**Specification:** ITU-T X.691  
**Verify with:** Wireshark, `asn1tools` (Python) and sample messages from 3GPP specifications

## What this is

The compact binary encoding used by 4G and 5G signalling (S1AP, NGAP, RRC). Decoding it needs bit-level reading
and constraint handling, which is the hard part.

## What to build

- Bit reader over a byte buffer (`bit_reader`)
- Constraint handling: ranges, sizes and extension markers (`constraints`)
- Types: boolean, integer, enumerated, bit string, octet string, sequence, choice, sequence-of (`types`)
- Decoder for aligned and unaligned PER (`decoder`)
- A program that decodes hex input and prints the structure (`per_decode`)

## Build and test

```bash
cd cpp
cmake -S . -B build && cmake --build build
ctest --test-dir build -R asn1-per --output-on-failure
```

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
