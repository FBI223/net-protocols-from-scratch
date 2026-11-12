package tftp

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"net"
	"os"
	"time"
)

// Defaults for a transfer. RFC 1350 gives no timeout values, so these are our
// own choices. RFC 1123 §4.2.3.2 requires at least exponential backoff.
const (
	defaultBlockSize  = 512 // RFC 1350 §2; fixed until options (RFC 2348) are added
	defaultTimeout    = 1 * time.Second
	defaultMaxTimeout = 8 * time.Second
	defaultRetries    = 5 // retransmissions after the first send
)

// dallyFactor sets the default dally to dallyFactor times the retransmission
// timeout. The dally should outlast the peer's first retransmission, or it
// cannot catch a repeated final DATA.
const dallyFactor = 1

var (
	// ErrTimeout means the peer stopped answering after all retransmissions.
	ErrTimeout = errors.New("tftp: timed out waiting for the peer")
	// ErrTransferTooLarge means the 16-bit block number would wrap around.
	// RFC 1350 does not say what happens then, so we stop instead of guessing.
	ErrTransferTooLarge = errors.New("tftp: file needs more than 65535 blocks")
)

// RemoteError is an ERROR packet received from the other side.
type RemoteError struct {
	Code uint16
	Msg  string
}

func (e *RemoteError) Error() string {
	return fmt.Sprintf("tftp: remote error %d: %s", e.Code, e.Msg)
}

// transfer is the lock-step engine shared by client and server (RFC 1350 §2).
// It owns no socket lifecycle: the caller creates and closes conn.
type transfer struct {
	conn       net.PacketConn // UDP socket for sending and receiving packets; not closed by transfer
	peer       net.Addr       // the other side's TID; nil until the first accepted reply
	blksize    int            // data bytes per block; do not change after newTransfer
	timeout    time.Duration  // first retransmission timeout
	maxTimeout time.Duration  // backoff cap
	retries    int            // retransmissions after the first send
	dally      time.Duration  // how long the receiver lingers after the final ACK
	buf        []byte         // receive buffer: one byte larger than the biggest packet we accept (an ERROR can be 516 bytes whatever blksize is)
}

// newTransfer returns an engine with default settings. Pass peer == nil when
// the other side's TID is not known yet (a client waiting for the first reply).
func newTransfer(conn net.PacketConn, peer net.Addr) *transfer {
	return &transfer{
		conn:       conn,
		peer:       peer,
		blksize:    defaultBlockSize,
		timeout:    defaultTimeout,
		maxTimeout: defaultMaxTimeout,
		retries:    defaultRetries,
		dally:      dallyFactor * defaultTimeout,
		// The extra byte lets us notice an oversized datagram: UDP reads into a
		// too-small buffer silently cut the packet and report no error.
		buf: make([]byte, 4+defaultBlockSize+1),
	}
}

// handler looks at one packet from the peer. It returns done == true when
// this is the packet the caller was waiting for.
type handler func(p Packet) (done bool, err error)

// sameAddr reports whether two UDP addresses have the same IP and port.
func sameAddr(a, b net.Addr) bool {
	ua, ok1 := a.(*net.UDPAddr)
	ub, ok2 := b.(*net.UDPAddr)
	if !ok1 || !ok2 {
		return a.String() == b.String()
	}
	return ua.Port == ub.Port && ua.IP.Equal(ub.IP)
}

// sendError sends a courtesy ERROR packet. RFC 1350 §2: it is never
// acknowledged or retransmitted, so a failure to send it is ignored.
func (t *transfer) sendError(to net.Addr, code uint16, msg string) {
	b, err := ErrorPacket{Code: code, Msg: msg}.MarshalBinary()
	if err != nil {
		return
	}
	_, _ = t.conn.WriteTo(b, to)
}

// wait reads packets until handle says done, the deadline passes, or an error
// happens. It returns done == false with a nil error on timeout.
//
// Rules applied to every incoming datagram:
//   - from an unknown source (wrong TID): answer ERROR 5 and keep waiting
//     (RFC 1350 §4); the transfer itself is not affected;
//   - unparsable or oversized: treat as lost and keep waiting;
//   - ERROR from the peer: the transfer is over.
//
// The deadline is absolute, so junk packets cannot extend the wait.
func (t *transfer) wait(ctx context.Context, deadline time.Time, handle handler) (bool, error) {
	for {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if err := t.conn.SetReadDeadline(deadline); err != nil {
			return false, err
		}
		// Cancellation may have fired between the check above and the line
		// before: look again so we never block for a whole timeout.
		if err := ctx.Err(); err != nil {
			return false, err
		}

		n, from, err := t.conn.ReadFrom(t.buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				if cerr := ctx.Err(); cerr != nil {
					return false, cerr
				}
				return false, nil // plain timeout
			}
			return false, err
		}

		if t.peer != nil && !sameAddr(from, t.peer) {
			// Never answer an ERROR with an ERROR, or two hosts could
			// bounce errors at each other forever.
			if op, perr := ParseOpcode(t.buf[:n]); perr == nil && op != OpError {
				t.sendError(from, ErrUnknownTID, "unknown transfer ID")
			}
			continue
		}
		if n == len(t.buf) {
			// The datagram filled even the spare byte, so it was longer than
			// any packet we accept (and the read cut it short).
			continue
		}
		p, err := Parse(t.buf[:n])
		if err != nil {
			continue
		}
		if e, ok := p.(ErrorPacket); ok {
			return false, &RemoteError{Code: e.Code, Msg: e.Msg}
		}

		done, err := handle(p)
		if err != nil {
			return false, err
		}
		if done {
			if t.peer == nil {
				t.peer = from // first valid reply fixes the peer's TID
			}
			return true, nil
		}
	}
}

