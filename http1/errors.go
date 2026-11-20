// Errors and their HTTP status codes: 400 for a malformed message, 414 for a request
// line over the limit, 431 for a header section over the limit, 501 for an unknown
// transfer coding, 505 for an unsupported HTTP version.
//
// A parse failure is a value the server can turn into a response, not a string.

package http1
