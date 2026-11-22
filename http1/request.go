package http1

import (
	"bufio"
	"bytes"
	"errors"
	"io"
	"strings"
)

// Request is a parsed HTTP/1.x request head.
type Request struct {
	Method       string
	Target       string // Request-target exactly as received.
	Path         string // Raw, still percent-encoded. "*" for OPTIONS *, empty for CONNECT.
	RawQuery     string // Raw query without the leading '?'.
	Major, Minor int
	Header       Header
	Host         string      // From the request-target if absolute-form, else the Host header.
	RemoteAddr   string      // Peer address, set by the server.
	Body         io.Reader   // Never nil; the handler may leave it unread.
	pathValues   []pathValue // Wildcard matches set by the Router; see PathValue.
}

// ReadRequest reads one request head (request line and headers) from r and
// stops right after the blank line, so r is positioned at the body or at the
// next pipelined request.
//
// It returns io.EOF if the connection closed before the first byte, and
// io.ErrUnexpectedEOF if it closed in the middle of a head. Every other
// error is a *ParseError.
func ReadRequest(r *bufio.Reader, lim Limits) (*Request, error) {
	readStartLine := func() ([]byte, error) {
		line, err := readLine(r, lim.MaxRequestLineBytes)
		if err == errLineTooLong {
			err = ErrRequestLineTooLong
		}
		return line, err
	}

	line, err := readStartLine()
	if err != nil {
		return nil, err
	}
	used := len(line) + 2

	// RFC 9112 §2.2: a server SHOULD ignore one empty line before the
	// request line. Only one, so this cannot be used to stall a connection.
	if len(line) == 0 {
		if line, err = readStartLine(); err != nil {
			return nil, err
		}
		used = len(line) + 2
	}

	req := &Request{Header: make(Header)}
	if err := req.parseRequestLine(line); err != nil {
		return nil, err
	}

	count := 0
	for {
		max := 0
		if lim.MaxHeaderBytes > 0 {
			if max = lim.MaxHeaderBytes - used; max <= 0 {
				return nil, ErrHeaderTooLarge
			}
		}
		line, err := readLine(r, max)
		if err != nil {
			switch err {
			case io.EOF:
				err = io.ErrUnexpectedEOF
			case errLineTooLong:
				err = ErrHeaderTooLarge
			}
			return nil, err
		}
		used += len(line) + 2
		if len(line) == 0 {
			break
		}
		if count++; lim.MaxHeaderCount > 0 && count > lim.MaxHeaderCount {
			return nil, ErrTooManyHeaders
		}
		name, value, err := parseHeaderLine(line)
		if err != nil {
			return nil, err
		}
		req.Header.Add(name, value)
	}

	if err := req.resolveHost(); err != nil {
		return nil, err
	}
	body, err := newBody(r, req.Header, lim)
	if err != nil {
		return nil, err
	}
	req.Body = body
	return req, nil
}

// errLineTooLong is returned by readLine; callers translate it into the
// ParseError that fits the kind of line they were reading.
var errLineTooLong = errors.New("http1: line too long")

