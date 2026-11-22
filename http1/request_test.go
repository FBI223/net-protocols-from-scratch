package http1

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

func read(t *testing.T, raw string, lim Limits) (*Request, error) {
	t.Helper()
	return ReadRequest(bufio.NewReader(strings.NewReader(raw)), lim)
}

func TestReadRequestValid(t *testing.T) {
	tests := []struct {
		name, raw                 string
		method, path, query, host string
		major, minor              int
	}{
		{"simple", "GET /a/b?x=1&y=2 HTTP/1.1\r\nHost: example.com\r\n\r\n",
			"GET", "/a/b", "x=1&y=2", "example.com", 1, 1},
		{"leading empty line", "\r\nGET / HTTP/1.1\r\nHost: h\r\n\r\n",
			"GET", "/", "", "h", 1, 1},
		{"http/1.0 without host", "GET / HTTP/1.0\r\n\r\n",
			"GET", "/", "", "", 1, 0},
		{"case and spaces", "GET / HTTP/1.1\r\nhOsT:   example.com  \t\r\n\r\n",
			"GET", "/", "", "example.com", 1, 1},
		{"asterisk", "OPTIONS * HTTP/1.1\r\nHost: h\r\n\r\n",
			"OPTIONS", "*", "", "h", 1, 1},
		{"connect", "CONNECT example.com:443 HTTP/1.1\r\nHost: example.com:443\r\n\r\n",
			"CONNECT", "", "", "example.com:443", 1, 1},
		{"absolute form", "GET http://a.example/p?q=1 HTTP/1.1\r\nHost: ignored\r\n\r\n",
			"GET", "/p", "q=1", "a.example", 1, 1},
		{"absolute form no path", "GET HTTP://a.example HTTP/1.1\r\nHost: x\r\n\r\n",
			"GET", "/", "", "a.example", 1, 1},
		{"ipv6 host", "GET / HTTP/1.1\r\nHost: [::1]:8080\r\n\r\n",
			"GET", "/", "", "[::1]:8080", 1, 1},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			req, err := read(t, tc.raw, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if req.Method != tc.method || req.Path != tc.path || req.RawQuery != tc.query ||
				req.Host != tc.host || req.Major != tc.major || req.Minor != tc.minor {
				t.Errorf("got %+v", req)
			}
		})
	}
}

func TestReadRequestRepeatedHeaders(t *testing.T) {
	req, err := read(t, "GET / HTTP/1.1\r\nHost: h\r\nAccept: a\r\nAccept: b, c\r\n\r\n", DefaultLimits())
	if err != nil {
		t.Fatal(err)
	}
	if got := req.Header.Values("accept"); len(got) != 2 || got[0] != "a" || got[1] != "b, c" {
		t.Errorf("Accept = %q", got)
	}
}

