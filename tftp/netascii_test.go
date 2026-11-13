package tftp

import (
	"bytes"
	"errors"
	"io"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/iotest"
	"time"
)

// These tests use helpers from transfer_test.go (listen, wantData, wantAck,
// readPacket), server_test.go (startServer, writeFile, rootDir, sendPacket,
// udpAddr) and client_test.go (fastClient, testCtx, failWriter).
//
// The conversion code takes the host's line end as a parameter, so the tests
// run it for a Unix host and for a Windows host on every platform.

// --- helpers -----------------------------------------------------------

var hosts = []struct {
	name string
	end  lineEnd
}{
	{"LF host", lineLF},
	{"CRLF host", lineCRLF},
}

func encode(t testing.TB, file string, host lineEnd) string {
	t.Helper()
	got, err := io.ReadAll(newNetasciiReader(strings.NewReader(file), host))
	if err != nil {
		t.Fatalf("encode %q: %v", file, err)
	}
	return string(got)
}

// decode feeds wire to a decoder in pieces of chunk bytes (0 means all at
// once) and returns what it wrote.
func decode(t testing.TB, wire string, host lineEnd, chunk int) string {
	t.Helper()
	var out bytes.Buffer
	w := newNetasciiWriter(&out, host)
	b := []byte(wire)
	for len(b) > 0 {
		n := len(b)
		if chunk > 0 && chunk < n {
			n = chunk
		}
		if m, err := w.Write(b[:n]); err != nil || m != n {
			t.Fatalf("Write(%q) = %d, %v", b[:n], m, err)
		}
		b = b[n:]
	}
	if err := w.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	return out.String()
}

// normalizeCRLF is what a CR LF host should get back from its own text: every
// line end becomes CR LF and a CR that does not start one is left alone.
func normalizeCRLF(s string) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		switch {
		case s[i] == '\n':
			b.WriteString("\r\n")
		case s[i] == '\r' && i+1 < len(s) && s[i+1] == '\n':
			b.WriteString("\r\n")
			i++
		default:
			b.WriteByte(s[i])
		}
	}
	return b.String()
}

// checkWire fails if wire breaks the netascii rules: every CR must be
// followed by LF or NUL, and every LF must follow a CR.
func checkWire(t testing.TB, wire string) {
	t.Helper()
	for i := 0; i < len(wire); i++ {
		switch wire[i] {
		case '\r':
			if i+1 >= len(wire) || (wire[i+1] != '\n' && wire[i+1] != 0) {
				t.Fatalf("wire %q: CR at %d is not followed by LF or NUL", wire, i)
			}
		case '\n':
			if i == 0 || wire[i-1] != '\r' {
				t.Fatalf("wire %q: LF at %d does not follow a CR", wire, i)
			}
		}
	}
}

// --- encoder -----------------------------------------------------------

func TestNetasciiEncode(t *testing.T) {
	tests := []struct {
		name string
		host lineEnd
		file string
		wire string
	}{
		{"LF: empty", lineLF, "", ""},
		{"LF: plain text", lineLF, "abc", "abc"},
		{"LF: line ends", lineLF, "a\nb\n", "a\r\nb\r\n"},
		{"LF: lone CR", lineLF, "a\rb", "a\r\x00b"},
		{"LF: CR LF in the file", lineLF, "a\r\nb", "a\r\x00\r\nb"},
		{"LF: CR at the end", lineLF, "a\r", "a\r\x00"},
		{"LF: bytes above 0x7F and NUL pass", lineLF, "\x80\xff\x00", "\x80\xff\x00"},
		{"LF: example from the docs", lineLF, "a\nb\r\n", "a\r\nb\r\x00\r\n"},

		{"CRLF: line ends", lineCRLF, "a\r\nb\r\n", "a\r\nb\r\n"},
		{"CRLF: lone LF is a line end too", lineCRLF, "a\nb", "a\r\nb"},
		{"CRLF: lone CR", lineCRLF, "a\rb", "a\r\x00b"},
		{"CRLF: CR at the end", lineCRLF, "a\r", "a\r\x00"},
		{"CRLF: CR CR LF", lineCRLF, "a\r\r\nb", "a\r\x00\r\nb"},
		{"CRLF: two CRs at the end", lineCRLF, "\r\r", "\r\x00\r\x00"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := encode(t, tt.file, tt.host); got != tt.wire {
				t.Errorf("encode(%q) = %q, want %q", tt.file, got, tt.wire)
			}
			checkWire(t, tt.wire)
		})
	}
}

