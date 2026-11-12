package tftp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// These tests use helpers from transfer_test.go (listen, fast, lossy,
// testData, mustMarshal).

// --- a small RFC 1350 server for the tests -----------------------------

type testServerConfig struct {
	files     map[string][]byte // served on RRQ
	denyWrite bool              // answer every WRQ with ERROR 2
	lossEvery int64             // drop every n-th packet the server sends; 0 = none
}

type upload struct {
	name string
	data []byte
	err  error
}

// testServer behaves like a real TFTP server: it takes requests on one
// socket and serves each one from a new socket, so the client sees a new TID.
type testServer struct {
	cfg     testServerConfig
	main    net.PacketConn
	addr    string
	uploads chan upload // one entry per finished WRQ
}

func newTestServer(t *testing.T, cfg testServerConfig) *testServer {
	t.Helper()
	main := listen(t)
	s := &testServer{cfg: cfg, main: main, addr: main.LocalAddr().String(), uploads: make(chan upload, 8)}
	go s.loop()
	return s
}

func (s *testServer) loop() {
	buf := make([]byte, 2048)
	for {
		n, from, err := s.main.ReadFrom(buf)
		if err != nil {
			return // the test closed the socket
		}
		if p, err := Parse(buf[:n]); err == nil {
			if req, ok := p.(Request); ok {
				go s.serve(req, from)
			}
		}
	}
}

func (s *testServer) serve(req Request, client net.Addr) {
	raw, err := net.ListenPacket("udp", "127.0.0.1:0") // the new socket is the server's TID
	if err != nil {
		return
	}
	defer func() { _ = raw.Close() }()
	conn := net.PacketConn(raw)
	if s.cfg.lossEvery > 0 {
		conn = &lossy{PacketConn: raw, every: s.cfg.lossEvery}
	}
	tr := fast(newTransfer(conn, client))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	switch req.Op {
	case OpRRQ:
		data, ok := s.cfg.files[req.Filename]
		if !ok {
			tr.sendError(client, ErrFileNotFound, "file not found")
			return
		}
		_ = tr.sendFile(ctx, bytes.NewReader(data))
	case OpWRQ:
		if s.cfg.denyWrite {
			tr.sendError(client, ErrAccessViolation, "read only")
			return
		}
		ack0, _ := Ack{Block: 0}.MarshalBinary()
		var buf bytes.Buffer
		err := tr.recvFile(ctx, &buf, ack0, client)
		s.uploads <- upload{name: req.Filename, data: buf.Bytes(), err: err}
	}
}

func receiveUpload(t *testing.T, s *testServer) upload {
	t.Helper()
	select {
	case u := <-s.uploads:
		return u
	case <-time.After(10 * time.Second):
		t.Fatal("the server did not finish the upload")
		return upload{}
	}
}

// silentServer returns an address that never answers. received returns the
// requests that arrived there, so call it after the client has finished.
func silentServer(t *testing.T) (addr string, received func() []Request) {
	t.Helper()
	conn := listen(t)
	return conn.LocalAddr().String(), func() []Request {
		var reqs []Request
		buf := make([]byte, 2048)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(50 * time.Millisecond))
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return reqs
			}
			if r, err := ParseRequest(buf[:n]); err == nil {
				reqs = append(reqs, r)
			}
		}
	}
}

// fastClient has short timers so tests that lose packets stay quick.
func fastClient() *Client {
	return &Client{Timeout: 40 * time.Millisecond, MaxTimeout: 160 * time.Millisecond, Retries: 10}
}

func testCtx(t *testing.T) context.Context {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	t.Cleanup(cancel)
	return ctx
}

// --- Get ---------------------------------------------------------------

var clientSizes = []struct {
	name string
	size int
}{
	{"empty file", 0},
	{"1 byte", 1},
	{"511 bytes", 511},
	{"512 bytes: ends with an empty block", 512},
	{"513 bytes", 513},
	{"1024 bytes: ends with an empty block", 1024},
	{"1300 bytes", 1300},
	{"100 full blocks and 1 byte", 51201},
}

func TestClientGet(t *testing.T) {
	files := map[string][]byte{}
	for _, tt := range clientSizes {
		files[strconv.Itoa(tt.size)] = testData(tt.size)
	}
	srv := newTestServer(t, testServerConfig{files: files})

	for _, tt := range clientSizes {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := fastClient().Get(testCtx(t), srv.addr, strconv.Itoa(tt.size), &out); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), files[strconv.Itoa(tt.size)]) {
				t.Fatalf("got %d bytes, which differ from the %d on the server", out.Len(), tt.size)
			}
		})
	}
}

