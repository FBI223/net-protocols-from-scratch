package tftp

// Scenario tests: the real Client talks to the real Server through a UDP
// proxy that damages chosen packets (drop, duplicate, delay). They check what
// the whole protocol does, not single functions.

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// --- the proxy -----------------------------------------------------------

type action int

const (
	pass action = iota
	drop
	dup
)

// pktInfo identifies one packet for the impairment rules.
type pktInfo struct {
	toServer bool
	op       Opcode
	block    uint16 // DATA and ACK only, 0 for everything else
	seen     int    // how many identical packets passed before this one
}

// rule decides what happens to a packet and how long it is held back.
type rule func(pktInfo) (action, time.Duration)

// classify reads just enough of a packet to describe it.
func classify(data []byte) (Opcode, uint16) {
	op, err := ParseOpcode(data)
	if err != nil {
		return 0, 0
	}
	switch op {
	case OpData:
		if d, err := ParseData(data); err == nil {
			return op, d.Block
		}
	case OpAck:
		if a, err := ParseAck(data); err == nil {
			return op, a.Block
		}
	}
	return op, 0
}

// proxy sits between a client and a server and looks like a real server to
// the client: requests go to its front address, every server transfer port
// (TID) is shown to the client as a separate port of its own, so the client
// sees the same port changes as with a direct connection.
type proxy struct {
	front  net.PacketConn
	server net.Addr
	rule   rule

	mu       sync.Mutex
	seen     map[pktInfo]int
	sessions map[string]*session
	conns    []net.PacketConn
	closed   bool
	wg       sync.WaitGroup
	start    time.Time
	events   []string // one line per packet, for the failure report
}

// session is one client. up is the socket the server sees as that client.
type session struct {
	client net.Addr
	up     net.PacketConn
	legs   map[string]*leg
}

// leg is one server TID, as shown to the client.
type leg struct {
	front  net.PacketConn
	server net.Addr
}

func newProxy(t *testing.T, serverAddr string, r rule) *proxy {
	t.Helper()
	front, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	server, err := net.ResolveUDPAddr("udp", serverAddr)
	if err != nil {
		t.Fatal(err)
	}
	p := &proxy{
		front:    front,
		server:   server,
		rule:     r,
		seen:     map[pktInfo]int{},
		sessions: map[string]*session{},
		conns:    []net.PacketConn{front},
		start:    time.Now(),
	}
	p.wg.Add(1)
	go p.serveFront()
	t.Cleanup(func() {
		p.mu.Lock()
		p.closed = true
		for _, c := range p.conns {
			_ = c.Close()
		}
		p.mu.Unlock()
		p.wg.Wait()
	})
	return p
}

func (p *proxy) addr() string { return p.front.LocalAddr().String() }

// count is how many packets of this kind went through, damaged or not.
func (p *proxy) count(toServer bool, op Opcode, block uint16) int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.seen[pktInfo{toServer: toServer, op: op, block: block}]
}

// forward applies the rule and sends the packet from conn to to.
func (p *proxy) forward(conn net.PacketConn, to net.Addr, data []byte, toServer bool) {
	op, block := classify(data)
	info := pktInfo{toServer: toServer, op: op, block: block}

	p.mu.Lock()
	key := info
	info.seen = p.seen[key]
	p.seen[key]++
	p.mu.Unlock()

	act, delay := pass, time.Duration(0)
	if p.rule != nil {
		act, delay = p.rule(info)
	}
	p.record(info, act, delay, to)
	if act == drop {
		return
	}
	d := bytes.Clone(data) // the read buffer is reused
	send := func() { _, _ = conn.WriteTo(d, to) }
	times := 1
	if act == dup {
		times = 2
	}
	for range times {
		if delay > 0 {
			time.AfterFunc(delay, send)
		} else {
			send()
		}
	}
}

// record keeps one line per packet; dump prints them when a test fails.
func (p *proxy) record(i pktInfo, act action, delay time.Duration, to net.Addr) {
	dir := "server->client"
	if i.toServer {
		dir = "client->server"
	}
	what := [...]string{"pass", "DROP", "DUP"}[act]
	p.mu.Lock()
	p.events = append(p.events, fmt.Sprintf("%7.1fms %s op=%d block=%d #%d to=%v %s delay=%v",
		float64(time.Since(p.start).Microseconds())/1000, dir, i.op, i.block, i.seen, to, what, delay))
	p.mu.Unlock()
}

