package http1

import (
	"bufio"
	"errors"
	"io"
	"strings"
	"testing"
)

func hdr(kv ...string) Header {
	h := Header{}
	for i := 0; i < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestBodyFraming(t *testing.T) {
	tests := []struct {
		name   string
		h      Header
		kind   framing
		length int64
		err    error
	}{
		{"no body", hdr("Host", "h"), framingNone, 0, nil},
		{"length", hdr("Content-Length", "5"), framingLength, 5, nil},
		{"length zero", hdr("Content-Length", "0"), framingLength, 0, nil},
		{"leading zeros", hdr("Content-Length", "007"), framingLength, 7, nil},
		{"spaces around", hdr("Content-Length", " 5\t"), framingLength, 5, nil},
		{"same value twice", hdr("Content-Length", "5", "Content-Length", "5"), framingLength, 5, nil},
		{"same value in list", hdr("Content-Length", "5, 5"), framingLength, 5, nil},
		{"chunked", hdr("Transfer-Encoding", "chunked"), framingChunked, 0, nil},
		{"chunked any case", hdr("transfer-encoding", " Chunked "), framingChunked, 0, nil},

		{"differing values", hdr("Content-Length", "5", "Content-Length", "6"), 0, 0, ErrConflictingContentLength},
		{"differing in list", hdr("Content-Length", "5, 6"), 0, 0, ErrConflictingContentLength},
		{"letters", hdr("Content-Length", "5a"), 0, 0, ErrInvalidContentLength},
		{"negative", hdr("Content-Length", "-5"), 0, 0, ErrInvalidContentLength},
		{"plus sign", hdr("Content-Length", "+5"), 0, 0, ErrInvalidContentLength},
		{"empty", hdr("Content-Length", ""), 0, 0, ErrInvalidContentLength},
		{"empty list item", hdr("Content-Length", "5,"), 0, 0, ErrInvalidContentLength},
		{"inner space", hdr("Content-Length", "5 5"), 0, 0, ErrInvalidContentLength},
		{"overflow", hdr("Content-Length", "99999999999999999999"), 0, 0, ErrInvalidContentLength},
		{"too large", hdr("Content-Length", "2000"), 0, 0, ErrBodyTooLarge},
		{"both", hdr("Content-Length", "5", "Transfer-Encoding", "chunked"), 0, 0, ErrContentLengthWithChunked},
		{"gzip", hdr("Transfer-Encoding", "gzip"), 0, 0, ErrUnsupportedTransferEncoding},
		{"gzip then chunked", hdr("Transfer-Encoding", "gzip, chunked"), 0, 0, ErrUnsupportedTransferEncoding},
		{"chunked twice", hdr("Transfer-Encoding", "chunked", "Transfer-Encoding", "chunked"), 0, 0, ErrUnsupportedTransferEncoding},
		{"empty encoding", hdr("Transfer-Encoding", ""), 0, 0, ErrUnsupportedTransferEncoding},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			kind, n, err := bodyFraming(tc.h, 1000)
			if !errors.Is(err, tc.err) || err == nil && (kind != tc.kind || n != tc.length) {
				t.Errorf("got (%v, %d, %v), want (%v, %d, %v)", kind, n, err, tc.kind, tc.length, tc.err)
			}
		})
	}
}

func TestBodyFramingNoLimit(t *testing.T) {
	if _, n, err := bodyFraming(hdr("Content-Length", "5000000"), 0); err != nil || n != 5000000 {
		t.Errorf("got %d, %v", n, err)
	}
}

func TestLengthBody(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("helloNEXT"))
	b := &lengthBody{r: r, remaining: 5}
	got, err := io.ReadAll(b)
	if err != nil || string(got) != "hello" {
		t.Fatalf("got %q, %v", got, err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "NEXT" {
		t.Errorf("body read past its end, rest = %q", rest)
	}
}

func TestLengthBodyEmpty(t *testing.T) {
	b := &lengthBody{r: bufio.NewReader(strings.NewReader("x")), remaining: 0}
	if n, err := b.Read(make([]byte, 4)); n != 0 || err != io.EOF {
		t.Errorf("got %d, %v", n, err)
	}
}

func TestLengthBodyTruncated(t *testing.T) {
	b := &lengthBody{r: bufio.NewReader(strings.NewReader("abc")), remaining: 5}
	got, err := io.ReadAll(b)
	if !errors.Is(err, ErrTruncatedBody) || string(got) != "abc" {
		t.Errorf("got %q, %v", got, err)
	}
}

func TestDiscardBody(t *testing.T) {
	r := bufio.NewReader(strings.NewReader("helloNEXT"))
	b := &lengthBody{r: r, remaining: 5}
	b.Read(make([]byte, 2)) // handler read only part of it
	if err := discardBody(b, 100); err != nil {
		t.Fatal(err)
	}
	if rest, _ := io.ReadAll(r); string(rest) != "NEXT" {
		t.Errorf("rest = %q", rest)
	}
}

func TestDiscardBodyTooLarge(t *testing.T) {
	b := &lengthBody{r: bufio.NewReader(strings.NewReader(strings.Repeat("x", 50))), remaining: 50}
	if err := discardBody(b, 10); !errors.Is(err, errBodyNotDrained) {
		t.Errorf("err = %v", err)
	}
}

func TestDiscardBodyNilAndEmpty(t *testing.T) {
	if err := discardBody(nil, 10); err != nil {
		t.Error(err)
	}
	if err := discardBody(emptyBody{}, 10); err != nil {
		t.Error(err)
	}
}