// The encoder must give the same bytes however the caller reads and however
// the source delivers, including a source that returns the last data together
// with io.EOF.
func TestNetasciiEncodeReadPatterns(t *testing.T) {
	file := "one\ntwo\r\nthree\r\rfour\r" + strings.Repeat("x\n", 700)
	for _, h := range hosts {
		t.Run(h.name, func(t *testing.T) {
			want := encode(t, file, h.end)
			sources := map[string]func() io.Reader{
				"whole":         func() io.Reader { return strings.NewReader(file) },
				"one byte":      func() io.Reader { return iotest.OneByteReader(strings.NewReader(file)) },
				"data with EOF": func() io.Reader { return iotest.DataErrReader(strings.NewReader(file)) },
				"half":          func() io.Reader { return iotest.HalfReader(strings.NewReader(file)) },
			}
			for name, src := range sources {
				// TestReader reads the result with many different buffer sizes.
				if err := iotest.TestReader(newNetasciiReader(src(), h.end), []byte(want)); err != nil {
					t.Errorf("%s: %v", name, err)
				}
			}
		})
	}
}

func TestNetasciiEncodeKeepsSourceError(t *testing.T) {
	boom := errors.New("disk on fire")
	src := io.MultiReader(strings.NewReader("a\n"), iotest.ErrReader(boom))
	got, err := io.ReadAll(newNetasciiReader(src, lineLF))
	if string(got) != "a\r\n" || !errors.Is(err, boom) {
		t.Fatalf("got %q, %v; want the data read so far and the source's error", got, err)
	}
}

// --- decoder -----------------------------------------------------------

func TestNetasciiDecode(t *testing.T) {
	tests := []struct {
		name string
		host lineEnd
		wire string
		file string
	}{
		{"LF: line ends", lineLF, "a\r\nb\r\n", "a\nb\n"},
		{"LF: CR NUL is a CR", lineLF, "a\r\x00b", "a\rb"},
		{"LF: CR NUL then CR LF", lineLF, "a\r\x00\r\nb", "a\r\nb"},
		{"LF: lone LF is kept", lineLF, "a\nb", "a\nb"},
		{"LF: CR and another byte is kept", lineLF, "a\rb", "a\rb"},
		{"LF: CR CR LF", lineLF, "\r\r\n", "\r\n"},
		{"LF: CR at the end is kept", lineLF, "a\r", "a\r"},

		{"CRLF: line ends", lineCRLF, "a\r\nb", "a\r\nb"},
		{"CRLF: CR NUL is a CR", lineCRLF, "a\r\x00b", "a\rb"},
		{"CRLF: lone LF is kept", lineCRLF, "a\nb", "a\nb"},
		{"CRLF: CR at the end is kept", lineCRLF, "a\r", "a\r"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The result must not depend on how the wire bytes are split
			// into Write calls: a CR can end one DATA block, and the LF or
			// NUL that decides its meaning start the next.
			for _, chunk := range []int{0, 1, 2, 3} {
				if got := decode(t, tt.wire, tt.host, chunk); got != tt.file {
					t.Errorf("chunk %d: decode(%q) = %q, want %q", chunk, tt.wire, got, tt.file)
				}
			}
		})
	}
}

func TestNetasciiDecodeSplitAtEveryPoint(t *testing.T) {
	wire := "a\r\nb\r\x00c\r\n\r\x00\r\nd\r"
	for _, h := range hosts {
		want := decode(t, wire, h.end, 0)
		for i := 0; i <= len(wire); i++ {
			var out bytes.Buffer
			w := newNetasciiWriter(&out, h.end)
			_, _ = w.Write([]byte(wire[:i]))
			_, _ = w.Write([]byte(wire[i:]))
			_ = w.Close()
			if out.String() != want {
				t.Fatalf("%s, split at %d: got %q, want %q", h.name, i, out.String(), want)
			}
		}
	}
}

