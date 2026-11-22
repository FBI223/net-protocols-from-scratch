package http1

import (
	"bufio"
	"bytes"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/hex"
	"fmt"
	"io"
	"math/big"
	"net"
	"net/http"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

// System tests: the whole server, real sockets, a standard HTTP client on the
// other side. Run them with -race.

// serveOn runs a server on an arbitrary listener and stops it at cleanup.
func serveOn(t *testing.T, l net.Listener, h HandlerFunc, lim Limits) *Server {
	t.Helper()
	srv := &Server{Handler: h, Limits: lim}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	t.Cleanup(func() {
		srv.Close()
		<-done
	})
	return srv
}

// selfSigned returns a TLS server config and a client pool that trusts it.
func selfSigned(t *testing.T) (*tls.Config, *x509.CertPool) {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		DNSNames:     []string{"localhost"},
		IPAddresses:  []net.IP{net.IPv4(127, 0, 0, 1), net.IPv6loopback},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	cert, _ := x509.ParseCertificate(der)
	pool := x509.NewCertPool()
	pool.AddCert(cert)
	return &tls.Config{
		Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}},
		// Our server speaks HTTP/1.1 only, so do not offer h2.
		NextProtos: []string{"http/1.1"},
	}, pool
}

func sum(b []byte) string {
	s := sha256.Sum256(b)
	return hex.EncodeToString(s[:])
}

// appHandler is a small application used by the system tests: /echo returns
// the body, /sha returns the SHA-256 of the body without buffering it, and
// /gen/{n} streams n bytes of a repeating pattern.
func appHandler(w *ResponseWriter, r *Request) {
	switch {
	case r.Path == "/echo":
		io.Copy(w, r.Body)
	case r.Path == "/sha":
		h := sha256.New()
		n, err := io.Copy(h, r.Body)
		if err != nil {
			Error(w, statusOf(err), "bad body")
			return
		}
		fmt.Fprintf(w, "%d %s", n, hex.EncodeToString(h.Sum(nil)))
	case strings.HasPrefix(r.Path, "/gen/"):
		var n int
		fmt.Sscan(r.Path[len("/gen/"):], &n)
		io.Copy(w, io.LimitReader(&patternReader{}, int64(n)))
	default:
		fmt.Fprintf(w, "hello %s", r.Path)
	}
}

func statusOf(err error) int {
	if s, ok := StatusCode(err); ok {
		return s
	}
	return 400
}

// patternReader yields bytes 0,1,...,250,0,1,... forever.
type patternReader struct{ pos int }

func (p *patternReader) Read(b []byte) (int, error) {
	for i := range b {
		b[i] = byte((p.pos + i) % 251)
	}
	p.pos += len(b)
	return len(b), nil
}

func patternBytes(n int) []byte {
	b := make([]byte, n)
	(&patternReader{}).Read(b)
	return b
}

