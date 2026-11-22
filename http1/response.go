package http1

import (
	"bufio"
	"errors"
	"sort"
	"strconv"
	"time"
)

const (
	// timeFormat is the IMF-fixdate format of RFC 9110 §5.6.7.
	timeFormat = "Mon, 02 Jan 2006 15:04:05 GMT"

	// bufferedBodyLimit is how much of the body is held back before the
	// headers are sent. A handler that finishes within it gets an exact
	// Content-Length; one that exceeds it, or calls Flush, gets chunked.
	bufferedBodyLimit = 4 << 10
)

var (
	// ErrBodyNotAllowed is returned by Write for 204 and 304 responses.
	ErrBodyNotAllowed = errors.New("http1: response status does not allow a body")

	// ErrContentLengthExceeded is returned by Write when the handler writes
	// more than the Content-Length it declared.
	ErrContentLengthExceeded = errors.New("http1: write exceeds declared Content-Length")

	// ErrContentLengthMismatch is reported when a handler returns after
	// writing less than the Content-Length it declared.
	ErrContentLengthMismatch = errors.New("http1: response shorter than declared Content-Length")
)

// bodyMode says how the response body is delimited on the wire.
type bodyMode int

const (
	modeNone       bodyMode = iota // No body is sent (HEAD, 204, 304).
	modeLength                     // Exactly declared bytes.
	modeChunked                    // Chunked transfer coding.
	modeUntilClose                 // Ends when the connection closes.
)

// ResponseWriter builds one response. Handlers set headers with Header, may
// call WriteHeader once, and then Write the body. Framing (Content-Length,
// chunking, Connection, Date) is decided here, so handlers cannot produce an
// ambiguous message.
//
// A ResponseWriter is not safe for concurrent use.
type ResponseWriter struct {
	w   *bufio.Writer
	req *Request

	header   Header // Mutable by the handler until WriteHeader.
	snapshot Header // Copy taken by WriteHeader; this is what gets sent.
	status   int    // Zero until WriteHeader.
	declared int64  // Content-Length from the handler or computed; -1 if unknown.

	pending []byte // Body held back until the headers are sent.
	sent    bool   // The head is in the output buffer.
	mode    bodyMode
	written int64 // Body bytes accepted from the handler.
	close   bool  // The connection must not be reused afterwards.
	err     error // Sticky.
}

// newResponseWriter returns a writer for a response to req. The connection is
// marked for closing up front when the request asks for it, or when the client
// speaks HTTP/1.0 (keep-alive for 1.0 is not supported).
func newResponseWriter(w *bufio.Writer, req *Request) *ResponseWriter {
	return &ResponseWriter{
		w:        w,
		req:      req,
		header:   make(Header),
		declared: -1,
		close:    req.Minor == 0 || req.Header.hasToken("Connection", "close"),
	}
}

// Header returns the header map that will be sent by WriteHeader. Changes
// made after WriteHeader or the first Write have no effect.
func (rw *ResponseWriter) Header() Header { return rw.header }

// WriteHeader records the status code and freezes the headers. Only the
// first call has an effect; Write calls it with 200 if the handler did not.
// It panics for codes outside 200-599: interim 1xx responses are sent by the
// connection, not by handlers.
func (rw *ResponseWriter) WriteHeader(code int) {
	if rw.status != 0 {
		return
	}
	if code < 200 || code > 599 {
		panic("http1: invalid status code " + strconv.Itoa(code))
	}
	rw.status = code
	rw.snapshot = rw.header.Clone()

	if values := rw.snapshot.Values("Content-Length"); len(values) > 0 {
		if n, err := parseContentLength(values); err == nil {
			rw.declared = n
		} else {
			rw.snapshot.Del("Content-Length")
		}
	}
	if rw.snapshot.hasToken("Connection", "close") {
		rw.close = true
	}
}

