# Reliable transport over UDP

**Specification:** inspired by RFC 9293 and 9000  
**Verify with:** tc netem, iperf3

## What this is

A small reliable protocol on top of UDP. Shows what TCP and QUIC do inside.

## What to build

- Sequence numbers and acknowledgements
- Sliding window
- Retransmission with timers
- Congestion control (a simple version)
- Tests under simulated loss and delay with tc netem
- Throughput measured with iperf3

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