// TestTransports runs the same exchange over every way of exposing the
// server: TCP on IPv4 and IPv6, a Unix socket, and TLS on top of TCP.
func TestTransports(t *testing.T) {
	tlsCfg, pool := selfSigned(t)

	transports := []struct {
		name   string
		listen func(t *testing.T) (net.Listener, func(*http.Transport), string)
	}{
		{"tcp4", func(t *testing.T) (net.Listener, func(*http.Transport), string) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			return l, func(*http.Transport) {}, "http://" + l.Addr().String()
		}},
		{"tcp6", func(t *testing.T) (net.Listener, func(*http.Transport), string) {
			l, err := net.Listen("tcp", "[::1]:0")
			if err != nil {
				t.Skipf("no IPv6: %v", err)
			}
			return l, func(*http.Transport) {}, "http://" + l.Addr().String()
		}},
		{"unix", func(t *testing.T) (net.Listener, func(*http.Transport), string) {
			path := filepath.Join(t.TempDir(), "s.sock")
			l, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			dial := func(tr *http.Transport) {
				tr.DialContext = func(ctx context.Context, _, _ string) (net.Conn, error) {
					return (&net.Dialer{}).DialContext(ctx, "unix", path)
				}
			}
			return l, dial, "http://unix"
		}},
		{"tls", func(t *testing.T) (net.Listener, func(*http.Transport), string) {
			l, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			l = tls.NewListener(l, tlsCfg)
			cfg := func(tr *http.Transport) { tr.TLSClientConfig = &tls.Config{RootCAs: pool} }
			return l, cfg, "https://localhost:" + fmt.Sprint(l.Addr().(*net.TCPAddr).Port)
		}},
	}

	for _, tr := range transports {
		t.Run(tr.name, func(t *testing.T) {
			l, configure, base := tr.listen(t)
			lim := DefaultLimits()
			lim.MaxBodyBytes = 16 << 20
			serveOn(t, l, appHandler, lim)

			transport := &http.Transport{ForceAttemptHTTP2: false, MaxIdleConnsPerHost: 1}
			configure(transport)
			client := &http.Client{Transport: transport, Timeout: 10 * time.Second}
			defer transport.CloseIdleConnections()

			// Many requests: the transport must reuse one connection.
			for i := 0; i < 20; i++ {
				resp, err := client.Get(base + fmt.Sprintf("/n/%d", i))
				if err != nil {
					t.Fatal(err)
				}
				body, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if resp.StatusCode != 200 || string(body) != fmt.Sprintf("hello /n/%d", i) {
					t.Fatalf("GET %d: %d %q", i, resp.StatusCode, body)
				}
			}

			// Known-length upload, unknown-length (chunked) upload, big download.
			payload := patternBytes(3 << 20)
			for name, body := range map[string]io.Reader{
				"content-length": bytes.NewReader(payload),
				"chunked":        io.MultiReader(bytes.NewReader(payload)), // Unknown length.
			} {
				resp, err := client.Post(base+"/sha", "application/octet-stream", body)
				if err != nil {
					t.Fatalf("%s upload: %v", name, err)
				}
				got, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				want := fmt.Sprintf("%d %s", len(payload), sum(payload))
				if string(got) != want {
					t.Errorf("%s upload: got %q, want %q", name, got, want)
				}
			}

			resp, err := client.Get(base + "/gen/5000000")
			if err != nil {
				t.Fatal(err)
			}
			got, err := io.ReadAll(resp.Body)
			resp.Body.Close()
			if err != nil || !bytes.Equal(got, patternBytes(5000000)) {
				t.Errorf("download: %d bytes, err %v", len(got), err)
			}
			if len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" {
				t.Errorf("large response not chunked: %v", resp.TransferEncoding)
			}
		})
	}
}

// Many clients at once, each checking its own data. Catches races and any
// mix-up of state between connections.
func TestConcurrentClients(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	lim := DefaultLimits()
	lim.MaxBodyBytes = 1 << 20
	serveOn(t, l, appHandler, lim)
	base := "http://" + l.Addr().String()

	const clients, rounds = 40, 40
	var wg sync.WaitGroup
	errs := make(chan error, clients)
	for c := 0; c < clients; c++ {
		wg.Add(1)
		go func(c int) {
			defer wg.Done()
			tr := &http.Transport{MaxIdleConnsPerHost: 1}
			defer tr.CloseIdleConnections()
			client := &http.Client{Transport: tr, Timeout: 20 * time.Second}
			for i := 0; i < rounds; i++ {
				payload := bytes.Repeat([]byte{byte(c), byte(i)}, 1+(c*131+i*17)%5000)
				resp, err := client.Post(base+"/echo", "x/y", bytes.NewReader(payload))
				if err != nil {
					errs <- fmt.Errorf("client %d round %d: %w", c, i, err)
					return
				}
				got, _ := io.ReadAll(resp.Body)
				resp.Body.Close()
				if !bytes.Equal(got, payload) {
					errs <- fmt.Errorf("client %d round %d: echo mismatch (%d vs %d bytes)", c, i, len(got), len(payload))
					return
				}
			}
		}(c)
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Error(err)
	}
}

