# TFTP in Go (RFC 1350)

A from-scratch implementation of the Trivial File Transfer Protocol: a packet
codec, a lock-step transfer engine, a client, a server, and two small
command-line programs. It uses only the Go standard library.

**Status:** RFC 1350 in octet mode, for both client and server. Not
implemented yet: netascii mode, and the option extensions of RFC 2347, 2348
and 2349. See [RFC compliance](#rfc-compliance) and
[Limitations](#limitations-and-not-yet-verified) for the exact list.

Tested against an independent implementation (`tftp-hpa`) in both directions;
packet captures are in [`testdata/pcap/`](testdata/pcap/).

- [What TFTP is](#what-tftp-is)
- [Quick start](#quick-start)
- [How the protocol works](#how-the-protocol-works)
- [Design](#design)
- [Security](#security)
- [RFC compliance](#rfc-compliance)
- [Testing](#testing)
- [Evidence from independent implementations](#evidence-from-independent-implementations)
- [Limitations and not yet verified](#limitations-and-not-yet-verified)
- [References](#references)

## What TFTP is

TFTP moves one file per transfer between two hosts over UDP. It has no login,
no directory listing and no encryption. Every data packet carries at most 512
bytes and must be acknowledged before the next one is sent ("lock-step"). That
makes it small enough for boot ROMs, which is why it is still used to boot
machines and to load configuration onto network devices.

Because the protocol itself offers no security, a server must be restricted by
its implementation. See [Security](#security).

## Quick start

Requires Go 1.26 or newer (the server uses `os.Root`).

```bash
go build -o tftpd ./cmd/tftpd
go build -o tftp  ./cmd/tftp

mkdir -p /tmp/tftproot && head -c 5000 /dev/urandom > /tmp/tftproot/a.bin

# terminal 1: serve a folder; add -write to accept uploads of new files
./tftpd -addr 127.0.0.1:6969 -root /tmp/tftproot -write

# terminal 2
./tftp -addr 127.0.0.1:6969 get a.bin copy.bin
./tftp -addr 127.0.0.1:6969 put copy.bin uploaded.bin
```

`tftp get` refuses to overwrite an existing local file and removes a partly
downloaded file when the transfer fails. The standard port 69 needs elevated
privileges on most systems, so the examples use 6969.

### As a library

```go
import tftp "github.com/FBI223/net-protocols-from-scratch/tftp"

// Download into any io.Writer.
var buf bytes.Buffer
err := tftp.Get(ctx, "127.0.0.1:6969", "a.bin", &buf)

// Upload from any io.Reader.
err = tftp.Put(ctx, "127.0.0.1:6969", "b.bin", bytes.NewReader(data))

// Change the timers (the zero value of Client uses the defaults below).
c := &tftp.Client{Timeout: 500 * time.Millisecond, Retries: 8}
err = c.Get(ctx, addr, "a.bin", &buf)

// Serve a folder until ctx is cancelled.
srv := &tftp.Server{Root: "/srv/tftp", AllowWrite: false, Logger: slog.Default()}
err = srv.ListenAndServe(ctx, "127.0.0.1:6969")
```

Errors can be inspected with `errors.Is` and `errors.As`:

| Error | Meaning |
|---|---|
| `*tftp.RemoteError` | the peer sent an ERROR packet (`Code`, `Msg`) |
| `tftp.ErrTimeout` | the peer stopped answering after all retransmissions |
| `tftp.ErrTransferTooLarge` | the file needs more than 65535 blocks (see [Design](#block-number-limit)) |
| `context.Canceled`, `context.DeadlineExceeded` | the caller's context ended the transfer |

### Defaults

| Setting | Default | Field |
|---|---|---|
| Wait before the first retransmission | 1 s | `Timeout` |
| Cap of the doubling wait | 8 s | `MaxTimeout` |
| Retransmissions before giving up | 5 | `Retries` |
| Wait after the final ACK ("dally") | 2 x `Timeout` | `Dally` (negative: none) |
| Simultaneous transfers on a server | 100 | `Server.MaxTransfers` |
| Data bytes per block | 512 | fixed (no option negotiation yet) |

These timer values are our own choices; RFC 1350 specifies none.

## How the protocol works

### Packets

Five packet types, all starting with a 2-byte opcode (big endian). Strings end
with a zero byte.

```
RRQ / WRQ   | 1 or 2 | filename | 0 | mode | 0 |  [ option | 0 | value | 0 ] ...
DATA        |   3    | block (2) | data (0..512 bytes)
ACK         |   4    | block (2) |
ERROR       |   5    | code (2)  | message | 0 |
```

`mode` is `netascii`, `octet` or `mail` in any letter case. This
implementation transfers `octet` (raw bytes). The bracketed option pairs are
RFC 2347 syntax: they are parsed but not acted on (see
[RFC compliance](#rfc-compliance)).

Error codes (RFC 1350 appendix, plus code 8 from RFC 2347):

| Code | Meaning | Code | Meaning |
|---|---|---|---|
| 0 | not defined | 5 | unknown transfer ID |
| 1 | file not found | 6 | file already exists |
| 2 | access violation | 7 | no such user |
| 3 | disk full | 8 | option negotiation failed |
| 4 | illegal operation | | |

### Starting a transfer, and the two TIDs

Each side of a transfer is identified by a *transfer ID* (TID), which is its
UDP port. The client picks a free port and sends its request to the server's
well-known port (69). The server answers **from a new port of its own**. From
then on both sides accept packets only from the other's TID. A packet from any
other port is answered with ERROR 5 and otherwise ignored, so a stray packet
cannot disturb a running transfer.

```
Download (RRQ)                              Upload (WRQ)

client            server                    client            server
  | -- RRQ file ---> | :69                    | -- WRQ file ---> | :69
  | <--- DATA 1 ---- | new port               | <---- ACK 0 ---- | new port
  | ---- ACK 1 ----> |                        | ---- DATA 1 ---> |
  | <--- DATA 2 ---- |                        | <---- ACK 1 ---- |
  |        ...       |                        |        ...       |
  | <-- DATA n (<512)|                        | -- DATA n (<512)>|
  | ---- ACK n ----> |                        | <---- ACK n ---- |
```

Blocks are numbered from 1. The answer to a write request is ACK 0.

### Ending a transfer

A DATA packet with fewer than 512 bytes is the last one. If the file size is an
exact multiple of 512 (including 0), the sender sends one final empty DATA
packet, so the receiver can tell the file has ended.

The side that sends the last ACK cannot know whether it arrived. RFC 1350 §6
therefore encourages it to *dally*: to keep listening for a short while, so it
can acknowledge again if the peer repeats the last DATA. This implementation
does so (default: twice the timeout).

### Errors and lost packets

- A lost DATA or ACK is found by a timeout. The sender repeats its last packet.
  Only one packet ever needs to be kept.
- The wait doubles after each repeat up to a cap, then the transfer fails with
  `ErrTimeout`.
- ERROR packets are never acknowledged or repeated. A lost ERROR is found when
  the request times out. An ERROR is never answered with an ERROR, so two hosts
  cannot bounce errors at each other.
- A sender never repeats DATA because of a duplicate ACK. Doing so makes every
  later block go out twice (RFC 1123's "Sorcerer's Apprentice Syndrome").

```
lost ACK 4:   sender              receiver
              | --- DATA 5 ---> |  (arrives, ACK 5 sent)
              | <--- ACK 5 --X  |  (lost)
              |  ... timeout ...
              | --- DATA 5 ---> |  (same block again)
              | <--- ACK 5 ---- |  (receiver acknowledges again, does not write it twice)
```

## Design

```
  cmd/tftp   cmd/tftpd         programs: flags, signals, files
       \        /
   client.go   server.go       RRQ/WRQ handling, files, policy
            \   /
         transfer.go           lock-step engine shared by both sides
              |
          packet.go            bytes <-> packets, no network, no I/O
```

| File | Responsibility |
|---|---|
| `packet.go` | Types `Ack`, `Data`, `ErrorPacket`, `Request`; `Parse*` and `MarshalBinary` for each; limits. Never panics on any input. |
| `transfer.go` | `exchange` (send, wait, retransmit with backoff), `sendFile`, `recvFile`, `linger`. Knows nothing about files or request types. |
| `client.go` | `Client`, `Get`, `Put`. Validates arguments before touching the network. |
| `server.go` | `Server`, `Serve`, `ListenAndServe`. One goroutine and one socket per transfer. |

Choices worth knowing:

- **One engine for both sides.** The same `sendFile` and `recvFile` run in the
  client and in the server, so each rule is implemented and tested once.
- **Absolute deadlines.** `wait` reads until one fixed deadline, so a stream of
  junk packets cannot stretch a timeout.
- **Cancellation.** A cancelled `context.Context` wakes a blocked read at once
  (via `context.AfterFunc`) instead of waiting for the timeout.
- **Parsers copy.** `ParseData` copies its payload because the receive buffer
  is reused for the next packet.
- **Oversized datagrams are detected.** The receive buffer is one byte larger
  than the biggest packet we accept, because UDP silently truncates a datagram
  that does not fit. A datagram that fills it is treated as lost.
- **Bad packets are lost packets.** Packets that cannot be parsed are ignored,
  never answered.

### Retransmission

A transfer starts with `Timeout` (1 s). After each unanswered send the wait
doubles, up to `MaxTimeout` (8 s). After `Retries` (5) retransmissions the
transfer fails with `ErrTimeout`. This is a plain exponential backoff, the
minimum RFC 1123 §4.2.3.2 asks for. It does not measure round-trip times.

### Block number limit

The block number is 16 bits. RFC 1350 does not say what happens after block
65535, so this implementation stops with `ErrTransferTooLarge` instead of
guessing. With 512-byte blocks the largest file that can be sent is therefore
33,553,919 bytes (about 32 MiB); a file that would need block 65536 is
rejected. This follows from the code, not from the RFC.

### Request limits

A request packet is at most 512 bytes (RFC 2347). We accept at most 16 option
pairs (our own limit). A repeated option name is rejected (RFC 2347: an option
"may only be specified once"). A DATA packet is at most 516 bytes (RFC 1350
§2); we apply the same limit to ERROR packets, since RFC 1350 states none for
them.

## Security

TFTP has no authentication, so the server is the only line of defence. It is
built to be safe by default.

| Threat | What the server does |
|---|---|
| Path traversal (`../`, absolute paths, symbolic links) | Files are opened through Go's `os.Root`, which refuses any path that would leave `Root`, symlinks included. A leading `/` is dropped, so `/a.txt` means `Root/a.txt`. |
| Unwanted uploads | Writes are off unless `AllowWrite` is set. |
| Overwriting or replacing files | An upload only creates a new file (`O_EXCL`). An existing file, or a symbolic link, gives ERROR 6. |
| Partial files | A failed or cancelled upload is removed. |
| Special files | Only regular files are served. A named pipe, device or directory is reported as "not found", because opening a pipe would block forever. |
| Leaking system details | ERROR messages are fixed texts. System error strings, which contain paths, are never sent. |
| Reflection and amplification | Datagrams that are not valid requests get no answer at all. Requests from port 0, or from an unspecified, multicast or broadcast address, are dropped. |
| Resource exhaustion | `MaxTransfers` limits simultaneous transfers; further requests get ERROR "server busy". |
| Repeated requests | A request repeated from the same client port while its transfer is still running is ignored (see below). |
| Unsupported modes | `mail` and `netascii` get ERROR 4. |

**Repeated requests.** If the first answer to a write request is lost, the
client sends the request again from the same port. A naive server starts a
second transfer, which answers ERROR 6 because the first transfer has already
created the file, and the upload fails. The server therefore ignores a request
that repeats a running one; the running transfer repeats its own first packet
after its timeout. This was found by the scenario tests described below.

The client checks the source address of every packet after the first. The very
first reply (which fixes the server's TID) is accepted from any address; see
[Limitations](#limitations-and-not-yet-verified).

## RFC compliance

Status: **Yes** implemented and tested; **Partial**; **No** not implemented.
"Tests" lists test functions; see [Testing](#testing) for what each group does.

### RFC 1350: The TFTP Protocol (Revision 2)

| Requirement | RFC 1350 | Status | Tests and evidence |
|---|---|---|---|
| Lock-step: each DATA is acknowledged before the next | §2 | Yes | `TestTransferSizes`; both captures |
| 512-byte blocks; a short block ends the transfer | §2, §6 | Yes | `TestTransferSizes` (0, 511, 512, 513, 1024 ... bytes) |
| An empty last block when the size is a multiple of 512 | §6 | Yes | `TestTransferSizes`; capture: 0-byte and 512-byte files |
| Block numbers start at 1; ACK 0 answers a WRQ | §4 | Yes | `TestServerGet`, `TestServerPut`; capture |
| Packet formats and opcodes 1-5 | §5 | Yes | `TestParse*`, `TestAckMarshal`, `TestDataMarshal`, `TestErrorMarshal`, `TestRequestMarshal`, round-trip tests, `FuzzParse` |
| Mode is case-insensitive | §5 | Yes | `TestParseRequest`, `TestLowerASCII` |
| Server answers from a new port (its TID) | §4 | Yes | `TestServerAnswersFromNewPortAndChecksTID`; capture |
| Packets from another TID get ERROR 5; the transfer goes on | §4 | Yes | `TestStrangerGetsErrorAndTransferContinues` |
| ERROR is not acknowledged or repeated; a lost one is found by timeout | §2 | Yes | `TestRemoteErrorEndsTransfer`, `TestScenarioLostErrorPacket` |
| Lost packets: timeout, then repeat the last packet | §2 | Yes | `TestTransferWithPacketLoss`, `TestClientGetWithPacketLoss`, `TestScenarioLostDataTwice`, `TestScenarioRandomDamage` |
| Dallying after the final ACK (encouraged) | §6 | Yes | `TestClientGetDally`, `TestScenarioLostFinalAckOnGet`, `TestScenarioLostFinalAckOnPut` |
| Premature termination by ERROR (a courtesy, not guaranteed) | §7 | Yes | `TestClientGetWriterError`, `TestServerPutAbortedIsRemoved` |
| Error codes 0-7 | appendix | Yes | `TestErrorCodeValues`; server tests for codes 1, 2, 4, 6 |
| Mode `netascii` | §5 | **No** | the server answers ERROR 4 (`TestServerModes`); the client always sends `octet` |
| Mode `mail` | §5 | Refused | ERROR 4; RFC 1123 says it SHOULD NOT be supported |
| Security considerations: the protocol has no access control | Security | Yes | see [Security](#security) |

### RFC 1123 §4.2: Requirements for Internet Hosts

| Requirement | RFC 1123 | Level | Status | Tests and evidence |
|---|---|---|---|---|
| Fix the Sorcerer's Apprentice Syndrome: never resend DATA on a duplicate ACK | 4.2.3.1 | MUST | Yes | `TestSenderIgnoresDuplicateAck`, `TestScenarioDuplicateAckDoesNotResendData`, `TestScenarioDuplicateDataIsHarmless`, `TestScenarioDuplicateDataOnPut` (they count DATA packets on the wire) |
| Adaptive timeout, at least exponential backoff | 4.2.3.2 | MUST | Yes (backoff only) | `TestSenderGivesUp`, `TestScenarioLostDataTwice` |
| Support `netascii` | RFC 783 | MUST | **No** | not implemented |
| Support `octet` | RFC 783 | MUST | Yes | all transfer tests |
| Do not support `mail` | 4.2.2.1 | SHOULD NOT | Yes | `TestServerModes` |
| Configurable access control over path names | 4.2.3.4 | SHOULD | Partial | one served root (`os.Root`) and a write switch; no per-user or per-path rules |
| Silently ignore requests sent to a broadcast address | 4.2.3.5 | SHOULD | **Yes** | `Serve` refuses a socket bound to a wildcard address; a socket bound to one unicast address does not receive broadcast.|

**The MUST for `netascii` is not met.** This implementation is therefore not
fully RFC 1123 conformant until netascii is added.

### RFC 2347, 2348, 2349: option extensions

Not implemented. What exists:

- The parser reads options in requests (names are case-insensitive, at most 16
  pairs, duplicates rejected, request at most 512 bytes) and writes them back.
- The server **ignores** options and answers as for a plain request, which
  RFC 2347 allows ("unsupported options ... should not cause an ERROR"). It
  never sends an OACK. `TestServerIgnoresOptions` covers this.
- The client sends no options.
- Error code 8 is defined but never sent.

So `blksize`, `timeout` and `tsize` are not negotiated, and the block size
stays at 512.

### Decisions that are not from an RFC

Timeout, backoff cap, retry count and dally defaults; the stop at block 65535;
refusing to overwrite files; writes off by default; `MaxTransfers`; the 516-byte
limit on ERROR packets; rejecting duplicate options; ignoring a repeated request
while its transfer runs.

## Testing

Run everything:

```bash
gofmt -l .                       # prints nothing when formatted
go vet ./...
go test -race -count=1 ./...     # add -short to skip the two 65535-block tests
```

Tests use only the loopback interface (`127.0.0.1`) and temporary folders.
The suite is organised in layers; each layer catches a different kind of bug.

| Layer | Files | What it proves |
|---|---|---|
| **Unit tests** | `packet_test.go` | Every parser and encoder, table-driven: valid input, truncated and oversized input, boundaries (a request of 512 bytes is accepted, 513 is not), a zero byte inside a field, duplicate options, mixed-case modes, round trips (`parse(marshal(x)) == x`), and that parsed payloads do not alias the receive buffer. |
| **Fuzzing** | `fuzz_test.go` | `FuzzParse` feeds arbitrary bytes to `Parse`. It must never panic, and anything it accepts must survive marshal, parse, marshal unchanged. |
| **Engine tests** | `transfer_test.go` | The lock-step engine between two sockets: file sizes from 0 to 51201 bytes, packet loss, a packet from a wrong TID, duplicate ACK and DATA, an oversized datagram, a peer that goes silent (exactly 1 + `Retries` datagrams are sent), context cancellation, a remote ERROR, and the 65535-block limit in both directions. |
| **Client tests** | `client_test.go` | The client against a small in-test server written straight from RFC 1350: downloads and uploads of boundary sizes, loss, file not found, a failing writer, a denied upload, a silent server, cancellation, bad arguments, and the dally timing. |
| **Server tests** | `server_test.go`, `scenario_test.go` | The real `Client` against the real `Server`. Includes 20 clients at once, path traversal attempts (`..`, absolute paths, backslashes, symbolic links) for reads and writes, no overwriting, aborted uploads removed, mode and option handling, answers from a new port, junk on the request port, `MaxTransfers`, and shutdown during a transfer. |
| **Scenario tests** | `scenario_test.go` | Client and server talk through a UDP proxy that drops, duplicates or delays chosen packets. See below. |
| **Leak test** | `scenario_test.go` | After 150 mixed transfers (good, failing, cancelled halfway) and a server shutdown, no goroutine and no file descriptor remains. |
| **Interoperability** | `testdata/pcap/` | Real `tftp-hpa` client and server, both directions, with packet captures. See [Evidence](#evidence-from-independent-implementations). |

### Fuzzing

```bash
go test -run='^$' -fuzz=FuzzParse -fuzztime=30s .
```

Plain `go test` runs the fuzz target on its seed inputs only. An input that
makes the fuzzer fail is saved in `testdata/fuzz/FuzzParse/` and then runs as a
regression test on every `go test`; keep such files in the repository.

### Scenario tests

The proxy shows each server TID to the client as a separate port, so the client
sees the same port changes as without it. Rules choose which packet to damage,
for example "drop the first ACK 10 sent by the client". The proxy also counts
packets on the wire, which lets a test check behaviour that cannot be seen from
the outside, such as "DATA 4 was sent exactly once".

| Scenario | What it checks |
|---|---|
| Lost final ACK, download | The server repeats the last DATA; the client, still dallying, acknowledges again. |
| Lost final ACK, upload | The client repeats the last DATA; the server acknowledges again. |
| Lost RRQ | The client repeats the request. |
| Lost ACK 0 after a WRQ (twice) | The repeated WRQ must not start a second upload (no ERROR 6). |
| Lost DATA 1 | The client repeats the RRQ and recovers. |
| DATA 3 lost twice | Retransmission with backoff; the third copy arrives; exactly 3 copies were sent. |
| Duplicate ACK | The sender does not resend DATA (Sorcerer's Apprentice). |
| Duplicate DATA, download and upload | The receiver acknowledges twice; the sender still sends every later block once. |
| Peer goes silent for good | The client returns `ErrTimeout` and does not hang. |
| Lost ERROR | The request is repeated and the same ERROR comes back. |
| Random damage | Seeds 1 to 24 (1 to 6 with `-short`): random file size up to 20 kB, download or upload by seed, 10% of packets dropped, 5% duplicated, 30% held back up to 70 ms (longer than the timeout, so some retransmissions are needless and some packets arrive late). The files must arrive intact. |

The damage in the random test depends only on the seed and on which packet
it is, not on timing. A failing seed can be rerun:

```bash
go test -run 'TestScenarioRandomDamage/seed_7$' -v .
```

When a scenario fails, the test prints the last 150 packets the proxy saw.

The scenario tests found a real defect in the server (a repeated write request
caused ERROR 6, see [Security](#security)). The test for it,
`TestScenarioLostAck0OnPut`, fails on the earlier server and passes on the
current one.

### Not covered by automated tests

- `ListenAndServe` (the tests call `Serve`).
- The programs in `cmd/`.
- IPv6.
- Any network other than the loopback interface.

## Evidence from independent implementations

Unit and scenario tests are written by the same author as the code, so a
misreading of the RFC could be present on both sides. To rule that out, the
client and the server were also run against `tftp-hpa` (Ubuntu package, client
`tftp` and server `in.tftpd`), which was written independently.

| Capture | Setup | Packets |
|---|---|---|
| `testdata/pcap/hpa-get-put.pcap` | `tftp-hpa` client against our server | 43 |
| `testdata/pcap/ours-vs-hpa-server.pcap` | our client against the `tftp-hpa` server | 873 |

Both captures were taken on the loopback interface with
`tcpdump -i lo -w FILE 'udp and not port 53'`.

View them with `tshark`, telling it that the test port carries TFTP (6969 for
the first file, 6970 for the second):

```bash
tshark -r - -d udp.port==6969,tftp < testdata/pcap/hpa-get-put.pcap
```

(Reading from standard input avoids a file-permission restriction that some
`tshark` installs have.) In Wireshark, use *Analyze, Decode As, UDP port 6969,
TFTP*.

### `hpa-get-put.pcap`

`tftp -m binary 127.0.0.1 6969 -c get a.bin` and then `put`, with a 5000-byte
file. The `-m binary` option selects octet mode; the default of `tftp-hpa` is
netascii, which this server refuses.

| Packets | Observation |
|---|---|
| 1 | RRQ `a.bin`, mode `octet`, sent to the server port |
| 2 | DATA 1 comes from a **different** server port (the TID) |
| 3 to 21 | blocks 1 to 10 and their ACKs; blocks 1 to 9 carry 512 bytes, block 10 carries 392 (9 x 512 + 392 = 5000) |
| 22 | WRQ `hpa-up.bin` |
| 23 | ACK 0 from a new server port |
| 24 to 43 | DATA 1 to 10 and ACKs; block 10 has 392 bytes; ACK 10 closes the transfer |

No packet is repeated and no ERROR occurs.

### `ours-vs-hpa-server.pcap`

Seven downloads and seven uploads of files of 0, 1, 511, 512, 513, 5000 and
100000 bytes, with our `tftp` program against `in.tftpd -L -c -s`. The test
script also compared every received file with its original using `cmp`.

The capture shows the boundary rules of RFC 1350 §6 with a foreign server:

| File size | Blocks on the wire |
|---|---|
| 0 | one empty DATA 1 |
| 511 | DATA 1 with 511 bytes |
| 512 | DATA 1 with 512 bytes, then an **empty** DATA 2 |
| 513 | DATA 1 with 512 bytes, then DATA 2 with 1 byte |
| 5000 | 10 blocks, the last with 392 bytes |
| 100000 | 196 blocks, the last with 160 bytes |

The packet count is a check by itself. A transfer without a single repeated
packet needs `1 + 2b` packets for a download and `2 + 2b` for an upload, where
`b` is the number of blocks. The block counts above add up to 213, so the
expected totals are 7 + 2 x 213 = 433 packets for the downloads and
14 + 2 x 213 = 440 for the uploads, 873 in all, which is exactly what the file
contains: no retransmissions occurred.

In this capture each download starts about 2 seconds after the previous one.
That is the client's dally (twice the 1 s timeout), not a delay of the server.

## Limitations and not yet verified

Not implemented:

- `netascii` mode (required by RFC 1123, see above).
- Options: `blksize`, `timeout`, `tsize` (RFC 2347 to 2349), and OACK.
- Adaptive timeout based on measured round-trip times (only exponential
  backoff).
- Block numbers beyond 65535 (the transfer is stopped).

Known weaknesses:

- The client accepts its first reply from any source address, because that
  reply is what fixes the server's TID. Only the port is meant to change at
  that point, so a stricter client would also require the same IP address as
  the one the request went to. Until then, use the client only on networks you
  trust.
- A server that sends a very large file to a client that never acknowledges
  keeps its transfer slot until the retransmissions run out (at most
  `Retries` repeats, 1 s up to 8 s apart by default).

Not verified:

- Behaviour on any system other than Linux with Go 1.26 or newer.
- Interoperability with servers and clients other than `tftp-hpa`.
- Transfers over a real network, as opposed to the loopback interface.


## References

- [RFC 1350](https://www.rfc-editor.org/rfc/rfc1350): The TFTP Protocol (Revision 2)
- [RFC 1123](https://www.rfc-editor.org/rfc/rfc1123): Requirements for Internet Hosts, section 4.2 (TFTP)
- [RFC 2347](https://www.rfc-editor.org/rfc/rfc2347): TFTP Option Extension
- [RFC 2348](https://www.rfc-editor.org/rfc/rfc2348): TFTP Blocksize Option
- [RFC 2349](https://www.rfc-editor.org/rfc/rfc2349): TFTP Timeout Interval and Transfer Size Options
- [RFC 768](https://www.rfc-editor.org/rfc/rfc768): User Datagram Protocol
- [RFC 2119](https://www.rfc-editor.org/rfc/rfc2119): Key words for use in RFCs (MUST, SHOULD)


