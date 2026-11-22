package http1

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"
)

func startServer(t *testing.T, h HandlerFunc, lim Limits, opts ...func(*Server)) (string, *Server) {
	t.Helper()
	srv := &Server{Handler: h, Limits: lim}
	for _, o := range opts {
		o(srv)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(l) }()
	t.Cleanup(func() {
		srv.Close()
		if err := <-done; !errors.Is(err, ErrServerClosed) {
			t.Errorf("Serve returned %v, want ErrServerClosed", err)
		}
	})
	return l.Addr().String(), srv
}

type client struct {
	t  *testing.T
	c  net.Conn
	br *bufio.Reader
}

func dial(t *testing.T, addr string) *client {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	c.SetDeadline(time.Now().Add(5 * time.Second))
	t.Cleanup(func() { c.Close() })
	return &client{t: t, c: c, br: bufio.NewReader(c)}
}

func (c *client) send(s string) {
	c.t.Helper()
	if _, err := io.WriteString(c.c, s); err != nil {
		c.t.Fatal(err)
	}
}

// response reads one response and its whole body.
func (c *client) response(method string) (*http.Response, string) {
	c.t.Helper()
	resp, err := http.ReadResponse(c.br, &http.Request{Method: method})
	if err != nil {
		c.t.Fatalf("reading response: %v", err)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		c.t.Fatalf("reading body: %v", err)
	}
	return resp, string(body)
}

// expectClosed requires a clean EOF: a reset would mean the server closed a
// socket with unread data and may have destroyed its own response.
func (c *client) expectClosed() {
	c.t.Helper()
	if b, err := c.br.ReadByte(); err != io.EOF {
		c.t.Fatalf("expected EOF, got byte %q, err %v", b, err)
	}
}

const getRoot = "GET / HTTP/1.1\r\nHost: h\r\n\r\n"

func echo(w *ResponseWriter, r *Request) {
	switch r.Path {
	case "/ignore": // Never reads the body.
		w.Write([]byte("ignored"))
		return
	case "/big":
		w.Write([]byte(strings.Repeat("z", 10000)))
		return
	case "/addr":
		w.Write([]byte(r.RemoteAddr))
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		w.WriteHeader(400)
		return
	}
	w.Header().Set("Content-Type", "text/plain")
	w.Write([]byte(r.Method + " " + r.Path + " " + string(body)))
}

func TestServerBasic(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{})
	c := dial(t, addr)
	c.send("GET /hello?x=1 HTTP/1.1\r\nHost: h\r\n\r\n")
	resp, body := c.response("GET")
	if resp.StatusCode != 200 || body != "GET /hello " || resp.Header.Get("Content-Type") != "text/plain" ||
		resp.Header.Get("Date") == "" || resp.ContentLength != int64(len(body)) {
		t.Errorf("resp = %+v, body = %q", resp, body)
	}
}

func TestServerKeepAliveAndPipelining(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{})
	c := dial(t, addr)

	c.send("GET /a HTTP/1.1\r\nHost: h\r\n\r\n")
	if _, body := c.response("GET"); body != "GET /a " {
		t.Fatalf("first: %q", body)
	}
	c.send("GET /b HTTP/1.1\r\nHost: h\r\n\r\n")
	if _, body := c.response("GET"); body != "GET /b " {
		t.Fatalf("second: %q", body)
	}

	// Two requests in a single write.
	c.send("GET /c HTTP/1.1\r\nHost: h\r\n\r\nPOST /d HTTP/1.1\r\nHost: h\r\nContent-Length: 2\r\n\r\nhi")
	if _, body := c.response("GET"); body != "GET /c " {
		t.Fatalf("pipelined 1: %q", body)
	}
	if _, body := c.response("POST"); body != "POST /d hi" {
		t.Fatalf("pipelined 2: %q", body)
	}
}

func TestServerBodies(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{})
	c := dial(t, addr)

	c.send("POST /cl HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\n\r\nhello")
	if _, body := c.response("POST"); body != "POST /cl hello" {
		t.Errorf("content-length: %q", body)
	}
	c.send("POST /ch HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: chunked\r\n\r\n5\r\nhello\r\n6;x=y\r\n world\r\n0\r\nT: v\r\n\r\n")
	if _, body := c.response("POST"); body != "POST /ch hello world" {
		t.Errorf("chunked: %q", body)
	}
}