func TestReadRequestInvalid(t *testing.T) {
	tests := []struct {
		name, raw string
		want      error
	}{
		{"bare CR in request line", "GET / HTTP/1.1\rHost: h\r\n\r\n", ErrBareCR},
		{"bare CR in header", "GET / HTTP/1.1\r\nHost: h\rX: y\r\n\r\n", ErrBareCR},
		{"bare LF", "GET / HTTP/1.1\nHost: h\n\n", ErrBareLF},
		{"bare LF in header", "GET / HTTP/1.1\r\nHost: h\n\r\n", ErrBareLF},
		{"no host", "GET / HTTP/1.1\r\n\r\n", ErrMissingHost},
		{"two host", "GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\n\r\n", ErrMultipleHost},
		{"empty host", "GET / HTTP/1.1\r\nHost:\r\n\r\n", ErrInvalidHost},
		{"host with slash", "GET / HTTP/1.1\r\nHost: a/b\r\n\r\n", ErrInvalidHost},
		{"http 2.0", "GET / HTTP/2.0\r\nHost: h\r\n\r\n", ErrUnsupportedVersion},
		{"http 1 without minor", "GET / HTTP/1\r\nHost: h\r\n\r\n", ErrMalformedVersion},
		{"lowercase http", "GET / http/1.1\r\nHost: h\r\n\r\n", ErrMalformedVersion},
		{"obs-fold", "GET / HTTP/1.1\r\nHost: h\r\nX: a\r\n b\r\n\r\n", ErrObsFold},
		{"space before colon", "GET / HTTP/1.1\r\nHost : h\r\n\r\n", ErrSpaceBeforeColon},
		{"NUL in value", "GET / HTTP/1.1\r\nHost: h\r\nX: a\x00b\r\n\r\n", ErrInvalidHeaderValue},
		{"two spaces", "GET  / HTTP/1.1\r\nHost: h\r\n\r\n", ErrMalformedRequestLine},
		{"missing version", "GET /\r\nHost: h\r\n\r\n", ErrMalformedRequestLine},
		{"extra part", "GET / HTTP/1.1 x\r\nHost: h\r\n\r\n", ErrMalformedRequestLine},
		{"bad method", "G(T / HTTP/1.1\r\nHost: h\r\n\r\n", ErrInvalidMethod},
		{"fragment in target", "GET /a#b HTTP/1.1\r\nHost: h\r\n\r\n", ErrInvalidTarget},
		{"asterisk with GET", "GET * HTTP/1.1\r\nHost: h\r\n\r\n", ErrInvalidTarget},
		{"relative target", "GET a/b HTTP/1.1\r\nHost: h\r\n\r\n", ErrInvalidTarget},
		{"two empty lines", "\r\n\r\nGET / HTTP/1.1\r\nHost: h\r\n\r\n", ErrMalformedRequestLine},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := read(t, tc.raw, DefaultLimits()); !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadRequestLimits(t *testing.T) {
	lim := Limits{MaxRequestLineBytes: 32, MaxHeaderBytes: 100, MaxHeaderCount: 2}
	tests := []struct {
		name, raw string
		want      error
	}{
		{"request line too long", "GET /" + strings.Repeat("a", 40) + " HTTP/1.1\r\n\r\n", ErrRequestLineTooLong},
		{"headers too large", "GET / HTTP/1.1\r\nHost: h\r\nX: " + strings.Repeat("a", 100) + "\r\n\r\n", ErrHeaderTooLarge},
		{"too many headers", "GET / HTTP/1.1\r\nHost: h\r\nA: 1\r\nB: 2\r\n\r\n", ErrTooManyHeaders},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := read(t, tc.raw, lim); !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestReadRequestEOF(t *testing.T) {
	if _, err := read(t, "", DefaultLimits()); err != io.EOF {
		t.Errorf("empty input: %v, want io.EOF", err)
	}
	for _, raw := range []string{"GET / HT", "GET / HTTP/1.1\r\n", "GET / HTTP/1.1\r\nHost: h\r\n"} {
		if _, err := read(t, raw, DefaultLimits()); err != io.ErrUnexpectedEOF {
			t.Errorf("%q: %v, want io.ErrUnexpectedEOF", raw, err)
		}
	}
}

// Two requests in one stream: the reader must stop exactly after the first
// blank line, so the second request (and any body) is left untouched.
func TestReadRequestPipelined(t *testing.T) {
	r := bufio.NewReader(strings.NewReader(
		"POST /a HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\n\r\nhello" +
			"GET /b HTTP/1.1\r\nHost: h\r\n\r\n"))
	req, err := ReadRequest(r, DefaultLimits())
	if err != nil || req.Path != "/a" {
		t.Fatalf("first: %v, %v", req, err)
	}
	if body, _ := r.Peek(5); string(body) != "hello" {
		t.Fatalf("reader not positioned at body: %q", body)
	}
	r.Discard(5)
	if req, err = ReadRequest(r, DefaultLimits()); err != nil || req.Path != "/b" {
		t.Fatalf("second: %v, %v", req, err)
	}
}
