# L4 load balancer

**Specification:** none (a design project); optionally the PROXY protocol v2  
**Verify with:** curl, wrk, iperf3, backends in network namespaces

## What this is

A TCP and UDP load balancer that forwards connections without reading the application protocol. The idea behind
cloud load balancers and the first tier of most CDNs.

## What to build

- TCP listener that proxies bytes in both directions, with timeouts and half-close handling
- Backend selection: round robin, least connections, consistent hashing
- Active health checks and removal of dead backends
- Graceful connection draining on reload
- UDP forwarding with a session table
- Load test with `wrk` and `iperf3`

## Design notes

_Package layout, key data structures and decisions._

## How it was tested

_Unit tests, captured packets in `testdata/`, fuzzing, and the real tools used._

## Benchmarks

_The exact command and the results._

## Limitations

_What is not implemented._
