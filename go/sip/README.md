# SIP + SDP

**Specification:** RFC 3261, 8866  
**Verify with:** SIPp, Linphone, Wireshark

## What this is

The signalling protocol of VoIP: registering users and setting up calls.

## What to build

- SIP message parser and builder
- REGISTER with digest authentication
- INVITE, 200 OK, ACK and BYE call flow
- SDP offer and answer
- User location and call routing
- Test scenarios in SIPp

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
