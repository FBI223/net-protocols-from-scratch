package http1

import (
	"bufio"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"
)

// The corpus in testdata/requests holds raw requests, byte for byte, in
// valid/ and invalid/. expect.json says what a correct server does with each
// one; see gen_corpus.py. A file without a manifest entry is checked
// generically: files in valid/ must parse cleanly, files in invalid/ must
// end in some error.

type corpusRequest struct {
	Method     string              `json:"method"`
	Path       string              `json:"path"`
	Query      string              `json:"query"`
	Host       string              `json:"host"`
	Minor      int                 `json:"minor"`
	Body       *string             `json:"body"`
	BodyB64    string              `json:"body_b64"`
	BodyLen    int                 `json:"body_len"`
	BodySHA256 string              `json:"body_sha256"`
	Headers    map[string][]string `json:"headers"`
}

type corpusExpect struct {
	Requests []corpusRequest `json:"requests"`
	Error    string          `json:"error"`
}

// sentinels maps the names used in expect.json to errors. Add new sentinels
// here when you add them to errors.go.
var sentinels = map[string]error{
	"io.ErrUnexpectedEOF":            io.ErrUnexpectedEOF,
	"ErrMalformedRequestLine":        ErrMalformedRequestLine,
	"ErrInvalidMethod":               ErrInvalidMethod,
	"ErrInvalidTarget":               ErrInvalidTarget,
	"ErrMalformedVersion":            ErrMalformedVersion,
	"ErrUnsupportedVersion":          ErrUnsupportedVersion,
	"ErrBareCR":                      ErrBareCR,
	"ErrBareLF":                      ErrBareLF,
	"ErrInvalidHeaderName":           ErrInvalidHeaderName,
	"ErrInvalidHeaderValue":          ErrInvalidHeaderValue,
	"ErrSpaceBeforeColon":            ErrSpaceBeforeColon,
	"ErrObsFold":                     ErrObsFold,
	"ErrMissingHost":                 ErrMissingHost,
	"ErrMultipleHost":                ErrMultipleHost,
	"ErrInvalidHost":                 ErrInvalidHost,
	"ErrInvalidContentLength":        ErrInvalidContentLength,
	"ErrConflictingContentLength":    ErrConflictingContentLength,
	"ErrContentLengthWithChunked":    ErrContentLengthWithChunked,
	"ErrUnsupportedTransferEncoding": ErrUnsupportedTransferEncoding,
	"ErrInvalidChunkSize":            ErrInvalidChunkSize,
	"ErrMalformedChunk":              ErrMalformedChunk,
	"ErrTruncatedBody":               ErrTruncatedBody,
	"ErrRequestLineTooLong":          ErrRequestLineTooLong,
	"ErrHeaderTooLarge":              ErrHeaderTooLarge,
	"ErrTooManyHeaders":              ErrTooManyHeaders,
	"ErrBodyTooLarge":                ErrBodyTooLarge,
	"ErrChunkLineTooLong":            ErrChunkLineTooLong,
	"ErrReadTimeout":                 ErrReadTimeout,
}

type corpusFile struct {
	name   string // e.g. "invalid/bare-cr.raw"
	data   []byte
	expect *corpusExpect // Nil if the manifest has no entry.
}

func loadCorpus(t testing.TB) []corpusFile {
	t.Helper()
	paths, _ := filepath.Glob("testdata/requests/*/*.raw")
	sort.Strings(paths)
	if len(paths) == 0 {
		t.Skip("no corpus in testdata/requests")
	}

	manifest := map[string]*corpusExpect{}
	if b, err := os.ReadFile("testdata/requests/expect.json"); err == nil {
		if err := json.Unmarshal(b, &manifest); err != nil {
			t.Fatalf("expect.json: %v", err)
		}
	}

	var files []corpusFile
	seen := map[string]bool{}
	for _, p := range paths {
		data, err := os.ReadFile(p)
		if err != nil {
			t.Fatal(err)
		}
		name := filepath.ToSlash(strings.TrimPrefix(p, "testdata/requests/"))
		seen[name] = true
		files = append(files, corpusFile{name, data, manifest[name]})
	}
	for name := range manifest {
		if !seen[name] {
			t.Errorf("expect.json lists %s, which does not exist", name)
		}
	}
	return files
}

func (c corpusFile) wantError(t *testing.T) (want error, hasWant bool) {
	t.Helper()
	if c.expect == nil || c.expect.Error == "" {
		return nil, false
	}
	err, ok := sentinels[c.expect.Error]
	if !ok {
		t.Fatalf("expect.json names unknown error %q: add it to sentinels", c.expect.Error)
	}
	return err, true
}

type parsedRequest struct {
	req  *Request
	body []byte
}

// parseAll reads requests and their bodies until the input ends or fails.
// A clean end of input is a nil error.
func parseAll(data []byte, lim Limits) ([]parsedRequest, error) {
	br := bufio.NewReader(strings.NewReader(string(data)))
	var out []parsedRequest
	for {
		req, err := ReadRequest(br, lim)
		if err == io.EOF {
			return out, nil
		}
		if err != nil {
			return out, err
		}
		body, err := io.ReadAll(req.Body)
		if err != nil {
			return out, err
		}
		out = append(out, parsedRequest{req, body})
	}
}

func (e corpusRequest) wantBody(t *testing.T) (body []byte, exact bool) {
	t.Helper()
	switch {
	case e.Body != nil:
		return []byte(*e.Body), true
	case e.BodyB64 != "":
		b, err := base64.StdEncoding.DecodeString(e.BodyB64)
		if err != nil {
			t.Fatal(err)
		}
		return b, true
	}
	return nil, false // Large body: only length and hash are recorded.
}

