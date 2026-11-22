package http1

import (
	"bufio"
	"bytes"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// siteFixture builds a directory tree plus a secret outside of it:
//
//	root/hello.txt  index.html  data.bin (bytes 0..99)  empty.txt  odd.weird
//	root/sub/index.html  sub/page.css   root/nolist/x.txt   root/"a&b <x>.txt"
//	root/inside-link -> hello.txt    root/escape-file -> ../secret.txt
//	root/escape-dir -> ..           secret.txt (outside)
func siteFixture(t *testing.T) (root string, data []byte) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "root")
	write := func(name, content string) {
		p := filepath.Join(root, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	data = make([]byte, 100)
	for i := range data {
		data[i] = byte(i)
	}
	write("hello.txt", "hello world\n")
	write("index.html", "<h1>home</h1>")
	write("data.bin", string(data))
	write("empty.txt", "")
	write("odd.weird", "?")
	write("sub/index.html", "<h1>sub</h1>")
	write("sub/page.css", "body{}")
	write("nolist/x.txt", "x")
	write("a&b <x>.txt", "tricky")
	if err := os.WriteFile(filepath.Join(base, "secret.txt"), []byte("TOP SECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	for link, target := range map[string]string{
		"inside-link": "hello.txt",
		"escape-file": "../secret.txt",
		"escape-dir":  "..",
	} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Skipf("symlinks unavailable: %v", err)
		}
	}
	return root, data
}

func siteRouter(t *testing.T, root string, listDirs bool) *Router {
	t.Helper()
	fs, err := NewFileServer(root)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { fs.Close() })
	fs.ListDirs = listDirs
	rt := &Router{}
	rt.Handle("GET", "/static/{path...}", fs)
	rt.Handle("POST", "/static/{path...}", fs) // Lets the 405 come from the FileServer itself.
	return rt
}

// fetch sends one request (with extra "Name: value" headers) through h.
func fetch(t *testing.T, h Handler, method, target string, headers ...string) (*http.Response, string) {
	t.Helper()
	raw := method + " " + target + " HTTP/1.1\r\nHost: h\r\n" + strings.Join(append(headers, ""), "\r\n") + "\r\n"
	req, err := ReadRequest(bufio.NewReader(strings.NewReader(raw)), DefaultLimits())
	if err != nil {
		t.Fatalf("%q: %v", raw, err)
	}
	var buf bytes.Buffer
	rw := newResponseWriter(bufio.NewWriter(&buf), req)
	h.ServeHTTP(rw, req)
	if err := rw.finish(); err != nil {
		t.Fatalf("%s %s: %v", method, target, err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(&buf), &http.Request{Method: method})
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	return resp, string(body)
}

func TestFileServerServesFiles(t *testing.T) {
	root, _ := siteFixture(t)
	rt := siteRouter(t, root, false)

	resp, body := fetch(t, rt, "GET", "/static/hello.txt")
	if resp.StatusCode != 200 || body != "hello world\n" {
		t.Fatalf("got %d %q", resp.StatusCode, body)
	}
	for name, want := range map[string]string{
		"Content-Type":           "text/plain; charset=utf-8",
		"Content-Length":         "12",
		"Accept-Ranges":          "bytes",
		"X-Content-Type-Options": "nosniff",
	} {
		if got := resp.Header.Get(name); got != want {
			t.Errorf("%s = %q, want %q", name, got, want)
		}
	}
	if _, err := time.Parse(timeFormat, resp.Header.Get("Last-Modified")); err != nil {
		t.Errorf("Last-Modified: %v", err)
	}
	if !strings.HasPrefix(resp.Header.Get("ETag"), `W/"`) {
		t.Errorf("ETag = %q", resp.Header.Get("ETag"))
	}

	resp, body = fetch(t, rt, "HEAD", "/static/hello.txt")
	if resp.StatusCode != 200 || body != "" || resp.ContentLength != 12 {
		t.Errorf("HEAD: %d len=%d %q", resp.StatusCode, resp.ContentLength, body)
	}

	if resp, body := fetch(t, rt, "GET", "/static/empty.txt"); resp.StatusCode != 200 || body != "" || resp.ContentLength != 0 {
		t.Errorf("empty file: %d %d %q", resp.StatusCode, resp.ContentLength, body)
	}
	if resp, body := fetch(t, rt, "GET", "/static/inside-link"); resp.StatusCode != 200 || body != "hello world\n" {
		t.Errorf("symlink inside root: %d %q", resp.StatusCode, body)
	}
	if resp, _ := fetch(t, rt, "GET", "/static/hello.txt?cache=bust"); resp.StatusCode != 200 {
		t.Errorf("query: %d", resp.StatusCode)
	}
}

func TestFileServerContentTypes(t *testing.T) {
	root, _ := siteFixture(t)
	rt := siteRouter(t, root, false)
	for target, prefix := range map[string]string{
		"/static/sub/page.css": "text/css",
		"/static/sub/":         "text/html",
		"/static/odd.weird":    "application/octet-stream",
		"/static/data.bin":     "application/octet-stream",
	} {
		resp, _ := fetch(t, rt, "GET", target)
		if got := resp.Header.Get("Content-Type"); !strings.HasPrefix(got, prefix) {
			t.Errorf("%s: Content-Type = %q, want prefix %q", target, got, prefix)
		}
	}
}

func TestFileServerDirectories(t *testing.T) {
	root, _ := siteFixture(t)

	rt := siteRouter(t, root, false)
	if resp, body := fetch(t, rt, "GET", "/static/"); resp.StatusCode != 200 || body != "<h1>home</h1>" {
		t.Errorf("root index: %d %q", resp.StatusCode, body)
	}
	if resp, body := fetch(t, rt, "GET", "/static/sub/"); resp.StatusCode != 200 || body != "<h1>sub</h1>" {
		t.Errorf("sub index: %d %q", resp.StatusCode, body)
	}
	resp, _ := fetch(t, rt, "GET", "/static/sub")
	if resp.StatusCode != 301 || resp.Header.Get("Location") != "/static/sub/" {
		t.Errorf("redirect: %d Location=%q", resp.StatusCode, resp.Header.Get("Location"))
	}
	if resp, _ := fetch(t, rt, "GET", "/static/nolist/"); resp.StatusCode != 404 {
		t.Errorf("listing disabled: %d", resp.StatusCode)
	}

	rt = siteRouter(t, root, true)
	resp, body := fetch(t, rt, "GET", "/static/nolist/")
	if resp.StatusCode != 200 || !strings.Contains(body, `<a href="x.txt">x.txt</a>`) {
		t.Errorf("listing: %d %q", resp.StatusCode, body)
	}
	// File names are data: they must be escaped in HTML and in the URL.
	listing := listRoot(t, root)
	if !strings.Contains(listing, "a&amp;b &lt;x&gt;.txt") || strings.Contains(listing, "<x>") {
		t.Errorf("file name not escaped: %s", listing)
	}
	if !strings.Contains(listing, `href="a&amp;b%20%3Cx%3E.txt"`) {
		t.Errorf("href not escaped: %s", listing)
	}
}

// listRoot lists a directory that has no index.html.
func listRoot(t *testing.T, root string) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "listed")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a&b <x>.txt"), []byte("t"), 0o644); err != nil {
		t.Fatal(err)
	}
	os.Mkdir(filepath.Join(dir, "d"), 0o755)
	rt := siteRouter(t, dir, true)
	resp, body := fetch(t, rt, "GET", "/static/")
	if resp.StatusCode != 200 || !strings.Contains(body, `href="d/"`) {
		t.Fatalf("listing: %d %q", resp.StatusCode, body)
	}
	return body
}

