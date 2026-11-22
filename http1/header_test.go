package http1

import (
	"errors"
	"testing"
)

func TestCanonicalKey(t *testing.T) {
	tests := map[string]string{
		"content-length":  "Content-Length",
		"CONTENT-LENGTH":  "Content-Length",
		"Content-Length":  "Content-Length",
		"x-forwarded-for": "X-Forwarded-For",
		"host":            "Host",
		"bad name":        "bad name",
		"":                "",
	}
	for in, want := range tests {
		if got := CanonicalKey(in); got != want {
			t.Errorf("CanonicalKey(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestHeaderMethods(t *testing.T) {
	h := Header{}
	h.Add("accept", "a")
	h.Add("ACCEPT", "b")
	if got := h.Values("Accept"); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("Values = %v", got)
	}
	h.Set("accept", "c")
	if h.Get("ACCEPT") != "c" || len(h.Values("accept")) != 1 {
		t.Fatalf("Set did not replace: %v", h)
	}
	h.Del("Accept")
	if h.Get("accept") != "" {
		t.Fatal("Del did not remove")
	}
}

func TestParseHeaderLine(t *testing.T) {
	valid := []struct{ in, name, value string }{
		{"Host: example.com", "Host", "example.com"},
		{"content-length:5", "Content-Length", "5"},
		{"X-Test:  \t spaced value \t ", "X-Test", "spaced value"},
		{"X-Empty:", "X-Empty", ""},
		{"X-Tab: a\tb", "X-Tab", "a\tb"},
		{"X-Colon: a:b", "X-Colon", "a:b"},
	}
	for _, tc := range valid {
		name, value, err := parseHeaderLine([]byte(tc.in))
		if err != nil || name != tc.name || value != tc.value {
			t.Errorf("parseHeaderLine(%q) = %q, %q, %v", tc.in, name, value, err)
		}
	}

	invalid := []struct {
		in   string
		want error
	}{
		{"Host : example.com", ErrSpaceBeforeColon},
		{"Host\t: example.com", ErrSpaceBeforeColon},
		{" folded: value", ErrObsFold},
		{"\tfolded: value", ErrObsFold},
		{"no colon here", ErrInvalidHeaderName},
		{": value", ErrInvalidHeaderName},
		{"Bad Name: v", ErrInvalidHeaderName},
		{"Bad(Name): v", ErrInvalidHeaderName},
		{"X: a\x00b", ErrInvalidHeaderValue},
		{"X: a\x7fb", ErrInvalidHeaderValue},
		{"X: a\rb", ErrBareCR},
		{"X: a\nb", ErrBareLF},
	}
	for _, tc := range invalid {
		_, _, err := parseHeaderLine([]byte(tc.in))
		if !errors.Is(err, tc.want) {
			t.Errorf("parseHeaderLine(%q) error = %v, want %v", tc.in, err, tc.want)
		}
	}
}

func TestHeaderHasToken(t *testing.T) {
	h := hdr("Connection", "keep-alive, Close", "Upgrade", "websocket")
	if !h.hasToken("connection", "close") || !h.hasToken("Connection", "KEEP-ALIVE") {
		t.Error("token not found")
	}
	if h.hasToken("Connection", "clos") || h.hasToken("Accept", "close") {
		t.Error("false positive")
	}
}

func TestHeaderClone(t *testing.T) {
	h := hdr("A", "1")
	c := h.Clone()
	c.Add("A", "2")
	c.Add("B", "3")
	if len(h.Values("A")) != 1 || h.Get("B") != "" {
		t.Errorf("original modified: %v", h)
	}
}
