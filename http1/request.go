// Request and ReadRequest: the request line, the target forms (RFC 9112 section
// 3.2), the Host rule, and the header section (section 5). Reads from a
// *bufio.Reader and never reads past the end of the message.

package http1
