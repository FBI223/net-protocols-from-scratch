// Package http1 implements HTTP/1.1 message parsing, body framing and a small
// server from RFC 9112 (syntax and framing) and RFC 9110 (semantics), without
// net/http.
//
// The package is built in layers. Each layer uses only the ones listed before
// it, so the lower layers can be tested without a network:
//
//  1. errors.go, limits.go, header.go: values and rules, no I/O.
//  2. request.go, response.go: the start line and the header section, read from
//     a bufio.Reader and written to an io.Writer.
//  3. body.go, chunked.go: where a message body ends (RFC 9112 section 6.3), and
//     the chunked coding (section 7.1).
//  4. conn.go, router.go, server.go: sockets, deadlines, keep-alive, routing.
//
// Everything that arrives from the network is untrusted: lengths are bounded
// before memory is allocated, and a message that cannot be framed with
// certainty is rejected, not repaired (RFC 9112 section 11.2).
//
// Status: under development. README.md says what is built and what is verified.
package http1