// A request and a pipeline of requests delivered one byte per write, with the
// responses compared to a delivery in one piece.
func TestByteAtATimeDelivery(t *testing.T) {
	addr, _ := startServer(t, appHandler, DefaultLimits())

	input := "GET /a HTTP/1.1\r\nHost: h\r\n\r\n" +
		"POST /echo HTTP/1.1\r\nHost: h\r\nContent-Length: 11\r\n\r\nhello world" +
		"POST /echo HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6;x=y\r\n world\r\n0\r\nT: v\r\n\r\n" +
		"HEAD /b HTTP/1.1\r\nHost: h\r\n\r\n" +
		"GET /c HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n"
	methods := []string{"GET", "POST", "POST", "HEAD", "GET"}

	exchange := func(chunk int) []string {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		c.(*net.TCPConn).SetNoDelay(true)
		c.SetDeadline(time.Now().Add(15 * time.Second))
		go func() {
			for i := 0; i < len(input); i += chunk {
				end := min(i+chunk, len(input))
				io.WriteString(c, input[i:end])
			}
		}()
		br := bufio.NewReader(c)
		var out []string
		for _, m := range methods {
			resp, err := http.ReadResponse(br, &http.Request{Method: m})
			if err != nil {
				t.Fatalf("chunk %d: %v", chunk, err)
			}
			body, _ := io.ReadAll(resp.Body)
			out = append(out, fmt.Sprintf("%d %q", resp.StatusCode, body))
		}
		return out
	}

	want := exchange(len(input))
	if want[1] != `200 "hello world"` || want[2] != `200 "hello world"` {
		t.Fatalf("baseline wrong: %v", want)
	}
	for _, chunk := range []int{1, 2, 3, 7} {
		got := exchange(chunk)
		if strings.Join(got, "|") != strings.Join(want, "|") {
			t.Errorf("chunk size %d: %v, want %v", chunk, got, want)
		}
	}
}

// Slow clients must not take the server down: they get cut off by the
// timeouts, and fast clients are served meanwhile.
func TestSlowlorisDoesNotBlockOthers(t *testing.T) {
	lim := DefaultLimits()
	lim.ReadHeaderTimeout = 300 * time.Millisecond
	addr, _ := startServer(t, appHandler, lim)

	const slow = 200
	var open []net.Conn
	for i := 0; i < slow; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		open = append(open, c)
		io.WriteString(c, "GET / HTTP/1.1\r\nHost: h\r\nX-Slow: ")
	}
	defer func() {
		for _, c := range open {
			c.Close()
		}
	}()

	// While 200 connections sit half-way through a header, normal requests work.
	for i := 0; i < 20; i++ {
		c := dial(t, addr)
		c.send(getRoot)
		if resp, _ := c.response("GET"); resp.StatusCode != 200 {
			t.Fatalf("fast client got %d", resp.StatusCode)
		}
	}

	// Then every slow one is answered with 408 and closed.
	for i, c := range open {
		c.SetReadDeadline(time.Now().Add(3 * time.Second))
		resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "GET"})
		if err != nil || resp.StatusCode != 408 {
			t.Fatalf("slow conn %d: %v %v", i, resp, err)
		}
	}
}

// A dripping sender that keeps every individual read under the timeout is
// still bounded: the deadline applies to the whole head, not to each read.
func TestDrippedHeadersHitTheDeadline(t *testing.T) {
	lim := DefaultLimits()
	lim.ReadHeaderTimeout = 400 * time.Millisecond
	addr, _ := startServer(t, appHandler, lim)

	c := dial(t, addr)
	go func() {
		io.WriteString(c.c, "GET / HTTP/1.1\r\nHost: h\r\n")
		for i := 0; i < 30; i++ {
			time.Sleep(100 * time.Millisecond)
			if _, err := io.WriteString(c.c, "X: y\r\n"); err != nil {
				return
			}
		}
	}()
	if resp, _ := c.response("GET"); resp.StatusCode != 408 {
		t.Errorf("status = %d, want 408", resp.StatusCode)
	}
}

