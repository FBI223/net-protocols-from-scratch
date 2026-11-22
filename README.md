# net-protocols-from-scratch

Network protocols implemented from scratch in **Go** and **Rust**, working directly from the specifications (RFCs, IEEE, OASIS).

> **Status: work in progress.** So far only TFTP has real code: a client, a server and two command-line programs, in octet and netascii modes, with unit, fuzz and packet-loss tests, checked against `tftp-hpa`. The TFTP option extensions and the other directories are skeletons that fix the package layout and the planned scope; they are implemented one at a time. A directory is marked *Implemented* only when it has real code, tests against vectors from the specification, and a documented way to run it.

| Status | Meaning |
|---|---|
| **In progress** | real code and tests exist, parts are still missing |
| **Planned** | skeleton only (package or crate layout, no protocol logic yet) |
| **Implemented** | complete for the documented scope, tested, runnable |

The goal is to understand how protocols behave on the wire: parsing and building packets, state machines, timers, and interoperability with real tools (`dig`, Wireshark, `h2spec`, ...).

---

## Projects

Grouped by network layer. Every directory has its own `README.md`.

| Directory | Lang | Protocol / specification | Status | Planned scope | Executables |
|---|---|---|---|---|---|
| [`tftp/`](tftp/) | Go | TFTP, RFC 1350 with options (RFC 2347, 2348, 2349) | **In progress** | done: RFC 1350 in octet and netascii modes: packet codec, lock-step transfer engine, client (Get/Put), server (ListenAndServe), unit, fuzz and packet-loss tests, interoperability with `tftp-hpa` (see [`tftp/README.md`](tftp/README.md)); next: options (RFC 2347, 2348, 2349); RFC texts in `tftp/rfcs/` | `tftp`, `tftpd` |
| [`arp/`](arp/) | Go | ARP, RFC 826 | Planned | packet codec, cache, responder | `arping` |
| [`icmp/`](icmp/) | Go | ICMP, RFC 792 | Planned | packet codec, raw sockets, ping, traceroute | `ping`, `traceroute` |
| [`todo_later/packet-parser`](todo_later/packet-parser/) | Rust | Ethernet, IPv4, ARP, ICMP, UDP, TCP, pcap | Planned | layered parser, checksums, pcap reader | `arpwatch`, `ping`, `traceroute` |
| [`todo_later/reliable-udp`](todo_later/reliable-udp/) | Rust | reliable transport over UDP (design references: RFC 768, 6298, 5681) | Planned | sequence numbers, sliding window, RTT estimation, congestion control | `rudp-send`, `rudp-recv` |
| [`dns/`](dns/) | Go + Rust | DNS, RFC 1034 / 1035 (EDNS(0), RFC 6891) | Planned | message and name codec, cache, resolver, zones, server; Rust version in [`dns/rust/`](dns/rust/) | `dnsd` (Go), `dns-server` (Rust) |
| [`todo_later/mdns`](todo_later/mdns/) | Go | mDNS, RFC 6762 | Planned | codec, cache, responder, browser | `mdnsd` |
| [`ntp/`](ntp/) | Go | NTP, RFC 5905 (SNTP, RFC 4330) | Planned | packet codec, client, server | `ntpd`, `sntp` |
| [`http1/`](http1/) | Go | HTTP/1.1, RFC 9112 | Planned | request and response parsing, chunked encoding, server | `httpd` |
| [`todo_later/http2`](todo_later/http2/) | Rust | HTTP/2, RFC 9113; HPACK, RFC 7541 | Planned | frames, HPACK with Huffman, streams, flow control | `h2c-server` |
| [`websocket/`](websocket/) | Go | WebSocket, RFC 6455 | Planned | handshake, frame codec, connection, server | `wsecho` |
| [`todo_later/reverse-proxy`](todo_later/reverse-proxy/) | Rust | HTTP reverse proxy and load balancer | Planned | configuration, router, balancer, health checks | `reverse-proxy` |
| [`todo_later/mqtt`](todo_later/mqtt/) | Go | MQTT (OASIS) | Planned | packet codec, broker, client, sessions | `mqttd` |
| [`todo_later/resp`](todo_later/resp/) | Go | RESP (Redis wire protocol) | Planned | parser, writer, commands, in-memory store | `respd` |
| [`todo_later/tls-clienthello`](todo_later/tls-clienthello/) | Rust | TLS records and ClientHello (RFC 8446), JA3 | Planned | record, handshake and extension parsing, JA3 | library only |

## Why two languages

- **Go** for network daemons and tools: goroutines and the standard library keep servers, clients and timers short and readable.
- **Rust** for parsers and state machines where memory safety and strict types on untrusted bytes matter.
- **DNS is planned in both** so the two implementations can be compared and tested against each other and against `dig`.

## Repository layout

```
net-protocols-from-scratch/
├── go.mod, go.sum          one Go module for every Go protocol
├── Cargo.toml, Cargo.lock  one Rust workspace for every Rust crate
├── <protocol>/             one directory per protocol (see the table above)
├── docs/                   roadmaps and planning documents
├── testdata/               shared test data
├── check-layout.sh         static checks of the repository structure
├── check-build.sh          build, vet, test and dependency checks
└── LICENSE
```

A **Go** protocol directory: `*.go` (library package), `doc.go`, `*_test.go`, `cmd/<tool>/` (executables), `testdata/`, `README.md`.

A **Rust** protocol directory (a crate in the workspace): `Cargo.toml`, `src/lib.rs`, `src/bin/` (executables), `tests/`, `testdata/`, `README.md`.

`dns/` is the one mixed directory: the Go package is in `dns/` itself and the Rust crate is in `dns/rust/`.

## Build and test

Go (module `github.com/FBI223/net-protocols-from-scratch`, standard library only):

```bash
go build ./...
go vet ./...
go test ./... -count=1
go test ./tftp/... -count=1        # a single protocol
```

Rust (workspace):

```bash
cargo build --workspace
cargo test --workspace
cargo test -p http2                # a single crate
```

Tests that open raw sockets or need external tools are skipped when they are not available. CI (GitHub Actions) runs `go vet`, `gofmt`, `go test -race`, `cargo fmt`, `cargo clippy`, `cargo test` and the repository checks.

## Repository checks

Two scripts validate the repository itself. They do not modify tracked files.

```bash
./check-layout.sh                  # layout, READMEs, tests, hygiene, manifests
./check-layout.sh --strict         # warnings also fail
./check-build.sh --skip-tests      # go mod tidy/verify, build, vet, gofmt; cargo fmt/check
./check-build.sh --only go,rust    # selected toolchains only
```

## Documentation

[`docs/`](docs/) contains the network backend roadmaps and the protocol plan this repository follows.

## Author

Marcin Sztukowski, [marcin.sztukowski.com](https://marcin.sztukowski.com), [github.com/FBI223](https://github.com/FBI223)

## License

See [LICENSE](LICENSE).
