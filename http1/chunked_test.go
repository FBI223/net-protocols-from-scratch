package http1

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
	"testing/iotest"
)

// decode reads a whole chunked body one byte at a time, which exercises
// every state transition of the decoder, and returns the stream remainder.
func decode(raw string, lim Limits) (body, rest string, err error) {
	r := bufio.NewReader(strings.NewReader(raw))
	data, err := io.ReadAll(iotest.OneByteReader(&chunkedBody{r: r, lim: lim}))
	tail, _ := io.ReadAll(r)
	return string(data), string(tail), err
}

func TestChunkedValid(t *testing.T) {
	tests := []struct{ name, raw, body, rest string }{
		{"two chunks", "5\r\nhello\r\n6\r\n world\r\n0\r\n\r\n", "hello world", ""},
		{"empty body", "0\r\n\r\n", "", ""},
		{"uppercase hex", "A\r\n0123456789\r\n0\r\n\r\n", "0123456789", ""},
		{"lowercase hex", "a\r\n0123456789\r\n0\r\n\r\n", "0123456789", ""},
		{"leading zeros", "0005\r\nhello\r\n0\r\n\r\n", "hello", ""},
		{"extension", "5;name=value\r\nhello\r\n0\r\n\r\n", "hello", ""},
		{"space before extension", "5 ;x\r\nhello\r\n0\r\n\r\n", "hello", ""},
		{"extension on last chunk", "5\r\nhello\r\n0;done\r\n\r\n", "hello", ""},
		{"data contains CRLF", "4\r\na\r\nb\r\n0\r\n\r\n", "a\r\nb", ""},
		{"trailer", "5\r\nhello\r\n0\r\nX-Sum: 1\r\nX-Other: 2\r\n\r\n", "hello", ""},
		{"stops at end", "5\r\nhello\r\n0\r\n\r\nGET / HTTP/1.1\r\n", "hello", "GET / HTTP/1.1\r\n"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			body, rest, err := decode(tc.raw, DefaultLimits())
			if err != nil || body != tc.body || rest != tc.rest {
				t.Errorf("got (%q, %q, %v), want (%q, %q, nil)", body, rest, err, tc.body, tc.rest)
			}
		})
	}
}