func (p *proxy) dump(t *testing.T) {
	t.Helper()
	p.mu.Lock()
	defer p.mu.Unlock()
	ev := p.events
	if len(ev) > 150 {
		ev = ev[len(ev)-150:]
	}
	t.Logf("last %d packets seen by the proxy:\n%s", len(ev), strings.Join(ev, "\n"))
}

func (p *proxy) listenLocked() net.PacketConn {
	if p.closed {
		return nil
	}
	c, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		return nil
	}
	p.conns = append(p.conns, c)
	return c
}

func (p *proxy) session(client net.Addr) *session {
	p.mu.Lock()
	defer p.mu.Unlock()
	if s, ok := p.sessions[client.String()]; ok {
		return s
	}
	up := p.listenLocked()
	if up == nil {
		return nil
	}
	s := &session{client: client, up: up, legs: map[string]*leg{}}
	p.sessions[client.String()] = s
	p.wg.Add(1)
	go p.serveUp(s)
	return s
}

func (p *proxy) leg(s *session, server net.Addr) *leg {
	p.mu.Lock()
	defer p.mu.Unlock()
	if l, ok := s.legs[server.String()]; ok {
		return l
	}
	front := p.listenLocked()
	if front == nil {
		return nil
	}
	l := &leg{front: front, server: server}
	s.legs[server.String()] = l
	p.wg.Add(1)
	go p.serveLeg(s, l)
	return l
}

// serveFront handles requests: they always go to the server's request port.
func (p *proxy) serveFront() {
	defer p.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := p.front.ReadFrom(buf)
		if err != nil {
			return
		}
		if s := p.session(from); s != nil {
			p.forward(s.up, p.server, buf[:n], true)
		}
	}
}

// serveUp handles everything the server sends to one client.
func (p *proxy) serveUp(s *session) {
	defer p.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, from, err := s.up.ReadFrom(buf)
		if err != nil {
			return
		}
		if l := p.leg(s, from); l != nil {
			p.forward(l.front, s.client, buf[:n], false)
		}
	}
}

// serveLeg handles what the client sends to one server TID.
func (p *proxy) serveLeg(s *session, l *leg) {
	defer p.wg.Done()
	buf := make([]byte, 2048)
	for {
		n, _, err := l.front.ReadFrom(buf)
		if err != nil {
			return
		}
		p.forward(s.up, l.server, buf[:n], true)
	}
}

// --- rules ---------------------------------------------------------------

func hit(i pktInfo, toServer bool, op Opcode, block uint16) bool {
	return i.toServer == toServer && i.op == op && i.block == block
}

// dropFirst drops the first n packets of one kind.
func dropFirst(toServer bool, op Opcode, block uint16, n int) rule {
	return func(i pktInfo) (action, time.Duration) {
		if hit(i, toServer, op, block) && i.seen < n {
			return drop, 0
		}
		return pass, 0
	}
}

// dupFirst delivers the first n packets of one kind twice.
func dupFirst(toServer bool, op Opcode, block uint16, n int) rule {
	return func(i pktInfo) (action, time.Duration) {
		if hit(i, toServer, op, block) && i.seen < n {
			return dup, 0
		}
		return pass, 0
	}
}

// roll gives a number in [0,1) that depends only on the seed and on which
// packet this is, not on timing, so one seed damages the same packets each run.
func roll(seed uint64, i pktInfo, salt int) float64 {
	h := fnv.New64a()
	_, _ = io.WriteString(h, fmt.Sprintf("%d/%d/%t/%d/%d/%d", seed, salt, i.toServer, i.op, i.block, i.seen))
	// FNV alone gives similar results for inputs that differ only in their
	// last characters (here: the retransmission count), which made a packet
	// look "lost" again and again. This finalizer (from splitmix64) mixes
	// all bits.
	x := h.Sum64()
	x ^= x >> 30
	x *= 0xbf58476d1ce4e5b9
	x ^= x >> 27
	x *= 0x94d049bb133111eb
	x ^= x >> 31
	return float64(x>>11) / (1 << 53)
}

// randomImpairments drops 10% of the packets, duplicates 5% and holds back
// 30% for up to 70 ms (longer than the timeout, so some retransmissions are
// needless and some packets arrive late or out of order).
func randomImpairments(seed uint64) rule {
	return func(i pktInfo) (action, time.Duration) {
		var delay time.Duration
		if roll(seed, i, 2) < 0.3 {
			delay = time.Duration(roll(seed, i, 3) * float64(70*time.Millisecond))
		}
		switch u := roll(seed, i, 1); {
		case u < 0.10:
			return drop, 0
		case u < 0.15:
			return dup, delay
		}
		return pass, delay
	}
}

