# NTP

**Specification:** RFC 5905  
**Verify with:** chronyc, ntpdate -q

## What this is

An SNTP client and a simple server. Shows how clocks are compared over a network.

## What to build

- Client: send a request, read the reply, compute offset and delay
- Packet format, modes and stratum
- A simple server answering from the local clock
- Timeouts and retries

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `../testdata`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
