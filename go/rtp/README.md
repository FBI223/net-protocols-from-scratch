# RTP / RTCP

**Specification:** RFC 3550  
**Verify with:** SIPp, ffmpeg, Wireshark

## What this is

The protocol that carries voice and video in real time.

## What to build

- RTP packet parser: sequence numbers, timestamps, SSRC
- Jitter buffer
- Loss and jitter statistics
- RTCP sender and receiver reports
- A simple RTP relay
- Test with SIPp media and ffmpeg

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
