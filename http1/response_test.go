package http1

import (
	"bufio"
	"bytes"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

var dateLine = regexp.MustCompile(`Date: [^\r]*\r\n`)

// respond runs fn as a handler for a request and returns the bytes written,
// with the Date header removed so outputs can be compared exactly.
func respond(method string, minor int, reqHeader Header, fn func(*ResponseWriter)) (string, *ResponseWriter, error) {
	var buf bytes.Buffer
	if reqHeader == nil {
		reqHeader = Header{}
	}
	req := &Request{Method: method, Major: 1, Minor: minor, Header: reqHeader}
	rw := newResponseWriter(bufio.NewWriter(&buf), req)
	fn(rw)
	err := rw.finish()
	return dateLine.ReplaceAllString(buf.String(), ""), rw, err
}

func TestResponseExact(t *testing.T) {
	tests := []struct {
		name   string
		method string
		fn     func(*ResponseWriter)
		want   string
	}{
		{"small body gets Content-Length", "GET",
			func(rw *ResponseWriter) { rw.Write([]byte("hello")) },
			"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"},
		{"empty handler", "GET",
			func(rw *ResponseWriter) {},
			"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"},
		{"status and headers sorted", "GET",
			func(rw *ResponseWriter) {
				rw.Header().Set("x-b", "2")
				rw.Header().Set("Content-Type", "text/plain")
				rw.Header().Add("X-A", "1")
				rw.Header().Add("X-A", "1b")
				rw.WriteHeader(404)
				rw.Write([]byte("not found"))
			},
			"HTTP/1.1 404 Not Found\r\nContent-Length: 9\r\nContent-Type: text/plain\r\nX-A: 1\r\nX-A: 1b\r\nX-B: 2\r\n\r\nnot found"},
		{"first WriteHeader wins", "GET",
			func(rw *ResponseWriter) { rw.WriteHeader(404); rw.WriteHeader(500) },
			"HTTP/1.1 404 Not Found\r\nContent-Length: 0\r\n\r\n"},
		{"header changes after WriteHeader are ignored", "GET",
			func(rw *ResponseWriter) { rw.WriteHeader(200); rw.Header().Set("X-Late", "1") },
			"HTTP/1.1 200 OK\r\nContent-Length: 0\r\n\r\n"},
		{"HEAD reports length, sends no body", "HEAD",
			func(rw *ResponseWriter) { rw.Write([]byte("hello")) },
			"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\n"},
		{"204 has no Content-Length", "GET",
			func(rw *ResponseWriter) { rw.Header().Set("Content-Length", "5"); rw.WriteHeader(204) },
			"HTTP/1.1 204 No Content\r\n\r\n"},
		{"304 has no body framing", "GET",
			func(rw *ResponseWriter) { rw.WriteHeader(304) },
			"HTTP/1.1 304 Not Modified\r\n\r\n"},
		{"declared Content-Length is used", "GET",
			func(rw *ResponseWriter) {
				rw.Header().Set("Content-Length", "5")
				rw.Write([]byte("hel"))
				rw.Write([]byte("lo"))
			},
			"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"},
		{"invalid declared Content-Length is replaced", "GET",
			func(rw *ResponseWriter) { rw.Header().Set("Content-Length", "abc"); rw.Write([]byte("hello")) },
			"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"},
		{"handler cannot set Transfer-Encoding", "GET",
			func(rw *ResponseWriter) { rw.Header().Set("Transfer-Encoding", "gzip"); rw.Write([]byte("hello")) },
			"HTTP/1.1 200 OK\r\nContent-Length: 5\r\n\r\nhello"},
		{"header injection is dropped", "GET",
			func(rw *ResponseWriter) {
				rw.Header().Set("X-Evil", "a\r\nInjected: yes")
				rw.Header().Set("Bad Name", "v")
				rw.Header().Set("X-Ok", "fine")
			},
			"HTTP/1.1 200 OK\r\nContent-Length: 0\r\nX-Ok: fine\r\n\r\n"},
		{"handler asks to close", "GET",
			func(rw *ResponseWriter) { rw.Header().Set("Connection", "close") },
			"HTTP/1.1 200 OK\r\nConnection: close\r\nContent-Length: 0\r\n\r\n"},
		{"unknown status has empty reason", "GET",
			func(rw *ResponseWriter) { rw.WriteHeader(299) },
			"HTTP/1.1 299 \r\nContent-Length: 0\r\n\r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, _, err := respond(tc.method, 1, nil, tc.fn)
			if err != nil || got != tc.want {
				t.Errorf("got %q, %v\nwant %q", got, err, tc.want)
			}
		})
	}
}