func TestClientGetWithPacketLoss(t *testing.T) {
	data := testData(10*512 + 100)
	srv := newTestServer(t, testServerConfig{files: map[string][]byte{"f": data}, lossEvery: 3})

	var out bytes.Buffer
	if err := fastClient().Get(testCtx(t), srv.addr, "f", &out); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), data) {
		t.Fatal("data differ after packet loss")
	}
}

// Get waits after the final ACK (dallying) unless told not to.
func TestClientGetDally(t *testing.T) {
	srv := newTestServer(t, testServerConfig{files: map[string][]byte{"f": testData(100)}})

	tests := []struct {
		name    string
		dally   time.Duration
		minTime time.Duration // at least this long
		maxTime time.Duration // at most this long (0 = no limit)
	}{
		{"default is twice Timeout", 0, 900 * time.Millisecond, 0},
		{"negative returns at once", -1, 0, 300 * time.Millisecond},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			c := &Client{Timeout: 500 * time.Millisecond, Dally: tt.dally}
			start := time.Now()
			if err := c.Get(testCtx(t), srv.addr, "f", &bytes.Buffer{}); err != nil {
				t.Fatal(err)
			}
			d := time.Since(start)
			if d < tt.minTime || (tt.maxTime > 0 && d > tt.maxTime) {
				t.Fatalf("Get took %v, want between %v and %v", d, tt.minTime, tt.maxTime)
			}
		})
	}
}

func TestClientGetFileNotFound(t *testing.T) {
	srv := newTestServer(t, testServerConfig{})
	err := fastClient().Get(testCtx(t), srv.addr, "missing", &bytes.Buffer{})

	var re *RemoteError
	if !errors.As(err, &re) || re.Code != ErrFileNotFound {
		t.Fatalf("err = %v, want a RemoteError with code 1", err)
	}
}

type failWriter struct{ err error }

func (w failWriter) Write([]byte) (int, error) { return 0, w.err }

func TestClientGetWriterError(t *testing.T) {
	srv := newTestServer(t, testServerConfig{files: map[string][]byte{"f": testData(1300)}})
	boom := errors.New("disk on fire")

	err := fastClient().Get(testCtx(t), srv.addr, "f", failWriter{boom})
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the writer's error", err)
	}
}

// --- Put ---------------------------------------------------------------

func TestClientPut(t *testing.T) {
	for _, tt := range clientSizes {
		t.Run(tt.name, func(t *testing.T) {
			srv := newTestServer(t, testServerConfig{})
			data := testData(tt.size)

			if err := fastClient().Put(testCtx(t), srv.addr, "up.bin", bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			u := receiveUpload(t, srv)
			if u.err != nil {
				t.Fatalf("server side: %v", u.err)
			}
			if u.name != "up.bin" || !bytes.Equal(u.data, data) {
				t.Fatalf("server got %q with %d bytes, want %q with %d", u.name, len(u.data), "up.bin", len(data))
			}
		})
	}
}

func TestClientPutWithPacketLoss(t *testing.T) {
	srv := newTestServer(t, testServerConfig{lossEvery: 3})
	data := testData(10*512 + 100)

	if err := fastClient().Put(testCtx(t), srv.addr, "f", bytes.NewReader(data)); err != nil {
		t.Fatal(err)
	}
	if u := receiveUpload(t, srv); u.err != nil || !bytes.Equal(u.data, data) {
		t.Fatalf("upload damaged: err=%v, %d bytes of %d", u.err, len(u.data), len(data))
	}
}

func TestClientPutDenied(t *testing.T) {
	srv := newTestServer(t, testServerConfig{denyWrite: true})
	err := fastClient().Put(testCtx(t), srv.addr, "f", bytes.NewReader([]byte("x")))

	var re *RemoteError
	if !errors.As(err, &re) || re.Code != ErrAccessViolation {
		t.Fatalf("err = %v, want a RemoteError with code 2", err)
	}
}

// --- timeouts, cancelling, bad arguments --------------------------------

func TestClientSilentServer(t *testing.T) {
	tests := []struct {
		name string
		op   Opcode
	}{
		{"get", OpRRQ},
		{"put", OpWRQ},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			addr, received := silentServer(t)
			c := &Client{Timeout: 20 * time.Millisecond, MaxTimeout: 40 * time.Millisecond, Retries: 3}

			var err error
			if tt.op == OpRRQ {
				err = c.Get(testCtx(t), addr, "f", &bytes.Buffer{})
			} else {
				err = c.Put(testCtx(t), addr, "f", bytes.NewReader([]byte("x")))
			}
			if !errors.Is(err, ErrTimeout) {
				t.Fatalf("err = %v, want ErrTimeout", err)
			}

			reqs := received()
			if len(reqs) != 4 { // the first send plus 3 retransmissions
				t.Fatalf("server saw %d requests, want 4", len(reqs))
			}
			for _, r := range reqs {
				if r.Op != tt.op || r.Filename != "f" || r.Mode != "octet" || len(r.Options) != 0 {
					t.Fatalf("request = %+v, want op %d, file %q, octet, no options", r, tt.op, "f")
				}
			}
		})
	}
}

