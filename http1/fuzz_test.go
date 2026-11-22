package http1

import (
	"bufio"
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"
)

// Run one target at a time, for example:
//
//	go test -run xxx -fuzz FuzzReadRequest -fuzztime 30s
//
// Seeds come from testdata/requests, so every corpus file is also a starting
// point for mutation. Inputs that crash a target are saved by the toolchain
// under testdata/fuzz and then run as ordinary tests.

// fuzzLimits are small so that limit errors are reachable with short inputs.
var fuzzLimits = Limits{
	MaxRequestLineBytes: 256,
	MaxHeaderBytes:      1024,
	MaxHeaderCount:      16,
	MaxBodyBytes:        2048,
	MaxChunkLineBytes:   64,
}

func addCorpusSeeds(f *testing.F) {
	f.Helper()
	f.Add([]byte("GET / HTTP/1.1\r\nHost: h\r\n\r\n"))
	paths, _ := filepath.Glob("testdata/requests/*/*.raw")
	for _, p := range paths {
		if b, err := os.ReadFile(p); err == nil && len(b) <= 64<<10 {
			f.Add(b)
		}
	}
}

// requireParseError fails unless err is a *ParseError with a sane status, or
// one of the two I/O outcomes the parser passes through.
func requireParseError(t *testing.T, err error) {
	t.Helper()
	if err == io.ErrUnexpectedEOF {
		return
	}
	status, ok := StatusCode(err)
	if !ok || status < 400 || status > 599 || StatusText(status) == "" {
		t.Fatalf("error %v (%T) is not a ParseError with a known status", err, err)
	}
}

// checkRequest asserts everything ReadRequest promises about an accepted
// request, independent of the input.
func checkRequest(t *testing.T, req *Request) {
	t.Helper()
	if !isToken(req.Method) || req.Major != 1 || req.Minor < 0 || req.Minor > 9 {
		t.Fatalf("bad method/version: %q %d.%d", req.Method, req.Major, req.Minor)
	}
	for i := 0; i < len(req.Target); i++ {
		if c := req.Target[i]; c <= 0x20 || c >= 0x7f || c == '#' {
			t.Fatalf("target contains %#x: %q", c, req.Target)
		}
	}
	switch {
	case req.Method == "CONNECT":
		if req.Path != "" || req.Host == "" {
			t.Fatalf("CONNECT: path %q host %q", req.Path, req.Host)
		}
	case req.Path == "*":
		if req.Method != "OPTIONS" {
			t.Fatalf("asterisk-form with %s", req.Method)
		}
	case !strings.HasPrefix(req.Path, "/"):
		t.Fatalf("path %q does not start with /", req.Path)
	}
	if strings.ContainsAny(req.Path, "?#") || strings.Contains(req.RawQuery, "#") {
		t.Fatalf("path %q query %q", req.Path, req.RawQuery)
	}

	for k, values := range req.Header {
		if !isToken(k) || CanonicalKey(k) != k || len(values) == 0 {
			t.Fatalf("header key %q is not canonical", k)
		}
		for _, v := range values {
			if !isFieldValue([]byte(v)) || v != strings.Trim(v, " \t") {
				t.Fatalf("header %s has bad value %q", k, v)
			}
		}
	}
	hosts := req.Header.Values("Host")
	if len(hosts) > 1 || (req.Minor >= 1 && len(hosts) != 1) {
		t.Fatalf("Host headers: %q on HTTP/1.%d", hosts, req.Minor)
	}
	if req.Minor >= 1 && !validHost(req.Host) {
		t.Fatalf("Host %q invalid", req.Host)
	}
	if len(req.Header.Values("Content-Length")) > 0 && len(req.Header.Values("Transfer-Encoding")) > 0 {
		t.Fatal("both Content-Length and Transfer-Encoding accepted")
	}
	if req.Body == nil {
		t.Fatal("nil Body")
	}
}