// --- scenario helper -----------------------------------------------------

type scenario struct {
	root   string
	proxy  *proxy
	client *Client
}

// newScenario starts a server that writes into a temp folder, a proxy in
// front of it and a client that talks to the proxy.
func newScenario(t *testing.T, timeout time.Duration, r rule) *scenario {
	t.Helper()
	const dally = 400 * time.Millisecond
	root := rootDir(t)
	srvAddr := startServer(t, root, func(s *Server) {
		s.AllowWrite = true
		s.Timeout = timeout
		s.MaxTimeout = 4 * timeout
		s.Retries = 12
		s.Dally = dally
	})
	return &scenario{
		root:  root,
		proxy: newProxy(t, srvAddr, r),
		client: &Client{
			Timeout:    timeout,
			MaxTimeout: 4 * timeout,
			Retries:    12,
			Dally:      dally,
		},
	}
}

func (s *scenario) get(t *testing.T, name string) ([]byte, error) {
	t.Helper()
	var buf bytes.Buffer
	err := s.client.Get(testCtx(t), s.proxy.addr(), name, &buf)
	return buf.Bytes(), err
}

func (s *scenario) put(t *testing.T, name string, data []byte) error {
	t.Helper()
	return s.client.Put(testCtx(t), s.proxy.addr(), name, bytes.NewReader(data))
}