// exchange sends out to dst and waits for a packet that handle accepts.
// On timeout it sends out again, doubling the wait each time (RFC 1123
// §4.2.3.2) up to maxTimeout, and gives up with ErrTimeout after t.retries
// retransmissions.
func (t *transfer) exchange(ctx context.Context, out []byte, dst net.Addr, handle handler) error {
	// Wake up a blocked read as soon as ctx is cancelled.
	stop := context.AfterFunc(ctx, func() { _ = t.conn.SetReadDeadline(time.Now()) })
	defer stop()

	timeout := t.timeout
	for attempt := 0; ; attempt++ {
		if _, err := t.conn.WriteTo(out, dst); err != nil {
			return fmt.Errorf("tftp: send: %w", err)
		}
		done, err := t.wait(ctx, time.Now().Add(timeout), handle)
		if err != nil {
			return err
		}
		if done {
			return nil
		}
		if attempt >= t.retries {
			return ErrTimeout
		}
		timeout = min(timeout*2, t.maxTimeout)
	}
}

// sendFile sends everything from r as DATA blocks 1, 2, ... and waits for
// each ACK. The peer must already be known. The last block is shorter than
// blksize; when the data length is a multiple of blksize (or zero) that last
// block is empty (RFC 1350 §6).
func (t *transfer) sendFile(ctx context.Context, r io.Reader) error {
	chunk := make([]byte, t.blksize)
	for block := uint16(1); ; block++ {
		n, err := io.ReadFull(r, chunk)
		if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
			t.sendError(t.peer, ErrNotDefined, "local read error")
			return fmt.Errorf("tftp: read: %w", err)
		}
		pkt, err := Data{Block: block, Payload: chunk[:n]}.MarshalBinary()
		if err != nil {
			return err
		}

		err = t.exchange(ctx, pkt, t.peer, func(p Packet) (bool, error) {
			// Only the ACK for the current block counts. An older ACK is
			// ignored and never triggers a resend: that would cause the
			// Sorcerer's Apprentice Syndrome (RFC 1123 §4.2.3.1).
			if a, ok := p.(Ack); ok {
				return a.Block == block, nil
			}
			return false, nil
		})
		if err != nil {
			return err
		}

		if n < t.blksize {
			return nil // short block acknowledged: transfer complete
		}
		if block == math.MaxUint16 {
			t.sendError(t.peer, ErrNotDefined, "file too large")
			return ErrTransferTooLarge
		}
	}
}

// recvFile writes the received DATA payloads to w and ACKs each block.
// first is the packet that starts the exchange and dst is where it goes: a
// client passes its RRQ and the server address, a server answering a WRQ
// passes ACK 0 and the client address. first is also what we resend if the
// first DATA never arrives.
func (t *transfer) recvFile(ctx context.Context, w io.Writer, first []byte, dst net.Addr) error {
	out, to := first, dst
	for expected := uint16(1); ; expected++ {
		var data Data
		err := t.exchange(ctx, out, to, func(p Packet) (bool, error) {
			d, ok := p.(Data)
			if !ok {
				return false, nil
			}
			if len(d.Payload) > t.blksize {
				return false, nil // more data than one block: not a valid DATA for us
			}
			switch {
			case d.Block == expected:
				data = d
				return true, nil
			case expected > 1 && d.Block == expected-1:
				// The peer sent the previous block again, so our ACK was lost
				// or late. Repeat it; harmless, because a correct sender
				// ignores a duplicate ACK.
				_, _ = t.conn.WriteTo(out, t.peer)
			}
			return false, nil
		})
		if err != nil {
			return err
		}

		if _, err := w.Write(data.Payload); err != nil {
			t.sendError(t.peer, ErrNotDefined, "local write error")
			return fmt.Errorf("tftp: write: %w", err)
		}
		ack, err := Ack{Block: data.Block}.MarshalBinary()
		if err != nil {
			return err
		}

		if len(data.Payload) < t.blksize {
			t.linger(ctx, ack, data.Block)
			return nil
		}
		if expected == math.MaxUint16 {
			t.sendError(t.peer, ErrNotDefined, "file too large")
			return ErrTransferTooLarge
		}
		out, to = ack, t.peer
	}
}

// linger sends the final ACK and then waits a while ("dallying", RFC 1350
// §6). If our ACK was lost, the sender repeats the last DATA and we can
// answer again. Nothing here can fail the transfer: the file is complete.
func (t *transfer) linger(ctx context.Context, ack []byte, block uint16) {
	_, _ = t.conn.WriteTo(ack, t.peer)
	_, _ = t.wait(ctx, time.Now().Add(t.dally), func(p Packet) (bool, error) {
		if d, ok := p.(Data); ok && d.Block == block {
			_, _ = t.conn.WriteTo(ack, t.peer)
		}
		return false, nil
	})
}