// Write implements io.Writer. The body is held back until it exceeds
// bufferedBodyLimit or the handler calls Flush, which is what allows an exact
// Content-Length for small responses.
func (rw *ResponseWriter) Write(p []byte) (int, error) {
	if rw.err != nil {
		return 0, rw.err
	}
	if rw.status == 0 {
		rw.WriteHeader(200)
	}
	if !rw.bodyAllowed() {
		return 0, ErrBodyNotAllowed
	}
	if rw.declared >= 0 && rw.written+int64(len(p)) > rw.declared {
		return 0, rw.fail(ErrContentLengthExceeded)
	}

	// A HEAD response has the headers of the GET response but no body, so
	// the bytes are only counted to be able to report Content-Length.
	if rw.req.Method == "HEAD" {
		rw.written += int64(len(p))
		return len(p), nil
	}

	if !rw.sent {
		if len(rw.pending)+len(p) <= bufferedBodyLimit {
			rw.pending = append(rw.pending, p...)
			rw.written += int64(len(p))
			return len(p), nil
		}
		if err := rw.commit(false); err != nil {
			return 0, err
		}
	}
	if err := rw.emit(p); err != nil {
		return 0, rw.fail(err)
	}
	rw.written += int64(len(p))
	return len(p), nil
}

// Flush sends the headers and any buffered body to the client, switching to
// chunked encoding if the length is not known yet.
func (rw *ResponseWriter) Flush() error {
	if rw.err != nil {
		return rw.err
	}
	if rw.status == 0 {
		rw.WriteHeader(200)
	}
	if !rw.sent {
		if err := rw.commit(false); err != nil {
			return err
		}
	}
	if err := rw.w.Flush(); err != nil {
		return rw.fail(err)
	}
	return nil
}

// finish completes the response after the handler returned and flushes it.
// A non-nil error means the connection must be closed.
func (rw *ResponseWriter) finish() error {
	if rw.err != nil {
		return rw.err
	}
	if rw.status == 0 {
		rw.WriteHeader(200)
	}
	if !rw.sent {
		if err := rw.commit(true); err != nil {
			return err
		}
	}
	switch rw.mode {
	case modeLength:
		if rw.written != rw.declared {
			return rw.fail(ErrContentLengthMismatch)
		}
	case modeChunked:
		rw.w.WriteString("0\r\n\r\n")
	}
	if err := rw.w.Flush(); err != nil {
		return rw.fail(err)
	}
	return nil
}

// willClose reports whether the connection must be closed after this
// response. It is final only once finish has returned.
func (rw *ResponseWriter) willClose() bool { return rw.close }

// committed reports whether any part of the response has been sent, which
// decides whether a handler panic can still be answered with a 500.
func (rw *ResponseWriter) committed() bool { return rw.sent }

func (rw *ResponseWriter) bodyAllowed() bool {
	return rw.status != 204 && rw.status != 304
}

func (rw *ResponseWriter) fail(err error) error {
	rw.err, rw.close = err, true
	return err
}

// commit chooses the framing and writes the status line and headers. final
// tells whether the handler is done, which means the body length is known.
func (rw *ResponseWriter) commit(final bool) error {
	h := rw.snapshot
	h.Del("Transfer-Encoding") // Framing is decided here, never by the handler.

	switch {
	case !rw.bodyAllowed():
		rw.mode = modeNone
		if rw.status == 204 {
			h.Del("Content-Length") // RFC 9110 §8.6: MUST NOT be sent.
		}
	case rw.req.Method == "HEAD":
		rw.mode = modeNone
		if rw.declared < 0 && final {
			h.Set("Content-Length", strconv.FormatInt(rw.written, 10))
		}
	case rw.declared >= 0:
		rw.mode = modeLength
	case final:
		rw.mode, rw.declared = modeLength, int64(len(rw.pending))
		h.Set("Content-Length", strconv.FormatInt(rw.declared, 10))
	case rw.req.Minor >= 1:
		rw.mode = modeChunked
		h.Set("Transfer-Encoding", "chunked")
	default:
		// HTTP/1.0 cannot decode chunked: the end of the body is the end
		// of the connection.
		rw.mode, rw.close = modeUntilClose, true
	}

	if rw.close {
		h.Set("Connection", "close")
	}
	if len(h.Values("Date")) == 0 {
		h.Set("Date", time.Now().UTC().Format(timeFormat))
	}
	rw.writeHead(h)
	rw.sent = true

	if len(rw.pending) > 0 {
		err := rw.emit(rw.pending)
		rw.pending = nil
		if err != nil {
			return rw.fail(err)
		}
	}
	return nil
}

