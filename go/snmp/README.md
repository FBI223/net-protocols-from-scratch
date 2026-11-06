# SNMP v2c

**Specification:** RFC 3416  
**Verify with:** snmpwalk, snmpget

## What this is

How network devices expose counters and status for monitoring.

## What to build

- BER encoding and decoding
- Agent with a few OIDs (system group, interface counters)
- Manager with GET, GETNEXT and WALK
- Error handling for unknown OIDs
- Test with snmpwalk and snmpget

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
