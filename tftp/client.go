package tftp

import (
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

// Client downloads and uploads files. The zero value works: it uses the
// default timers. Fields are only needed to change them, for example in tests.
// A Client holds no connection, so one value can be used from many goroutines.
type Client struct {
	// Mode is the transfer mode: ModeOctet (bytes as they are) or
	// ModeNetascii (text; line ends are converted). Empty means ModeOctet.
	Mode string
	// Timeout is how long to wait for a reply before the first
	// retransmission. Zero or negative means 1 second.
	Timeout time.Duration
	// MaxTimeout caps the wait after repeated retransmissions (the wait
	// doubles each time). Zero or negative means 8 seconds.
	MaxTimeout time.Duration
	// Retries is how many times a packet is sent again before giving up
	// with ErrTimeout. Zero or negative means 5.
	Retries int
	// Dally is how long Get keeps listening after it has sent the final ACK,
	// in case that ACK was lost and the server repeats its last packet
	// (RFC 1350 §6). Zero means twice Timeout, so a Get returns about 2
	// seconds after the last byte by default. A negative value returns
	// right away: the file is complete either way, but the server may
	// log an error if the final ACK was lost.
	Dally time.Duration
}

// Get downloads filename from the TFTP server at addr ("host:port") and
// writes it to w. On error, w may already hold part of the file; discard it.
//
// The server decides how large the file is, so w should not be trusted to
// stay small: wrap it or watch the size if you do not trust the server.
func Get(ctx context.Context, addr, filename string, w io.Writer) error {
	return (&Client{}).Get(ctx, addr, filename, w)
}

// Put uploads r to the TFTP server at addr ("host:port") as filename.
func Put(ctx context.Context, addr, filename string, r io.Reader) error {
	return (&Client{}).Put(ctx, addr, filename, r)
}

// Get is like the package-level Get, with this client's timers and mode.
func (c *Client) Get(ctx context.Context, addr, filename string, w io.Writer) error {
	t, req, server, mode, err := c.start(OpRRQ, addr, filename)
	if err != nil {
		return fmt.Errorf("tftp get %q: %w", filename, err)
	}
	defer func() { _ = t.conn.Close() }()

	// The RRQ is the first packet. The server answers DATA 1 from a new port
	// (its TID), and recvFile remembers that port from then on.
	out, finish := writerFor(mode, w)
	if err := t.recvFile(ctx, out, req, server); err != nil {
		return fmt.Errorf("tftp get %q: %w", filename, err)
	}
	if err := finish(); err != nil {
		return fmt.Errorf("tftp get %q: %w", filename, err)
	}
	return nil
}

// Put is like the package-level Put, with this client's timers and mode.
func (c *Client) Put(ctx context.Context, addr, filename string, r io.Reader) error {
	t, req, server, mode, err := c.start(OpWRQ, addr, filename)
	if err != nil {
		return fmt.Errorf("tftp put %q: %w", filename, err)
	}
	defer func() { _ = t.conn.Close() }()

	// The server answers a WRQ with ACK 0 (RFC 1350 §4), again from a new
	// port. exchange resends the WRQ if that ACK does not arrive, and
	// remembers the port the ACK came from.
	err = t.exchange(ctx, req, server, func(p Packet) (bool, error) {
		a, ok := p.(Ack)
		return ok && a.Block == 0, nil
	})
	if err != nil {
		return fmt.Errorf("tftp put %q: %w", filename, err)
	}
	if err := t.sendFile(ctx, readerFor(mode, r)); err != nil {
		return fmt.Errorf("tftp put %q: %w", filename, err)
	}
	return nil
}

// start checks the arguments, then opens a socket on a free local port (our
// TID). Nothing is sent. The caller must close t.conn.
//
// Cheap checks come first, so a bad mode or file name never touches the
// network. It also returns the transfer mode in lower case.
func (c *Client) start(op Opcode, addr, filename string) (t *transfer, req []byte, server net.Addr, mode string, err error) {
	mode = strings.ToLower(c.Mode)
	if mode == "" {
		mode = ModeOctet
	}
	if !supportedMode(mode) {
		return nil, nil, nil, "", fmt.Errorf("unsupported transfer mode %q", c.Mode)
	}
	req, err = Request{Op: op, Filename: filename, Mode: mode}.MarshalBinary()
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("build request: %w", err)
	}
	server, err = net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("server address: %w", err)
	}
	conn, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return nil, nil, nil, "", fmt.Errorf("open socket: %w", err)
	}

	t = newTransfer(conn, nil) // the server's TID is unknown until it answers
	if c.Timeout > 0 {
		t.timeout = c.Timeout
		t.dally = dallyFactor * c.Timeout
	}
	if c.MaxTimeout > 0 {
		t.maxTimeout = c.MaxTimeout
	}
	if t.maxTimeout < t.timeout {
		t.maxTimeout = t.timeout
	}
	if c.Retries > 0 {
		t.retries = c.Retries
	}
	switch {
	case c.Dally > 0:
		t.dally = c.Dally
	case c.Dally < 0:
		t.dally = 0
	}
	return t, req, server, mode, nil
}