// writeHead writes the status line and headers in sorted order. Fields with
// an invalid name or value are dropped rather than sent: a CR or LF in a
// value would otherwise let handler data inject headers or a second response.
// Write errors are not checked here; bufio.Writer keeps them and reports
// them on the next Write or Flush.
func (rw *ResponseWriter) writeHead(h Header) {
	w := rw.w
	w.WriteString("HTTP/1.1 " + strconv.Itoa(rw.status) + " " + StatusText(rw.status) + "\r\n")

	keys := make([]string, 0, len(h))
	for k := range h {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, k := range keys {
		if !isToken(k) {
			continue
		}
		for _, v := range h[k] {
			if isFieldValue([]byte(v)) {
				w.WriteString(k + ": " + v + "\r\n")
			}
		}
	}
	w.WriteString("\r\n")
}

// emit writes body bytes with the framing chosen by commit.
func (rw *ResponseWriter) emit(p []byte) error {
	switch rw.mode {
	case modeLength, modeUntilClose:
		_, err := rw.w.Write(p)
		return err
	case modeChunked:
		if len(p) == 0 {
			return nil // A zero-length chunk would end the body.
		}
		_, err := rw.w.WriteString(strconv.FormatInt(int64(len(p)), 16) + "\r\n")
		if err == nil {
			_, err = rw.w.Write(p)
		}
		if err == nil {
			_, err = rw.w.WriteString("\r\n")
		}
		return err
	}
	return nil
}

// writeErrorResponse sends a minimal response without a body, used for
// requests the server rejected before a handler could run. The caller must
// close the connection afterwards.
func writeErrorResponse(w *bufio.Writer, status int) error {
	w.WriteString("HTTP/1.1 " + strconv.Itoa(status) + " " + StatusText(status) + "\r\n")
	w.WriteString("Date: " + time.Now().UTC().Format(timeFormat) + "\r\n")
	w.WriteString("Content-Length: 0\r\nConnection: close\r\n\r\n")
	return w.Flush()
}

// StatusText returns the reason phrase for code, or "" if it is unknown.
func StatusText(code int) string { return statusText[code] }

var statusText = map[int]string{
	200: "OK",
	201: "Created",
	202: "Accepted",
	204: "No Content",
	206: "Partial Content",
	301: "Moved Permanently",
	302: "Found",
	303: "See Other",
	304: "Not Modified",
	307: "Temporary Redirect",
	308: "Permanent Redirect",
	400: "Bad Request",
	401: "Unauthorized",
	403: "Forbidden",
	404: "Not Found",
	405: "Method Not Allowed",
	408: "Request Timeout",
	409: "Conflict",
	410: "Gone",
	411: "Length Required",
	412: "Precondition Failed",
	413: "Content Too Large",
	414: "URI Too Long",
	415: "Unsupported Media Type",
	416: "Range Not Satisfiable",
	417: "Expectation Failed",
	422: "Unprocessable Content",
	429: "Too Many Requests",
	431: "Request Header Fields Too Large",
	500: "Internal Server Error",
	501: "Not Implemented",
	502: "Bad Gateway",
	503: "Service Unavailable",
	504: "Gateway Timeout",
	505: "HTTP Version Not Supported",
	507: "Insufficient Storage",
}
