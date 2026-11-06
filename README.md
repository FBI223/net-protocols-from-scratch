# protocols-from-scratch

Network protocols implemented from scratch in **Go** and **Rust**, straight from the RFCs

## What this repository is

A collection of small, focused implementations. Each protocol lives in its own folder with its own README
that says what was built, how it was tested and what is missing. The goal is to understand how networks work
at the packet level, not to replace production libraries.

## Repository layout

| Path | Contents |
|---|---|
| `go/` | One Go module. Every protocol is a package with its own README. |
| `rust/` | One Cargo workspace. Every protocol is a crate with its own README. |
| `testdata/pcap/` | Small captured packets used in tests. Keep large captures out of the repo. |
| `docs/rfc-notes/` | My notes from reading the RFCs. |

## Protocols

### Level 1 · Foundations

| # | Protocol | RFC | Where |
|---|---|---|---|
| 1 | TFTP | [1350](https://www.rfc-editor.org/rfc/rfc1350) | [`go/tftp`](go/tftp) |
| 2 | TCP and HTTP/1.1 | [9293](https://www.rfc-editor.org/rfc/rfc9293), [9112](https://www.rfc-editor.org/rfc/rfc9112) | [`go/tcp-http`](go/tcp-http) |
| 3 | WebSocket | [6455](https://www.rfc-editor.org/rfc/rfc6455) | [`go/websocket`](go/websocket) |
| 4 | DNS | [1034](https://www.rfc-editor.org/rfc/rfc1034), [1035](https://www.rfc-editor.org/rfc/rfc1035), [6891](https://www.rfc-editor.org/rfc/rfc6891) | [`go/dns`](go/dns), then [`rust/dns-rust`](rust/dns-rust) |
| 5 | DHCP | [2131](https://www.rfc-editor.org/rfc/rfc2131), [2132](https://www.rfc-editor.org/rfc/rfc2132) | [`go/dhcp`](go/dhcp) |

### Level 2 · Packets and lower layers

| # | Protocol | RFC | Where |
|---|---|---|---|
| 6 | Ethernet, ARP, IPv4, ICMP, UDP, TCP headers | [826](https://www.rfc-editor.org/rfc/rfc826), [791](https://www.rfc-editor.org/rfc/rfc791), [792](https://www.rfc-editor.org/rfc/rfc792), [768](https://www.rfc-editor.org/rfc/rfc768) | [`rust/packet-parser`](rust/packet-parser) |
| 7 | NTP | [5905](https://www.rfc-editor.org/rfc/rfc5905) | [`go/ntp`](go/ntp) |
| 8 | STUN | [8489](https://www.rfc-editor.org/rfc/rfc8489) | [`go/stun`](go/stun) |
| 9 | TLS 1.3 ClientHello (parser only) | [8446](https://www.rfc-editor.org/rfc/rfc8446) | [`rust/tls-clienthello`](rust/tls-clienthello) |
| 10 | Reliable transport over UDP | inspired by [9293](https://www.rfc-editor.org/rfc/rfc9293), [9000](https://www.rfc-editor.org/rfc/rfc9000) | [`rust/reliable-udp`](rust/reliable-udp) |
| 11 | GRE and VXLAN | [2784](https://www.rfc-editor.org/rfc/rfc2784), [7348](https://www.rfc-editor.org/rfc/rfc7348) | [`rust/gre-vxlan`](rust/gre-vxlan) |

### Level 3 · Routing and telemetry

| # | Protocol | RFC | Where |
|---|---|---|---|
| 12 | BGP-4 | [4271](https://www.rfc-editor.org/rfc/rfc4271), [7854](https://www.rfc-editor.org/rfc/rfc7854) (BMP) | [`go/bgp`](go/bgp) |
| 13 | OSPFv2 | [2328](https://www.rfc-editor.org/rfc/rfc2328) | [`go/ospf`](go/ospf) |
| 14 | SNMP v2c | [3416](https://www.rfc-editor.org/rfc/rfc3416) | [`go/snmp`](go/snmp) |
| 15 | sFlow v5 / IPFIX | [7011](https://www.rfc-editor.org/rfc/rfc7011) | [`go/sflow`](go/sflow) |
| 16 | Syslog | [5424](https://www.rfc-editor.org/rfc/rfc5424) | [`go/syslog`](go/syslog) |

### Level 4 · ISP and telecom

| # | Protocol | RFC | Where |
|---|---|---|---|
| 17 | RADIUS (with accounting) | [2865](https://www.rfc-editor.org/rfc/rfc2865), [2866](https://www.rfc-editor.org/rfc/rfc2866) | [`go/radius`](go/radius) |
| 18 | SIP + SDP | [3261](https://www.rfc-editor.org/rfc/rfc3261), [8866](https://www.rfc-editor.org/rfc/rfc8866) | [`go/sip`](go/sip) |
| 19 | RTP / RTCP | [3550](https://www.rfc-editor.org/rfc/rfc3550) | [`go/rtp`](go/rtp) |
| 20 | MQTT 3.1.1 / 5.0 | OASIS specification | [`go/mqtt`](go/mqtt) |

## What each protocol is about

**1. TFTP.** A tiny file-transfer protocol over UDP. Small enough to finish in a weekend, and it teaches the
essentials: UDP sockets, request/ACK exchanges, retransmission and timeouts. *Verify with:* `tftp-hpa`, Wireshark.

**2. TCP and HTTP/1.1.** A TCP server with message framing and timeouts, then an HTTP server written without
`net/http`: request parser, headers, keep-alive, chunked encoding. *Verify with:* `curl`, `wrk`, `k6`, Wireshark.

**3. WebSocket.** The upgrade handshake, frame format, masking, ping/pong and the close handshake. Used for
dashboards and live notifications. *Verify with:* `websocat`, a browser.

**4. DNS.** Message parser and builder with name compression, a stub resolver, an authoritative server over UDP
and TCP, and a TTL cache. Written in Go first, then ported to Rust (`dns-rust`).
*Verify with:* `dig`, `kdig`, Wireshark.

**5. DHCP.** A server with an address pool, leases and options, plus a small REST API for managing addresses.
*Verify with:* `dhclient` inside a network namespace, Wireshark.

**6. Ethernet, ARP, IPv4, ICMP, UDP, TCP headers.** A raw-byte parser that reads pcap files, then your own
`ping` and `traceroute` and ARP-spoofing detection. This is the core of packet processing. *Verify with:* Wireshark, `tcpdump`.

**7. NTP.** An SNTP client and a simple server: offset, delay, modes and stratum. *Verify with:* `chronyc`,
`ntpdate -q`.

**8. STUN.** Binding requests, the XOR-MAPPED-ADDRESS attribute and NAT type detection. A first step towards
WebRTC and VoIP. *Verify with:* Wireshark, a public STUN server.

**9. TLS 1.3 ClientHello.** Only the parser: SNI, ALPN, supported versions and the JA3 fingerprint. No
cryptography is written here. Useful for security and traffic filtering. *Verify with:* `openssl s_client`,
`curl -v`.

**10. Reliable transport over UDP.** A sliding window, retransmission and congestion control on top of UDP,
tested under simulated packet loss. Shows what TCP and QUIC do inside. *Verify with:* `tc netem`, `iperf3`.

**11. GRE and VXLAN.** Encapsulation over a TUN device, with two hosts in separate network namespaces. The idea
behind VPNs, SD-WAN and cloud overlay networks. *Verify with:* `ip`, `tcpdump`.

**12. BGP-4.** The state machine and the OPEN, KEEPALIVE, UPDATE and NOTIFICATION messages, plus a route table.
The implementation peers with FRR inside containerlab. *Verify with:* FRR (`vtysh`), Wireshark.

**13. OSPFv2.** Hello packets, adjacencies, type-1 LSAs and the SPF algorithm. An advanced item for routing
roles. *Verify with:* FRR, Wireshark.

**14. SNMP v2c.** An agent with a few OIDs and a manager that does GET, GETNEXT and WALK, including BER
encoding. *Verify with:* `snmpwalk`, `snmpget`.

**15. sFlow v5 / IPFIX.** A datagram collector that aggregates flows and stores them in ClickHouse. *Verify with:* `hsflowd`, `softflowd`.

**16. Syslog.** A UDP and TCP receiver, a parser, an index and simple queries. *Verify with:* `logger`,
`rsyslog`.

**17. RADIUS.** A server for Access-Request, Accept and Reject, attributes, Message-Authenticator and
accounting. The authentication backbone of ISPs and Wi-Fi networks. *Verify with:* `radtest`, `eapol_test`.

**18. SIP + SDP.** A parser, REGISTER with digest authentication, the INVITE / 200 / ACK / BYE flow, user
location and call routing. *Verify with:* SIPp, Linphone, Wireshark.

**19. RTP / RTCP.** A receiver with a jitter buffer, loss and jitter statistics and a simple RTP relay.
*Verify with:* SIPp (media), `ffmpeg`, Wireshark.

**20. MQTT 3.1.1 / 5.0.** A broker with CONNECT, PUBLISH, SUBSCRIBE, QoS 0 and 1, and retained messages.
*Verify with:* `mosquitto_pub`, `mosquitto_sub`.

## Libraries used instead of writing from scratch

| Topic | Libraries | Why |
|---|---|---|
| QUIC and HTTP/3 | `quic-go`, `quinn` | A huge specification. Item 10 teaches the core ideas. |
| TLS | `crypto/tls`, `rustls` | Never write your own cryptography. |
| SSH and NETCONF | `golang.org/x/crypto/ssh`, `russh` | Cryptography. Focus on the client and automation. |
| gRPC and gNMI | `grpc-go`, `tonic` | Designing the API in protobuf is enough. |
| WireGuard / Noise | `wireguard-go`, `boringtun` | Build the tunnel from item 11, take encryption from a library. |

## Definition of done for a protocol

A protocol counts as finished only when all of this is true:

- Implemented from the RFC, with the section numbers in code comments.
- Unit tests plus at least one test on a real captured packet from `testdata/pcap/`.
- A fuzz target for every parser, run for at least 30 seconds without a crash.
- Checked with a real client or server tool, named in the protocol's README.
- A benchmark or load number with the exact command that produced it.
- A README saying what is implemented, what is **not**, and how it was tested.
- CI is green.

## Running the tests

```bash
(cd go && go test -race ./...)
(cd rust && cargo test --all)
```

Fuzzing a parser:

```bash
(cd go && go test -fuzz=Fuzz -fuzztime=30s ./dns)
(cd rust/packet-parser && cargo fuzz run parse -- -max_total_time=30)
```

## License

MIT