func TestFileServerCannotEscapeRoot(t *testing.T) {
	root, _ := siteFixture(t)
	rt := siteRouter(t, root, true)

	for _, target := range []string{
		"/static/escape-file",           // Symlink to a file outside.
		"/static/escape-dir/secret.txt", // Symlink to a directory outside.
		"/static/escape-dir/",
		"/static/escape-dir//",
		"/static/escape-dir//secret.txt",
	} {
		resp, body := fetch(t, rt, "GET", target)
		if resp.StatusCode != 404 || strings.Contains(body, "SECRET") {
			t.Errorf("%s: %d %q", target, resp.StatusCode, body)
		}
	}
	// Dot segments never reach the FileServer: the Router rejects them.
	for _, target := range []string{
		"/static/../secret.txt", "/static/%2e%2e/secret.txt", "/static/sub/../../secret.txt",
		"/static/..%2fsecret.txt", "/static/escape-dir/%2e/secret.txt", "/static/inside-link/../secret.txt",
	} {
		resp, body := fetch(t, rt, "GET", target)
		if resp.StatusCode == 200 || strings.Contains(body, "SECRET") {
			t.Errorf("%s: %d %q", target, resp.StatusCode, body)
		}
	}
	// Calling ServeFile directly with hostile names is safe too: os.Root checks.
	fs, _ := NewFileServer(root)
	defer fs.Close()
	for _, name := range []string{"../secret.txt", "sub/../../secret.txt", "/etc/passwd", "escape-file",
		"escape-dir/", "escape-dir//", "./escape-dir/", "escape-dir/./secret.txt", "sub//x", "sub\\x", "a\x00b"} {
		resp, body := fetch(t, HandlerFunc(func(w *ResponseWriter, r *Request) { fs.ServeFile(w, r, name) }), "GET", "/x")
		if resp.StatusCode != 404 || strings.Contains(body, "SECRET") {
			t.Errorf("ServeFile(%q): %d %q", name, resp.StatusCode, body)
		}
	}
}

