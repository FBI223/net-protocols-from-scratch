package tftp

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"testing"
	"time"
)

// These tests use helpers from transfer_test.go (listen, testData, readPacket,
// mustMarshal, waitErr) and from client_test.go (fastClient, testCtx,
// clientSizes). The server is exercised with the real Client, and with a raw
// UDP socket when a test needs to send or look at single packets.

// --- helpers -----------------------------------------------------------

// startServer runs a Server on a free localhost port with short timers and
// returns its address. At cleanup it stops the server and waits for it.
func startServer(t *testing.T, root string, configure func(*Server)) string {
	t.Helper()
	conn := listen(t)
	s := &Server{Root: root, Timeout: 40 * time.Millisecond, MaxTimeout: 160 * time.Millisecond, Retries: 10}
	if configure != nil {
		configure(s)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, conn) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Errorf("Serve returned %v", err)
			}
		case <-time.After(5 * time.Second):
			t.Error("Serve did not return after cancel")
		}
	})
	return conn.LocalAddr().String()
}

func writeFile(t *testing.T, path string, data []byte) {
	t.Helper()
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func udpAddr(t *testing.T, s string) net.Addr {
	t.Helper()
	a, err := net.ResolveUDPAddr("udp", s)
	if err != nil {
		t.Fatal(err)
	}
	return a
}

func sendPacket(t *testing.T, c net.PacketConn, to net.Addr, p Packet) {
	t.Helper()
	if _, err := c.WriteTo(mustMarshal(t, p), to); err != nil {
		t.Fatal(err)
	}
}

// wantError reads one packet and checks that it is an ERROR with this code.
func wantError(t *testing.T, c net.PacketConn, code uint16) net.Addr {
	t.Helper()
	p, from := readPacket(t, c, time.Second)
	if e, ok := p.(ErrorPacket); !ok || e.Code != code {
		t.Fatalf("got %#v, want ERROR %d", p, code)
	}
	return from
}

func wantRemoteError(t *testing.T, err error, code uint16) {
	t.Helper()
	var re *RemoteError
	if !errors.As(err, &re) || re.Code != code {
		t.Fatalf("err = %v, want a RemoteError with code %d", err, code)
	}
}

func rootDir(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

// --- reading -----------------------------------------------------------

func TestServerGet(t *testing.T) {
	root := rootDir(t)
	for _, tt := range clientSizes {
		writeFile(t, filepath.Join(root, strconv.Itoa(tt.size)), testData(tt.size))
	}
	addr := startServer(t, root, nil)

	for _, tt := range clientSizes {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			if err := fastClient().Get(testCtx(t), addr, strconv.Itoa(tt.size), &out); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(out.Bytes(), testData(tt.size)) {
				t.Fatalf("got %d bytes, which differ from the %d in the file", out.Len(), tt.size)
			}
		})
	}
}

func TestServerGetFileNotFound(t *testing.T) {
	addr := startServer(t, rootDir(t), nil)
	err := fastClient().Get(testCtx(t), addr, "missing", &bytes.Buffer{})
	wantRemoteError(t, err, ErrFileNotFound)
}

func TestServerGetConcurrent(t *testing.T) {
	root := rootDir(t)
	const clients = 20
	for i := 0; i < clients; i++ {
		writeFile(t, filepath.Join(root, strconv.Itoa(i)), testData(500+i*300))
	}
	addr := startServer(t, root, nil)

	var wg sync.WaitGroup
	for i := 0; i < clients; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			var out bytes.Buffer
			if err := fastClient().Get(testCtx(t), addr, strconv.Itoa(i), &out); err != nil {
				t.Errorf("client %d: %v", i, err)
				return
			}
			if !bytes.Equal(out.Bytes(), testData(500+i*300)) {
				t.Errorf("client %d: wrong content", i)
			}
		}()
	}
	wg.Wait()
}

// --- files outside the root must stay out of reach ----------------------

