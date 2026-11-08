package tftp

import (
	"bytes"
	"testing"
)

// FuzzParse feeds random bytes to Parse. Parse must never panic, and anything
// it accepts must survive marshal -> parse -> marshal unchanged.
func FuzzParse(f *testing.F) {
	f.Add([]byte{})
	f.Add([]byte{0, 4, 0, 7})
	f.Add([]byte{0, 3, 0, 1, 'a', 'b', 'c'})
	f.Add([]byte{0, 5, 0, 1, 'n', 'o', 0})
	f.Add([]byte("\x00\x01file\x00octet\x00"))
	f.Add([]byte("\x00\x02f\x00NetAscii\x00BlkSize\x001428\x00"))

	f.Fuzz(func(t *testing.T, data []byte) {
		p, err := Parse(data)
		if err != nil {
			return // rejecting is fine; panicking is not
		}
		b2, err := p.MarshalBinary()
		if err != nil {
			t.Fatalf("parsed %q but cannot marshal it: %v", data, err)
		}
		p2, err := Parse(b2)
		if err != nil {
			t.Fatalf("marshaled %q but cannot parse it back: %v", b2, err)
		}
		b3, err := p2.MarshalBinary()
		if err != nil {
			t.Fatalf("second marshal failed: %v", err)
		}
		if !bytes.Equal(b2, b3) {
			t.Fatalf("not stable: %q vs %q", b2, b3)
		}
	})
}