func TestFileServerMissingAndMethods(t *testing.T) {
	root, _ := siteFixture(t)
	rt := siteRouter(t, root, false)
	if resp, _ := fetch(t, rt, "GET", "/static/nope.txt"); resp.StatusCode != 404 {
		t.Errorf("missing: %d", resp.StatusCode)
	}
	if resp, _ := fetch(t, rt, "GET", "/static/hello.txt/"); resp.StatusCode != 404 {
		t.Errorf("file with trailing slash: %d", resp.StatusCode)
	}
	resp, _ := fetch(t, rt, "POST", "/static/hello.txt")
	if resp.StatusCode != 405 || resp.Header.Get("Allow") != "GET, HEAD, OPTIONS" {
		t.Errorf("POST: %d Allow=%q", resp.StatusCode, resp.Header.Get("Allow"))
	}
}

func TestFileServerConditional(t *testing.T) {
	root, _ := siteFixture(t)
	rt := siteRouter(t, root, false)
	first, _ := fetch(t, rt, "GET", "/static/hello.txt")
	etag, lm := first.Header.Get("ETag"), first.Header.Get("Last-Modified")
	past := time.Now().Add(-48 * time.Hour).UTC().Format(timeFormat)
	future := time.Now().Add(48 * time.Hour).UTC().Format(timeFormat)

	tests := []struct {
		name    string
		headers []string
		status  int
	}{
		{"matching etag", []string{"If-None-Match: " + etag}, 304},
		{"strong form of weak etag matches", []string{"If-None-Match: " + strings.TrimPrefix(etag, "W/")}, 304},
		{"etag in list", []string{`If-None-Match: "other", ` + etag}, 304},
		{"star", []string{"If-None-Match: *"}, 304},
		{"other etag", []string{`If-None-Match: "other"`}, 200},
		{"if-modified-since equal", []string{"If-Modified-Since: " + lm}, 304},
		{"if-modified-since future", []string{"If-Modified-Since: " + future}, 304},
		{"if-modified-since past", []string{"If-Modified-Since: " + past}, 200},
		{"if-modified-since garbage", []string{"If-Modified-Since: yesterday"}, 200},
		{"etag mismatch wins over date match", []string{`If-None-Match: "other"`, "If-Modified-Since: " + lm}, 200},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			resp, body := fetch(t, rt, "GET", "/static/hello.txt", tc.headers...)
			if resp.StatusCode != tc.status {
				t.Fatalf("status = %d, want %d", resp.StatusCode, tc.status)
			}
			if tc.status == 304 && (body != "" || resp.Header.Get("ETag") != etag || resp.Header.Get("Content-Length") != "") {
				t.Errorf("304 must carry validators and no body: body=%q headers=%v", body, resp.Header)
			}
		})
	}
}

func TestFileServerRanges(t *testing.T) {
	root, data := siteFixture(t)
	rt := siteRouter(t, root, false)
	lm := func() string {
		resp, _ := fetch(t, rt, "GET", "/static/data.bin")
		return resp.Header.Get("Last-Modified")
	}()

	tests := []struct {
		rng          string
		status       int
		from, to     int // Inclusive, for 206.
		contentRange string
	}{
		{"bytes=0-9", 206, 0, 9, "bytes 0-9/100"},
		{"bytes=10-19", 206, 10, 19, "bytes 10-19/100"},
		{"bytes=90-", 206, 90, 99, "bytes 90-99/100"},
		{"bytes=-10", 206, 90, 99, "bytes 90-99/100"},
		{"bytes=-200", 206, 0, 99, "bytes 0-99/100"},
		{"bytes=0-1000", 206, 0, 99, "bytes 0-99/100"},
		{"bytes=99-99", 206, 99, 99, "bytes 99-99/100"},
		{"bytes=0-0", 206, 0, 0, "bytes 0-0/100"},
		{"bytes=100-", 416, 0, 0, "bytes */100"},
		{"bytes=500-600", 416, 0, 0, "bytes */100"},
		{"bytes=-0", 416, 0, 0, "bytes */100"},
		// Ignored: the whole file is sent.
		{"bytes=50-40", 200, 0, 99, ""},
		{"bytes=0-1,5-6", 200, 0, 99, ""},
		{"items=0-5", 200, 0, 99, ""},
		{"bytes=abc", 200, 0, 99, ""},
		{"bytes=", 200, 0, 99, ""},
		{"bytes=-", 200, 0, 99, ""},
		{"bytes=1-2-3", 200, 0, 99, ""},
		{"bytes=+5-9", 200, 0, 99, ""},
	}
	for _, tc := range tests {
		t.Run(tc.rng, func(t *testing.T) {
			resp, body := fetch(t, rt, "GET", "/static/data.bin", "Range: "+tc.rng)
			if resp.StatusCode != tc.status || resp.Header.Get("Content-Range") != tc.contentRange {
				t.Fatalf("got %d Content-Range=%q, want %d %q", resp.StatusCode, resp.Header.Get("Content-Range"), tc.status, tc.contentRange)
			}
			if tc.status != 416 && body != string(data[tc.from:tc.to+1]) {
				t.Errorf("body = %v", []byte(body))
			}
		})
	}

	// HEAD with a range reports the partial length and sends nothing.
	resp, body := fetch(t, rt, "HEAD", "/static/data.bin", "Range: bytes=10-19")
	if resp.StatusCode != 206 || body != "" || resp.ContentLength != 10 {
		t.Errorf("HEAD range: %d len=%d %q", resp.StatusCode, resp.ContentLength, body)
	}
	// If-Range: the range is honoured only while the validator matches.
	if resp, _ := fetch(t, rt, "GET", "/static/data.bin", "Range: bytes=0-9", "If-Range: "+lm); resp.StatusCode != 206 {
		t.Errorf("If-Range match: %d", resp.StatusCode)
	}
	if resp, body := fetch(t, rt, "GET", "/static/data.bin", "Range: bytes=0-9", "If-Range: Mon, 01 Jan 2001 00:00:00 GMT"); resp.StatusCode != 200 || len(body) != 100 {
		t.Errorf("If-Range mismatch: %d len=%d", resp.StatusCode, len(body))
	}
	// An empty file has nothing to range over: the header is ignored.
	if resp, _ := fetch(t, rt, "GET", "/static/empty.txt", "Range: bytes=-5"); resp.StatusCode != 200 {
		t.Errorf("empty file suffix range: %d", resp.StatusCode)
	}
}