// pathSetup builds   parent/secret.txt   and   parent/root/{ok.txt, sub/in.txt,
// links}, and returns both folders. The links point at the secret.
func pathSetup(t *testing.T) (parent, root string) {
	t.Helper()
	parent = t.TempDir()
	root = filepath.Join(parent, "root")
	if err := os.MkdirAll(filepath.Join(root, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(parent, "secret.txt"), []byte("TOP SECRET"))
	writeFile(t, filepath.Join(root, "ok.txt"), []byte("fine"))
	writeFile(t, filepath.Join(root, "sub", "in.txt"), []byte("inside"))
	for link, target := range map[string]string{
		"link":   "../secret.txt", // file outside the root
		"up":     "..",            // directory outside the root
		"inlink": "ok.txt",        // stays inside the root
	} {
		if err := os.Symlink(target, filepath.Join(root, link)); err != nil {
			t.Skipf("symbolic links are not available: %v", err)
		}
	}
	return parent, root
}

func TestServerGetPaths(t *testing.T) {
	parent, root := pathSetup(t)
	addr := startServer(t, root, nil)

	tests := []struct {
		name string
		file string
		want string // content; empty means the request must fail
	}{
		{"plain file", "ok.txt", "fine"},
		{"leading slash is relative to the root", "/ok.txt", "fine"},
		{"file in a subdirectory", "sub/in.txt", "inside"},
		{".. that stays inside the root", "sub/../ok.txt", "fine"},
		{"symbolic link inside the root", "inlink", "fine"},

		{"parent directory", "../secret.txt", ""},
		{"many parents", "../../../../../../../../etc/passwd", ""},
		{".. in the middle", "sub/../../secret.txt", ""},
		{"absolute path of the secret", filepath.Join(parent, "secret.txt"), ""},
		{"symbolic link to a file outside", "link", ""},
		{"symbolic link to a directory outside", "up/secret.txt", ""},
		{"backslashes are ordinary characters", `..\secret.txt`, ""},
		{"a directory", "sub", ""},
		{"the root itself", "/", ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var out bytes.Buffer
			err := fastClient().Get(testCtx(t), addr, tt.file, &out)
			if tt.want == "" {
				var re *RemoteError
				if !errors.As(err, &re) {
					t.Fatalf("err = %v, want the server to refuse", err)
				}
			} else if err != nil {
				t.Fatal(err)
			}
			if out.String() != tt.want {
				t.Fatalf("received %q, want %q", out.String(), tt.want)
			}
			if bytes.Contains(out.Bytes(), []byte("SECRET")) {
				t.Fatal("the secret leaked")
			}
		})
	}
}

// --- writing -----------------------------------------------------------

func TestServerPut(t *testing.T) {
	for _, tt := range clientSizes {
		t.Run(tt.name, func(t *testing.T) {
			root := rootDir(t)
			addr := startServer(t, root, func(s *Server) { s.AllowWrite = true })
			data := testData(tt.size)

			if err := fastClient().Put(testCtx(t), addr, "up.bin", bytes.NewReader(data)); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(root, "up.bin"))
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got, data) {
				t.Fatalf("file has %d bytes, want %d, or the content differs", len(got), len(data))
			}
		})
	}
}

func TestServerPutDeniedByDefault(t *testing.T) {
	root := rootDir(t)
	addr := startServer(t, root, nil)

	err := fastClient().Put(testCtx(t), addr, "up.bin", bytes.NewReader([]byte("x")))
	wantRemoteError(t, err, ErrAccessViolation)
	if _, err := os.Stat(filepath.Join(root, "up.bin")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("the file was created anyway: %v", err)
	}
}

func TestServerPutNeverOverwrites(t *testing.T) {
	root := rootDir(t)
	writeFile(t, filepath.Join(root, "old.txt"), []byte("original"))
	addr := startServer(t, root, func(s *Server) { s.AllowWrite = true })

	err := fastClient().Put(testCtx(t), addr, "old.txt", bytes.NewReader([]byte("new")))
	wantRemoteError(t, err, ErrFileExists)
	if got, _ := os.ReadFile(filepath.Join(root, "old.txt")); string(got) != "original" {
		t.Fatalf("the existing file was changed: %q", got)
	}
}

func TestServerPutPaths(t *testing.T) {
	parent, root := pathSetup(t)
	// A dangling link whose target lies outside the root.
	if err := os.Symlink("../victim.txt", filepath.Join(root, "dangling")); err != nil {
		t.Fatal(err)
	}
	addr := startServer(t, root, func(s *Server) { s.AllowWrite = true })

	tests := []struct {
		name string
		file string
		code uint16 // expected ERROR code; 0 = upload must succeed
		path string // where the data must (or must not) end up
	}{
		{"leading slash is relative to the root", "/new.txt", 0, filepath.Join(root, "new.txt")},
		{"subdirectory that exists", "sub/new.txt", 0, filepath.Join(root, "sub", "new.txt")},

		{"parent directory", "../evil.txt", ErrAccessViolation, filepath.Join(parent, "evil.txt")},
		{".. in the middle", "sub/../../evil.txt", ErrAccessViolation, filepath.Join(parent, "evil.txt")},
		{"dangling link to outside", "dangling", ErrFileExists, filepath.Join(parent, "victim.txt")},
		{"existing link to outside", "link", ErrFileExists, filepath.Join(parent, "secret.txt")},
		{"subdirectory that does not exist", "nope/new.txt", ErrFileNotFound, filepath.Join(root, "nope")},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := fastClient().Put(testCtx(t), addr, tt.file, bytes.NewReader([]byte("DATA")))
			if tt.code == 0 {
				if err != nil {
					t.Fatal(err)
				}
				if got, _ := os.ReadFile(tt.path); string(got) != "DATA" {
					t.Fatalf("%s holds %q", tt.path, got)
				}
				return
			}
			wantRemoteError(t, err, tt.code)
			if got, _ := os.ReadFile(tt.path); string(got) == "DATA" {
				t.Fatalf("data reached %s", tt.path)
			}
		})
	}
	if got, _ := os.ReadFile(filepath.Join(parent, "secret.txt")); string(got) != "TOP SECRET" {
		t.Fatalf("the secret was changed: %q", got)
	}
}