func TestChunkedInvalid(t *testing.T) {
	small := Limits{MaxBodyBytes: 8, MaxChunkLineBytes: 32, MaxHeaderBytes: 64, MaxHeaderCount: 2}
	tests := []struct {
		name, raw string
		lim       Limits
		want      error
	}{
		{"not hex", "zz\r\nhi\r\n0\r\n\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"empty size", "\r\nhi\r\n0\r\n\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"extension only", ";x\r\nhi\r\n0\r\n\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"leading space", " 5\r\nhello\r\n0\r\n\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"minus sign", "-5\r\nhello\r\n0\r\n\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"plus sign", "+5\r\nhello\r\n0\r\n\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"0x prefix", "0x5\r\nhello\r\n0\r\n\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"overflow", "ffffffffffffffffff\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"bare CR in size", "5\rx\r\nhello\r\n0\r\n\r\n", DefaultLimits(), ErrInvalidChunkSize},
		{"control char in extension", "5;a\x00b\r\nhello\r\n0\r\n\r\n", DefaultLimits(), ErrMalformedChunk},
		{"bare LF in size line", "5\nhello\r\n0\r\n\r\n", DefaultLimits(), ErrBareLF},
		{"missing CRLF after data", "5\r\nhelloXX0\r\n\r\n", DefaultLimits(), ErrMalformedChunk},
		{"LF only after data", "5\r\nhello\n0\r\n\r\n", DefaultLimits(), ErrMalformedChunk},
		{"size larger than data", "7\r\nhello\r\n0\r\n\r\n", DefaultLimits(), ErrMalformedChunk},

		{"truncated input", "", DefaultLimits(), ErrTruncatedBody},
		{"truncated in size line", "5", DefaultLimits(), ErrTruncatedBody},
		{"truncated in data", "5\r\nhel", DefaultLimits(), ErrTruncatedBody},
		{"truncated before data CRLF", "5\r\nhello", DefaultLimits(), ErrTruncatedBody},
		{"truncated before next chunk", "5\r\nhello\r\n", DefaultLimits(), ErrTruncatedBody},
		{"truncated in trailers", "0\r\nX: 1\r\n", DefaultLimits(), ErrTruncatedBody},

		{"body too large", "5\r\nhello\r\n5\r\nworld\r\n0\r\n\r\n", small, ErrBodyTooLarge},
		{"huge announced chunk", "7fffffffffffffff\r\n", small, ErrBodyTooLarge},
		{"chunk line too long", "5;" + strings.Repeat("a", 40) + "\r\nhello\r\n0\r\n\r\n", small, ErrChunkLineTooLong},
		{"too many trailers", "0\r\nA: 1\r\nB: 2\r\nC: 3\r\n\r\n", small, ErrTooManyHeaders},
		{"trailers too large", "0\r\nA: " + strings.Repeat("a", 80) + "\r\n\r\n", small, ErrHeaderTooLarge},
		{"invalid trailer", "0\r\nbad trailer\r\n\r\n", DefaultLimits(), ErrInvalidHeaderName},
		{"folded trailer", "0\r\nA: 1\r\n b\r\n\r\n", DefaultLimits(), ErrObsFold},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := decode(tc.raw, tc.lim); !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}

func TestChunkedErrorIsSticky(t *testing.T) {
	b := &chunkedBody{r: bufio.NewReader(strings.NewReader("zz\r\nhello\r\n0\r\n\r\n")), lim: DefaultLimits()}
	for i := 0; i < 3; i++ {
		if _, err := b.Read(make([]byte, 8)); !errors.Is(err, ErrInvalidChunkSize) {
			t.Fatalf("read %d: %v", i, err)
		}
	}
}

func TestChunkedDiscard(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("5\r\nhello\r\n5\r\nworld\r\n0\r\n\r\nNEXT"))
	b := &chunkedBody{r: r, lim: DefaultLimits()}
	b.Read(make([]byte, 3)) // handler read only part of it
	if err := discardBody(b, 100); err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "NEXT" {
		t.Errorf("rest = %q", rest)
	}
}

// Bodies read through ReadRequest: framing is chosen from the headers and the
// stream stays positioned for the next request.
func TestReadRequestBody(t *testing.T) {
	next := "GET /next HTTP/1.1\r\nHost: h\r\n\r\n"
	tests := []struct{ name, head, body, want string }{
		{"content-length", "Content-Length: 5\r\n", "hello", "hello"},
		{"chunked", "Transfer-Encoding: chunked\r\n", "5\r\nhello\r\n0\r\n\r\n", "hello"},
		{"no body", "", "", ""},
		{"zero length", "Content-Length: 0\r\n", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r := bufio.NewReader(strings.NewReader("POST /u HTTP/1.1\r\nHost: h\r\n" + tc.head + "\r\n" + tc.body + next))
			req, err := ReadRequest(r, DefaultLimits())
			if err != nil {
				t.Fatal(err)
			}
			if got, err := io.ReadAll(req.Body); err != nil || string(got) != tc.want {
				t.Fatalf("body = %q, %v", got, err)
			}
			if req, err = ReadRequest(r, DefaultLimits()); err != nil || req.Path != "/next" {
				t.Fatalf("next request: %v, %v", req, err)
			}
		})
	}
}

func TestReadRequestBodyFramingErrors(t *testing.T) {
	tests := []struct {
		name, head string
		want       error
	}{
		{"both", "Content-Length: 5\r\nTransfer-Encoding: chunked\r\n", ErrContentLengthWithChunked},
		{"two lengths", "Content-Length: 5\r\nContent-Length: 6\r\n", ErrConflictingContentLength},
		{"not digits", "Content-Length: 5x\r\n", ErrInvalidContentLength},
		{"too large", "Content-Length: 99999999\r\n", ErrBodyTooLarge},
		{"gzip", "Transfer-Encoding: gzip\r\n", ErrUnsupportedTransferEncoding},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := read(t, "POST / HTTP/1.1\r\nHost: h\r\n"+tc.head+"\r\n", DefaultLimits())
			if !errors.Is(err, tc.want) {
				t.Errorf("error = %v, want %v", err, tc.want)
			}
		})
	}
}
