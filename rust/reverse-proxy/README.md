# Reverse proxy (Rust)

**Specification:** RFC 9110, 9112  
**Verify with:** `curl`, `wrk`, several backend servers on different ports, Wireshark

## What this is

An HTTP reverse proxy: accepts client connections, picks an upstream server and forwards the request. It is the
layer 7 counterpart of `go/l4-loadbalancer`; Cloudflare's Pingora is a production proxy written in Rust.
The async runtime comes from `tokio`; routing, balancing and forwarding are what you write.

## What to build

- Configuration file with listeners, routes and upstream pools (`config`)
- Async listener with timeouts (`listener`)
- Routing by host and path prefix (`router`)
- Balancing: round robin and least connections (`balancer`)
- Upstream connections with reuse (`upstream`)
- Active health checks that remove dead upstreams (`health`)
- Forwarding (`proxy`): hop-by-hop headers (RFC 9110, section 7.6.1), `X-Forwarded-For`, streamed bodies
- End-to-end test with real backends (`tests/end_to_end.rs`)
- Load test with `wrk`

## Design notes

_Module layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/pcap/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