func TestServerUnreadBodyIsDrained(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{})
	c := dial(t, addr)
	c.send("POST /ignore HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\n\r\nhelloGET /after HTTP/1.1\r\nHost: h\r\n\r\n")
	if _, body := c.response("POST"); body != "ignored" {
		t.Fatalf("first: %q", body)
	}
	if _, body := c.response("GET"); body != "GET /after " {
		t.Fatalf("request after unread body: %q", body)
	}
}

func TestServerLargeResponseIsChunked(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{})
	c := dial(t, addr)
	c.send("GET /big HTTP/1.1\r\nHost: h\r\n\r\n")
	resp, body := c.response("GET")
	if body != strings.Repeat("z", 10000) || len(resp.TransferEncoding) != 1 || resp.TransferEncoding[0] != "chunked" {
		t.Errorf("TE = %v, body length %d", resp.TransferEncoding, len(body))
	}
	c.send(getRoot) // The connection survives a chunked response.
	if resp, _ := c.response("GET"); resp.StatusCode != 200 {
		t.Errorf("status = %d", resp.StatusCode)
	}
}

func TestServerRejectsBadRequests(t *testing.T) {
	tests := []struct {
		name, raw string
		status    int
	}{
		{"no host", "GET / HTTP/1.1\r\n\r\n", 400},
		{"two host", "GET / HTTP/1.1\r\nHost: a\r\nHost: b\r\n\r\n", 400},
		{"bare LF", "GET / HTTP/1.1\nHost: h\n\n", 400},
		{"smuggling attempt", "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\nTransfer-Encoding: chunked\r\n\r\n", 400},
		{"http 2.0", "GET / HTTP/2.0\r\nHost: h\r\n\r\n", 505},
		{"request line too long", "GET /" + strings.Repeat("a", 9000) + " HTTP/1.1\r\nHost: h\r\n\r\n", 414},
		{"headers too large", "GET / HTTP/1.1\r\nHost: h\r\nX: " + strings.Repeat("a", 20000) + "\r\n\r\n", 431},
		{"body too large", "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 99999999\r\n\r\n", 413},
		{"unsupported coding", "POST / HTTP/1.1\r\nHost: h\r\nTransfer-Encoding: gzip\r\n\r\n", 501},
		{"unknown expectation", "POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 1\r\nExpect: magic\r\n\r\n", 417},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			addr, _ := startServer(t, echo, Limits{})
			c := dial(t, addr)
			c.send(tc.raw)
			resp, _ := c.response("GET")
			if resp.StatusCode != tc.status || !resp.Close {
				t.Errorf("status = %d close = %v, want %d and close", resp.StatusCode, resp.Close, tc.status)
			}
			c.expectClosed()
		})
	}
}

// The server closes while unread request data is still in flight. Without
// the lingering close the client would see a reset instead of the 400.
func TestServerErrorResponseSurvivesUnreadData(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{})
	c := dial(t, addr)
	c.send("POST / HTTP/1.1\r\nContent-Length: 50000\r\n\r\n" + strings.Repeat("x", 50000))
	if resp, _ := c.response("POST"); resp.StatusCode != 400 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	c.expectClosed()
}

func TestServerConnectionClose(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{})

	c := dial(t, addr)
	c.send("GET / HTTP/1.1\r\nHost: h\r\nConnection: close\r\n\r\n")
	if resp, _ := c.response("GET"); !resp.Close {
		t.Error("response must announce close")
	}
	c.expectClosed()

	c = dial(t, addr)
	c.send("GET /big HTTP/1.0\r\n\r\n")
	resp, body := c.response("GET")
	if resp.StatusCode != 200 || !resp.Close || len(body) != 10000 {
		t.Errorf("HTTP/1.0: status=%d close=%v len=%d", resp.StatusCode, resp.Close, len(body))
	}
	c.expectClosed()
}