// Requests that try to smuggle a second request past the framing rules. The
// handler counts how often the hidden request is executed: never.
func TestSmugglingNeverReachesTheHandler(t *testing.T) {
	var smuggled atomic.Int32
	h := func(w *ResponseWriter, r *Request) {
		if r.Path == "/smuggled" {
			smuggled.Add(1)
		}
		corpusHandler(w, r)
	}
	addr, _ := startServer(t, h, DefaultLimits())

	attacks := map[string]string{
		"CL.TE":         "POST /a HTTP/1.1\r\nHost: h\r\nContent-Length: 4\r\nTransfer-Encoding: chunked\r\n\r\n0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"TE.CL":         "POST /a HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\nContent-Length: 4\r\n\r\n0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"TE obfuscated": "POST /a HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: xchunked\r\nContent-Length: 4\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"TE with space": "POST /a HTTP/1.1\r\nHost: h\r\nTransfer-Encoding : chunked\r\nContent-Length: 4\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"two CL":        "POST /a HTTP/1.1\r\nHost: h\r\nContent-Length: 0\r\nContent-Length: 40\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"folded header": "POST /a HTTP/1.1\r\nHost: h\r\nX: y\r\n Content-Length: 40\r\nContent-Length: 0\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"bare LF":       "POST /a HTTP/1.1\nHost: h\nContent-Length: 0\n\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"two Host":      "GET /a HTTP/1.1\r\nHost: h\r\nHost: evil\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"chunk ext CR":  "POST /a HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n0;x\r\r\n\r\nGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"CL plus sign":  "POST /a HTTP/1.1\r\nHost: h\r\nContent-Length: +5\r\n\r\nhelloGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
		"CL hex":        "POST /a HTTP/1.1\r\nHost: h\r\nContent-Length: 0x5\r\n\r\nhelloGET /smuggled HTTP/1.1\r\nHost: h\r\n\r\n",
	}
	for name, attack := range attacks {
		t.Run(name, func(t *testing.T) {
			c := dial(t, addr)
			c.send(attack)
			resp, _ := c.response("POST")
			if resp.StatusCode != 400 && resp.StatusCode != 501 {
				t.Errorf("status = %d, want 400 or 501", resp.StatusCode)
			}
			c.expectClosed()
		})
	}
	// Whether /a reaches the handler depends on where the attack is caught
	// (head or body); the smuggled request must never run.
	if n := smuggled.Load(); n != 0 {
		t.Errorf("the smuggled request was executed %d times", n)
	}
}

// Shutdown under load: every request that was already running completes with
// its full answer, and nothing new is accepted afterwards.
func TestShutdownUnderLoad(t *testing.T) {
	var started sync.WaitGroup
	release := make(chan struct{})
	h := func(w *ResponseWriter, r *Request) {
		started.Done()
		<-release
		fmt.Fprintf(w, "finished %s", r.Path)
	}
	addr, srv := startServer(t, h, DefaultLimits())

	const n = 50
	started.Add(n)
	results := make(chan string, n)
	for i := 0; i < n; i++ {
		go func(i int) {
			c, err := net.Dial("tcp", addr)
			if err != nil {
				results <- err.Error()
				return
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(10 * time.Second))
			fmt.Fprintf(c, "GET /%d HTTP/1.1\r\nHost: h\r\n\r\n", i)
			resp, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "GET"})
			if err != nil {
				results <- err.Error()
				return
			}
			body, _ := io.ReadAll(resp.Body)
			results <- fmt.Sprintf("%d %s close=%v", resp.StatusCode, body, resp.Close)
		}(i)
	}
	started.Wait()

	shut := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		shut <- srv.Shutdown(ctx)
	}()
	time.Sleep(100 * time.Millisecond)
	if c, err := net.DialTimeout("tcp", addr, time.Second); err == nil {
		// Some systems complete the TCP handshake from the backlog; the
		// connection must then be dead, not served.
		c.SetDeadline(time.Now().Add(500 * time.Millisecond))
		io.WriteString(c, getRoot)
		if _, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "GET"}); err == nil {
			t.Error("a new connection was served during shutdown")
		}
		c.Close()
	}

	close(release)
	for i := 0; i < n; i++ {
		got := <-results
		if !strings.HasPrefix(got, "200 finished /") || !strings.HasSuffix(got, "close=true") {
			t.Errorf("in-flight request ended with %q", got)
		}
	}
	if err := <-shut; err != nil {
		t.Errorf("Shutdown = %v", err)
	}
}