// serializeHead renders a request head without body framing headers.
func serializeHead(req *Request) []byte {
	var b bytes.Buffer
	fmt.Fprintf(&b, "%s %s HTTP/%d.%d\r\n", req.Method, req.Target, req.Major, req.Minor)
	keys := make([]string, 0, len(req.Header))
	for k := range req.Header {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		if k == "Content-Length" || k == "Transfer-Encoding" {
			continue
		}
		for _, v := range req.Header[k] {
			fmt.Fprintf(&b, "%s: %s\r\n", k, v)
		}
	}
	b.WriteString("\r\n")
	return b.Bytes()
}

func withoutFraming(h Header) Header {
	c := h.Clone()
	c.Del("Content-Length")
	c.Del("Transfer-Encoding")
	return c
}

// FuzzReadRequest: no input may panic or hang the parser, every error must be
// a well-formed one, every accepted request must satisfy checkRequest, and a
// parsed head must survive serialisation and parsing again unchanged.
func FuzzReadRequest(f *testing.F) {
	addCorpusSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		br := bufio.NewReader(bytes.NewReader(data))
		for n := 0; n < 100; n++ {
			req, err := ReadRequest(br, fuzzLimits)
			if err == io.EOF {
				return
			}
			if err != nil {
				requireParseError(t, err)
				return
			}
			checkRequest(t, req)

			body, err := io.ReadAll(req.Body)
			if err != nil {
				requireParseError(t, err)
				return
			}
			if int64(len(body)) > fuzzLimits.MaxBodyBytes {
				t.Fatalf("body of %d bytes exceeds the limit", len(body))
			}

			again, err := ReadRequest(bufio.NewReader(bytes.NewReader(serializeHead(req))), fuzzLimits)
			if err != nil {
				t.Fatalf("reserialised head rejected: %v\n%q", err, serializeHead(req))
			}
			if again.Method != req.Method || again.Target != req.Target || again.Path != req.Path ||
				again.RawQuery != req.RawQuery || again.Host != req.Host || again.Minor != req.Minor ||
				!reflect.DeepEqual(again.Header, withoutFraming(req.Header)) {
				t.Fatalf("round trip changed the request:\n%+v\n%+v", req, again)
			}
		}
	})
}

// FuzzParseHeaderLine: accepted lines come back in canonical form and parse
// to the same thing when written out again.
func FuzzParseHeaderLine(f *testing.F) {
	for _, s := range []string{"Host: h", "x:y", "X-A:\t v \t", "Bad Name: v", " fold", "A: b\x00", ": v", "A : b", "A: \xff"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, line []byte) {
		name, value, err := parseHeaderLine(line)
		if err != nil {
			if _, ok := StatusCode(err); !ok {
				t.Fatalf("non-ParseError %v", err)
			}
			return
		}
		if !isToken(name) || CanonicalKey(name) != name || !isFieldValue([]byte(value)) ||
			value != strings.Trim(value, " \t") || strings.ContainsAny(value, "\r\n") {
			t.Fatalf("accepted %q as %q = %q", line, name, value)
		}
		n2, v2, err := parseHeaderLine([]byte(name + ": " + value))
		if err != nil || n2 != name || v2 != value {
			t.Fatalf("round trip of %q: %q %q %v", line, n2, v2, err)
		}
	})
}

// FuzzChunked compares the decoder with net/http's: whenever ours accepts a
// chunked body, the standard library must read the same bytes and stop at the
// same place. (Ours is stricter, so the other direction is not checked.)
func FuzzChunked(f *testing.F) {
	for _, s := range []string{
		"5\r\nhello\r\n0\r\n\r\n", "0\r\n\r\n", "A\r\n0123456789\r\n0\r\n\r\n",
		"5;x=y\r\nhello\r\n0\r\nT: v\r\n\r\n", "3\r\nabc\r\n3\r\ndef\r\n0\r\n\r\nNEXT",
		"zz\r\n", "5\r\nhel", "0\r\nX: 1\r\n\r\n", "1\r\na\r\n1\r\nb\r\n0\r\n\r\n",
	} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		r := bufio.NewReader(bytes.NewReader(data))
		body, err := io.ReadAll(&chunkedBody{r: r, lim: Limits{}})
		if err != nil {
			requireParseError(t, err)
			return
		}
		rest, _ := io.ReadAll(r)

		head := "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n"
		sr := bufio.NewReader(strings.NewReader(head + string(data)))
		req, err := http.ReadRequest(sr)
		if err != nil {
			t.Fatal(err)
		}
		theirs, err := io.ReadAll(req.Body)
		if err != nil || !bytes.Equal(theirs, body) {
			t.Fatalf("ours accepted %q => %q; net/http => %q, %v", data, body, theirs, err)
		}
		theirRest, _ := io.ReadAll(sr)
		if !bytes.Equal(theirRest, rest) {
			t.Fatalf("ours left %q after %q; net/http left %q", rest, data, theirRest)
		}
	})
}

