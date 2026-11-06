package tftp

import (
	"bytes"
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
