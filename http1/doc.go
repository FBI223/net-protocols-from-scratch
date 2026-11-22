// Package http1 is an HTTP/1.1 server written from scratch on top of net.Conn,
// following RFC 9110 (semantics) and RFC 9112 (message syntax).
//
// It is a learning project with production habits: the parser is strict, every
// resource is bounded, and the failure modes of the protocol (request
// smuggling, slow clients, oversized input) are handled on purpose and covered
// by tests. It speaks HTTP/1.1 only; TLS comes from wrapping the listener.
//
// # Quick start
//
//	rt := &http1.Router{}
//	rt.HandleFunc("GET", "/hello/{name}", func(w *http1.ResponseWriter, r *http1.Request) {
//		fmt.Fprintf(w, "Hello, %s!\n", r.PathValue("name"))
//	})
//
//	srv := &http1.Server{Addr: ":8080", Handler: rt}
//	log.Fatal(srv.ListenAndServe())
//
// Use Serve to bring your own listener: a Unix socket, or a TLS listener from
// crypto/tls.NewListener. Shutdown stops accepting, closes idle connections
// and waits for running requests; Close drops everything at once.
//
// # Parsing
//
// ReadRequest turns bytes into a Request and is usable without a Server. It
// accepts only what the RFCs define, and rejects the rest with a *ParseError
// that carries the HTTP status to answer with (see StatusCode). Rejected:
// bare CR or LF, obsolete line folding, whitespace before the colon, missing
// or repeated Host, invalid Content-Length, and any message that has both
// Content-Length and Transfer-Encoding. A parse error always ends the
// connection, because after it the framing of the stream cannot be trusted.
//
// Request bodies are streams: Content-Length and chunked bodies both arrive
// as an io.Reader, and Limits.MaxBodyBytes applies to both. What a handler
// leaves unread is drained before the next request, up to a small bound.
//
// # Responses
//
// A ResponseWriter chooses the framing itself. A response that fits the
// internal buffer is sent with Content-Length; a larger one, or one that is
// flushed, is sent chunked; for HTTP/1.0 clients it is delimited by closing
// the connection. Header names and values that could inject a second header
// or response are dropped. HEAD, 204 and 304 never carry a body, and a
// handler panic becomes a 500 when nothing has been sent yet.
//
// # Limits
//
// Limits bounds what one connection can cost: request line, header bytes and
// count, body size, chunk-size lines, and four timeouts (headers, body,
// idle, write). The zero Limits of a Server means DefaultLimits. A zero field
// in a Limits you pass to ReadRequest disables that check.
//
// # Routing and files
//
// Router matches a method and a pattern with {name} and {name...} wildcards,
// on decoded path segments. Dot segments, bad percent-escapes and control
// characters are answered with 400 before any handler runs, and 404, 405
// (with Allow), HEAD and OPTIONS are handled automatically.
//
// FileServer serves a directory with content types, conditional requests
// (ETag, Last-Modified), single byte ranges, and optional listings. It opens
// files through os.Root, so a symlink cannot lead out of the directory.
//
// # Testing
//
// Besides unit tests the package has a corpus of raw requests in
// testdata/requests (see gen_corpus.py there), fuzz targets that compare the
// parser with net/http, and system tests over TCP, Unix sockets and TLS. The
// command cmd/httpd is a runnable demo.
package http1