// An upload that breaks off must not leave a half-written file.
func TestServerPutAbortedIsRemoved(t *testing.T) {
	root := rootDir(t)
	addr := startServer(t, root, func(s *Server) { s.AllowWrite = true })
	server := udpAddr(t, addr)
	c := listen(t)

	sendPacket(t, c, server, Request{Op: OpWRQ, Filename: "half.bin", Mode: "octet"})
	p, tid := readPacket(t, c, time.Second)
	if a, ok := p.(Ack); !ok || a.Block != 0 {
		t.Fatalf("got %#v, want ACK 0", p)
	}
	sendPacket(t, c, tid, Data{Block: 1, Payload: bytes.Repeat([]byte{'x'}, 512)})
	if p, _ := readPacket(t, c, time.Second); p.Opcode() != OpAck {
		t.Fatalf("got %#v, want ACK 1", p)
	}
	if _, err := os.Stat(filepath.Join(root, "half.bin")); err != nil {
		t.Fatalf("the file should exist during the upload: %v", err)
	}
	sendPacket(t, c, tid, ErrorPacket{Code: ErrNotDefined, Msg: "changed my mind"})

	deadline := time.Now().Add(3 * time.Second)
	for {
		_, err := os.Stat(filepath.Join(root, "half.bin"))
		if errors.Is(err, os.ErrNotExist) {
			return
		}
		if time.Now().After(deadline) {
			t.Fatal("the half-written file is still there")
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// --- protocol details, seen with a raw socket ---------------------------

func TestServerModes(t *testing.T) {
	tests := []struct {
		name string
		op   Opcode
		mode string
	}{
		{"RRQ netascii", OpRRQ, "netascii"},
		{"RRQ mail", OpRRQ, "mail"},
		{"WRQ netascii", OpWRQ, "netascii"},
		{"WRQ mail", OpWRQ, "mail"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := rootDir(t)
			writeFile(t, filepath.Join(root, "f.txt"), []byte("hello"))
			addr := startServer(t, root, func(s *Server) { s.AllowWrite = true })
			c := listen(t)

			name := "f.txt"
			if tt.op == OpWRQ {
				name = "w.txt"
			}
			sendPacket(t, c, udpAddr(t, addr), Request{Op: tt.op, Filename: name, Mode: tt.mode})
			wantError(t, c, ErrIllegalOp)
			if _, err := os.Stat(filepath.Join(root, "w.txt")); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("a file was created for an unsupported mode: %v", err)
			}
		})
	}
}

// RFC 2347: a server that does not know the options answers as if they were
// not there.
func TestServerIgnoresOptions(t *testing.T) {
	root := rootDir(t)
	writeFile(t, filepath.Join(root, "f"), testData(1300))
	addr := startServer(t, root, nil)
	c := listen(t)

	sendPacket(t, c, udpAddr(t, addr), Request{Op: OpRRQ, Filename: "f", Mode: "octet",
		Options: map[string]string{"blksize": "1024", "tsize": "0"}})
	p, _ := readPacket(t, c, time.Second)
	d, ok := p.(Data)
	if !ok || d.Block != 1 || len(d.Payload) != 512 {
		t.Fatalf("got %#v, want DATA 1 with 512 bytes and no OACK", p)
	}
}

func TestServerAnswersFromNewPortAndChecksTID(t *testing.T) {
	root := rootDir(t)
	writeFile(t, filepath.Join(root, "f"), testData(1300))
	// Long timers: this test talks slowly, and must not see retransmissions.
	addr := startServer(t, root, func(s *Server) { s.Timeout, s.MaxTimeout = 2*time.Second, 2*time.Second })
	c, stranger := listen(t), listen(t)

	sendPacket(t, c, udpAddr(t, addr), Request{Op: OpRRQ, Filename: "f", Mode: "octet"})
	p, tid := readPacket(t, c, time.Second)
	if d, ok := p.(Data); !ok || d.Block != 1 {
		t.Fatalf("got %#v, want DATA 1", p)
	}
	if tid.String() == addr {
		t.Fatal("the answer came from the request port; it must come from a new one (the TID)")
	}

	// A stranger writes to the transfer's port: it gets ERROR 5 ...
	sendPacket(t, stranger, tid, Ack{Block: 1})
	wantError(t, stranger, ErrUnknownTID)
	// ... and the transfer goes on as if nothing happened.
	sendPacket(t, c, tid, Ack{Block: 1})
	p, _ = readPacket(t, c, time.Second)
	if d, ok := p.(Data); !ok || d.Block != 2 {
		t.Fatalf("got %#v, want DATA 2", p)
	}
}

func TestServerIgnoresJunkOnTheRequestPort(t *testing.T) {
	root := rootDir(t)
	writeFile(t, filepath.Join(root, "f"), []byte("hello"))
	addr := startServer(t, root, nil)
	server := udpAddr(t, addr)
	c := listen(t)

	for _, junk := range [][]byte{
		{1, 2, 3},
		mustMarshal(t, Data{Block: 1, Payload: []byte("x")}),
		mustMarshal(t, Ack{Block: 1}),
		mustMarshal(t, ErrorPacket{Code: ErrNotDefined, Msg: "x"}),
		[]byte("\x00\x01f\x00binary\x00"), // a request with an unknown mode
		[]byte("\x00\x06blksize\x00512\x00"),
	} {
		if _, err := c.WriteTo(junk, server); err != nil {
			t.Fatal(err)
		}
	}
	_ = c.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	if n, from, err := c.ReadFrom(make([]byte, 2048)); err == nil {
		t.Fatalf("the server answered junk with %d bytes from %v", n, from)
	}

	// And it still works.
	var out bytes.Buffer
	if err := fastClient().Get(testCtx(t), addr, "f", &out); err != nil || out.String() != "hello" {
		t.Fatalf("Get after junk: %q, %v", out.String(), err)
	}
}

// --- limits and shutdown -----------------------------------------------

func TestServerMaxTransfers(t *testing.T) {
	root := rootDir(t)
	writeFile(t, filepath.Join(root, "f"), testData(1300))
	addr := startServer(t, root, func(s *Server) {
		s.MaxTransfers = 1
		s.Timeout, s.MaxTimeout = 2*time.Second, 2*time.Second
	})
	server := udpAddr(t, addr)
	a, b := listen(t), listen(t)

	// The first client takes the only slot and keeps it by not acknowledging.
	sendPacket(t, a, server, Request{Op: OpRRQ, Filename: "f", Mode: "octet"})
	if p, _ := readPacket(t, a, time.Second); p.Opcode() != OpData {
		t.Fatalf("got %#v, want DATA 1", p)
	}

	sendPacket(t, b, server, Request{Op: OpRRQ, Filename: "f", Mode: "octet"})
	from := wantError(t, b, ErrNotDefined)
	if from.String() != addr {
		t.Fatalf("the refusal came from %v, want the request port %v", from, addr)
	}
}

func TestServerStopsWhileTransferIsRunning(t *testing.T) {
	root := rootDir(t)
	writeFile(t, filepath.Join(root, "f"), testData(1300))
	conn := listen(t)
	s := &Server{Root: root, Timeout: 5 * time.Second, MaxTimeout: 5 * time.Second}

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- s.Serve(ctx, conn) }()

	c := listen(t)
	sendPacket(t, c, conn.LocalAddr(), Request{Op: OpRRQ, Filename: "f", Mode: "octet"})
	readPacket(t, c, time.Second) // DATA 1; we never answer, so the transfer hangs

	start := time.Now()
	cancel()
	if err := waitErr(t, done); err != nil {
		t.Fatalf("Serve returned %v, want nil after cancel", err)
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("shutdown took %v", d)
	}
}

func TestServerBadRoot(t *testing.T) {
	s := &Server{Root: filepath.Join(t.TempDir(), "does-not-exist")}
	if err := s.Serve(context.Background(), listen(t)); err == nil {
		t.Fatal("expected an error for a missing root folder")
	}
}

func TestValidClient(t *testing.T) {
	tests := []struct {
		name string
		addr net.Addr
		want bool
	}{
		{"normal", &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 4000}, true},
		{"IPv6", &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 4000}, true},
		{"port 0", &net.UDPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 0}, false},
		{"unspecified", &net.UDPAddr{IP: net.IPv4zero, Port: 4000}, false},
		{"multicast", &net.UDPAddr{IP: net.IPv4(224, 0, 0, 1), Port: 4000}, false},
		{"broadcast", &net.UDPAddr{IP: net.IPv4bcast, Port: 4000}, false},
		{"not a UDP address", &net.TCPAddr{IP: net.IPv4(192, 0, 2, 1), Port: 4000}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := validClient(tt.addr); got != tt.want {
				t.Errorf("validClient(%v) = %v, want %v", tt.addr, got, tt.want)
			}
		})
	}
}
