# RESP (Redis protocol)

**Specification:** RESP2 and RESP3 (Redis protocol specification)  
**Verify with:** redis-cli, redis-benchmark

## What this is

A simple text-and-length protocol that shows how to build a fast server: parsing a stream, many connections and
shared state.

## What to build

- Parser and writer for simple strings, errors, integers, bulk strings and arrays
- A server with PING, ECHO, GET, SET (with expiry), DEL, EXISTS and INCR
- Pipelining and many concurrent connections
- Measure with redis-benchmark
- Fuzz target for the parser

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `../testdata`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
