package http1

import (
	"bufio"
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"time"
)

// ErrServerClosed is returned by Serve and ListenAndServe after Shutdown or
// Close.
var ErrServerClosed = errors.New("http1: Server closed")

// Handler responds to an HTTP request. ServeHTTP must not use w or r after it
// returns.
type Handler interface {
	ServeHTTP(w *ResponseWriter, r *Request)
}

// HandlerFunc adapts an ordinary function to a Handler.
type HandlerFunc func(w *ResponseWriter, r *Request)

// ServeHTTP calls f(w, r).
func (f HandlerFunc) ServeHTTP(w *ResponseWriter, r *Request) { f(w, r) }

// Server is an HTTP/1.1 server. The zero value of every field except Handler
// is usable.
type Server struct {
	// Addr is the TCP address for ListenAndServe; ":8080" if empty.
	Addr string

	// Handler serves every request. It must not be nil.
	Handler Handler

	// Limits bounds each connection. The zero value means DefaultLimits.
	Limits Limits

	// MaxConns caps concurrent connections; further ones are answered with
	// 503 and closed. Zero means unlimited.
	MaxConns int

	// ErrorLog receives recovered handler panics and accept errors. Nil
	// discards them.
	ErrorLog func(format string, args ...any)

	shuttingDown atomic.Bool
	mu           sync.Mutex
	listeners    map[net.Listener]struct{}
	conns        map[*conn]struct{}
}

// ListenAndServe listens on s.Addr and serves connections until Shutdown or
// Close, after which it returns ErrServerClosed.
func (s *Server) ListenAndServe() error {
	addr := s.Addr
	if addr == "" {
		addr = ":8080"
	}
	l, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

// Serve accepts connections on l, one goroutine each, until Shutdown or
// Close. It takes ownership of l and closes it before returning.
func (s *Server) Serve(l net.Listener) error {
	defer l.Close()
	if s.Handler == nil {
		return errors.New("http1: Server.Handler is nil")
	}
	if err := s.limits().Validate(); err != nil {
		return err
	}
	if !s.trackListener(l) {
		return ErrServerClosed
	}
	defer s.untrackListener(l)

	var delay time.Duration
	for {
		rwc, err := l.Accept()
		if err != nil {
			if s.shuttingDown.Load() {
				return ErrServerClosed
			}
			// Temporary errors such as running out of file descriptors must
			// not kill the server: back off and retry.
			if te, ok := err.(interface{ Temporary() bool }); ok && te.Temporary() {
				if delay == 0 {
					delay = 5 * time.Millisecond
				} else if delay *= 2; delay > time.Second {
					delay = time.Second
				}
				s.logf("http1: accept error: %v; retrying in %v", err, delay)
				time.Sleep(delay)
				continue
			}
			return err
		}
		delay = 0

		c := &conn{srv: s, rwc: rwc}
		if !s.track(c) {
			go s.reject(rwc)
			continue
		}
		go func() {
			defer s.untrack(c)
			c.serve()
		}()
	}
}

// Shutdown stops accepting connections, closes idle ones, and waits for
// requests in progress to finish; each such connection is closed after its
// response. If ctx expires first, Shutdown returns ctx.Err() and leaves the
// remaining connections open; call Close to cut them.
func (s *Server) Shutdown(ctx context.Context) error {
	s.shuttingDown.Store(true)
	s.closeListeners()

	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		if s.closeIdleConns() {
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

// Close immediately closes all listeners and connections, interrupting
// requests in progress.
func (s *Server) Close() error {
	s.shuttingDown.Store(true)
	s.closeListeners()

	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		c.rwc.Close()
	}
	return nil
}

func (s *Server) limits() Limits {
	if s.Limits == (Limits{}) {
		return DefaultLimits()
	}
	return s.Limits
}

func (s *Server) logf(format string, args ...any) {
	if s.ErrorLog != nil {
		s.ErrorLog(format, args...)
	}
}

func (s *Server) trackListener(l net.Listener) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown.Load() {
		return false
	}
	if s.listeners == nil {
		s.listeners = make(map[net.Listener]struct{})
	}
	s.listeners[l] = struct{}{}
	return true
}

func (s *Server) untrackListener(l net.Listener) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.listeners, l)
}

func (s *Server) closeListeners() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for l := range s.listeners {
		l.Close()
	}
}

// track registers c, or returns false if the server is shutting down or full.
func (s *Server) track(c *conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.shuttingDown.Load() || (s.MaxConns > 0 && len(s.conns) >= s.MaxConns) {
		return false
	}
	if s.conns == nil {
		s.conns = make(map[*conn]struct{})
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *Server) untrack(c *conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
}

// closeIdleConns closes connections that are waiting for a request and
// reports whether no connections are left. The state transition is atomic, so
// a connection that just received a request is never closed from under it.
func (s *Server) closeIdleConns() (done bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.conns {
		if c.state.CompareAndSwap(stateIdle, stateClosing) {
			c.rwc.Close()
		}
	}
	return len(s.conns) == 0
}

// reject answers a connection the server has no room for.
func (s *Server) reject(rwc net.Conn) {
	c := &conn{srv: s, rwc: rwc}
	defer c.close(true)
	rwc.SetWriteDeadline(time.Now().Add(errorWriteTimeout))
	writeErrorResponse(bufio.NewWriterSize(rwc, 256), 503)
}
