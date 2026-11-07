# HTTP/2 (Rust)

**Specification:** RFC 9113, 7541  
**Verify with:** `curl --http2-prior-knowledge`, h2spec, nghttp, Wireshark

## What this is

A Rust port of `go/http2`: HTTP/2 over cleartext (h2c) with binary framing, HPACK header compression and
multiplexed streams. Written after the Go version works and checked against the same HPACK test vectors
(RFC 7541, appendix C). No TLS is written here; encryption would come from `rustls` once ALPN is added.

## What to build

- Frame parser and writer (`frame`): DATA, HEADERS, SETTINGS, WINDOW_UPDATE, PING, RST_STREAM, GOAWAY, CONTINUATION
- Settings exchange (`settings`)
- HPACK: static and dynamic table (`hpack`) and Huffman coding (`huffman`)
- Stream state machine (`stream`) with per-stream and per-connection flow control (`flow_control`)
- Connection handling (`connection`) and the `h2c-server` program
- Pass the h2spec conformance suite (`tests/h2spec.rs`)
- Compare throughput and memory with the Go version

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