// A client that vanishes in the middle of an upload must neither leak the
// goroutine nor leave the handler hanging.
func TestClientDisconnectMidBody(t *testing.T) {
	got := make(chan error, 1)
	h := func(w *ResponseWriter, r *Request) {
		_, err := io.Copy(io.Discard, r.Body)
		got <- err
	}
	addr, _ := startServer(t, h, DefaultLimits())
	before := runtime.NumGoroutine()

	for i := 0; i < 20; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		io.WriteString(c, "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 100000\r\n\r\npartial")
		c.Close()
		select {
		case err := <-got:
			if err == nil {
				t.Fatal("handler saw a complete body")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("handler still blocked after the client left")
		}
	}

	deadline := time.Now().Add(3 * time.Second)
	for runtime.NumGoroutine() > before+2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if after := runtime.NumGoroutine(); after > before+2 {
		t.Errorf("goroutines: %d before, %d after", before, after)
	}
}

// A client that never reads its response must not pin a connection past the
// write timeout.
func TestClientThatNeverReads(t *testing.T) {
	lim := DefaultLimits()
	lim.WriteTimeout = 300 * time.Millisecond
	finished := make(chan struct{})
	h := func(w *ResponseWriter, r *Request) {
		defer close(finished)
		chunk := make([]byte, 64<<10)
		for i := 0; i < 2000; i++ { // 128 MB: far more than the socket buffers.
			if _, err := w.Write(chunk); err != nil {
				return
			}
		}
	}
	addr, _ := startServer(t, h, lim)

	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	io.WriteString(c, getRoot)
	select {
	case <-finished:
	case <-time.After(5 * time.Second):
		t.Fatal("handler is stuck writing to a client that does not read")
	}
}

// HTTP/1.0 clients (ab, old proxies, health checks) still work.
func TestHTTP10Client(t *testing.T) {
	addr, _ := startServer(t, appHandler, DefaultLimits())
	c := dial(t, addr)
	c.send("GET /x HTTP/1.0\r\n\r\n")
	resp, body := c.response("GET")
	if resp.StatusCode != 200 || body != "hello /x" || resp.ProtoMinor != 1 && resp.ProtoMinor != 0 {
		t.Errorf("%d %q", resp.StatusCode, body)
	}
	c.expectClosed()
}

// Everything over a connection limit is refused with 503, not queued.
func TestManyIdleConnectionsAndLimit(t *testing.T) {
	addr, _ := startServer(t, appHandler, DefaultLimits(), func(s *Server) { s.MaxConns = 100 })
	var held []net.Conn
	defer func() {
		for _, c := range held {
			c.Close()
		}
	}()
	for i := 0; i < 100; i++ {
		c, err := net.Dial("tcp", addr)
		if err != nil {
			t.Fatal(err)
		}
		held = append(held, c)
		fmt.Fprint(c, getRoot)
		if _, err := http.ReadResponse(bufio.NewReader(c), &http.Request{Method: "GET"}); err != nil {
			t.Fatalf("connection %d: %v", i, err)
		}
	}
	c := dial(t, addr)
	if resp, _ := c.response("GET"); resp.StatusCode != 503 {
		t.Errorf("connection 101 got %d, want 503", resp.StatusCode)
	}
}
