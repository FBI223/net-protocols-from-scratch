package tftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

const defaultMaxTransfers = 100

// Server serves the files under Root over TFTP (RFC 1350), octet mode only.
//
// Defaults are chosen to be safe: files are read-only, and nothing outside
// Root can be reached. The zero value of every field except Root is valid.
//
// Not supported yet: netascii mode (RFC 1123 requires it), options (they are
// ignored, which RFC 2347 allows), and ignoring requests sent to a broadcast
// address (RFC 1123 SHOULD; bind to one unicast address to avoid them).
type Server struct {
	// Root is the directory that is served. Required.
	Root string
	// AllowWrite lets clients upload (WRQ). Even then only new files can be
	// created: an upload never replaces an existing file (ERROR 6).
	AllowWrite bool
	// MaxTransfers is how many transfers may run at once. Further requests
	// are refused with an ERROR. Zero or negative means 100.
	MaxTransfers int

	// Timeout, MaxTimeout, Retries and Dally work like the fields of Client.
	Timeout    time.Duration
	MaxTimeout time.Duration
	Retries    int
	Dally      time.Duration

	// Logger receives one line per request. Nil means no logging.
	Logger *slog.Logger
}

// ListenAndServe listens on the UDP address addr (for example
// "127.0.0.1:6969") and serves until ctx is cancelled. Port 69, the standard
// one, needs elevated privileges on most systems.
func (s *Server) ListenAndServe(ctx context.Context, addr string) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return fmt.Errorf("tftp server: %w", err)
	}
	defer func() { _ = conn.Close() }()
	return s.Serve(ctx, conn)
}

// Serve takes requests from conn until ctx is cancelled (then it returns
// nil) or reading fails. Every request is served from a new socket on its own
// port: that port is the server's TID for the transfer (RFC 1350 §4), and conn
// itself is used only for new requests. Serve does not close conn. It returns
// after all running transfers have ended.
func (s *Server) Serve(ctx context.Context, conn net.PacketConn) error {
	root, err := os.OpenRoot(s.Root)
	if err != nil {
		return fmt.Errorf("tftp server: open root: %w", err)
	}
	defer func() { _ = root.Close() }()

	lg := s.Logger
	if lg == nil {
		lg = slog.New(slog.NewTextHandler(io.Discard, nil))
	}
	limit := s.MaxTransfers
	if limit <= 0 {
		limit = defaultMaxTransfers
	}
	slots := make(chan struct{}, limit)

	// Wake up the blocked read below when ctx is cancelled.
	stop := context.AfterFunc(ctx, func() { _ = conn.SetReadDeadline(time.Now()) })
	defer stop()

	// Deferred calls run in reverse order: wait for the transfers first, and
	// only then close root.
	var wg sync.WaitGroup
	defer wg.Wait()

	buf := make([]byte, 2048)
	for {
		n, from, err := conn.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			return fmt.Errorf("tftp server: read: %w", err)
		}

		// Anything that is not a valid request is dropped without an answer:
		// answering strangers would let anyone bounce our packets at a
		// victim with a faked source address.
		if !validClient(from) {
			continue
		}
		p, err := Parse(buf[:n])
		if err != nil {
			continue
		}
		req, ok := p.(Request)
		if !ok {
			continue
		}

		select {
		case slots <- struct{}{}:
		default:
			lg.Warn("too many transfers, request refused", "client", from.String())
			refuse(conn, from, "server busy")
			continue
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			defer func() { <-slots }()
			s.handle(ctx, conn, root, lg, req, from)
		}()
	}
}

// validClient reports whether a request may come from this address. A
// request from port 0, an unspecified or a multicast address, or the
// broadcast address cannot be answered, and replying could amplify traffic.
func validClient(a net.Addr) bool {
	ua, ok := a.(*net.UDPAddr)
	if !ok || ua.Port == 0 {
		return false
	}
	ip := ua.IP
	return !ip.IsUnspecified() && !ip.IsMulticast() && !ip.Equal(net.IPv4bcast)
}

// refuse answers a request with an ERROR from the main socket.
func refuse(conn net.PacketConn, to net.Addr, msg string) {
	b, err := ErrorPacket{Code: ErrNotDefined, Msg: msg}.MarshalBinary()
	if err != nil {
		return
	}
	_, _ = conn.WriteTo(b, to)
}

