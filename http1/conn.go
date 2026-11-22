package http1

import (
	"bufio"
	"errors"
	"io"
	"net"
	"runtime/debug"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// maxDiscardBytes is how much unread request body is skipped to keep a
	// connection reusable; beyond it the connection is closed instead.
	maxDiscardBytes = 256 << 10

	// errorWriteTimeout bounds writing an error response when no
	// Limits.WriteTimeout is configured.
	errorWriteTimeout = 5 * time.Second

	// lingerTimeout bounds how long a closing connection waits for the peer
	// to finish sending, see conn.close.
	lingerTimeout = 500 * time.Millisecond
)

// Connection states. The transitions are atomic so that Shutdown can close
// idle connections without ever interrupting a request in progress.
const (
	stateIdle    int32 = iota // Waiting for the next request.
	stateActive               // Reading a request or running a handler.
	stateClosing              // Closed by Shutdown.
)

type conn struct {
	srv   *Server
	rwc   net.Conn
	state atomic.Int32
}

// serve handles requests on c until the connection should be closed.
func (c *conn) serve() {
	lingering := false
	defer func() { c.close(lingering) }()

	lim := c.srv.limits()
	br := bufio.NewReader(c.rwc)
	bw := bufio.NewWriter(c.rwc)

	for {
		if c.srv.shuttingDown.Load() {
			return
		}

		// Wait for the first byte of the next request. Anything that goes
		// wrong here (EOF, idle timeout, closed by Shutdown) is not an
		// error worth answering.
		c.state.CompareAndSwap(stateActive, stateIdle)
		c.setReadDeadline(lim.IdleTimeout)
		if _, err := br.Peek(1); err != nil {
			return
		}
		if !c.state.CompareAndSwap(stateIdle, stateActive) {
			return // Shutdown closed us in the meantime.
		}

		c.setReadDeadline(lim.ReadHeaderTimeout)
		req, err := ReadRequest(br, lim)
		if err != nil {
			c.reject(bw, lim, err)
			lingering = true
			return
		}
		req.RemoteAddr = c.rwc.RemoteAddr().String()

		if !c.serveRequest(bw, req, lim) {
			lingering = true
			return
		}
	}
}

// serveRequest runs the handler for req and writes the response. It reports
// whether the connection can be used for another request.
func (c *conn) serveRequest(bw *bufio.Writer, req *Request, lim Limits) bool {
	rw := newResponseWriter(bw, req)

	// RFC 9110 §10.1.1: answer Expect: 100-continue lazily, when the handler
	// first reads the body, so a handler that rejects the request never
	// makes the client send it.
	var cb *continueBody
	if v := req.Header.Values("Expect"); len(v) > 0 && req.Minor >= 1 {
		if len(v) != 1 || !strings.EqualFold(strings.TrimSpace(v[0]), "100-continue") {
			c.sendError(bw, lim, 417)
			return false
		}
		if _, empty := req.Body.(emptyBody); !empty {
			cb = &continueBody{r: req.Body, rw: rw, w: bw}
			req.Body = cb
		}
	}

	c.setReadDeadline(lim.ReadBodyTimeout)
	c.setWriteDeadline(lim.WriteTimeout)

	if c.callHandler(rw, req) {
		if !rw.committed() {
			c.sendError(bw, lim, 500)
		}
		return false
	}

	// The client was told to wait for 100 Continue and never was: it may
	// still be holding the body back, so the connection cannot be reused.
	if cb != nil && !cb.sent {
		rw.close = true
	}
	if c.srv.shuttingDown.Load() {
		rw.close = true
	}
	if err := rw.finish(); err != nil || rw.willClose() {
		return false
	}

	// Unread body bytes would be mistaken for the next request.
	return discardBody(req.Body, maxDiscardBytes) == nil
}

// callHandler runs the handler and reports whether it panicked.
func (c *conn) callHandler(rw *ResponseWriter, req *Request) (panicked bool) {
	defer func() {
		if v := recover(); v != nil {
			panicked = true
			c.srv.logf("http1: panic serving %v: %v\n%s", c.rwc.RemoteAddr(), v, debug.Stack())
		}
	}()
	c.srv.Handler.ServeHTTP(rw, req)
	return false
}

// reject answers a request that could not be parsed, if anything can be said
// about it at all.
func (c *conn) reject(bw *bufio.Writer, lim Limits, err error) {
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		err = ErrReadTimeout
	}
	if status, ok := StatusCode(err); ok {
		c.sendError(bw, lim, status)
	}
}

func (c *conn) sendError(bw *bufio.Writer, lim Limits, status int) {
	d := lim.WriteTimeout
	if d <= 0 {
		d = errorWriteTimeout
	}
	c.rwc.SetWriteDeadline(time.Now().Add(d))
	writeErrorResponse(bw, status)
}

func (c *conn) setReadDeadline(d time.Duration) {
	var t time.Time
	if d > 0 {
		t = time.Now().Add(d)
	}
	c.rwc.SetReadDeadline(t)
}

func (c *conn) setWriteDeadline(d time.Duration) {
	var t time.Time
	if d > 0 {
		t = time.Now().Add(d)
	}
	c.rwc.SetWriteDeadline(t)
}

// close closes the connection. With linger set it first half-closes and
// reads whatever the peer still sends: closing a socket that has unread
// incoming data makes the OS send a reset, which can destroy the response we
// just wrote before the client has read it.
func (c *conn) close(linger bool) {
	if linger {
		if cw, ok := c.rwc.(interface{ CloseWrite() error }); ok && cw.CloseWrite() == nil {
			c.rwc.SetReadDeadline(time.Now().Add(lingerTimeout))
			io.Copy(io.Discard, io.LimitReader(c.rwc, maxDiscardBytes))
		}
	}
	c.rwc.Close()
}

// continueBody sends "100 Continue" before the handler's first read of the
// body, unless the final response has already started.
type continueBody struct {
	r    io.Reader
	rw   *ResponseWriter
	w    *bufio.Writer
	sent bool
}

func (b *continueBody) Read(p []byte) (int, error) {
	if !b.sent {
		b.sent = true
		if !b.rw.committed() {
			b.w.WriteString("HTTP/1.1 100 Continue\r\n\r\n")
			if err := b.w.Flush(); err != nil {
				return 0, err
			}
		}
	}
	return b.r.Read(p)
}
