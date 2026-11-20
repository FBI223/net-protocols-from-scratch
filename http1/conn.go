// One connection: read a request, call the handler, write the response, decide
// whether the connection stays open (RFC 9112 section 9.3), and apply the read and
// write deadlines.

package http1