// handle serves one request, from a new socket.
func (s *Server) handle(ctx context.Context, main net.PacketConn, root *os.Root, lg *slog.Logger, req Request, client net.Addr) {
	op := "RRQ"
	if req.Op == OpWRQ {
		op = "WRQ"
	}
	lg = lg.With("client", client.String(), "op", op, "file", req.Filename)

	// The new socket gets the same local IP as the main one, so the client
	// sees answers from the address it wrote to, but a new port.
	host, _, err := net.SplitHostPort(main.LocalAddr().String())
	if err != nil {
		host = ""
	}
	sock, err := net.ListenPacket("udp", net.JoinHostPort(host, "0"))
	if err != nil {
		lg.Error("cannot open a socket for the transfer", "err", err)
		refuse(main, client, "server error")
		return
	}
	defer func() { _ = sock.Close() }()
	t := s.newTransfer(sock, client)

	if req.Op == OpRRQ {
		err = s.serveRead(ctx, t, root, req)
	} else {
		err = s.serveWrite(ctx, t, root, req)
	}
	if err != nil {
		lg.Warn("transfer failed", "err", err)
		return
	}
	lg.Info("transfer complete")
}

func (s *Server) newTransfer(conn net.PacketConn, client net.Addr) *transfer {
	t := newTransfer(conn, client)
	if s.Timeout > 0 {
		t.timeout = s.Timeout
		t.dally = dallyFactor * s.Timeout
	}
	if s.MaxTimeout > 0 {
		t.maxTimeout = s.MaxTimeout
	}
	if t.maxTimeout < t.timeout {
		t.maxTimeout = t.timeout
	}
	if s.Retries > 0 {
		t.retries = s.Retries
	}
	switch {
	case s.Dally > 0:
		t.dally = s.Dally
	case s.Dally < 0:
		t.dally = 0
	}
	return t
}

// reject sends an ERROR to the client and returns it as a Go error too.
func (t *transfer) reject(code uint16, msg string) error {
	t.sendError(t.peer, code, msg)
	return errors.New(msg)
}

// localName turns the name from a request into a path relative to Root.
// Leading slashes are dropped, so "/a.txt" means Root/a.txt. Nothing else is
// rewritten: os.Root refuses every path that would leave Root, including
// ones that use ".." or symbolic links.
func localName(name string) string {
	return strings.TrimLeft(name, "/")
}

// fsError picks the ERROR code for a failed file operation. The message is
// fixed on purpose: system error texts would tell the client about our paths.
func fsError(err error) (uint16, string) {
	switch {
	case errors.Is(err, fs.ErrNotExist):
		return ErrFileNotFound, "file not found"
	case errors.Is(err, fs.ErrExist):
		return ErrFileExists, "file already exists"
	default:
		return ErrAccessViolation, "access denied"
	}
}

// openRegular opens a regular file under root for reading. Other kinds of
// files are reported as missing: opening a named pipe, for one, would block
// forever, and a directory cannot be sent.
func openRegular(root *os.Root, name string) (*os.File, error) {
	if name == "" {
		return nil, fs.ErrNotExist
	}
	fi, err := root.Stat(name)
	if err != nil {
		return nil, err
	}
	if !fi.Mode().IsRegular() {
		return nil, fs.ErrNotExist
	}
	f, err := root.Open(name)
	if err != nil {
		return nil, err
	}
	// The file may have been swapped between Stat and Open; look again.
	if fi, err := f.Stat(); err != nil || !fi.Mode().IsRegular() {
		_ = f.Close()
		return nil, fs.ErrNotExist
	}
	return f, nil
}

func (s *Server) serveRead(ctx context.Context, t *transfer, root *os.Root, req Request) error {
	if req.Mode != transferMode {
		return t.reject(ErrIllegalOp, "only octet mode is supported")
	}
	f, err := openRegular(root, localName(req.Filename))
	if err != nil {
		code, msg := fsError(err)
		return t.reject(code, msg)
	}
	defer func() { _ = f.Close() }()
	return t.sendFile(ctx, f)
}

func (s *Server) serveWrite(ctx context.Context, t *transfer, root *os.Root, req Request) error {
	if !s.AllowWrite {
		return t.reject(ErrAccessViolation, "writing is not allowed")
	}
	if req.Mode != transferMode {
		return t.reject(ErrIllegalOp, "only octet mode is supported")
	}
	name := localName(req.Filename)
	if name == "" {
		return t.reject(ErrAccessViolation, "access denied")
	}
	// O_EXCL: create a new file or fail. An existing file, or a symbolic
	// link in its place, is never opened.
	f, err := root.OpenFile(name, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o644)
	if err != nil {
		code, msg := fsError(err)
		return t.reject(code, msg)
	}

	// RFC 1350 §4: a WRQ is answered with ACK 0 from the new port.
	ack0, err := Ack{Block: 0}.MarshalBinary()
	if err != nil {
		_ = f.Close()
		return err
	}
	err = t.recvFile(ctx, f, ack0, t.peer)
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		_ = root.Remove(name) // do not leave a half-written file behind
	}
	return err
}