func TestClientCancel(t *testing.T) {
	addr, _ := silentServer(t)
	c := &Client{Timeout: 5 * time.Second, MaxTimeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- c.Get(ctx, addr, "f", &bytes.Buffer{}) }()

	time.Sleep(50 * time.Millisecond)
	start := time.Now()
	cancel()
	err := waitErr(t, done)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cancel took %v", d)
	}
}

// Bad arguments must fail before anything is sent.
func TestClientBadArguments(t *testing.T) {
	tests := []struct {
		name string
		addr string
		file string
	}{
		{"empty file name", "127.0.0.1:69", ""},
		{"zero byte in file name", "127.0.0.1:69", "a\x00b"},
		{"file name too long for a request", "127.0.0.1:69", strings.Repeat("x", 504)},
		{"address without a port", "127.0.0.1", "f"},
		{"not an address", "not an address", "f"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if err := Get(testCtx(t), tt.addr, tt.file, &bytes.Buffer{}); err == nil {
				t.Error("Get: expected an error")
			}
			if err := Put(testCtx(t), tt.addr, tt.file, bytes.NewReader(nil)); err == nil {
				t.Error("Put: expected an error")
			}
		})
	}
}

// startDiskServer runs our Server on a random localhost port, serving root,
// and returns its address. It is stopped when the test ends.
func startDiskServer(t *testing.T, root string) string {
	t.Helper()
	conn, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	srv := &Server{Root: root, AllowWrite: true, Dally: -1}
	go func() {
		defer close(done)
		_ = srv.Serve(ctx, conn)
	}()
	t.Cleanup(func() {
		cancel()
		<-done
		_ = conn.Close()
	})
	return conn.LocalAddr().String()
}

// diskClient does not dally, so each transfer ends right after the last block.
var diskClient = &Client{Dally: -1}

// TestGetFilesFromDisk puts real files of boundary sizes in a folder, serves
// the folder and checks that Get returns exactly the bytes stored on disk.
func TestGetFilesFromDisk(t *testing.T) {
	root := t.TempDir()
	sizes := []int{0, 1, 511, 512, 513, 1024, 5000, 100000}
	for _, size := range sizes {
		name := "f" + strconv.Itoa(size) + ".bin"
		if err := os.WriteFile(filepath.Join(root, name), testData(size), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	addr := startDiskServer(t, root)

	for _, size := range sizes {
		name := "f" + strconv.Itoa(size) + ".bin"
		t.Run(name, func(t *testing.T) {
			want, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			if err := diskClient.Get(testCtx(t), addr, name, &got); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("got %d bytes, file on disk has %d, or the content differs", got.Len(), len(want))
			}
		})
	}
}

// TestPutThenGetOnDisk uploads files, checks that they landed on disk intact
// and reads them back with Get.
func TestPutThenGetOnDisk(t *testing.T) {
	root := t.TempDir()
	addr := startDiskServer(t, root)

	for _, size := range []int{0, 1, 511, 512, 1300, 5000} {
		t.Run(strconv.Itoa(size), func(t *testing.T) {
			name := "up-" + strconv.Itoa(size) + ".bin"
			data := testData(size)
			if err := diskClient.Put(testCtx(t), addr, name, bytes.NewReader(data)); err != nil {
				t.Fatalf("put: %v", err)
			}
			onDisk, err := os.ReadFile(filepath.Join(root, name))
			if err != nil {
				t.Fatalf("uploaded file missing on disk: %v", err)
			}
			if !bytes.Equal(onDisk, data) {
				t.Fatalf("file on disk has %d bytes, uploaded %d, or the content differs", len(onDisk), len(data))
			}
			var got bytes.Buffer
			if err := diskClient.Get(testCtx(t), addr, name, &got); err != nil {
				t.Fatalf("get: %v", err)
			}
			if !bytes.Equal(got.Bytes(), data) {
				t.Fatalf("read back %d bytes, differ from the %d uploaded", got.Len(), len(data))
			}
		})
	}
}
