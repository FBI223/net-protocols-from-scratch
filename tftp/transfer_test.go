package tftp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"
)

// --- helpers -----------------------------------------------------------

// listen opens a UDP socket on a free localhost port.
func listen(t *testing.T) net.PacketConn {
	t.Helper()
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = c.Close() })
	return c
}

// fast shortens the timers so tests that lose packets stay quick.
func fast(tr *transfer) *transfer {
	tr.timeout = 40 * time.Millisecond
	tr.maxTimeout = 160 * time.Millisecond
	tr.retries = 10
	tr.dally = 120 * time.Millisecond
	return tr
}

// testData returns n deterministic bytes (including zero bytes).
func testData(n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = byte(i*7 + i>>8)
	}
	return b
}

func mustMarshal(t *testing.T, p Packet) []byte {
	t.Helper()
	b, err := p.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func rrq(t *testing.T) []byte {
	t.Helper()
	return mustMarshal(t, Request{Op: OpRRQ, Filename: "f", Mode: "octet"})
}

func readPacket(t *testing.T, c net.PacketConn, d time.Duration) (Packet, net.Addr) {
	t.Helper()
	buf := make([]byte, 2048)
	_ = c.SetReadDeadline(time.Now().Add(d))
	n, from, err := c.ReadFrom(buf)
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	p, err := Parse(buf[:n])
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return p, from
}

func wantAck(t *testing.T, c net.PacketConn, block uint16) {
	t.Helper()
	p, _ := readPacket(t, c, time.Second)
	if a, ok := p.(Ack); !ok || a.Block != block {
		t.Fatalf("got %#v, want ACK %d", p, block)
	}
}

func wantData(t *testing.T, c net.PacketConn, block uint16) (Data, net.Addr) {
	t.Helper()
	p, from := readPacket(t, c, time.Second)
	d, ok := p.(Data)
	if !ok || d.Block != block {
		t.Fatalf("got %#v, want DATA %d", p, block)
	}
	return d, from
}

func waitErr(t *testing.T, ch <-chan error) error {
	t.Helper()
	select {
	case err := <-ch:
		return err
	case <-time.After(10 * time.Second):
		t.Fatal("transfer did not finish")
		return nil
	}
}

type pairResult struct {
	got     []byte
	sendErr error
	recvErr error
}

// runPair sends data from sendConn to recvConn with two real engines.
// The receiver starts like a client: it sends an RRQ and does not know the
// sender's TID until the first DATA arrives.
func runPair(t *testing.T, data []byte, send, recv *transfer) pairResult {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	sendDone := make(chan error, 1)
	go func() { sendDone <- send.sendFile(ctx, bytes.NewReader(data)) }()

	var out bytes.Buffer
	recvErr := recv.recvFile(ctx, &out, rrq(t), send.conn.LocalAddr())
	return pairResult{got: out.Bytes(), sendErr: waitErr(t, sendDone), recvErr: recvErr}
}

// lossy drops every n-th outgoing packet.
type lossy struct {
	net.PacketConn
	count atomic.Int64
	every int64
}

func (l *lossy) WriteTo(p []byte, a net.Addr) (int, error) {
	if l.count.Add(1)%l.every == 0 {
		return len(p), nil // pretend it was sent
	}
	return l.PacketConn.WriteTo(p, a)
}

// --- tests -------------------------------------------------------------

func TestTransferSizes(t *testing.T) {
	// 0, 512 and 1024 matter: the file ends with an empty DATA block.
	for _, size := range []int{0, 1, 511, 512, 513, 1023, 1024, 1025, 1300, 5000, 51200, 51201} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			data := testData(size)
			sc, rc := listen(t), listen(t)
			res := runPair(t, data, fast(newTransfer(sc, rc.LocalAddr())), fast(newTransfer(rc, nil)))
			if res.sendErr != nil || res.recvErr != nil {
				t.Fatalf("sendErr=%v recvErr=%v", res.sendErr, res.recvErr)
			}
			if !bytes.Equal(res.got, data) {
				t.Fatalf("received %d bytes, differ from the %d sent", len(res.got), len(data))
			}
		})
	}
}

func TestTransferWithPacketLoss(t *testing.T) {
	for _, every := range []int64{3, 4} {
		t.Run("every "+strconv.FormatInt(every, 10), func(t *testing.T) {
			data := testData(10*512 + 100)
			sc := &lossy{PacketConn: listen(t), every: every}
			rc := &lossy{PacketConn: listen(t), every: every}
			res := runPair(t, data, fast(newTransfer(sc, rc.LocalAddr())), fast(newTransfer(rc, nil)))
			if res.sendErr != nil || res.recvErr != nil {
				t.Fatalf("sendErr=%v recvErr=%v", res.sendErr, res.recvErr)
			}
			if !bytes.Equal(res.got, data) {
				t.Fatal("data differ after packet loss")
			}
		})
	}
}