// Behaviour on the wire, compared with net/http's own file server.
func TestFileServerAgainstStdlib(t *testing.T) {
	root, _ := siteFixture(t)
	rt := siteRouter(t, root, false)
	addr, _ := startServer(t, rt.ServeHTTP, Limits{})

	std := httptest.NewServer(http.StripPrefix("/static/", http.FileServer(http.Dir(root))))
	t.Cleanup(std.Close)

	client := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	get := func(base, method, target, rng string) (int, string, string) {
		req, _ := http.NewRequest(method, base+target, nil)
		if rng != "" {
			req.Header.Set("Range", rng)
		}
		resp, err := client.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return resp.StatusCode, string(b), resp.Header.Get("Content-Range")
	}

	cases := []struct{ method, target, rng string }{
		{"GET", "/static/hello.txt", ""},
		{"HEAD", "/static/hello.txt", ""},
		{"GET", "/static/data.bin", ""},
		{"GET", "/static/empty.txt", ""},
		{"GET", "/static/missing", ""},
		{"GET", "/static/data.bin", "bytes=0-9"},
		{"GET", "/static/data.bin", "bytes=90-"},
		{"GET", "/static/data.bin", "bytes=-10"},
		{"GET", "/static/data.bin", "bytes=0-1000"},
		{"GET", "/static/data.bin", "bytes=99-99"},
		{"GET", "/static/data.bin", "bytes=100-"},
		{"GET", "/static/data.bin", "bytes=500-600"},
		{"HEAD", "/static/data.bin", "bytes=10-19"},
		{"GET", "/static/sub/", ""},
	}
	for _, c := range cases {
		wantStatus, wantBody, wantCR := get(std.URL, c.method, c.target, c.rng)
		gotStatus, gotBody, gotCR := get("http://"+addr, c.method, c.target, c.rng)
		if wantStatus >= 400 {
			gotBody, wantBody = "", "" // Error texts differ by design.
		}
		if gotStatus != wantStatus || gotBody != wantBody || gotCR != wantCR {
			t.Errorf("%s %s [%s]: ours %d %q %q, stdlib %d %q %q",
				c.method, c.target, c.rng, gotStatus, gotBody, gotCR, wantStatus, wantBody, wantCR)
		}
	}
}

func TestFileServerLargeFileStreams(t *testing.T) {
	root, _ := siteFixture(t)
	big := bytes.Repeat([]byte("0123456789abcdef"), 1<<16) // 1 MiB.
	if err := os.WriteFile(filepath.Join(root, "big.bin"), big, 0o644); err != nil {
		t.Fatal(err)
	}
	rt := siteRouter(t, root, false)
	addr, _ := startServer(t, rt.ServeHTTP, Limits{})

	resp, err := http.Get("http://" + addr + "/static/big.bin")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	got, err := io.ReadAll(resp.Body)
	if err != nil || !bytes.Equal(got, big) || resp.ContentLength != int64(len(big)) {
		t.Errorf("len=%d err=%v ContentLength=%d", len(got), err, resp.ContentLength)
	}
}
