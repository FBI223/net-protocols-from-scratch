# HTTP/2

**Specification:** RFC 9113, 7541  
**Verify with:** curl --http2-prior-knowledge, h2spec, nghttp, Wireshark

## What this is

HTTP/2 over cleartext (h2c): binary framing, header compression and multiplexed streams. No TLS is written here;
encryption comes from `crypto/tls` once ALPN is added.

## What to build

- Frame parser and writer: DATA, HEADERS, SETTINGS, WINDOW_UPDATE, PING, RST_STREAM, GOAWAY, CONTINUATION
- HPACK: static table, dynamic table and Huffman coding
- Stream state machine, with per-stream and per-connection flow control
- A small server that answers a few routes
- Pass the h2spec conformance suite
- Fuzz target for the frame and HPACK decoders

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