// splitResponse separates head and body of a serialized response.
func splitResponse(t *testing.T, out string) (head, body string) {
	t.Helper()
	head, body, ok := strings.Cut(out, "\r\n\r\n")
	if !ok {
		t.Fatalf("no end of head in %q", out)
	}
	return head, body
}

func decodeChunked(t *testing.T, body string) string {
	t.Helper()
	got, rest, err := decode(body, Limits{})
	if err != nil || rest != "" {
		t.Fatalf("invalid chunked body: err=%v rest=%q", err, rest)
	}
	return got
}

func TestResponseLargeBodyIsChunked(t *testing.T) {
	payload := strings.Repeat("x", 10000)
	out, rw, err := respond("GET", 1, nil, func(rw *ResponseWriter) { rw.Write([]byte(payload)) })
	if err != nil {
		t.Fatal(err)
	}
	head, body := splitResponse(t, out)
	if !strings.Contains(head, "Transfer-Encoding: chunked") || strings.Contains(head, "Content-Length") {
		t.Errorf("head = %q", head)
	}
	if decodeChunked(t, body) != payload {
		t.Error("body does not round-trip")
	}
	if rw.willClose() {
		t.Error("chunked response must keep the connection open")
	}
}

func TestResponseManySmallWritesBecomeChunked(t *testing.T) {
	var want strings.Builder
	out, _, err := respond("GET", 1, nil, func(rw *ResponseWriter) {
		for i := 0; i < 600; i++ {
			rw.Write([]byte("0123456789"))
			want.WriteString("0123456789")
		}
	})
	if err != nil {
		t.Fatal(err)
	}
	head, body := splitResponse(t, out)
	if !strings.Contains(head, "Transfer-Encoding: chunked") {
		t.Errorf("head = %q", head)
	}
	if decodeChunked(t, body) != want.String() {
		t.Error("body does not round-trip")
	}
}

func TestResponseFlushSwitchesToChunked(t *testing.T) {
	out, _, err := respond("GET", 1, nil, func(rw *ResponseWriter) {
		rw.Write([]byte("hello"))
		rw.Flush()
		rw.Write([]byte(" world"))
	})
	if err != nil {
		t.Fatal(err)
	}
	head, body := splitResponse(t, out)
	if !strings.Contains(head, "Transfer-Encoding: chunked") {
		t.Errorf("head = %q", head)
	}
	if decodeChunked(t, body) != "hello world" {
		t.Errorf("body = %q", body)
	}
}

func TestResponseHTTP10ClosesInsteadOfChunking(t *testing.T) {
	payload := strings.Repeat("x", 10000)
	out, rw, err := respond("GET", 0, nil, func(rw *ResponseWriter) { rw.Write([]byte(payload)) })
	if err != nil {
		t.Fatal(err)
	}
	head, body := splitResponse(t, out)
	if strings.Contains(head, "Transfer-Encoding") || !strings.Contains(head, "Connection: close") {
		t.Errorf("head = %q", head)
	}
	if body != payload || !rw.willClose() {
		t.Errorf("body ok=%v willClose=%v", body == payload, rw.willClose())
	}
}

func TestResponseConnectionClose(t *testing.T) {
	out, rw, _ := respond("GET", 1, hdr("Connection", "keep-alive, close"), func(rw *ResponseWriter) {})
	if !rw.willClose() || !strings.Contains(out, "Connection: close\r\n") {
		t.Errorf("willClose=%v out=%q", rw.willClose(), out)
	}
	out, rw, _ = respond("GET", 1, nil, func(rw *ResponseWriter) {})
	if rw.willClose() || strings.Contains(out, "Connection") {
		t.Errorf("keep-alive expected: willClose=%v out=%q", rw.willClose(), out)
	}
}

func TestResponseBodyNotAllowed(t *testing.T) {
	_, _, err := respond("GET", 1, nil, func(rw *ResponseWriter) {
		rw.WriteHeader(204)
		if _, err := rw.Write([]byte("x")); !errors.Is(err, ErrBodyNotAllowed) {
			t.Errorf("Write error = %v", err)
		}
	})
	if err != nil {
		t.Errorf("finish after rejected write: %v", err)
	}
}

