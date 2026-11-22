package http1

import (
	"bufio"
	"errors"
	"io"
	"strconv"
	"strings"
)

// framing says how the end of a request body is found (RFC 9112 §6.3).
type framing int

const (
	framingNone    framing = iota // No body.
	framingLength                 // Exactly Content-Length bytes.
	framingChunked                // Chunked transfer coding.
)

// bodyFraming decides how the body of a request with headers h is delimited.
// Anything ambiguous is rejected: disagreeing parsers on body length are the
// root cause of request smuggling. maxBody of zero disables the size check.
func bodyFraming(h Header, maxBody int64) (framing, int64, error) {
	te, cl := h.Values("Transfer-Encoding"), h.Values("Content-Length")

	if len(te) > 0 {
		if len(cl) > 0 {
			return framingNone, 0, ErrContentLengthWithChunked
		}
		if !isChunkedOnly(te) {
			return framingNone, 0, ErrUnsupportedTransferEncoding
		}
		return framingChunked, 0, nil
	}

	if len(cl) == 0 {
		return framingNone, 0, nil
	}
	n, err := parseContentLength(cl)
	if err != nil {
		return framingNone, 0, err
	}
	if maxBody > 0 && n > maxBody {
		return framingNone, 0, ErrBodyTooLarge
	}
	return framingLength, n, nil
}

// isChunkedOnly reports whether the Transfer-Encoding values list exactly one
// coding and it is "chunked". Other codings (gzip, ...) are not supported, and
// chunked must not be applied twice (RFC 9112 §6.1).
func isChunkedOnly(values []string) bool {
	count := 0
	for _, v := range values {
		for _, coding := range strings.Split(v, ",") {
			if !strings.EqualFold(strings.Trim(coding, " \t"), "chunked") {
				return false
			}
			count++
		}
	}
	return count == 1
}

// parseContentLength accepts repeated headers and comma-separated lists only
// if every value is identical (RFC 9110 §8.6). Each value must be plain ASCII
// digits: no sign, no whitespace inside, no overflow.
func parseContentLength(values []string) (int64, error) {
	var n int64
	first := true
	for _, v := range values {
		for _, part := range strings.Split(v, ",") {
			part = strings.Trim(part, " \t")
			if part == "" || strings.Trim(part, "0123456789") != "" {
				return 0, ErrInvalidContentLength
			}
			x, err := strconv.ParseInt(part, 10, 64)
			if err != nil {
				return 0, ErrInvalidContentLength
			}
			if !first && x != n {
				return 0, ErrConflictingContentLength
			}
			n, first = x, false
		}
	}
	return n, nil
}

// lengthBody reads exactly remaining bytes from r and then reports io.EOF.
// It never reads past the body, so r stays positioned at the next request.
type lengthBody struct {
	r         *bufio.Reader
	remaining int64
}

func (b *lengthBody) Read(p []byte) (int, error) {
	if b.remaining == 0 {
		return 0, io.EOF
	}
	if int64(len(p)) > b.remaining {
		p = p[:b.remaining]
	}
	n, err := b.r.Read(p)
	b.remaining -= int64(n)
	if err == io.EOF && b.remaining > 0 {
		err = ErrTruncatedBody
	}
	return n, err
}

// emptyBody is the body of a request that has none.
type emptyBody struct{}

func (emptyBody) Read([]byte) (int, error) { return 0, io.EOF }

// errBodyNotDrained means the unread rest of a body was too large to skip.
var errBodyNotDrained = errors.New("http1: unread request body too large to discard")

// discardBody drops what is left of body so the next request can be parsed
// from the same connection. If more than max bytes remain it gives up with
// errBodyNotDrained, and the connection must be closed instead of reused.
func discardBody(body io.Reader, max int64) error {
	if body == nil {
		return nil
	}
	n, err := io.Copy(io.Discard, io.LimitReader(body, max+1))
	if err != nil {
		return err
	}
	if n > max {
		return errBodyNotDrained
	}
	return nil
}

// newBody returns the reader for the request body described by h. It never
// returns nil: a request without a body gets one that is immediately at EOF.
func newBody(r *bufio.Reader, h Header, lim Limits) (io.Reader, error) {
	kind, n, err := bodyFraming(h, lim.MaxBodyBytes)
	if err != nil {
		return nil, err
	}
	switch {
	case kind == framingChunked:
		return &chunkedBody{r: r, lim: lim}, nil
	case kind == framingLength && n > 0:
		return &lengthBody{r: r, remaining: n}, nil
	}
	return emptyBody{}, nil
}