func TestServerTimeouts(t *testing.T) {
	lim := DefaultLimits()
	lim.IdleTimeout = 150 * time.Millisecond
	lim.ReadHeaderTimeout = 150 * time.Millisecond
	lim.ReadBodyTimeout = 150 * time.Millisecond
	addr, _ := startServer(t, echo, lim)

	t.Run("idle", func(t *testing.T) {
		c := dial(t, addr)
		c.send(getRoot)
		c.response("GET")
		c.expectClosed() // Closed silently once idle for too long.
	})
	t.Run("slow headers", func(t *testing.T) {
		c := dial(t, addr)
		c.send("GET / HTTP/1.1\r\nHost: h\r\n") // Never finished.
		if resp, _ := c.response("GET"); resp.StatusCode != 408 {
			t.Errorf("status = %d", resp.StatusCode)
		}
		c.expectClosed()
	})
	t.Run("slow body", func(t *testing.T) {
		c := dial(t, addr)
		c.send("POST / HTTP/1.1\r\nHost: h\r\nContent-Length: 10\r\n\r\nabc") // 7 bytes missing.
		if resp, _ := c.response("POST"); resp.StatusCode != 400 {
			t.Errorf("status = %d", resp.StatusCode)
		}
		c.expectClosed()
	})
}

func TestServerHandlerPanic(t *testing.T) {
	logged := make(chan string, 1)
	h := func(w *ResponseWriter, r *Request) {
		if r.Path == "/panic" {
			panic("boom")
		}
		w.Write([]byte("fine"))
	}
	addr, _ := startServer(t, h, Limits{}, func(s *Server) {
		s.ErrorLog = func(format string, args ...any) {
			select {
			case logged <- fmt.Sprintf(format, args...):
			default:
			}
		}
	})

	c := dial(t, addr)
	c.send("GET /panic HTTP/1.1\r\nHost: h\r\n\r\n")
	if resp, _ := c.response("GET"); resp.StatusCode != 500 || !resp.Close {
		t.Errorf("status = %d close = %v", resp.StatusCode, resp.Close)
	}
	c.expectClosed()
	if msg := <-logged; !strings.Contains(msg, "boom") {
		t.Errorf("log = %q", msg)
	}

	c = dial(t, addr) // The server itself survived.
	c.send(getRoot)
	if _, body := c.response("GET"); body != "fine" {
		t.Errorf("body = %q", body)
	}
}

func TestServerPanicAfterHeadersSent(t *testing.T) {
	h := func(w *ResponseWriter, r *Request) {
		w.Write([]byte("partial"))
		w.Flush()
		panic("late")
	}
	addr, _ := startServer(t, h, Limits{})
	c := dial(t, addr)
	c.send(getRoot)
	resp, err := http.ReadResponse(c.br, &http.Request{Method: "GET"})
	if err != nil || resp.StatusCode != 200 {
		t.Fatalf("%v, %v", resp, err)
	}
	// No 500 can follow a started response: the body is cut and the
	// connection closed, which the client sees as a truncated chunked body.
	if _, err := io.ReadAll(resp.Body); err == nil {
		t.Error("truncated body went unnoticed")
	}
}

func TestServerExpectContinue(t *testing.T) {
	h := func(w *ResponseWriter, r *Request) {
		if r.Path == "/reject" {
			w.WriteHeader(403) // Never reads the body.
			return
		}
		echo(w, r)
	}
	addr, _ := startServer(t, h, Limits{})

	t.Run("accepted", func(t *testing.T) {
		c := dial(t, addr)
		c.send("POST /up HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\n")
		if resp, _ := c.response("POST"); resp.StatusCode != 100 {
			t.Fatalf("interim status = %d", resp.StatusCode)
		}
		c.send("hello")
		if resp, body := c.response("POST"); resp.StatusCode != 200 || body != "POST /up hello" {
			t.Errorf("status = %d body = %q", resp.StatusCode, body)
		}
		c.send(getRoot) // Connection is still reusable.
		c.response("GET")
	})
	t.Run("rejected without reading", func(t *testing.T) {
		c := dial(t, addr)
		c.send("POST /reject HTTP/1.1\r\nHost: h\r\nContent-Length: 5\r\nExpect: 100-continue\r\n\r\n")
		resp, _ := c.response("POST")
		if resp.StatusCode != 403 || !resp.Close {
			t.Errorf("status = %d close = %v", resp.StatusCode, resp.Close)
		}
		c.expectClosed()
	})
}

