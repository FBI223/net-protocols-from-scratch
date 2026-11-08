# TCP and HTTP/1.1

**Specification:** RFC 9293, 9112  
**Verify with:** curl, wrk, k6, Wireshark

## What this is

A TCP server with message framing and timeouts, then an HTTP/1.1 server written without net/http.

## What to build

- TCP listener with read/write deadlines and graceful shutdown
- Request-line and header parser with size limits
- Keep-alive, Content-Length and chunked transfer encoding
- A small router with a few routes
- Load test with wrk or k6

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `../testdata`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
