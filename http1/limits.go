// Limits on untrusted input: request line, header bytes, number of fields, body
// size, and the timeouts of a connection. One struct with defaults, so every
// bound is in one place and every one has a test.
//
// RFC 9112 section 3 recommends supporting request lines of at least 8000 octets.

package http1
