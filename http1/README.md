# HTTP/1.1 in Go (RFC 9112)

An HTTP/1.1 message parser, body framing and a small server, written from the
specification without `net/http`. The network code uses only `net` and `bufio`
from the Go standard library.

**Status: Planned.** The layout, the scope and the test plan below are fixed;
there is no protocol code yet. This README is updated with results after they
exist, never before.

**Specification:** RFC 9112 (HTTP/1.1 syntax and framing), RFC 9110 (semantics),
RFC 9293 (TCP, used through sockets), RFC 2119 and 8174 (the words MUST,
SHOULD, MAY). Reading order and section numbers: [`rfcs/http1-rfc-notes.md`](rfcs/http1-rfc-notes.md).

**Verify with:** `curl`, `nc`, `wrk`, `k6`, Wireshark.

- [Scope](#scope)
- [Layout](#layout)
- [Design](#design)
- [Security rules](#security-rules)
- [Test plan](#test-plan)
- [How to run](#how-to-run)
- [Benchmarks](#benchmarks)
- [Limitations](#limitations)

## Scope

Planned:

- A TCP server with read and write deadlines and graceful shutdown.
- Request line and header parser with size limits.
- Body framing: `Content-Length` and `chunked` (reading and writing).
- Keep-alive and `Connection: close`.
- A small router with a few routes.
- A way for a handler to take over the connection (the WebSocket handshake in
  `websocket/` needs it).

## Layout

```
http1/
├── doc.go           package overview and the layers
├── errors.go        errors and their status codes
├── limits.go        every bound on untrusted input, in one struct
├── header.go        header fields
├── request.go       request line and header section
├── response.go      status line, header section, response writer
├── body.go          where a body ends (RFC 9112 section 6.3), Content-Length
├── chunked.go       chunked coding
├── conn.go          one connection: loop, keep-alive, deadlines
├── router.go        method and path to handler
├── server.go        accept loop, shutdown, taking over a connection
├── *_test.go        one test file per source file, plus fuzz_test.go
├── cmd/httpd/       the server program
├── rfcs/            fetch-rfcs.sh and the reading notes
└── testdata/
    ├── requests/    raw requests, valid and invalid (see its README)
    └── pcap/        captures of runs against real tools
```

## Design

```
  cmd/httpd
      |
  server.go  conn.go  router.go     sockets, deadlines, keep-alive
      |
  body.go  chunked.go               where a body ends; chunked coding
      |
  request.go  response.go           start line + header section, on bufio
      |
  errors.go  limits.go  header.go   values and rules, no I/O
```

A layer uses only the layers below it, so the lower three can be tested with
`strings.NewReader` and no sockets, as the TFTP codec is.

Rules that shape the code (decisions are recorded in
[`rfcs/http1-rfc-notes.md`](rfcs/http1-rfc-notes.md)):

- **Strict, not forgiving.** A message that cannot be framed with certainty is
  rejected, not repaired.
- **Bounds before allocation.** No length from the network sizes a buffer
  before it is checked against `Limits`.
- **Streams, not buffers.** A body is an `io.Reader`; a handler can read a large
  upload without it being held in memory.
- **A parser reads exactly one message.** Bytes after it belong to the next
  request and stay in the `bufio.Reader`.
- **Timeouts at every stage.** Waiting for the request line, for the rest of the
  headers, for the body, for the next request on an idle connection, and for the
  client to read the response, each with its own limit.

## Security rules

Each rule has a file in [`testdata/requests/`](testdata/requests/README.md) and a
test. The rules come from RFC 9112 sections 2.2, 5, 6 and 11.

| Threat | Rule |
|---|---|
| Request smuggling (`Content-Length` with `Transfer-Encoding`, repeated or invalid `Content-Length`) | reject the request |
| Differences in line ends (bare LF, bare CR) | reject |
| Obsolete line folding, whitespace before the colon, NUL in a value | reject |
| Missing or repeated `Host` | 400 |
| Huge request line, headers or chunk sizes | 414, 431, or a closed connection; no allocation first |
| Slow clients that send a few bytes at a time | per-stage deadlines |
| Many connections | limit on simultaneous connections |

## Test plan

Written before the code, one layer at a time. Nothing in this table exists yet.

| Layer | File | What it will prove |
|---|---|---|
| Unit | `header_test.go` | field-name syntax, case-insensitive lookup, repeated fields, trimming, forbidden bytes |
| Unit | `request_test.go` | request line, target forms, versions, limits, and every file in `testdata/requests` |
| Unit | `response_test.go` | status line and fields as written; `Content-Length` or chunked chosen correctly |
| Unit | `body_test.go` | every case of RFC 9112 section 6.3; a short body is an error; bytes after the body are not consumed |
| Unit | `chunked_test.go` | hex sizes, extensions, trailers, truncated input, huge sizes, a round trip |
| Property | `request_test.go` | the same bytes cut into pieces at every possible point give the same result |
| Fuzz | `fuzz_test.go` | `FuzzReadRequest` and `FuzzChunkedReader` never panic; what they accept survives write and read again |
| Server | `server_test.go` | real TCP on `127.0.0.1`: keep-alive, `Connection: close`, slow headers, oversized input, graceful shutdown |
| Leak | `server_test.go` | after many good, failing and aborted connections no goroutine and no file descriptor remains |
| Real tools | `testdata/pcap/` | `curl` and `wrk` against `httpd`, with captures |

`http1_test.go` and the current `fuzz_test.go` contain skipped placeholders;
they are replaced by the files above.

## How to run

```bash
go test -race -count=1 ./http1/...
go test -run='^$' -fuzz=FuzzReadRequest -fuzztime=30s ./http1/    # when the target exists
bash rfcs/fetch-rfcs.sh                                           # from this folder
```

Once the server exists:

```bash
go run ./http1/cmd/httpd -addr 127.0.0.1:8080
curl -v http://127.0.0.1:8080/hello
```

## Benchmarks

None measured yet. The numbers will go here together with the exact command
and the machine, for example `wrk -t2 -c100 -d30s http://127.0.0.1:8080/hello`.

## Limitations

None to report yet. This section grows with the code: what is not implemented,
and what has not been verified.