func TestServerRemoteAddrAndStdlibClient(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{})
	client := &http.Client{}
	url := "http://" + addr

	get := func() string {
		resp, err := client.Get(url + "/addr")
		if err != nil {
			t.Fatal(err)
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return string(b)
	}
	first, second := get(), get()
	if first == "" || first != second {
		t.Errorf("RemoteAddr %q then %q: connection not reused", first, second)
	}

	resp, err := client.Post(url+"/p", "text/plain", strings.NewReader("known length"))
	if err != nil {
		t.Fatal(err)
	}
	b, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "POST /p known length" {
		t.Errorf("POST body = %q", b)
	}

	// Unknown length: the stdlib client sends the body chunked.
	req, _ := http.NewRequest("POST", url+"/chunked", io.NopCloser(strings.NewReader("streamed")))
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	b, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if string(b) != "POST /chunked streamed" {
		t.Errorf("chunked POST body = %q", b)
	}
}

func TestServerMaxConns(t *testing.T) {
	addr, _ := startServer(t, echo, Limits{}, func(s *Server) { s.MaxConns = 1 })
	first := dial(t, addr)
	first.send(getRoot)
	first.response("GET") // Registered and now idle.

	second := dial(t, addr)
	if resp, _ := second.response("GET"); resp.StatusCode != 503 {
		t.Errorf("status = %d", resp.StatusCode)
	}
	second.expectClosed()
}

func TestServerShutdown(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	h := func(w *ResponseWriter, r *Request) {
		if r.Path == "/slow" {
			close(started)
			<-release
		}
		w.Write([]byte("done"))
	}
	addr, srv := startServer(t, h, Limits{})

	slow := dial(t, addr)
	slow.send("GET /slow HTTP/1.1\r\nHost: h\r\n\r\n")
	<-started

	idle := dial(t, addr)
	idle.send("GET /fast HTTP/1.1\r\nHost: h\r\n\r\n")
	idle.response("GET")

	result := make(chan error, 1)
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		result <- srv.Shutdown(ctx)
	}()

	idle.expectClosed() // Idle connections are closed at once.
	select {
	case err := <-result:
		t.Fatalf("Shutdown returned %v while a request was in flight", err)
	case <-time.After(100 * time.Millisecond):
	}

	close(release)
	resp, body := slow.response("GET")
	if body != "done" || !resp.Close {
		t.Errorf("in-flight request: body = %q close = %v", body, resp.Close)
	}
	slow.expectClosed()
	if err := <-result; err != nil {
		t.Errorf("Shutdown = %v", err)
	}
	if c, err := net.Dial("tcp", addr); err == nil {
		c.Close()
		t.Error("server still accepts connections")
	}
}

func TestServerShutdownDeadline(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	h := func(w *ResponseWriter, r *Request) {
		once.Do(func() { close(started) })
		<-release
	}
	addr, srv := startServer(t, h, Limits{})
	t.Cleanup(func() { close(release) })

	c := dial(t, addr)
	c.send(getRoot)
	<-started
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	if err := srv.Shutdown(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("Shutdown = %v, want deadline exceeded", err)
	}
}

func TestServeConfigErrors(t *testing.T) {
	l, _ := net.Listen("tcp", "127.0.0.1:0")
	if err := (&Server{}).Serve(l); err == nil || errors.Is(err, ErrServerClosed) {
		t.Errorf("nil handler: %v", err)
	}
	l, _ = net.Listen("tcp", "127.0.0.1:0")
	if err := (&Server{Handler: HandlerFunc(echo), Limits: Limits{MaxBodyBytes: -1}}).Serve(l); err == nil {
		t.Error("invalid limits accepted")
	}
	l, _ = net.Listen("tcp", "127.0.0.1:0")
	s := &Server{Handler: HandlerFunc(echo)}
	s.Close()
	if err := s.Serve(l); !errors.Is(err, ErrServerClosed) {
		t.Errorf("Serve after Close = %v", err)
	}
}
