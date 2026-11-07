# gRPC

**Specification:** gRPC over HTTP/2 (grpc/grpc `PROTOCOL-HTTP2.md`)  
**Verify with:** grpcurl, a grpc-go client, Wireshark

## What this is

The RPC framework behind most cloud services: length-prefixed messages over HTTP/2 streams with status in the
trailers. Protobuf encoding comes from `google.golang.org/protobuf` (Go has no protobuf in the standard library); the transport is what you build.

## What to build

- Message framing (1-byte compressed flag and 4-byte length)
- Status codes, metadata and trailers
- Unary calls, then server, client and bidirectional streaming, on top of `go/http2`
- Deadlines and cancellation
- Fuzz target for the framing decoder

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