func TestStrangerGetsErrorAndTransferContinues(t *testing.T) {
	sc, rc, s := listen(t), listen(t), listen(t)
	data := testData(2000)

	// The stranger sends a DATA (answered with ERROR 5) and an ERROR
	// (never answered) before the real transfer starts.
	if _, err := s.WriteTo(mustMarshal(t, Data{Block: 1, Payload: []byte("evil")}), rc.LocalAddr()); err != nil {
		t.Fatal(err)
	}
	if _, err := s.WriteTo(mustMarshal(t, ErrorPacket{Code: ErrNotDefined, Msg: "go away"}), rc.LocalAddr()); err != nil {
		t.Fatal(err)
	}

	res := runPair(t, data, fast(newTransfer(sc, rc.LocalAddr())), fast(newTransfer(rc, sc.LocalAddr())))
	if res.sendErr != nil || res.recvErr != nil {
		t.Fatalf("sendErr=%v recvErr=%v", res.sendErr, res.recvErr)
	}
	if !bytes.Equal(res.got, data) {
		t.Fatal("the stranger's data leaked into the file")
	}

	p, _ := readPacket(t, s, time.Second)
	if e, ok := p.(ErrorPacket); !ok || e.Code != ErrUnknownTID {
		t.Fatalf("stranger got %#v, want ERROR 5", p)
	}
	_ = s.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if n, _, err := s.ReadFrom(make([]byte, 2048)); err == nil {
		t.Fatalf("stranger got a second packet (%d bytes); an ERROR must not be answered", n)
	}
}

// RFC 1123 4.2.3.1: a duplicate ACK must not make the sender resend DATA.
func TestSenderIgnoresDuplicateAck(t *testing.T) {
	sc, f := listen(t), listen(t)
	ts := newTransfer(sc, f.LocalAddr())
	ts.timeout, ts.maxTimeout, ts.retries = 500*time.Millisecond, 500*time.Millisecond, 2

	done := make(chan error, 1)
	go func() { done <- ts.sendFile(context.Background(), bytes.NewReader(testData(1030))) }() // 512+512+6

	_, from := wantData(t, f, 1)
	ack1 := mustMarshal(t, Ack{Block: 1})
	_, _ = f.WriteTo(ack1, from)
	_, _ = f.WriteTo(ack1, from) // duplicate

	wantData(t, f, 2)
	_ = f.SetReadDeadline(time.Now().Add(100 * time.Millisecond))
	if n, _, err := f.ReadFrom(make([]byte, 2048)); err == nil {
		t.Fatalf("sender sent another packet (%d bytes) after a duplicate ACK", n)
	}

	_, _ = f.WriteTo(mustMarshal(t, Ack{Block: 2}), from)
	d3, _ := wantData(t, f, 3)
	if len(d3.Payload) != 6 {
		t.Fatalf("last block has %d bytes, want 6", len(d3.Payload))
	}
	_, _ = f.WriteTo(mustMarshal(t, Ack{Block: 3}), from)
	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
}

func TestReceiverRepeatsAckForDuplicateData(t *testing.T) {
	rc, f := listen(t), listen(t)
	tr := newTransfer(rc, nil)
	tr.timeout, tr.maxTimeout, tr.retries, tr.dally = 300*time.Millisecond, 300*time.Millisecond, 3, 150*time.Millisecond

	var out bytes.Buffer
	first := rrq(t)
	done := make(chan error, 1)
	go func() { done <- tr.recvFile(context.Background(), &out, first, f.LocalAddr()) }()

	if p, _ := readPacket(t, f, time.Second); p.Opcode() != OpRRQ {
		t.Fatalf("first packet is %v, want RRQ", p.Opcode())
	}
	full := bytes.Repeat([]byte{'a'}, 512)
	d1 := mustMarshal(t, Data{Block: 1, Payload: full})
	_, _ = f.WriteTo(d1, rc.LocalAddr())
	wantAck(t, f, 1)
	_, _ = f.WriteTo(d1, rc.LocalAddr()) // duplicate: our ACK "was lost"
	wantAck(t, f, 1)

	d2 := mustMarshal(t, Data{Block: 2, Payload: []byte("end")})
	_, _ = f.WriteTo(d2, rc.LocalAddr())
	wantAck(t, f, 2)
	_, _ = f.WriteTo(d2, rc.LocalAddr()) // final DATA again while dallying
	wantAck(t, f, 2)

	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	if want := append(append([]byte{}, full...), "end"...); !bytes.Equal(out.Bytes(), want) {
		t.Fatalf("got %d bytes, want %d", out.Len(), len(want))
	}
}

// A datagram one byte over the limit must be dropped, not cut and accepted.
func TestOversizedDatagramIgnored(t *testing.T) {
	rc, f := listen(t), listen(t)
	tr := newTransfer(rc, f.LocalAddr())
	tr.timeout, tr.maxTimeout, tr.dally = 300*time.Millisecond, 300*time.Millisecond, 50*time.Millisecond

	var out bytes.Buffer
	first := rrq(t)
	done := make(chan error, 1)
	go func() { done <- tr.recvFile(context.Background(), &out, first, f.LocalAddr()) }()

	readPacket(t, f, time.Second) // the RRQ
	tooBig := append([]byte{0, 3, 0, 1}, bytes.Repeat([]byte{'x'}, 513)...)
	_, _ = f.WriteTo(tooBig, rc.LocalAddr())
	_, _ = f.WriteTo(mustMarshal(t, Data{Block: 1, Payload: []byte("ok")}), rc.LocalAddr())
	wantAck(t, f, 1)

	if err := waitErr(t, done); err != nil {
		t.Fatal(err)
	}
	if out.String() != "ok" {
		t.Fatalf("got %q, want the small packet only", out.String())
	}
}

