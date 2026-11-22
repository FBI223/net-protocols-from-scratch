# http1

An HTTP/1.1 server written from scratch in Go, on top of `net.Conn`, with no
use of `net/http` in the implementation. It follows
[RFC 9112](https://www.rfc-editor.org/rfc/rfc9112) (message syntax) and
[RFC 9110](https://www.rfc-editor.org/rfc/rfc9110) (semantics).

The goal is to learn the protocol by building it, but with production habits:
a strict parser, bounded resources, and the nasty parts of HTTP (request
smuggling, slow clients, oversized input) handled deliberately and tested.

Requires Go 1.24 or newer (`os.Root` is used for safe file serving).

## Quick start

```go
rt := &http1.Router{}
rt.HandleFunc("GET", "/hello/{name}", func(w *http1.ResponseWriter, r *http1.Request) {
	fmt.Fprintf(w, "Hello, %s!\n", r.PathValue("name"))
})

srv := &http1.Server{Addr: ":8080", Handler: rt}
log.Fatal(srv.ListenAndServe())
```

Or run the demo server:

```sh
go run ./cmd/httpd -addr :8080 -root ./testdata -list
```

```sh
curl localhost:8080/hello/Ola
curl -X PUT -d '{"a":1}' localhost:8080/kv/test     # JSON key-value store
curl localhost:8080/kv/test
curl localhost:8080/files/                          # static files from -root
curl -N localhost:8080/stream                       # chunked, flushed per line
```

Other ways to expose it:

```sh
go run ./cmd/httpd -unix /tmp/httpd.sock            # curl --unix-socket /tmp/httpd.sock http://x/
go run ./cmd/httpd -addr :8443 -tls-self-signed     # curl -k https://localhost:8443/
go run ./cmd/httpd -addr :8443 -tls-cert c.pem -tls-key k.pem
```

`httpd -h` lists the limit flags (`-max-body`, `-max-conns`,
`-read-header-timeout`, `-idle-timeout`, `-shutdown-grace`). Ctrl+C drains
running requests; a second Ctrl+C quits at once.

## What is implemented

| Area | Details |
|---|---|
| Request line | origin, absolute, authority (`CONNECT`) and asterisk forms; HTTP/1.0 and 1.1 |
| Headers | strict field names and values, canonical keys, repeated headers kept in order |
| Framing | `Content-Length` and `Transfer-Encoding: chunked`, trailers validated and discarded |
| Connections | keep-alive, pipelining, `Connection: close`, HTTP/1.0 close-delimited responses |
| `Expect: 100-continue` | interim response sent only when the handler reads the body |
| Responses | automatic `Content-Length` or chunked framing, `Date`, HEAD/204/304 rules, flush |
| Errors | parse errors carry their status (400, 408, 413, 414, 431, 501, 505) and close the connection |
| Router | `{name}` and `{name...}` wildcards, automatic 404, 405 + `Allow`, HEAD and OPTIONS |
| File server | content types, `ETag`, `Last-Modified`, `If-None-Match`, `If-Modified-Since`, `Range`, `If-Range`, listings |
| Lifecycle | `Shutdown` (graceful), `Close`, connection limit, panic recovery |
| Transports | any `net.Listener`: TCP, Unix socket, TLS via `tls.NewListener` |

Not implemented: HTTP/2 and HTTP/3, compression, multiple byte ranges,
`Upgrade` (WebSocket), sending trailers, and HTTP proxy or caching behaviour.

## Safety design

- **Strict parsing.** Bare CR or LF, obsolete line folding, whitespace before
  the colon, a missing or repeated `Host`, a bad `Content-Length`, and a message
  with both `Content-Length` and `Transfer-Encoding` are all rejected. Two
  servers that read the same bytes differently is what makes request smuggling
  possible, so ambiguity is an error rather than a guess.
- **A parse error ends the connection.** After one, the framing of the stream
  can no longer be trusted. The error response is sent first, with a lingering
  close so the client does not lose it to a TCP reset.
- **Everything is bounded.** `Limits` covers request line, header bytes and
  count, body size (also for chunked), chunk-size lines, and four timeouts:
  headers (slow-loris defence), body, idle and write. The deadline for headers
  covers the whole head, so dripping one byte at a time does not help.
- **Safe paths.** The router decodes segments and answers 400 for `.`, `..`,
  bad percent-escapes and control characters. The file server opens files
  through `os.Root`, so symlinks cannot lead outside the directory.
- **No header injection.** Response header names and values that contain
  forbidden characters are dropped.

## Layout

| File | Purpose |
|---|---|
| `errors.go`, `limits.go` | `ParseError` and sentinel errors; `Limits` and defaults |
| `header.go`, `request.go` | header map and field parsing; request-line and head parsing (`ReadRequest`) |
| `body.go`, `chunked.go` | body framing rules; the chunked decoder |
| `response.go` | `ResponseWriter`, framing choice, status texts |
| `conn.go`, `server.go` | one connection's life cycle; `Server`, `Serve`, `Shutdown` |
| `router.go`, `fileserver.go` | routing; static files |
| `cmd/httpd/` | demo server: flags, transports, JSON key-value API |
| `interop/` | script and clients that test the server with other people's HTTP software |
| `testdata/requests/` | corpus of raw requests and `expect.json` (see below) |

## Testing

```sh
go vet ./...
go test -race ./...                         # everything, about 6 seconds
go test -run TestCorpus -v ./...            # the raw-request corpus
go test -run '^$' -bench . -benchmem ./     # benchmarks against net/http
```

Fuzz targets run one at a time:

```sh
go test -run '^$' -fuzz FuzzReadRequest -fuzztime 30s ./
```

Targets: `FuzzReadRequest`, `FuzzParseHeaderLine`, `FuzzChunked`,
`FuzzRequestVsStdlib`, `FuzzRouterPath`, `FuzzServeConn`. Several compare the
result with `net/http`, so a disagreement between the two is reported. Inputs
that crash a target are saved under `testdata/fuzz/` and then run as ordinary
tests.

The test suite has five layers:

1. **Unit tests** for each file (parser, headers, bodies, chunked decoder,
   response writer, router, file server).
2. **A corpus** of 256 raw requests in `testdata/requests/{valid,invalid}`,
   byte for byte, with the expected outcome of each in `expect.json`. They are
   checked both against the parser and over a real connection. Regenerate them
   with `python3 gen_corpus.py` in that directory; it recreates `valid/` and
   `invalid/`. The `.gitattributes` there keeps CRLFs intact.
3. **Fuzzing**, seeded from the corpus.
4. **System tests** (`system_test.go`): every transport, many concurrent
   clients, requests delivered byte by byte, slow-loris, smuggling attempts,
   shutdown under load, clients that vanish or never read, connection limits.
5. **Interoperability tests** (`interop/`), described next.

## Interoperability

Tests written by the author of a parser share the author's misreadings. To
catch those, `run.sh` starts `cmd/httpd` and talks to it with HTTP
software written by other people:

```sh
interop/run.sh
```

| Client | What it checks |
|---|---|
| curl | GET, POST, chunked POST, PUT/GET/DELETE, download with SHA-256, `Range`, keep-alive (one connection for two URLs), 404, path traversal, HTTP/1.0; over TCP, a Unix socket and TLS |
| wget | GET and POST |
| nc, `openssl s_client` | hand-typed bytes over TCP and TLS, including a missing `Host` and `Content-Length` together with `Transfer-Encoding` (both must give 400) |
| Python `requests`, `http.client` | the same scenarios, chunked upload from a generator, three requests on one connection, an unknown method |
| Node `http`, `fetch` | chunked and sized uploads, five requests on one socket |
| Ruby `Net::HTTP` | sized and chunked POST, JSON store, twenty requests on one connection |

Clients that are not installed are skipped, and the script ends with a count
of passed, failed and skipped checks. On Linux and macOS it needs `bash`, `go`
and whichever of the clients above exist (`apt install curl wget netcat-openbsd
openssl`, or `brew install` the same).

To try other platforms or clients by hand, start the server on a reachable
address and point the client at it:

```sh
go run ./cmd/httpd -addr 0.0.0.0:8080 -root ./testdata -list   # find your IP with `ip a` or `ipconfig`
```

```powershell
# Windows PowerShell
Invoke-WebRequest http://SERVER_IP:8080/hello/Ola
Invoke-RestMethod -Method Put -Body '{"a":1}' http://SERVER_IP:8080/kv/test
curl.exe -v http://SERVER_IP:8080/headers          # curl.exe ships with Windows 10+
```

A browser is a good client too (Firefox, Chrome, Safari): open
`http://SERVER_IP:8080/files/` with `-list`, and check the Network tab for
keep-alive and chunked responses. For load, `ab -n 10000 -c 50 URL`,
`wrk -t2 -c50 -d10s URL` or `hey` work as they would against any server (use
`ab -k` to keep connections alive). To see the bytes themselves, capture on the
loopback interface in Wireshark with the filter `tcp.port == 8080`.

## Further reading

The RFCs this is built on are 9110 and 9112. Good places to start in 9112 are
section 2 (message format), section 6 (message body and framing, including the
smuggling rules in 6.3) and section 9 (connection management).