// wantGet downloads a file and fails unless it arrives intact.
func (s *scenario) wantGet(t *testing.T, name string, want []byte) {
	t.Helper()
	got, err := s.get(t, name)
	if err != nil {
		s.proxy.dump(t)
		t.Fatalf("Get: %v", err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("Get returned %d bytes, differ from the %d stored", len(got), len(want))
	}
}

// wantPut uploads a file and fails unless it lands intact in the folder.
func (s *scenario) wantPut(t *testing.T, name string, data []byte) {
	t.Helper()
	if err := s.put(t, name, data); err != nil {
		s.proxy.dump(t)
		t.Fatalf("Put: %v", err)
	}
	got, err := os.ReadFile(filepath.Join(s.root, name))
	if err != nil {
		t.Fatalf("uploaded file is missing: %v", err)
	}
	if !bytes.Equal(got, data) {
		t.Fatalf("stored %d bytes, differ from the %d uploaded", len(got), len(data))
	}
}

func wantCount(t *testing.T, what string, got, want int) {
	t.Helper()
	if got != want {
		t.Errorf("%s: %d, want %d", what, got, want)
	}
}

func wantAtLeast(t *testing.T, what string, got, min int) {
	t.Helper()
	if got < min {
		t.Errorf("%s: %d, want at least %d", what, got, min)
	}
}

// --- scripted loss -------------------------------------------------------

const (
	tick  = 50 * time.Millisecond  // short timeout for tests that lose packets
	calm  = 300 * time.Millisecond // long timeout: nothing should be resent
	size5 = 5000                   // 10 blocks, the last one has 392 bytes
)

// A lost final ACK is the case dallying exists for: the server resends the
// last DATA and the client, still listening, acknowledges it again.
func TestScenarioLostFinalAckOnGet(t *testing.T) {
	sc := newScenario(t, tick, dropFirst(true, OpAck, 10, 1))
	data := testData(size5)
	writeFile(t, filepath.Join(sc.root, "f.bin"), data)

	sc.wantGet(t, "f.bin", data)
	wantAtLeast(t, "DATA 10 sent by the server", sc.proxy.count(false, OpData, 10), 2)
	wantAtLeast(t, "ACK 10 sent by the client", sc.proxy.count(true, OpAck, 10), 2)
}

// Same on upload: the client resends the last DATA, the server (dallying)
// acknowledges it again.
func TestScenarioLostFinalAckOnPut(t *testing.T) {
	sc := newScenario(t, tick, dropFirst(false, OpAck, 10, 1))
	sc.wantPut(t, "up.bin", testData(size5))
	wantAtLeast(t, "DATA 10 sent by the client", sc.proxy.count(true, OpData, 10), 2)
}

func TestScenarioLostReadRequest(t *testing.T) {
	sc := newScenario(t, tick, dropFirst(true, OpRRQ, 0, 1))
	data := testData(size5)
	writeFile(t, filepath.Join(sc.root, "f.bin"), data)

	sc.wantGet(t, "f.bin", data)
	wantCount(t, "RRQ sent", sc.proxy.count(true, OpRRQ, 0), 2)
}

// If the ACK 0 answering a WRQ is lost, the client sends the WRQ again. The
// server must not treat it as a second upload: the first one has already
// created the file, so a second transfer would answer ERROR 6 and the client
// would give up. The first ACK 0 and its first repeat are both lost, so the
// repeated WRQ is the first thing the client hears back about.
func TestScenarioLostAck0OnPut(t *testing.T) {
	sc := newScenario(t, tick, dropFirst(false, OpAck, 0, 2))
	sc.wantPut(t, "up.bin", testData(size5))
	wantAtLeast(t, "WRQ sent", sc.proxy.count(true, OpWRQ, 0), 2)
	wantCount(t, "ERROR sent by the server", sc.proxy.count(false, OpError, 0), 0)
}

// If DATA 1 answering an RRQ is lost, the client sends the RRQ again.
func TestScenarioLostFirstDataOnGet(t *testing.T) {
	sc := newScenario(t, tick, dropFirst(false, OpData, 1, 1))
	data := testData(size5)
	writeFile(t, filepath.Join(sc.root, "f.bin"), data)
	sc.wantGet(t, "f.bin", data)
}

// DATA 3 is lost twice: it is resent after the timeout, then after a longer
// one, and the third copy gets through.
func TestScenarioLostDataTwice(t *testing.T) {
	sc := newScenario(t, tick, dropFirst(false, OpData, 3, 2))
	data := testData(size5)
	writeFile(t, filepath.Join(sc.root, "f.bin"), data)

	sc.wantGet(t, "f.bin", data)
	wantCount(t, "DATA 3 sent by the server", sc.proxy.count(false, OpData, 3), 3)
}

// RFC 1123 §4.2.3.1, Sorcerer's Apprentice: a duplicate ACK must not make the
// sender resend DATA, or every later block would be sent twice.
func TestScenarioDuplicateAckDoesNotResendData(t *testing.T) {
	sc := newScenario(t, calm, dupFirst(true, OpAck, 2, 1))
	data := testData(size5)
	writeFile(t, filepath.Join(sc.root, "f.bin"), data)

	sc.wantGet(t, "f.bin", data)
	wantCount(t, "DATA 3 sent by the server", sc.proxy.count(false, OpData, 3), 1)
	wantCount(t, "DATA 4 sent by the server", sc.proxy.count(false, OpData, 4), 1)
}

// A duplicate DATA makes the receiver ACK twice. The sender must still send
// every later block only once.
func TestScenarioDuplicateDataIsHarmless(t *testing.T) {
	sc := newScenario(t, calm, dupFirst(false, OpData, 3, 1))
	data := testData(size5)
	writeFile(t, filepath.Join(sc.root, "f.bin"), data)

	sc.wantGet(t, "f.bin", data)
	wantCount(t, "DATA 4 sent by the server", sc.proxy.count(false, OpData, 4), 1)
}

// The same check for uploads.
func TestScenarioDuplicateDataOnPut(t *testing.T) {
	sc := newScenario(t, calm, dupFirst(true, OpData, 3, 1))
	sc.wantPut(t, "up.bin", testData(size5))
	wantCount(t, "DATA 4 sent by the client", sc.proxy.count(true, OpData, 4), 1)
}

// When the peer goes silent for good the client must give up, not hang.
func TestScenarioPeerGoesSilent(t *testing.T) {
	sc := newScenario(t, 30*time.Millisecond, dropFirst(false, OpData, 3, 1<<30))
	sc.client.Retries = 3
	writeFile(t, filepath.Join(sc.root, "f.bin"), testData(size5))

	start := time.Now()
	_, err := sc.get(t, "f.bin")
	if !errors.Is(err, ErrTimeout) {
		t.Fatalf("err = %v, want ErrTimeout", err)
	}
	if d := time.Since(start); d > 5*time.Second {
		t.Fatalf("giving up took %v", d)
	}
}

// ERROR is never retransmitted, so a lost ERROR is found by the request
// timing out and being sent again.
func TestScenarioLostErrorPacket(t *testing.T) {
	sc := newScenario(t, tick, dropFirst(false, OpError, 0, 1))

	_, err := sc.get(t, "no-such-file")
	wantRemoteError(t, err, ErrFileNotFound)
	wantCount(t, "RRQ sent", sc.proxy.count(true, OpRRQ, 0), 2)
	wantCount(t, "ERROR sent by the server", sc.proxy.count(false, OpError, 0), 2)
}

// --- random damage -------------------------------------------------------

// Many transfers of random sizes in both directions through a damaging
// network. A failure names the seed: run it again with
//
//	go test -run 'TestScenarioRandomDamage/seed_7$' -v
func TestScenarioRandomDamage(t *testing.T) {
	seeds := 24
	if testing.Short() {
		seeds = 6
	}
	for s := 1; s <= seeds; s++ {
		seed := uint64(s)
		t.Run("seed_"+strconv.Itoa(s), func(t *testing.T) {
			t.Parallel()
			size := int(roll(seed, pktInfo{}, 9) * 20000)
			data := testData(size)
			sc := newScenario(t, 40*time.Millisecond, randomImpairments(seed))
			if seed%2 == 0 {
				writeFile(t, filepath.Join(sc.root, "f.bin"), data)
				sc.wantGet(t, "f.bin", data)
			} else {
				sc.wantPut(t, "up.bin", data)
			}
		})
	}
}

// --- leaks ---------------------------------------------------------------

func openFDs() int {
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		return -1 // not Linux: skip the descriptor check
	}
	return len(entries)
}