func TestSenderGivesUp(t *testing.T) {
	sc, f := listen(t), listen(t)
	ts := newTransfer(sc, f.LocalAddr())
	ts.timeout, ts.maxTimeout, ts.retries = 20*time.Millisecond, 40*time.Millisecond, 3

	err := ts.sendFile(context.Background(), bytes.NewReader(testData(10)))
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	count := 0
	for {
		_ = f.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
		if _, _, err := f.ReadFrom(make([]byte, 2048)); err != nil {
			break
		}
		count++
	}
	if count != 4 { // the first send plus 3 retransmissions
		t.Fatalf("peer saw %d datagrams, want 4", count)
	}
}

func TestContextCancelStopsTransfer(t *testing.T) {
	sc, f := listen(t), listen(t)
	ts := newTransfer(sc, f.LocalAddr())
	ts.timeout, ts.maxTimeout = 5*time.Second, 5*time.Second

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- ts.sendFile(ctx, bytes.NewReader(testData(10))) }()

	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	cancel()
	err := waitErr(t, done)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cancel took %v; the blocked read was not interrupted", d)
	}
}

func TestRemoteErrorEndsTransfer(t *testing.T) {
	t.Run("sender", func(t *testing.T) {
		sc, f := listen(t), listen(t)
		ts := newTransfer(sc, f.LocalAddr())
		done := make(chan error, 1)
		go func() { done <- ts.sendFile(context.Background(), bytes.NewReader(testData(10))) }()

		_, from := wantData(t, f, 1)
		_, _ = f.WriteTo(mustMarshal(t, ErrorPacket{Code: ErrDiskFull, Msg: "full"}), from)

		var re *RemoteError
		if err := waitErr(t, done); !errors.As(err, &re) || re.Code != ErrDiskFull {
			t.Fatalf("err = %v, want RemoteError code 3", err)
		}
	})
	t.Run("client waiting for the first DATA", func(t *testing.T) {
		rc, f := listen(t), listen(t)
		tr := newTransfer(rc, nil)
		first := rrq(t)
		done := make(chan error, 1)
		go func() { done <- tr.recvFile(context.Background(), &bytes.Buffer{}, first, f.LocalAddr()) }()

		_, from := readPacket(t, f, time.Second) // the RRQ
		_, _ = f.WriteTo(mustMarshal(t, ErrorPacket{Code: ErrFileNotFound, Msg: "nope"}), from)

		var re *RemoteError
		if err := waitErr(t, done); !errors.As(err, &re) || re.Code != ErrFileNotFound {
			t.Fatalf("err = %v, want RemoteError code 1", err)
		}
	})
}

// Block numbers are 16 bits; a file needing more blocks cannot be sent.
func TestSenderStopsAtBlockLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("65535 round trips")
	}
	t.Parallel()
	sc, f := listen(t), listen(t)
	ts := newTransfer(sc, f.LocalAddr())
	ts.blksize = 1 // 65535 one-byte blocks are as many blocks as 33 MB
	ts.timeout, ts.maxTimeout = 2*time.Second, 2*time.Second

	go func() { // a peer that acknowledges everything
		buf := make([]byte, 2048)
		for {
			n, from, err := f.ReadFrom(buf)
			if err != nil {
				return
			}
			if p, err := Parse(buf[:n]); err == nil {
				if d, ok := p.(Data); ok {
					b, _ := Ack{Block: d.Block}.MarshalBinary()
					_, _ = f.WriteTo(b, from)
				}
			}
		}
	}()

	err := ts.sendFile(context.Background(), bytes.NewReader(testData(65536)))
	if !errors.Is(err, ErrTransferTooLarge) {
		t.Fatalf("err = %v, want ErrTransferTooLarge", err)
	}
}

func TestReceiverStopsAtBlockLimit(t *testing.T) {
	if testing.Short() {
		t.Skip("65535 round trips")
	}
	t.Parallel()
	sc, rc := listen(t), listen(t)
	ts := newTransfer(sc, rc.LocalAddr())
	tr := newTransfer(rc, nil)
	ts.blksize, tr.blksize = 1, 1
	ts.timeout, ts.maxTimeout = 2*time.Second, 2*time.Second
	tr.timeout, tr.maxTimeout = 2*time.Second, 2*time.Second

	res := runPair(t, testData(65536), ts, tr)
	if !errors.Is(res.recvErr, ErrTransferTooLarge) {
		t.Fatalf("recvErr = %v, want ErrTransferTooLarge", res.recvErr)
	}
	var re *RemoteError
	if !errors.As(res.sendErr, &re) {
		t.Fatalf("sendErr = %v, want the peer's ERROR", res.sendErr)
	}
}