// readLine reads one CRLF-terminated line and returns it without the CRLF.
// max bounds the line length including the CRLF; zero means unlimited.
func readLine(r *bufio.Reader, max int) ([]byte, error) {
	var line []byte
	for {
		chunk, err := r.ReadSlice('\n')
		line = append(line, chunk...)
		if max > 0 && len(line) > max {
			return nil, errLineTooLong
		}
		if err == nil {
			break
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err == io.EOF && len(line) > 0 {
			err = io.ErrUnexpectedEOF
		}
		return nil, err
	}
	n := len(line)
	if n < 2 || line[n-2] != '\r' {
		return nil, ErrBareLF
	}
	return line[:n-2], nil
}

func (req *Request) parseRequestLine(line []byte) error {
	if bytes.IndexByte(line, '\r') >= 0 {
		return ErrBareCR
	}
	sp1 := bytes.IndexByte(line, ' ')
	if sp1 < 0 {
		return ErrMalformedRequestLine
	}
	sp2 := bytes.IndexByte(line[sp1+1:], ' ')
	if sp2 < 0 {
		return ErrMalformedRequestLine
	}
	sp2 += sp1 + 1
	method, target, version := line[:sp1], line[sp1+1:sp2], line[sp2+1:]
	if bytes.IndexByte(version, ' ') >= 0 {
		return ErrMalformedRequestLine
	}

	if !isToken(string(method)) {
		return ErrInvalidMethod
	}
	req.Method = string(method)

	var err error
	if req.Major, req.Minor, err = parseVersion(version); err != nil {
		return err
	}

	req.Target = string(target)
	return req.parseTarget()
}

// parseVersion parses "HTTP/<digit>.<digit>". The name is case-sensitive.
func parseVersion(v []byte) (major, minor int, err error) {
	if len(v) != 8 || string(v[:5]) != "HTTP/" || v[6] != '.' ||
		v[5] < '0' || v[5] > '9' || v[7] < '0' || v[7] > '9' {
		return 0, 0, ErrMalformedVersion
	}
	major, minor = int(v[5]-'0'), int(v[7]-'0')
	if major != 1 {
		return 0, 0, ErrUnsupportedVersion
	}
	return major, minor, nil
}

// parseTarget fills Path and RawQuery from the four request-target forms of
// RFC 9112 §3.2. For absolute-form it also sets Host.
func (req *Request) parseTarget() error {
	t := req.Target
	if t == "" {
		return ErrInvalidTarget
	}
	for i := 0; i < len(t); i++ {
		if c := t[i]; c <= 0x20 || c >= 0x7f || c == '#' {
			return ErrInvalidTarget
		}
	}

	switch {
	case req.Method == "CONNECT": // authority-form
		if strings.ContainsAny(t, "/?") || !validHost(t) {
			return ErrInvalidTarget
		}
		req.Host = t
	case t == "*": // asterisk-form
		if req.Method != "OPTIONS" {
			return ErrInvalidTarget
		}
		req.Path = "*"
	case t[0] == '/': // origin-form
		req.Path, req.RawQuery, _ = strings.Cut(t, "?")
	default: // absolute-form, which a server MUST accept
		rest, ok := cutScheme(t)
		if !ok {
			return ErrInvalidTarget
		}
		authority, tail := rest, "/"
		if i := strings.IndexAny(rest, "/?"); i >= 0 {
			authority, tail = rest[:i], rest[i:]
			if tail[0] == '?' {
				tail = "/" + tail
			}
		}
		if !validHost(authority) {
			return ErrInvalidTarget
		}
		req.Host = authority
		req.Path, req.RawQuery, _ = strings.Cut(tail, "?")
	}
	return nil
}

func cutScheme(s string) (string, bool) {
	for _, p := range [...]string{"http://", "https://"} {
		if len(s) >= len(p) && strings.EqualFold(s[:len(p)], p) {
			return s[len(p):], true
		}
	}
	return "", false
}

// resolveHost enforces RFC 9110 §7.2: HTTP/1.1 requests need exactly one
// valid Host header. If the target was absolute-form its authority wins and
// the header is ignored (RFC 9112 §3.2.2), but must still be well-formed.
func (req *Request) resolveHost() error {
	hosts := req.Header.Values("Host")
	switch {
	case len(hosts) > 1:
		return ErrMultipleHost
	case len(hosts) == 0:
		if req.Minor >= 1 {
			return ErrMissingHost
		}
	case !validHost(hosts[0]):
		return ErrInvalidHost
	}
	if req.Host == "" && len(hosts) == 1 {
		req.Host = hosts[0]
	}
	return nil
}

// validHost is a deliberately small check: non-empty, printable ASCII, and
// none of the delimiters that would make the value ambiguous.
func validHost(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; c <= 0x20 || c >= 0x7f || strings.IndexByte(`/\?#@`, c) >= 0 {
			return false
		}
	}
	return true
}