// transferBatch starts a server, runs n mixed transfers against it (good,
// failing and cancelled halfway) and stops the server again.
func transferBatch(t *testing.T, n int) {
	t.Helper()
	root := rootDir(t)
	small, big := testData(3000), testData(60*1024)
	writeFile(t, filepath.Join(root, "small.bin"), small)
	writeFile(t, filepath.Join(root, "big.bin"), big)

	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	srv := &Server{Root: root, AllowWrite: true, Timeout: 30 * time.Millisecond,
		MaxTimeout: 120 * time.Millisecond, Retries: 5, Dally: -1}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ctx, conn) }()
	addr := conn.LocalAddr().String()
	c := &Client{Timeout: 30 * time.Millisecond, MaxTimeout: 120 * time.Millisecond, Retries: 5, Dally: -1}

	for i := 0; i < n; i++ {
		switch i % 5 {
		case 0:
			var buf bytes.Buffer
			if err := c.Get(testCtx(t), addr, "small.bin", &buf); err != nil || !bytes.Equal(buf.Bytes(), small) {
				t.Fatalf("transfer %d: Get: err=%v, %d bytes", i, err, buf.Len())
			}
		case 1:
			name := "up-" + strconv.Itoa(i) + ".bin"
			if err := c.Put(testCtx(t), addr, name, bytes.NewReader(small)); err != nil {
				t.Fatalf("transfer %d: Put: %v", i, err)
			}
		case 2:
			var buf bytes.Buffer
			wantRemoteError(t, c.Get(testCtx(t), addr, "missing.bin", &buf), ErrFileNotFound)
		case 3: // cancelled halfway: the result does not matter, the cleanup does
			cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
			_ = c.Get(cctx, addr, "big.bin", io.Discard)
			ccancel()
		case 4:
			cctx, ccancel := context.WithTimeout(context.Background(), 2*time.Millisecond)
			_ = c.Put(cctx, addr, "up-big-"+strconv.Itoa(i)+".bin", bytes.NewReader(big))
			ccancel()
		}
	}

	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Errorf("Serve returned %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after cancel")
	}
	_ = conn.Close()
}

// After many transfers, some failed or cancelled, and a server shutdown, no
// goroutine and no file descriptor may be left behind.
func TestNoLeaksAfterManyTransfers(t *testing.T) {
	// A warm-up batch lets the runtime create its own long-lived state first.
	transferBatch(t, 5)
	g0, f0 := settled()

	// A socket nobody closed is closed later by the garbage collector, which
	// would hide the leak. Keep the collector off while the batch runs.
	gc := debug.SetGCPercent(-1)
	defer debug.SetGCPercent(gc)
	transferBatch(t, 150)

	// Goroutines and sockets close a little after the last call returns, so
	// wait for the counts to come back down before judging.
	deadline := time.Now().Add(3 * time.Second)
	for {
		g, f := runtime.NumGoroutine(), openFDs()
		if g <= g0 && f <= f0 {
			return
		}
		if time.Now().After(deadline) {
			buf := make([]byte, 1<<20)
			buf = buf[:runtime.Stack(buf, true)]
			t.Fatalf("after 150 transfers: %d goroutines (before: %d), %d open files (before: %d)\n%s",
				g, g0, f, f0, buf)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

// settled returns the goroutine and descriptor counts once they stop changing.
func settled() (goroutines, fds int) {
	stable := 0
	for i := 0; i < 100 && stable < 4; i++ {
		g, f := runtime.NumGoroutine(), openFDs()
		if g == goroutines && f == fds {
			stable++
		} else {
			goroutines, fds, stable = g, f, 0
		}
		time.Sleep(25 * time.Millisecond)
	}
	return goroutines, fds
}
