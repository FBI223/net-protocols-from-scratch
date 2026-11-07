# Longest-prefix-match FIB

**Specification:** none (data structures); background in RFC 4632 (CIDR)  
**Verify with:** comparison against a naive linear scan, routing table dumps (RIPE RIS), `ip route get`

## What this is

The lookup a router performs for every packet: find the most specific prefix that matches a destination
address. Two structures are compared, a binary trie and a DIR-24-8 table.

## What to build

- Prefix type with parsing and masking (`prefix`)
- Binary trie with insert, delete and lookup (`trie`)
- DIR-24-8 table for IPv4 (`dir24_8`)
- FIB wrapper with route updates (`fib`)
- Benchmark on a full internet routing table (`fib_bench`)
- Check every lookup against a linear scan

## Build and test

```bash
cd cpp
cmake -S . -B build && cmake --build build
ctest --test-dir build -R lpm-fib --output-on-failure
```

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