// FuzzRequestVsStdlib is a parser-differential check: a request we accept
// should be accepted by net/http and mean the same thing. Disagreement is how
// request smuggling happens between two servers in a chain.
func FuzzRequestVsStdlib(f *testing.F) {
	addCorpusSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		ours, err := ReadRequest(bufio.NewReader(bytes.NewReader(data)), DefaultLimits())
		if err != nil {
			return
		}
		theirs, err := http.ReadRequest(bufio.NewReader(bytes.NewReader(data)))
		if err != nil {
			if knownStdlibStricter(data, err) {
				return
			}
			t.Fatalf("we accept, net/http rejects (%v):\n%q", err, data)
		}

		if theirs.Method != ours.Method {
			t.Errorf("method: ours %q, net/http %q", ours.Method, theirs.Method)
		}
		if theirs.Host != ours.Host && ours.Minor >= 1 {
			t.Errorf("host: ours %q, net/http %q", ours.Host, theirs.Host)
		}
		if strings.HasPrefix(ours.Target, "/") && theirs.RequestURI != ours.Target {
			t.Errorf("target: ours %q, net/http %q", ours.Target, theirs.RequestURI)
		}
		a, b := withoutFraming(ours.Header), withoutFraming(Header(theirs.Header))
		a.Del("Host")
		b.Del("Host")
		if !reflect.DeepEqual(a, b) && len(a)+len(b) > 0 {
			t.Errorf("headers differ:\nours    %q\nnet/http %q", a, b)
		}
	})
}

// knownStdlibStricter lists the deliberate differences where net/http refuses
// something RFC 9112 allows and we accept: a single empty line before the
// request line, a list of identical Content-Length values, and a bad
// percent-escape (we pass it on and the router answers 400).
func knownStdlibStricter(data []byte, err error) bool {
	msg := err.Error()
	switch {
	case bytes.HasPrefix(data, []byte("\r\n")):
		return true
	case strings.Contains(msg, "Content-Length") && bytes.Contains(bytes.ToLower(data), []byte("content-length:")):
		return true
	case strings.Contains(msg, "invalid URL escape") || strings.Contains(msg, "invalid URL"):
		return true
	}
	return false
}

// FuzzRouterPath: random paths must never panic the router, never produce an
// unparseable response, and never reach a route by way of ".." or "." even
// when those are hidden behind percent-encoding.
func FuzzRouterPath(f *testing.F) {
	for _, s := range []string{"/a/b", "/a/../b", "/a/%2e%2e/b", "/a%2Fb", "/users/caf%C3%A9", "/files/x/y", "/%zz", "/a/./b", "//", "/a/b/"} {
		f.Add(s)
	}
	rt := newTestRouter()
	rt.Handle("GET", "/a/b", say("secret"))
	f.Fuzz(func(t *testing.T, path string) {
		for i := 0; i < len(path); i++ {
			if c := path[i]; c <= 0x20 || c >= 0x7f || c == '#' || c == '?' {
				t.Skip("the request parser never lets such a path through")
			}
		}
		if !strings.HasPrefix(path, "/") {
			path = "/" + path
		}
		req := &Request{Method: "GET", Path: path, Major: 1, Minor: 1, Header: Header{}, Body: emptyBody{}}
		var out bytes.Buffer
		rw := newResponseWriter(bufio.NewWriter(&out), req)
		rt.ServeHTTP(rw, req)
		if err := rw.finish(); err != nil {
			t.Fatal(err)
		}
		resp, err := http.ReadResponse(bufio.NewReader(&out), &http.Request{Method: "GET"})
		if err != nil {
			t.Fatalf("unparseable response for %q: %v", path, err)
		}
		body, _ := io.ReadAll(resp.Body)

		// Independent decoding with net/url.
		var segs []string
		hostile := false
		for _, s := range strings.Split(path[1:], "/") {
			d, err := url.PathUnescape(s)
			if err != nil || d == "." || d == ".." {
				hostile = true
			}
			segs = append(segs, d)
		}
		if resp.StatusCode == 200 && hostile {
			t.Fatalf("%q matched a route (%q) although a segment is hostile", path, body)
		}
		if string(body) == "secret" && !reflect.DeepEqual(segs, []string{"a", "b"}) {
			t.Fatalf("%q reached /a/b with segments %q", path, segs)
		}
	})
}

