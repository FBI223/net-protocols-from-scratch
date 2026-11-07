# protocols-from-scratch

Network protocols implemented from scratch in **Go**, **Rust** and **C++**, straight from the RFCs

## What this repository is

A collection of small, focused implementations. Each protocol lives in its own folder with its own README
that says what was built, how it was tested and what is missing. The goal is to understand how networks work
at the packet level, not to replace production libraries.

## Repository layout

| Path | Contents |
|---|---|
| `go/` | One Go module. Every protocol is a package with its own README. |
| `rust/` | One Cargo workspace. Every protocol is a crate with its own README. |
| `cpp/` | One CMake project. Every protocol is a subdirectory with its own library, program and tests. |
| `<project>/testdata/pcap/` | Small captured packets used only by that project's tests. Every Go package, Rust crate and C++ project has one. |
| `testdata/pcap/` | Captures shared between projects, for example the same packets used to test a Go version and its Rust or C++ port. Keep large captures out of the repo. |
| `docs/rfc-notes/` | My notes from reading the RFCs. |

## Protocols

### Level 1 · Foundations

| # | Protocol | RFC | Where |
|---|---|---|---|
| 1 | TFTP | [1350](https://www.rfc-editor.org/rfc/rfc1350) | [`go/tftp`](go/tftp) |
| 2 | TCP and HTTP/1.1 | [9293](https://www.rfc-editor.org/rfc/rfc9293), [9112](https://www.rfc-editor.org/rfc/rfc9112) | [`go/http1`](go/http1) |
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
| 15 | Syslog | [5424](https://www.rfc-editor.org/rfc/rfc5424) | [`go/syslog`](go/syslog) |

### Level 4 · ISP and telecom

| # | Protocol | RFC | Where |
|---|---|---|---|
| 16 | RADIUS (with accounting) | [2865](https://www.rfc-editor.org/rfc/rfc2865), [2866](https://www.rfc-editor.org/rfc/rfc2866) | [`go/radius`](go/radius) |
| 17 | SIP + SDP | [3261](https://www.rfc-editor.org/rfc/rfc3261), [8866](https://www.rfc-editor.org/rfc/rfc8866) | [`go/sip`](go/sip) |
| 18 | RTP / RTCP | [3550](https://www.rfc-editor.org/rfc/rfc3550) | [`go/rtp`](go/rtp) |
| 19 | MQTT 3.1.1 / 5.0 | OASIS specification | [`go/mqtt`](go/mqtt) |

### Level 5 · Routing, access and infrastructure

| # | Protocol | RFC | Where |
|---|---|---|---|
| 20 | BFD | [5880](https://www.rfc-editor.org/rfc/rfc5880), [5881](https://www.rfc-editor.org/rfc/rfc5881) | [`go/bfd`](go/bfd), then [`cpp/bfd`](cpp/bfd) |
| 21 | LLDP | IEEE 802.1AB | [`go/lldp`](go/lldp) |
| 22 | IS-IS | ISO 10589, [1195](https://www.rfc-editor.org/rfc/rfc1195) | [`go/isis`](go/isis) |
| 23 | VRRP | [5798](https://www.rfc-editor.org/rfc/rfc5798) | [`go/vrrp`](go/vrrp) |
| 24 | ARP and ICMP tools (`arping`, `ping`, `traceroute`) | [826](https://www.rfc-editor.org/rfc/rfc826), [792](https://www.rfc-editor.org/rfc/rfc792), [4443](https://www.rfc-editor.org/rfc/rfc4443) | [`go/arp`](go/arp), [`go/icmp`](go/icmp) |
| 25 | mDNS and DNS-SD | [6762](https://www.rfc-editor.org/rfc/rfc6762), [6763](https://www.rfc-editor.org/rfc/rfc6763) | [`go/mdns`](go/mdns) |
| 26 | PPPoE | [2516](https://www.rfc-editor.org/rfc/rfc2516), [1661](https://www.rfc-editor.org/rfc/rfc1661) | [`go/pppoe`](go/pppoe) |
| 27 | RPKI-to-Router | [8210](https://www.rfc-editor.org/rfc/rfc8210), [6811](https://www.rfc-editor.org/rfc/rfc6811) | [`go/rpki-rtr`](go/rpki-rtr) |
| 28 | Diameter | [6733](https://www.rfc-editor.org/rfc/rfc6733) | [`go/diameter`](go/diameter), then [`cpp/diameter`](cpp/diameter) |
| 29 | TURN | [8656](https://www.rfc-editor.org/rfc/rfc8656) | [`go/turn`](go/turn) |
| 30 | NETCONF | [6241](https://www.rfc-editor.org/rfc/rfc6241) | [`go/netconf`](go/netconf) |

### Level 6 · Application layer and tunnels

| # | Protocol | RFC | Where |
|---|---|---|---|
| 31 | HTTP/2 | [9113](https://www.rfc-editor.org/rfc/rfc9113), [7541](https://www.rfc-editor.org/rfc/rfc7541) | [`go/http2`](go/http2), then [`rust/http2`](rust/http2) |
| 32 | gRPC | gRPC over HTTP/2 | [`go/grpc`](go/grpc) |
| 33 | RESP (Redis protocol) | RESP2 / RESP3 | [`go/resp`](go/resp) |
| 34 | L4 load balancer | none (design project) | [`go/l4-loadbalancer`](go/l4-loadbalancer) |
| 35 | L7 reverse proxy | [9110](https://www.rfc-editor.org/rfc/rfc9110) | [`rust/reverse-proxy`](rust/reverse-proxy) |
| 36 | WireGuard-style tunnel | WireGuard whitepaper | [`go/wireguard`](go/wireguard) |

### Level 7 · C++: mobile core and fast packet processing

| # | Protocol | Spec | Where |
|---|---|---|---|
| 37 | GTP-U / GTP-C | 3GPP TS 29.281, 29.274 | [`cpp/gtp`](cpp/gtp) |
| 38 | SCTP | [9260](https://www.rfc-editor.org/rfc/rfc9260) | [`cpp/sctp`](cpp/sctp) |
| 39 | PFCP | 3GPP TS 29.244 | [`cpp/pfcp`](cpp/pfcp) |
| 40 | ASN.1 PER | ITU-T X.691 | [`cpp/asn1-per`](cpp/asn1-per) |
| 41 | Longest-prefix-match FIB | none (data structures) | [`cpp/lpm-fib`](cpp/lpm-fib) |
| 42 | NAT and connection tracking | [3022](https://www.rfc-editor.org/rfc/rfc3022) | [`cpp/nat-conntrack`](cpp/nat-conntrack) |

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

**15. Syslog.** A UDP and TCP receiver, a parser, an index and simple queries. *Verify with:* `logger`,
`rsyslog`.

**16. RADIUS.** A server for Access-Request, Accept and Reject, attributes, Message-Authenticator and
accounting. The authentication backbone of ISPs and Wi-Fi networks. *Verify with:* `radtest`, `eapol_test`.

**17. SIP + SDP.** A parser, REGISTER with digest authentication, the INVITE / 200 / ACK / BYE flow, user
location and call routing. *Verify with:* SIPp, Linphone, Wireshark.

**18. RTP / RTCP.** A receiver with a jitter buffer, loss and jitter statistics and a simple RTP relay.
*Verify with:* SIPp (media), `ffmpeg`, Wireshark.

**19. MQTT 3.1.1 / 5.0.** A broker with CONNECT, PUBLISH, SUBSCRIBE, QoS 0 and 1, and retained messages.
*Verify with:* `mosquitto_pub`, `mosquitto_sub`.

**20. BFD.** Tiny hello packets that detect a dead link in milliseconds. BGP and OSPF use it as a fast failure
detector. Go first, then C++ for the timer-sensitive path. *Verify with:* FRR (`bfdd`), BIRD, Wireshark.

**21. LLDP.** How switches, routers and access points announce themselves to the device on the other end of a
cable. This is how a controller builds its topology map. *Verify with:* `lldpd`, `tcpdump`.

**22. IS-IS.** The second great link-state routing protocol, common in ISP backbones. Runs directly over Ethernet.
Reuses the shortest-path code from OSPF. *Verify with:* FRR (`isisd`), Wireshark.

**23. VRRP.** Routers share one virtual gateway address and a backup takes over when the master dies.
*Verify with:* `keepalived`, Wireshark.

**24. ARP and ICMP tools.** `arping`, `ping` and `traceroute` over raw sockets, in Go. The Rust parser in item 6
covers the same headers from the packet-parsing side. *Verify with:* `arping`, `ping`, `tcpdump`.

**25. mDNS and DNS-SD.** DNS without a server, over multicast. This is how a controller finds devices on a local
network. Reuses the codec from item 4. *Verify with:* `avahi-browse`, `dns-sd`.

**26. PPPoE.** How a home customer connects to an ISP over Ethernet: discovery, PPP, LCP, authentication and
address assignment. Authenticates against item 16. *Verify with:* `pppd` in a network namespace.

**27. RPKI-to-Router.** How a router learns which AS may announce which prefix, and the origin validation that
uses it. Hooks into item 12. *Verify with:* Routinator, StayRTR.

**28. Diameter.** The AAA protocol of mobile networks, successor of RADIUS. *Verify with:* freeDiameter, Wireshark.

**29. TURN.** A relay for clients behind NAT. Completes item 8. *Verify with:* `coturn`, Wireshark.

**30. NETCONF.** Configuring network devices with XML RPCs over SSH, the base of network automation.
*Verify with:* `netopeer2`, `ncclient`.

**31. HTTP/2.** Binary framing, HPACK header compression and multiplexed streams, over cleartext (h2c) first.
*Verify with:* `curl --http2-prior-knowledge`, `h2spec`, `nghttp`.

**32. gRPC.** Length-prefixed messages over HTTP/2 streams with status in the trailers, built on item 31.
*Verify with:* `grpcurl`, a grpc-go client.

**33. RESP.** The Redis protocol and a small server that speaks it: stream parsing, pipelining, many connections.
*Verify with:* `redis-cli`, `redis-benchmark`.

**34. L4 load balancer.** A TCP and UDP balancer with several algorithms, health checks and connection draining.
*Verify with:* `curl`, `wrk`, `iperf3`.

**35. L7 reverse proxy.** A Rust proxy with routing, upstream pools and health checks. Pairs with item 34.
*Verify with:* `curl`, `wrk`.

**36. WireGuard-style tunnel.** A Noise handshake, encrypted transport packets and a TUN device. Builds on the
tunnel idea from item 11. *Verify with:* `wg` in two network namespaces.

**37 to 40. Mobile core.** GTP tunnels, SCTP, PFCP and ASN.1 PER decoding: the protocols between a base station
and the core network. *Verify with:* Open5GS, free5GC, Wireshark.

**41. Longest-prefix-match FIB.** A trie and a DIR-24-8 table for IP lookup, benchmarked against each other.
*Verify with:* a routing table dump (RIPE RIS), a benchmark.

**42. NAT and connection tracking.** Flow table, address and port translation with checksum updates.
*Verify with:* `conntrack`, `iptables` in a network namespace.

## Protocols in more than one language

Some protocols appear in two languages on purpose. The rule: write the Go version first as the reference, then
port it to Rust or C++ using the same captured packets (kept in the shared `testdata/pcap/`), so the two can be compared on
correctness, speed and memory. A port counts as done only when it passes the same test vectors.

| Protocol | Go | Rust | C++ | How they differ |
|---|---|---|---|---|
| DNS | [`go/dns`](go/dns) | [`rust/dns-rust`](rust/dns-rust) | | Rust is the port of the Go server and resolver. |
| HTTP/2 | [`go/http2`](go/http2) | [`rust/http2`](rust/http2) | | Go first (gRPC builds on it); the Rust port comes later. |
| BFD | [`go/bfd`](go/bfd) | | [`cpp/bfd`](cpp/bfd) | C++ for the timing-sensitive path, after Go works. |
| Diameter | [`go/diameter`](go/diameter) | | [`cpp/diameter`](cpp/diameter) | Same protocol; pick one first, port only if time allows. |
| ARP and ICMP | [`go/arp`](go/arp), [`go/icmp`](go/icmp) | [`rust/packet-parser`](rust/packet-parser) | | Go: tools over raw sockets. Rust: header parsing and pcap files. |
| Load balancing | [`go/l4-loadbalancer`](go/l4-loadbalancer) | [`rust/reverse-proxy`](rust/reverse-proxy) | | Different layers: L4 in Go, L7 in Rust. |
| Tunnels | [`go/wireguard`](go/wireguard) | [`rust/gre-vxlan`](rust/gre-vxlan) | | Different protocols, same TUN technique. |

## Libraries used instead of writing from scratch

| Topic | Libraries | Why |
|---|---|---|
| QUIC and HTTP/3 | `quic-go`, `quinn` | A huge specification. Item 10 teaches the core ideas. |
| TLS | `crypto/tls`, `rustls` | Never write your own cryptography. |
| SSH and NETCONF | `golang.org/x/crypto/ssh`, `russh` | Cryptography. Focus on the client and automation. |
| gRPC in production, gNMI | `grpc-go`, `tonic` | Item 32 builds the transport; production services use the library. |
| WireGuard / Noise | `golang.org/x/crypto`, `wireguard-go` | Item 36 writes the protocol and tunnel; the primitives come from the library. |

## Definition of done for a protocol

A protocol counts as finished only when all of this is true:

- Implemented from the RFC, with the section numbers in code comments.
- Unit tests plus at least one test on a real captured packet from the project's own `testdata/pcap/` (or the shared one).
- A fuzz target for every parser, run for at least 30 seconds without a crash.
- Checked with a real client or server tool, named in the protocol's README.
- A benchmark or load number with the exact command that produced it.
- A README saying what is implemented, what is **not**, and how it was tested.
- CI is green.

## Running the tests

```bash
(cd go && go test -race ./...)
(cd rust && cargo test --all)
(cd cpp && cmake -S . -B build && cmake --build build && ctest --test-dir build)
```

Fuzzing a parser:

```bash
(cd go && go test -fuzz=Fuzz -fuzztime=30s ./dns)
(cd rust/packet-parser && cargo fuzz run parse -- -max_total_time=30)
```

## License

MIT