func TestNetasciiWriterErrors(t *testing.T) {
	boom := errors.New("disk full")

	w := newNetasciiWriter(failWriter{boom}, lineLF)
	if _, err := w.Write([]byte("abc")); !errors.Is(err, boom) {
		t.Errorf("Write: err = %v, want the writer's error", err)
	}

	// A CR still waiting at the end is written by Close, and that can fail.
	w = newNetasciiWriter(failWriter{boom}, lineLF)
	if _, err := w.Write([]byte("\r")); err != nil {
		t.Fatalf("a lone CR is only remembered, got %v", err)
	}
	if err := w.Close(); !errors.Is(err, boom) {
		t.Errorf("Close: err = %v, want the writer's error", err)
	}

	// With nothing waiting, Close writes nothing and cannot fail.
	if err := newNetasciiWriter(failWriter{boom}, lineLF).Close(); err != nil {
		t.Errorf("Close with nothing pending: %v", err)
	}
}

// --- properties --------------------------------------------------------

// Whatever the file holds, the wire form obeys the netascii rules, and a host
// gets its own text back: a LF host exactly, a CR LF host with every line end
// in CR LF form.
func TestNetasciiRoundTripRandom(t *testing.T) {
	alphabet := []byte{'a', 'b', ' ', '\r', '\n', '\r', '\n', 0, 0x80, 0xff}
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 3000; i++ {
		b := make([]byte, rng.IntN(40))
		for j := range b {
			b[j] = alphabet[rng.IntN(len(alphabet))]
		}
		file := string(b)
		for _, h := range hosts {
			wire := encode(t, file, h.end)
			checkWire(t, wire)

			want := file
			if h.end == lineCRLF {
				want = normalizeCRLF(file)
			}
			for _, chunk := range []int{0, 1, 7} {
				if got := decode(t, wire, h.end, chunk); got != want {
					t.Fatalf("%s: %q -> %q -> %q (chunk %d), want %q", h.name, file, wire, got, chunk, want)
				}
			}
		}
	}
}

func FuzzNetascii(f *testing.F) {
	for _, s := range []string{"", "a\nb\r\n", "\r", "\r\r\n", "\n\r", "x\x00\r\x00", "\r\n\r\n"} {
		f.Add([]byte(s))
	}
	f.Fuzz(func(t *testing.T, data []byte) {
		file := string(data)
		for _, h := range hosts {
			wire := encode(t, file, h.end)
			checkWire(t, wire)
			want := file
			if h.end == lineCRLF {
				want = normalizeCRLF(file)
			}
			if got := decode(t, wire, h.end, 0); got != want {
				t.Fatalf("%s: %q -> %q -> %q, want %q", h.name, file, wire, got, want)
			}
			// Any bytes may arrive from the network. The decoder must not
			// care how they are split into DATA blocks.
			if a, b := decode(t, file, h.end, 0), decode(t, file, h.end, 1); a != b {
				t.Fatalf("%s: decoding %q whole gives %q, byte by byte %q", h.name, file, a, b)
			}
		}
	})
}

// --- through the server and client -------------------------------------

// Netascii on the wire, seen with a raw socket: the server turns the line
// ends of its file into CR LF, and the length of a DATA block counts the bytes
// on the wire, not the bytes of the file.
func TestNetasciiServerSendsWireForm(t *testing.T) {
	x := func(n int) string { return strings.Repeat("x", n) }
	tests := []struct {
		name   string
		file   string
		blocks []string // payload of DATA 1, 2, ...
	}{
		{"line ends", "line1\nline2\n", []string{"line1\r\nline2\r\n"}},
		{"CR ends the first block", x(511) + "\ny", []string{x(511) + "\r", "\ny"}},
		{"wire length is exactly one block", x(510) + "\n", []string{x(510) + "\r\n", ""}},
		{"one byte short of a block", x(509) + "\n", []string{x(509) + "\r\n"}},
		{"lone CR", "a\rb", []string{"a\r\x00b"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := rootDir(t)
			writeFile(t, filepath.Join(root, "f.txt"), []byte(tt.file))
			addr := startServer(t, root, nil)
			c := listen(t)

			sendPacket(t, c, udpAddr(t, addr), Request{Op: OpRRQ, Filename: "f.txt", Mode: "netascii"})
			for i, want := range tt.blocks {
				d, from := wantData(t, c, uint16(i+1))
				if string(d.Payload) != want {
					t.Fatalf("DATA %d = %q, want %q", i+1, d.Payload, want)
				}
				sendPacket(t, c, from, Ack{Block: d.Block})
			}
		})
	}
}

