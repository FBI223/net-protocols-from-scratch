# sFlow v5 / IPFIX

**Specification:** RFC 7011  
**Verify with:** hsflowd, softflowd

## What this is

Flow telemetry from network devices.

## What to build

- UDP collector for sFlow datagrams and IPFIX messages
- Parsing of samples and templates
- Aggregation by source, destination and protocol
- Export to a time-series or columnar store such as ClickHouse
- Test with hsflowd and softflowd

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
