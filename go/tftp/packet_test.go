package tftp

import (
	"bytes"
	"maps"
	"testing"
)

func TestParseOpcode(t *testing.T) {
	// A table of test cases: each row is one input and what we expect back.
	tests := []struct {
		name    string // label shown in the test output
		input   []byte // the raw bytes we feed to ParseOpcode
		want    Opcode // the opcode we expect to get back
		wantErr bool   // true if we expect an error
	}{
		{"empty", []byte{}, 0, true},            // 0 bytes: too short, must be an error
		{"one byte", []byte{0}, 0, true},        // 1 byte: opcode needs 2, must be an error
		{"data opcode", []byte{0, 3}, 3, false}, // bytes 00 03 = number 3 (DATA), no error
	}

	// Loop over the rows; "_" means we ignore the row index.
	for _, tt := range tests {
		// Run each row as its own named subtest (e.g. TestParseOpcode/one_byte).
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseOpcode(tt.input) // call the function under test

			// Compare "did we get an error?" with "did we expect one?".
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr) // stop this subtest
			}

			// Compare the opcode we got with the one we expected.
			if got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want) // report the mismatch
			}
		})
	}
}

func TestParseAck(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		want    Ack
		wantErr bool
	}{
		{"block 7", []byte{0, 4, 0, 7}, Ack{Block: 7}, false},               // normal ACK
		{"block 0", []byte{0, 4, 0, 0}, Ack{Block: 0}, false},               // valid: first ACK of a WRQ
		{"block 65535", []byte{0, 4, 0xFF, 0xFF}, Ack{Block: 65535}, false}, // biggest 16-bit number
		{"too short", []byte{0, 4, 0}, Ack{}, true},                         // 3 bytes, block number is cut off
		{"wrong opcode", []byte{0, 3, 0, 7}, Ack{}, true},                   // this is DATA, not ACK
		{"too long", []byte{0, 4, 0, 7, 9}, Ack{}, true},                    // extra byte at the end
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseAck(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestAckMarshal(t *testing.T) {
	got, err := Ack{Block: 7}.MarshalBinary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0, 4, 0, 7} // opcode 4 (ACK), block 7
	if !bytes.Equal(got, want) {
		t.Errorf("got % x, want % x", got, want)
	}
}

func TestParseData(t *testing.T) {
	full := append([]byte{0, 3, 0, 1}, make([]byte, 512)...)   // header + 512 bytes
	tooBig := append([]byte{0, 3, 0, 1}, make([]byte, 513)...) // header + 513 bytes

	tests := []struct {
		name        string
		input       []byte
		wantBlock   uint16
		wantPayload []byte
		wantErr     bool
	}{
		{"3 bytes", []byte{0, 3, 0, 1, 'a', 'b', 'c'}, 1, []byte("abc"), false},
		{"empty payload", []byte{0, 3, 0, 5}, 5, nil, false}, // valid: last block of a 512*N file
		{"full 512", full, 1, make([]byte, 512), false},
		{"513 too big", tooBig, 0, nil, true},
		{"too short", []byte{0, 3, 0}, 0, nil, true},
		{"wrong opcode", []byte{0, 4, 0, 1, 'a'}, 0, nil, true}, // this is ACK, not DATA
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseData(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got.Block != tt.wantBlock {
				t.Errorf("block = %d, want %d", got.Block, tt.wantBlock)
			}
			if !bytes.Equal(got.Payload, tt.wantPayload) {
				t.Errorf("payload mismatch")
			}
		})
	}
}

func TestDataMarshal(t *testing.T) {
	got, err := Data{Block: 2, Payload: []byte("hi")}.MarshalBinary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := []byte{0, 3, 0, 2, 'h', 'i'} // opcode 3, block 2, "hi"
	if !bytes.Equal(got, want) {
		t.Errorf("got % x, want % x", got, want)
	}
}

// We must never send a packet that our own parser would reject.
func TestDataMarshalTooBig(t *testing.T) {
	_, err := Data{Block: 1, Payload: make([]byte, 513)}.MarshalBinary()
	if err == nil {
		t.Fatal("expected an error for a 513-byte payload")
	}
}

func TestDataRoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 511, 512} {
		in := Data{Block: 300, Payload: bytes.Repeat([]byte{0xAB}, n)}
		b, err := in.MarshalBinary()
		if err != nil {
			t.Fatalf("n=%d: marshal: %v", n, err)
		}
		out, err := ParseData(b)
		if err != nil {
			t.Fatalf("n=%d: parse: %v", n, err)
		}
		if out.Block != in.Block || !bytes.Equal(out.Payload, in.Payload) {
			t.Errorf("n=%d: round trip mismatch", n)
		}
	}
}