func TestCorpusParser(t *testing.T) {
	for _, c := range loadCorpus(t) {
		t.Run(c.name, func(t *testing.T) {
			got, err := parseAll(c.data, DefaultLimits())
			want, hasWant := c.wantError(t)

			switch {
			case c.expect == nil && strings.HasPrefix(c.name, "valid/"):
				if err != nil {
					t.Fatalf("valid request rejected: %v", err)
				}
				return
			case c.expect == nil:
				if err == nil {
					t.Fatal("invalid request accepted")
				}
				return
			case !hasWant && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case hasWant && !errors.Is(err, want):
				t.Fatalf("error = %v, want %v (after %d requests)", err, want, len(got))
			}

			if len(got) != len(c.expect.Requests) {
				t.Fatalf("parsed %d requests, want %d (error: %v)", len(got), len(c.expect.Requests), err)
			}
			for i, w := range c.expect.Requests {
				g := got[i]
				if g.req.Method != w.Method || g.req.Path != w.Path || g.req.RawQuery != w.Query ||
					g.req.Host != w.Host || g.req.Minor != w.Minor {
					t.Errorf("request %d: got %s %q ?%q host=%q minor=%d, want %s %q ?%q host=%q minor=%d", i,
						g.req.Method, g.req.Path, g.req.RawQuery, g.req.Host, g.req.Minor,
						w.Method, w.Path, w.Query, w.Host, w.Minor)
				}
				for name, values := range w.Headers {
					if !reflect.DeepEqual(g.req.Header.Values(name), values) {
						t.Errorf("request %d header %s = %q, want %q", i, name, g.req.Header.Values(name), values)
					}
				}
				if body, exact := w.wantBody(t); exact {
					if string(g.body) != string(body) {
						t.Errorf("request %d body = %q, want %q", i, g.body, body)
					}
				} else {
					sum := sha256.Sum256(g.body)
					if len(g.body) != w.BodyLen || hex.EncodeToString(sum[:]) != w.BodySHA256 {
						t.Errorf("request %d body: len %d, want %d, or hash differs", i, len(g.body), w.BodyLen)
					}
				}
			}
		})
	}
}

// Every sentinel in errors.go should be provoked by at least one file.
func TestCorpusCoversEverySentinel(t *testing.T) {
	used := map[string]bool{}
	for _, c := range loadCorpus(t) {
		if c.expect != nil {
			used[c.expect.Error] = true
		}
	}
	var missing []string
	for name := range sentinels {
		if !used[name] {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	// ErrReadTimeout comes from the connection, not the parser.
	missing = removeString(missing, "ErrReadTimeout")
	if len(missing) > 0 {
		t.Errorf("no corpus file produces: %v", missing)
	}
}

func removeString(s []string, v string) []string {
	out := s[:0]
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}

// corpusHandler accepts everything. A body that fails to decode is answered
// with the status of the error, as a real application should.
func corpusHandler(w *ResponseWriter, r *Request) {
	if _, err := io.Copy(io.Discard, r.Body); err != nil {
		status, ok := StatusCode(err)
		if !ok {
			status = 400
		}
		Error(w, status, "body error")
		return
	}
	io.WriteString(w, "ok")
}

// The same files, sent over a real TCP connection to a running server: the
// statuses on the wire must match what the parser decided.
func TestCorpusOverTheWire(t *testing.T) {
	addr, _ := startServer(t, corpusHandler, Limits{})

	for _, c := range loadCorpus(t) {
		if c.expect == nil {
			continue
		}
		t.Run(c.name, func(t *testing.T) {
			want, hasWant := c.wantError(t)
			statuses, err := sendRaw(addr, c.data, c.expect.Requests)
			if err != nil {
				t.Fatal(err)
			}

			expected := make([]int, len(c.expect.Requests))
			for i := range expected {
				expected[i] = 200
			}
			var pe *ParseError
			if hasWant && errors.As(want, &pe) {
				expected = append(expected, pe.Status)
			}
			// A truncated head gets no response at all: the server just closes.
			if len(statuses) != len(expected) || (len(expected) > 0 && !reflect.DeepEqual(statuses, expected)) {
				t.Errorf("statuses = %v, want %v", statuses, expected)
			}
		})
	}
}

// sendRaw writes data to a fresh connection, half-closes it, and returns the
// status of every final response until the server closes. Interim 1xx
// responses are skipped.
func sendRaw(addr string, data []byte, reqs []corpusRequest) ([]int, error) {
	c, err := net.Dial("tcp", addr)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(10 * time.Second))

	// Written concurrently: the server may answer and close before it has
	// read everything, which would otherwise deadlock or fail the write.
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		c.Write(data)
		c.(*net.TCPConn).CloseWrite()
	}()
	defer wg.Wait()

	br := bufio.NewReader(c)
	var statuses []int
	for {
		method := "GET"
		if i := len(statuses); i < len(reqs) {
			method = reqs[i].Method
		}
		// ReadResponse reports even a clean EOF as unexpected, so look first.
		if _, err := br.Peek(1); err == io.EOF {
			return statuses, nil
		}
		resp, err := http.ReadResponse(br, &http.Request{Method: method})
		if err != nil {
			return statuses, fmt.Errorf("after statuses %v: %w", statuses, err)
		}
		if _, err := io.Copy(io.Discard, resp.Body); err != nil {
			return statuses, fmt.Errorf("reading body: %w", err)
		}
		if resp.StatusCode/100 == 1 {
			continue
		}
		statuses = append(statuses, resp.StatusCode)
	}
}
