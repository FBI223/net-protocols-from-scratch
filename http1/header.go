package http1

import (
	"bytes"
	"strings"
)

// Header maps canonical field names ("Content-Length") to their values, in
// order of arrival. Use the methods rather than indexing the map directly, so
// that names are always canonicalised.
type Header map[string][]string

// Add appends value to the values of name.
func (h Header) Add(name, value string) {
	k := CanonicalKey(name)
	h[k] = append(h[k], value)
}

// Set replaces all values of name with value.
func (h Header) Set(name, value string) {
	h[CanonicalKey(name)] = []string{value}
}

// Get returns the first value of name, or "" if there is none.
func (h Header) Get(name string) string {
	if v := h[CanonicalKey(name)]; len(v) > 0 {
		return v[0]
	}
	return ""
}

// Values returns all values of name. The slice must not be modified.
func (h Header) Values(name string) []string {
	return h[CanonicalKey(name)]
}

// Del removes name and all its values.
func (h Header) Del(name string) {
	delete(h, CanonicalKey(name))
}

// Clone returns a deep copy of h.
func (h Header) Clone() Header {
	c := make(Header, len(h))
	for k, v := range h {
		c[k] = append([]string(nil), v...)
	}
	return c
}

// hasToken reports whether the comma-separated values of name contain token,
// compared case-insensitively (as for Connection: keep-alive, close).
func (h Header) hasToken(name, token string) bool {
	for _, v := range h.Values(name) {
		for _, part := range strings.Split(v, ",") {
			if strings.EqualFold(strings.Trim(part, " \t"), token) {
				return true
			}
		}
	}
	return false
}

// CanonicalKey returns name in canonical form: the first letter and every
// letter after a hyphen upper-case, the rest lower-case. Only ASCII is
// touched; names containing other bytes are returned unchanged.
func CanonicalKey(name string) string {
	if !isToken(name) {
		return name
	}
	upper := true
	for i := 0; i < len(name); i++ {
		c := name[i]
		if upper && 'a' <= c && c <= 'z' || !upper && 'A' <= c && c <= 'Z' {
			return canonicalize(name)
		}
		upper = c == '-'
	}
	return name
}

func canonicalize(name string) string {
	b := []byte(name)
	upper := true
	for i, c := range b {
		if upper && 'a' <= c && c <= 'z' {
			b[i] = c - 'a' + 'A'
		} else if !upper && 'A' <= c && c <= 'Z' {
			b[i] = c - 'A' + 'a'
		}
		upper = c == '-'
	}
	return string(b)
}

// tokenChars marks the bytes allowed in a token (RFC 9110 §5.6.2).
var tokenChars = func() (t [256]bool) {
	for c := 'a'; c <= 'z'; c++ {
		t[c], t[c-'a'+'A'] = true, true
	}
	for c := '0'; c <= '9'; c++ {
		t[c] = true
	}
	for _, c := range "!#$%&'*+-.^_`|~" {
		t[c] = true
	}
	return t
}()

// isToken reports whether s is a non-empty token.
func isToken(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if !tokenChars[s[i]] {
			return false
		}
	}
	return true
}

// isFieldValue reports whether v contains only bytes allowed in a field
// value: HTAB, SP, VCHAR and obs-text (RFC 9110 §5.5). This rejects NUL and
// all other control characters.
func isFieldValue(v []byte) bool {
	for _, c := range v {
		if c != '\t' && (c < 0x20 || c == 0x7f) {
			return false
		}
	}
	return true
}

// parseHeaderLine splits one header line (without its CRLF) into a field
// name and a trimmed value, enforcing RFC 9112 §5.
func parseHeaderLine(line []byte) (name, value string, err error) {
	// The line reader splits on CRLF, so any CR or LF left is a bare one.
	if bytes.IndexByte(line, '\r') >= 0 {
		return "", "", ErrBareCR
	}
	if bytes.IndexByte(line, '\n') >= 0 {
		return "", "", ErrBareLF
	}
	if len(line) > 0 && (line[0] == ' ' || line[0] == '\t') {
		return "", "", ErrObsFold
	}
	colon := bytes.IndexByte(line, ':')
	if colon < 0 {
		return "", "", ErrInvalidHeaderName
	}
	rawName := line[:colon]
	if n := len(rawName); n > 0 && (rawName[n-1] == ' ' || rawName[n-1] == '\t') {
		return "", "", ErrSpaceBeforeColon
	}
	if !isToken(string(rawName)) {
		return "", "", ErrInvalidHeaderName
	}
	rawValue := bytes.Trim(line[colon+1:], " \t")
	if !isFieldValue(rawValue) {
		return "", "", ErrInvalidHeaderValue
	}
	return CanonicalKey(string(rawName)), string(rawValue), nil
}