// The encoded bytes must not share memory with the payload.
func TestDataMarshalCopiesPayload(t *testing.T) {
	payload := []byte("abc")
	b, err := Data{Block: 1, Payload: payload}.MarshalBinary()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	payload[0] = 'X' // change the original after marshaling
	if b[4] != 'a' {
		t.Errorf("marshaled bytes changed together with the payload: got %q", b[4])
	}
}

func TestAckRoundTrip(t *testing.T) {
	for _, block := range []uint16{0, 1, 7, 255, 256, 65535} {
		in := Ack{Block: block}
		b, err := in.MarshalBinary()
		if err != nil {
			t.Fatalf("block %d: marshal: %v", block, err)
		}
		out, err := ParseAck(b)
		if err != nil {
			t.Fatalf("block %d: parse: %v", block, err)
		}
		if out != in {
			t.Errorf("block %d: got %+v, want %+v", block, out, in)
		}
	}
}

func TestParseError(t *testing.T) {
	longMsg := bytes.Repeat([]byte{'x'}, 512)
	tooLong := append(append([]byte{0, 5, 0, 1}, longMsg...), 0) // 517 bytes

	tests := []struct {
		name    string
		input   []byte
		want    ErrorPacket
		wantErr bool
	}{
		{"file not found", []byte{0, 5, 0, 1, 'n', 'o', 0}, ErrorPacket{1, "no"}, false},
		{"empty message", []byte{0, 5, 0, 2, 0}, ErrorPacket{2, ""}, false},
		{"missing terminator", []byte{0, 5, 0, 1, 'n', 'o'}, ErrorPacket{}, true},
		{"too short", []byte{0, 5, 0, 1}, ErrorPacket{}, true},
		{"zero inside message", []byte{0, 5, 0, 1, 'a', 0, 'b', 0}, ErrorPacket{}, true},
		{"wrong opcode", []byte{0, 4, 0, 1, 'a', 0}, ErrorPacket{}, true},
		{"too long", tooLong, ErrorPacket{}, true},
		{"option negotiation", []byte{0, 5, 0, 8, 'x', 0}, ErrorPacket{8, "x"}, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseError(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if got != tt.want {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestErrorMarshal(t *testing.T) {
	long511 := string(bytes.Repeat([]byte{'x'}, 511))
	long512 := string(bytes.Repeat([]byte{'x'}, 512))

	tests := []struct {
		name    string
		in      ErrorPacket
		want    []byte
		wantErr bool
	}{
		{"file not found", ErrorPacket{1, "no"}, []byte{0, 5, 0, 1, 'n', 'o', 0}, false},
		{"empty message", ErrorPacket{2, ""}, []byte{0, 5, 0, 2, 0}, false},
		{"code 256", ErrorPacket{256, "x"}, []byte{0, 5, 1, 0, 'x', 0}, false}, // byte order check
		{"511 chars is the max", ErrorPacket{1, long511},
			append(append([]byte{0, 5, 0, 1}, []byte(long511)...), 0), false},
		{"512 chars too long", ErrorPacket{1, long512}, nil, true},
		{"zero inside message", ErrorPacket{1, "a\x00b"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.MarshalBinary()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("got % x, want % x", got, tt.want)
			}
		})
	}
}

// Round trip: ParseError(MarshalBinary(x)) must give back x.
func TestErrorRoundTrip(t *testing.T) {
	for _, in := range []ErrorPacket{{1, "no"}, {0, ""}, {7, "no such user"}, {65535, "x"}} {
		b, err := in.MarshalBinary()
		if err != nil {
			t.Fatalf("%+v: marshal: %v", in, err)
		}
		out, err := ParseError(b)
		if err != nil {
			t.Fatalf("%+v: parse: %v", in, err)
		}
		if out != in {
			t.Errorf("got %+v, want %+v", out, in)
		}
	}
}

func TestParseRequestDuplicateOption(t *testing.T) {
	in := []byte("\x00\x01f\x00octet\x00blksize\x00512\x00BLKSIZE\x001024\x00")
	if _, err := ParseRequest(in); err == nil {
		t.Fatal("expected an error for a duplicated option")
	}
}

func TestRequestMarshal(t *testing.T) {
	long := Request{Op: OpRRQ, Filename: string(bytes.Repeat([]byte{'x'}, 511)), Mode: "octet"} // 520 bytes
	many := map[string]string{}
	for i := 0; i < MaxOptions+1; i++ {
		many[string(rune('a'+i))] = "1"
	}

	name503 := string(bytes.Repeat([]byte{'x'}, 503))
	name504 := string(bytes.Repeat([]byte{'x'}, 504))

	tests := []struct {
		name    string
		in      Request
		want    []byte
		wantErr bool
	}{
		{"RRQ", Request{Op: OpRRQ, Filename: "file", Mode: "octet"}, []byte("\x00\x01file\x00octet\x00"), false},
		{"mode is lower-cased", Request{Op: OpWRQ, Filename: "a", Mode: "OCTET"}, []byte("\x00\x02a\x00octet\x00"), false},
		{"options sorted by name", Request{Op: OpRRQ, Filename: "f", Mode: "octet",
			Options: map[string]string{"tsize": "0", "blksize": "1024"}},
			[]byte("\x00\x01f\x00octet\x00blksize\x001024\x00tsize\x000\x00"), false},
		{"bad opcode", Request{Op: OpAck, Filename: "f", Mode: "octet"}, nil, true},
		{"empty filename", Request{Op: OpRRQ, Mode: "octet"}, nil, true},
		{"zero in filename", Request{Op: OpRRQ, Filename: "a\x00b", Mode: "octet"}, nil, true},
		{"bad mode", Request{Op: OpRRQ, Filename: "f", Mode: "binary"}, nil, true},
		{"empty option name", Request{Op: OpRRQ, Filename: "f", Mode: "octet", Options: map[string]string{"": "1"}}, nil, true},
		{"upper-case option name", Request{Op: OpRRQ, Filename: "f", Mode: "octet", Options: map[string]string{"BlkSize": "1"}}, nil, true},
		{"zero in option value", Request{Op: OpRRQ, Filename: "f", Mode: "octet", Options: map[string]string{"a": "1\x002"}}, nil, true},
		{"too many options", Request{Op: OpRRQ, Filename: "f", Mode: "octet", Options: many}, nil, true},
		{"too long", long, nil, true},
		{"512 bytes is the max", Request{Op: OpRRQ, Filename: name503, Mode: "octet"}, []byte("\x00\x01" + name503 + "\x00octet\x00"), false},
		{"513 bytes too long", Request{Op: OpRRQ, Filename: name504, Mode: "octet"}, nil, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := tt.in.MarshalBinary()
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if !bytes.Equal(got, tt.want) {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRequestRoundTrip(t *testing.T) {
	for _, in := range []Request{
		{Op: OpRRQ, Filename: "file", Mode: "octet"},
		{Op: OpWRQ, Filename: "dir/a.txt", Mode: "netascii"},
		{Op: OpRRQ, Filename: "f", Mode: "mail"},
		{Op: OpRRQ, Filename: "f", Mode: "octet", Options: map[string]string{"blksize": "1428", "tsize": "0"}},
	} {
		b, err := in.MarshalBinary()
		if err != nil {
			t.Fatalf("%+v: marshal: %v", in, err)
		}
		out, err := ParseRequest(b)
		if err != nil {
			t.Fatalf("%+v: parse: %v", in, err)
		}
		if out.Op != in.Op || out.Filename != in.Filename || out.Mode != in.Mode ||
			!maps.Equal(out.Options, in.Options) {
			t.Errorf("got %+v, want %+v", out, in)
		}
	}
}

func TestParse(t *testing.T) {
	tests := []struct {
		name    string
		input   []byte
		wantOp  Opcode
		wantErr bool
	}{
		{"RRQ", []byte("\x00\x01f\x00octet\x00"), OpRRQ, false},
		{"WRQ", []byte("\x00\x02f\x00octet\x00"), OpWRQ, false},
		{"DATA", []byte{0, 3, 0, 1, 'a'}, OpData, false},
		{"ACK", []byte{0, 4, 0, 1}, OpAck, false},
		{"ERROR", []byte{0, 5, 0, 1, 'x', 0}, OpError, false},
		{"opcode 0", []byte{0, 0, 0, 0}, 0, true},
		{"opcode 6 (OACK, not supported yet)", []byte{0, 6, 0, 0}, 0, true},
		{"empty", nil, 0, true},
		{"truncated ACK", []byte{0, 4, 0}, 0, true},
		{"bad RRQ", []byte("\x00\x01f"), 0, true},
		{"bad DATA", []byte{0, 3, 0}, 0, true},
		{"bad ERROR", []byte{0, 5, 0, 1}, 0, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p, err := Parse(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			if err != nil {
				if p != nil { // guards against the "non-nil interface holding a zero value" trap
					t.Errorf("want nil Packet on error, got %#v", p)
				}
				return
			}
			if p.Opcode() != tt.wantOp {
				t.Errorf("opcode = %d, want %d", p.Opcode(), tt.wantOp)
			}
		})
	}
}

func TestParseRequest(t *testing.T) {
	tooLong := append([]byte("\x00\x01"), bytes.Repeat([]byte{'x'}, 510)...)
	tooLong = append(tooLong, []byte("\x00octet\x00")...) // 519 bytes
	exact := append([]byte("\x00\x01"), bytes.Repeat([]byte{'x'}, 503)...)
	exact = append(exact, []byte("\x00octet\x00")...) // 2+503+1+5+1 = 512 bytes
	over := append([]byte("\x00\x01"), bytes.Repeat([]byte{'x'}, 504)...)
	over = append(over, []byte("\x00octet\x00")...) // 513 bytes

	tooMany := []byte("\x00\x01f\x00octet\x00")
	for i := 0; i < MaxOptions+1; i++ {
		tooMany = append(tooMany, 'a'+byte(i), 0, '1', 0) // options "a","b",... = "1"
	}

	tests := []struct {
		name    string
		input   []byte
		want    Request
		wantErr bool
	}{
		{"RRQ octet", []byte("\x00\x01file\x00octet\x00"), Request{OpRRQ, "file", "octet", nil}, false},
		{"WRQ upper-case mode", []byte("\x00\x02a.txt\x00OCTET\x00"), Request{OpWRQ, "a.txt", "octet", nil}, false},
		{"mixed-case netascii", []byte("\x00\x01f\x00NetAscii\x00"), Request{OpRRQ, "f", "netascii", nil}, false},
		{"mail parses", []byte("\x00\x01f\x00mail\x00"), Request{OpRRQ, "f", "mail", nil}, false},
		{"blksize option", []byte("\x00\x01f\x00octet\x00BlkSize\x001428\x00"),
			Request{OpRRQ, "f", "octet", map[string]string{"blksize": "1428"}}, false},
		{"two options", []byte("\x00\x01f\x00octet\x00tsize\x000\x00blksize\x001024\x00"),
			Request{OpRRQ, "f", "octet", map[string]string{"tsize": "0", "blksize": "1024"}}, false},
		{"empty filename", []byte("\x00\x01\x00octet\x00"), Request{}, true},
		{"unknown mode", []byte("\x00\x01f\x00binary\x00"), Request{}, true},
		{"no final zero", []byte("\x00\x01f\x00octet"), Request{}, true},
		{"no zeros at all", []byte("\x00\x01file"), Request{}, true},
		{"option without value", []byte("\x00\x01f\x00octet\x00blksize\x00"), Request{}, true},
		{"empty option name", []byte("\x00\x01f\x00octet\x00\x001\x00"), Request{}, true},
		{"too many options", tooMany, Request{}, true},
		{"wrong opcode", []byte("\x00\x04f\x00octet\x00"), Request{}, true},
		{"too long", tooLong, Request{}, true},
		{"too short", []byte("\x00\x01f\x00o"), Request{}, true},
		{"512 bytes is the max", exact, Request{OpRRQ, string(bytes.Repeat([]byte{'x'}, 503)), "octet", nil}, false},
		{"513 bytes too long", over, Request{}, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := ParseRequest(tt.input)
			if (err != nil) != tt.wantErr {
				t.Fatalf("err = %v, wantErr = %v", err, tt.wantErr)
			}
			// A struct with a map cannot be compared with ==, so compare field by field.
			if got.Op != tt.want.Op || got.Filename != tt.want.Filename ||
				got.Mode != tt.want.Mode || !maps.Equal(got.Options, tt.want.Options) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

// ParseData must copy the payload: the receive buffer gets reused.
func TestParseDataCopiesPayload(t *testing.T) {
	buf := []byte{0, 3, 0, 1, 'a', 'b', 'c'}
	d, err := ParseData(buf)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	buf[4] = 'X' // simulate the next packet overwriting the buffer
	if string(d.Payload) != "abc" {
		t.Errorf("payload changed together with the buffer: got %q", d.Payload)
	}
}

// Only A-Z are lower-cased. strings.ToLower would also fold some non-ASCII
// characters (e.g. the Kelvin sign U+212A turns into "k").
func TestLowerASCII(t *testing.T) {
	tests := []struct{ in, want string }{
		{"OcTeT", "octet"},
		{"netascii", "netascii"},
		{"BLKSIZE", "blksize"},
		{"\u212A", "\u212A"},
		{"", ""},
	}
	for _, tt := range tests {
		if got := lowerASCII(tt.in); got != tt.want {
			t.Errorf("lowerASCII(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestErrorCodeValues(t *testing.T) {
	// Values fixed by the RFC 1350 appendix and RFC 2347.
	tests := []struct {
		name string
		got  uint16
		want uint16
	}{
		{"not defined", ErrNotDefined, 0},
		{"file not found", ErrFileNotFound, 1},
		{"access violation", ErrAccessViolation, 2},
		{"disk full", ErrDiskFull, 3},
		{"illegal operation", ErrIllegalOp, 4},
		{"unknown TID", ErrUnknownTID, 5},
		{"file exists", ErrFileExists, 6},
		{"no such user", ErrNoSuchUser, 7},
		{"option negotiation", ErrOptionNegotiation, 8},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.got != tt.want {
				t.Errorf("got %d, want %d", tt.got, tt.want)
			}
		})
	}
}
