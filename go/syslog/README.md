# Syslog

**Specification:** RFC 5424  
**Verify with:** logger, rsyslog

## What this is

The standard way devices ship logs.

## What to build

- UDP and TCP receiver
- Parser for the RFC 5424 format (priority, timestamp, hostname, structured data)
- A simple index and queries by host, severity and time
- Test with logger and rsyslog

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
