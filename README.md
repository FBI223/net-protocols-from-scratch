# net-protocols-from-scratch

Network protocols implemented from scratch in **Go** and **Rust**, working directly from the specifications (RFCs, IEEE, OASIS). Each protocol lives in its own directory with its own README, tests, captured traffic (`testdata/pcap/`) and, where it makes sense, runnable tools (`cmd/` in Go, `src/bin/` in Rust).

The goal is to understand how protocols behave on the wire: parsing and building packets, state machines, timers, and interoperability with real tools (`dig`, Wireshark, `h2spec`, ...).

| | Go | Rust |
|---|---|---|
| Projects | 11 | 6 (one of them, DNS, exists in both languages) |
| Build unit | one module (`go.mod`) | one Cargo workspace (`Cargo.toml`) |

---

## Projects

Grouped by network layer. Every directory has its own `README.md` with the details.

| Directory | Lang | Protocol / specification | What is inside | Executables |
|---|---|---|---|---|
| [`arp/`](arp/) | Go | ARP, RFC 826 | packet codec, ARP cache, responder | `arping` |
| [`lldp/`](lldp/) | Go | LLDP, IEEE 802.1AB | frame and TLV codec, agent | `lldpd` |
| [`icmp/`](icmp/) | Go | ICMP, RFC 792 | packet codec, raw socket layer, ping, traceroute | `ping`, `traceroute` |
| [`packet-parser/`](packet-parser/) | Rust | Ethernet, IPv4, ARP, ICMP, UDP, TCP, pcap | layered packet parser, checksums, pcap file reader | `arpwatch`, `ping`, `traceroute` |
| [`reliable-udp/`](reliable-udp/) | Rust | custom reliable transport over UDP (RFC 768; RFC 6298 / RFC 5681 as design references) | sequence numbers, sliding window, RTT estimation, congestion control, sender and receiver | `rudp-send`, `rudp-recv` |
| [`dns/`](dns/) | Go + Rust | DNS, RFC 1034 / 1035 (EDNS(0), RFC 6891, in the Rust version) | message and name codec, cache, resolver, zones, server; Rust version in [`dns/rust/`](dns/rust/) | `dnsd` (Go), `dns-server` (Rust) |
| [`mdns/`](mdns/) | Go | mDNS, RFC 6762 | message codec, cache, responder, browser | `mdnsd` |
| [`ntp/`](ntp/) | Go | NTP, RFC 5905 (SNTP, RFC 4330) | packet codec, client, server | `ntpd`, `sntp` |
| [`tftp/`](tftp/) | Go | TFTP, RFC 1350 with options (RFC 2347, 2348, 2349) | client, server, transfer logic; the RFC texts are kept in `tftp/rfcs/` | `tftp`, `tftpd` |
| [`http1/`](http1/) | Go | HTTP/1.1, RFC 9112 | request and response parsing, chunked transfer encoding, server | `httpd` |
| [`http2/`](http2/) | Rust | HTTP/2, RFC 9113; HPACK, RFC 7541 | frames, HPACK with Huffman coding, streams, flow control, settings, connection | `h2c-server` |
| [`websocket/`](websocket/) | Go | WebSocket, RFC 6455 | opening handshake, frame codec, connection, server | `wsecho` |
| [`reverse-proxy/`](reverse-proxy/) | Rust | HTTP reverse proxy and load balancer | configuration, router, balancer, health checks, upstreams | `reverse-proxy` |
| [`mqtt/`](mqtt/) | Go | MQTT (OASIS) | packet codec, broker, client, sessions, topic matching | `mqttd` |
| [`resp/`](resp/) | Go | RESP (Redis wire protocol) | parser, writer, commands, in-memory store, server | `respd` |
| [`tls-clienthello/`](tls-clienthello/) | Rust | TLS records and ClientHello (RFC 8446, RFC 5246); JA3 fingerprint | record, handshake and extension parsing, JA3 | library only |

## Why two languages

- **Go** for network daemons and tools: goroutines and the standard library make servers, clients and timers short and readable.
- **Rust** for parsers, state machines and anything where memory safety and strict types on untrusted bytes matter (packet parsing, HTTP/2 framing, TLS records).
- **DNS exists in both** on purpose, so the two implementations can be compared and tested against each other and against `dig`.

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

A **Go** protocol directory:

```
arp/
├── *.go              library package (codec, state, server/client)
├── doc.go            package documentation
├── *_test.go         unit tests
├── fuzz_test.go      fuzz targets (go test -fuzz)
├── cmd/<tool>/       executables
├── testdata/pcap/    captured traffic used by tests
└── README.md
```

A **Rust** protocol directory (a crate in the workspace):

```
http2/
├── Cargo.toml
├── src/
│   ├── lib.rs        library
│   └── bin/          executables
├── tests/            integration tests
├── testdata/pcap/    captured traffic used by tests
└── README.md
```

`dns/` is the one mixed directory: the Go package is in `dns/` itself and the Rust crate is in `dns/rust/`.

## Build and test

Go (module `github.com/FBI223/net-protocols-from-scratch`):

```bash
go build ./...
go vet ./...
go test ./... -count=1
go test ./arp/... -count=1                    # a single protocol
go run ./dns/cmd/dnsd                         # run a tool
```

Rust (workspace):

```bash
cargo build --workspace
cargo test --workspace
cargo test -p http2                           # a single crate
cargo run -p dns-rust --bin dns-server
```

Notes:

- Tools that open raw sockets (for example in `icmp/`, `arp/`, `lldp/`) need `sudo` or the `CAP_NET_RAW` capability.
- Some tests start local servers and use loopback ports; interop tests expect external tools such as `dig` to be installed.
- Fuzzing in Go: list the targets with `grep -n 'func Fuzz' <protocol>/fuzz_test.go`, then run `go test ./<protocol> -run '^$' -fuzz '^FuzzName$' -fuzztime 30s`.



## Documentation

[`docs/`](docs/) contains the network backend roadmaps and the protocol plan this repository follows.

## Author

Marcin Sztukowski, [marcin.sztukowski.com](https://marcin.sztukowski.com), [github.com/FBI223](https://github.com/FBI223)

## License

See [LICENSE](LICENSE).