func TestResponseContentLengthViolations(t *testing.T) {
	out, rw, err := respond("GET", 1, nil, func(rw *ResponseWriter) {
		rw.Header().Set("Content-Length", "3")
		if _, err := rw.Write([]byte("hello")); !errors.Is(err, ErrContentLengthExceeded) {
			t.Errorf("Write error = %v", err)
		}
	})
	if !errors.Is(err, ErrContentLengthExceeded) || out != "" || !rw.willClose() || rw.committed() {
		t.Errorf("exceeded: out=%q err=%v close=%v committed=%v", out, err, rw.willClose(), rw.committed())
	}

	_, rw, err = respond("GET", 1, nil, func(rw *ResponseWriter) {
		rw.Header().Set("Content-Length", "10")
		rw.Write([]byte("hello"))
	})
	if !errors.Is(err, ErrContentLengthMismatch) || !rw.willClose() {
		t.Errorf("short: err=%v close=%v", err, rw.willClose())
	}
}

func TestResponseInvalidStatusPanics(t *testing.T) {
	for _, code := range []int{0, 100, 199, 600, -1} {
		func() {
			defer func() {
				if recover() == nil {
					t.Errorf("WriteHeader(%d) did not panic", code)
				}
			}()
			rw := newResponseWriter(bufio.NewWriter(&bytes.Buffer{}), &Request{Method: "GET", Minor: 1})
			rw.WriteHeader(code)
		}()
	}
}

func TestResponseDate(t *testing.T) {
	var buf bytes.Buffer
	rw := newResponseWriter(bufio.NewWriter(&buf), &Request{Method: "GET", Minor: 1, Header: Header{}})
	rw.finish()
	m := regexp.MustCompile(`Date: ([^\r]*)\r\n`).FindStringSubmatch(buf.String())
	if m == nil {
		t.Fatalf("no Date header in %q", buf.String())
	}
	d, err := time.Parse(timeFormat, m[1])
	if err != nil || time.Since(d) > time.Minute || time.Since(d) < -time.Minute {
		t.Errorf("Date = %q (%v)", m[1], err)
	}

	buf.Reset()
	rw = newResponseWriter(bufio.NewWriter(&buf), &Request{Method: "GET", Minor: 1, Header: Header{}})
	rw.Header().Set("Date", "custom")
	rw.finish()
	if !strings.Contains(buf.String(), "Date: custom\r\n") {
		t.Errorf("handler Date overridden: %q", buf.String())
	}
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

func TestResponseWriteErrorIsSticky(t *testing.T) {
	rw := newResponseWriter(bufio.NewWriterSize(failingWriter{}, 16), &Request{Method: "GET", Minor: 1, Header: Header{}})
	if _, err := rw.Write(make([]byte, 10000)); err == nil {
		t.Fatal("expected write error")
	}
	if _, err := rw.Write([]byte("x")); err == nil {
		t.Error("error not sticky")
	}
	if rw.finish() == nil || !rw.willClose() {
		t.Error("finish must fail and request close")
	}
}

func TestWriteErrorResponse(t *testing.T) {
	var buf bytes.Buffer
	if err := writeErrorResponse(bufio.NewWriter(&buf), 414); err != nil {
		t.Fatal(err)
	}
	got := dateLine.ReplaceAllString(buf.String(), "")
	if want := "HTTP/1.1 414 URI Too Long\r\nContent-Length: 0\r\nConnection: close\r\n\r\n"; got != want {
		t.Errorf("got %q", got)
	}
}

func TestStatusText(t *testing.T) {
	if StatusText(404) != "Not Found" || StatusText(299) != "" {
		t.Error("StatusText")
	}
	// Every status the parser can ask for must have a reason phrase.
	for _, pe := range []*ParseError{ErrBareCR, ErrUnsupportedVersion, ErrRequestLineTooLong, ErrHeaderTooLarge,
		ErrBodyTooLarge, ErrReadTimeout, ErrUnsupportedTransferEncoding} {
		if StatusText(pe.Status) == "" {
			t.Errorf("no reason phrase for %d", pe.Status)
		}
	}
}