// waitForFile polls until the file holds want. The server finishes a write
// (and writes a CR it was still holding) a moment after the last ACK.
func waitForFile(t *testing.T, path, want string) {
	t.Helper()
	var got []byte
	for deadline := time.Now().Add(3 * time.Second); time.Now().Before(deadline); time.Sleep(10 * time.Millisecond) {
		got, _ = os.ReadFile(path)
		if string(got) == want {
			return
		}
	}
	t.Fatalf("file holds %q, want %q", got, want)
}

// The server decodes an upload, also when a CR ends one block and the LF or
// NUL that explains it starts the next one.
func TestNetasciiServerReceivesWireForm(t *testing.T) {
	nl := string(nativeLineEnd())
	x := func(n int) string { return strings.Repeat("x", n) }
	tests := []struct {
		name   string
		blocks []string // payload of DATA 1, 2, ... as the client sends it
		file   string   // what must end up on disk
	}{
		{"line ends and a lone CR", []string{"a\r\nb\r\x00c"}, "a" + nl + "b\rc"},
		{"CR LF across two blocks", []string{x(511) + "\r", "\nz"}, x(511) + nl + "z"},
		{"CR NUL across two blocks", []string{x(511) + "\r", "\x00z"}, x(511) + "\rz"},
		{"CR at the very end", []string{"ab\r"}, "ab\r"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := rootDir(t)
			addr := startServer(t, root, func(s *Server) { s.AllowWrite = true })
			c := listen(t)

			sendPacket(t, c, udpAddr(t, addr), Request{Op: OpWRQ, Filename: "up.txt", Mode: "netascii"})
			p, from := readPacket(t, c, time.Second)
			if a, ok := p.(Ack); !ok || a.Block != 0 {
				t.Fatalf("got %#v, want ACK 0", p)
			}
			for i, payload := range tt.blocks {
				sendPacket(t, c, from, Data{Block: uint16(i + 1), Payload: []byte(payload)})
				wantAck(t, c, uint16(i+1))
			}
			waitForFile(t, filepath.Join(root, "up.txt"), tt.file)
		})
	}
}

// Our client and our server agree: text goes there and back unchanged.
func TestNetasciiClientRoundTrip(t *testing.T) {
	nl := string(nativeLineEnd())
	x := func(n int) string { return strings.Repeat("x", n) }
	texts := []struct{ name, text string }{
		{"empty", ""},
		{"no line end", "hello"},
		{"many blocks", strings.Repeat("some text"+nl, 400)},
		{"line end on a block boundary", x(511) + nl + "y"},
		{"wire length is a multiple of a block", x(510) + nl},
		{"lone CR", "a\rb"},
		{"ends with CR", "abc\r"},
		{"UTF-8", "zażółć gęślą jaźń" + nl},
	}
	for _, tt := range texts {
		t.Run(tt.name, func(t *testing.T) {
			root := rootDir(t)
			addr := startServer(t, root, func(s *Server) { s.AllowWrite = true })
			c := fastClient()
			c.Mode = "NetAscii" // the mode name is not case sensitive

			if err := c.Put(testCtx(t), addr, "t.txt", strings.NewReader(tt.text)); err != nil {
				t.Fatalf("Put: %v", err)
			}
			waitForFile(t, filepath.Join(root, "t.txt"), tt.text)

			var got bytes.Buffer
			if err := c.Get(testCtx(t), addr, "t.txt", &got); err != nil {
				t.Fatalf("Get: %v", err)
			}
			if got.String() != tt.text {
				t.Fatalf("Get returned %q, want %q", got.String(), tt.text)
			}
		})
	}
}

func TestClientRejectsUnknownMode(t *testing.T) {
	for _, mode := range []string{"mail", "binary", "octet "} {
		c := &Client{Mode: mode}
		if err := c.Get(testCtx(t), "127.0.0.1:69", "f", &bytes.Buffer{}); err == nil {
			t.Errorf("Get with mode %q: expected an error", mode)
		}
		if err := c.Put(testCtx(t), "127.0.0.1:69", "f", strings.NewReader("")); err == nil {
			t.Errorf("Put with mode %q: expected an error", mode)
		}
	}
}

func TestClientGetNetasciiWriterError(t *testing.T) {
	root := rootDir(t)
	writeFile(t, filepath.Join(root, "f"), []byte(strings.Repeat("line\n", 300)))
	addr := startServer(t, root, nil)
	boom := errors.New("disk on fire")

	c := fastClient()
	c.Mode = ModeNetascii
	if err := c.Get(testCtx(t), addr, "f", failWriter{boom}); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want it to wrap the writer's error", err)
	}
}
