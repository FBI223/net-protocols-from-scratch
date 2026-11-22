package http1

import "errors"

// ParseError is a request the server must reject. Every ParseError is fatal
// for the connection: send Status (best effort), then close, because the
// position of the next request in the stream is unknown.
//
// Compare with errors.Is against the sentinels below, not by Reason.
type ParseError struct {
	Status int    // HTTP status to send before closing.
	Reason string // For logs only; never echoed to the client.
}

// Error implements the error interface.
func (e *ParseError) Error() string { return "http1: " + e.Reason }

func newParseError(status int, reason string) *ParseError {
	return &ParseError{Status: status, Reason: reason}
}

// Request line (RFC 9112 §3).
var (
	ErrMalformedRequestLine = newParseError(400, "malformed request line")
	ErrInvalidMethod        = newParseError(400, "invalid request method")
	ErrInvalidTarget        = newParseError(400, "invalid request target")
	ErrMalformedVersion     = newParseError(400, "malformed HTTP version")
	ErrUnsupportedVersion   = newParseError(505, "HTTP version not supported")
)

// Line endings (RFC 9112 §2.2). Both are rejected, although the RFC permits
// leniency, to avoid parsing differentials against stricter intermediaries.
var (
	ErrBareCR = newParseError(400, "bare CR in message")
	ErrBareLF = newParseError(400, "bare LF in message")
)

// Header fields (RFC 9112 §3.2, §5; RFC 9110 §7.2).
var (
	ErrInvalidHeaderName  = newParseError(400, "invalid header field name")
	ErrInvalidHeaderValue = newParseError(400, "invalid header field value")
	ErrSpaceBeforeColon   = newParseError(400, "whitespace before colon in header")
	ErrObsFold            = newParseError(400, "obsolete line folding")
	ErrMissingHost        = newParseError(400, "missing Host header")
	ErrMultipleHost       = newParseError(400, "multiple Host headers")
	ErrInvalidHost        = newParseError(400, "invalid Host header")
)

// Message framing (RFC 9112 §6, §7). Ambiguous body length is a request
// smuggling vector, so each case is rejected rather than repaired.
var (
	ErrInvalidContentLength        = newParseError(400, "invalid Content-Length")
	ErrConflictingContentLength    = newParseError(400, "conflicting Content-Length values")
	ErrContentLengthWithChunked    = newParseError(400, "both Content-Length and Transfer-Encoding")
	ErrUnsupportedTransferEncoding = newParseError(501, "unsupported Transfer-Encoding")
	ErrInvalidChunkSize            = newParseError(400, "invalid chunk size")
	ErrMalformedChunk              = newParseError(400, "malformed chunked encoding")
	ErrTruncatedBody               = newParseError(400, "message body truncated")
)

// Limits exceeded (see Limits).
var (
	ErrRequestLineTooLong = newParseError(414, "request line too long")
	ErrHeaderTooLarge     = newParseError(431, "header section too large")
	ErrTooManyHeaders     = newParseError(431, "too many header fields")
	ErrBodyTooLarge       = newParseError(413, "request body too large")
	ErrChunkLineTooLong   = newParseError(400, "chunk line too long")
)

// ErrReadTimeout means the client did not finish the header in time.
var ErrReadTimeout = newParseError(408, "request timeout")

// StatusCode returns the status to send for err. ok is false when err is not
// a ParseError (connection reset, clean EOF between requests); the caller
// should then just close the connection.
func StatusCode(err error) (status int, ok bool) {
	var pe *ParseError
	if errors.As(err, &pe) {
		return pe.Status, true
	}
	return 0, false
}