// fakeConn is an in-memory net.Conn: reads come from a fixed input that ends
// in EOF, writes are collected. It lets the whole connection loop run
// synchronously inside a fuzz iteration.
type fakeConn struct {
	in  *bytes.Reader
	out bytes.Buffer
}

type fakeAddr struct{}

func (fakeAddr) Network() string { return "fake" }
func (fakeAddr) String() string  { return "fake:0" }

func (c *fakeConn) Read(p []byte) (int, error)       { return c.in.Read(p) }
func (c *fakeConn) Write(p []byte) (int, error)      { return c.out.Write(p) }
func (c *fakeConn) Close() error                     { return nil }
func (c *fakeConn) LocalAddr() net.Addr              { return fakeAddr{} }
func (c *fakeConn) RemoteAddr() net.Addr             { return fakeAddr{} }
func (c *fakeConn) SetDeadline(time.Time) error      { return nil }
func (c *fakeConn) SetReadDeadline(time.Time) error  { return nil }
func (c *fakeConn) SetWriteDeadline(time.Time) error { return nil }

// serveBytes runs the connection loop over data and returns what it wrote.
func serveBytes(data []byte) []byte {
	srv := &Server{Handler: HandlerFunc(corpusHandler)}
	c := &conn{srv: srv, rwc: &fakeConn{in: bytes.NewReader(data)}}
	fc := c.rwc.(*fakeConn)
	c.serve()
	return fc.out.Bytes()
}

// FuzzServeConn feeds arbitrary bytes to the whole connection loop. The server
// must terminate, everything it writes must be well-formed HTTP, and its
// responses must agree with what the stand-alone parser makes of the input.
func FuzzServeConn(f *testing.F) {
	addCorpusSeeds(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		out := serveBytes(data)
		reqs, perr := parseAll(data, DefaultLimits())

		br := bufio.NewReader(bytes.NewReader(out))
		var statuses []int
		for {
			if _, err := br.Peek(1); err == io.EOF {
				break
			}
			method := "GET"
			if i := len(statuses); i < len(reqs) {
				method = reqs[i].req.Method
			}
			resp, err := http.ReadResponse(br, &http.Request{Method: method})
			if err != nil {
				t.Fatalf("malformed response after %v: %v\noutput: %q\ninput: %q", statuses, err, out, data)
			}
			if _, err := io.Copy(io.Discard, resp.Body); err != nil {
				t.Fatalf("malformed response body: %v\noutput: %q", err, out)
			}
			if resp.StatusCode/100 != 1 {
				statuses = append(statuses, resp.StatusCode)
			}
		}

		// Connection: close, HTTP/1.0 and Expect end or change the exchange
		// early; without them the counts must match exactly.
		lower := bytes.ToLower(data)
		if bytes.Contains(lower, []byte("connection")) || bytes.Contains(lower, []byte("http/1.0")) ||
			bytes.Contains(lower, []byte("expect")) {
			return
		}
		want := len(reqs)
		if status, ok := StatusCode(perr); ok {
			want++
			if len(statuses) > 0 && statuses[len(statuses)-1] != status {
				t.Fatalf("last status %d, parser error %v wants %d\ninput: %q", statuses[len(statuses)-1], perr, status, data)
			}
		}
		if len(statuses) != want {
			t.Fatalf("%d responses %v for %d parsed requests and error %v\ninput: %q", len(statuses), statuses, len(reqs), perr, data)
		}
	})
}
