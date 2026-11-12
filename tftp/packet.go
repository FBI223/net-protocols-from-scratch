package tftp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Opcode is the first 2 bytes of every TFTP packet; it says what kind of packet it is.
type Opcode uint16

// ParseOpcode reads the opcode from the start of a raw packet.
func ParseOpcode(b []byte) (Opcode, error) {
	// A packet shorter than 2 bytes has no opcode; indexing it would panic.
	if len(b) < 2 {
		return 0, errors.New("packet too short")
	}
	// Read bytes 0 and 1 as a big-endian number and convert it to our Opcode type.
	return Opcode(binary.BigEndian.Uint16(b[:2])), nil
}

// Opcodes from RFC 1350 §5.
const (
	OpRRQ   Opcode = 1 // read request
	OpWRQ   Opcode = 2 // write request
	OpData  Opcode = 3 // a chunk of the file
	OpAck   Opcode = 4 // "I received block N"
	OpError Opcode = 5 // something went wrong
)

// Ack confirms that block number Block was received.
type Ack struct {
	Block uint16
}

// ParseAck turns raw bytes into an Ack.
func ParseAck(b []byte) (Ack, error) {
	// RFC 1350 §5: an ACK is exactly 4 bytes (2 opcode + 2 block number).
	if len(b) != 4 {
		return Ack{}, errors.New("ack must be exactly 4 bytes")
	}
	// Reuse ParseOpcode to read the first 2 bytes.
	op, err := ParseOpcode(b)
	if err != nil {
		return Ack{}, err
	}
	// Reject packets that are not ACK (for example DATA).
	if op != OpAck {
		return Ack{}, errors.New("not an ACK packet")
	}
	// Bytes 2 and 3 hold the block number, big endian.
	return Ack{Block: binary.BigEndian.Uint16(b[2:4])}, nil
}

// MarshalBinary encodes the ACK as 4 bytes (RFC 1350 §5).
func (a Ack) MarshalBinary() ([]byte, error) {
	// An ACK is always 2 bytes of opcode + 2 bytes of block number.
	b := make([]byte, 4)
	// Bytes 0-1: the opcode (4), big endian.
	binary.BigEndian.PutUint16(b[0:2], uint16(OpAck))
	// Bytes 2-3: the block number, big endian.
	binary.BigEndian.PutUint16(b[2:4], a.Block)
	return b, nil
}

// MaxPayload is the largest DATA payload in bytes (RFC 1350 §2).
const MaxPayload = 512

// Data carries one chunk of the file.
type Data struct {
	Block   uint16
	Payload []byte
}

// ParseData turns raw bytes into a Data packet.
func ParseData(b []byte) (Data, error) {
	// RFC 1350 §5: header is 4 bytes (opcode + block), payload may be empty.
	if len(b) < 4 {
		return Data{}, errors.New("data packet too short")
	}
	// RFC 1350 §2: payload is at most 512 bytes, so the packet is at most 516.
	if len(b) > 4+MaxPayload {
		return Data{}, errors.New("data packet too long")
	}
	op, err := ParseOpcode(b)
	if err != nil {
		return Data{}, err
	}
	if op != OpData {
		return Data{}, errors.New("not a DATA packet")
	}
	// Copy the payload: b is usually a reusable receive buffer that the
	// next packet will overwrite.
	payload := make([]byte, len(b)-4)
	copy(payload, b[4:])
	return Data{
		Block:   binary.BigEndian.Uint16(b[2:4]),
		Payload: payload,
	}, nil
}

// MarshalBinary encodes the DATA packet (RFC 1350 §5).
func (d Data) MarshalBinary() ([]byte, error) {
	// Never send a packet that our own parser would reject (RFC 1350 §2).
	if len(d.Payload) > MaxPayload {
		return nil, errors.New("payload larger than 512 bytes")
	}
	// 2 bytes opcode + 2 bytes block number + the payload.
	b := make([]byte, 4+len(d.Payload))
	// Bytes 0-1: the opcode (3), big endian.
	binary.BigEndian.PutUint16(b[0:2], uint16(OpData))
	// Bytes 2-3: the block number, big endian.
	binary.BigEndian.PutUint16(b[2:4], d.Block)
	// Bytes 4...: the file data, copied in.
	copy(b[4:], d.Payload)
	return b, nil
}

// Error codes. Codes 0-7 are from the RFC 1350 appendix,
// code 8 is from RFC 2347.
const (
	ErrNotDefined        uint16 = 0 // see the message text, if any
	ErrFileNotFound      uint16 = 1 // requested file does not exist
	ErrAccessViolation   uint16 = 2 // file exists but may not be read or written
	ErrDiskFull          uint16 = 3 // disk full or allocation exceeded
	ErrIllegalOp         uint16 = 4 // illegal TFTP operation
	ErrUnknownTID        uint16 = 5 // packet came from an unexpected port (does not end the transfer)
	ErrFileExists        uint16 = 6 // cannot create a file that already exists
	ErrNoSuchUser        uint16 = 7 // no such user
	ErrOptionNegotiation uint16 = 8 // transfer ended because of option negotiation
)

// ErrorPacket tells the other side that the transfer failed.
type ErrorPacket struct {
	Code uint16
	Msg  string
}

// ParseError turns raw bytes into an ErrorPacket.
func ParseError(b []byte) (ErrorPacket, error) {
	// Minimum: 2 opcode + 2 code + 1 terminating zero. Maximum 516 is our own
	// limit (same as the largest DATA packet); RFC 1350 does not state one.
	if len(b) < 5 {
		return ErrorPacket{}, errors.New("error packet too short")
	}
	if len(b) > 4+MaxPayload {
		return ErrorPacket{}, errors.New("error packet too long")
	}
	op, err := ParseOpcode(b)

	if err != nil {
		return ErrorPacket{}, err
	}
	if op != OpError {
		return ErrorPacket{}, errors.New("not an ERROR packet")
	}
	// The message is b[4:] and must end with exactly one zero, as the last byte.
	rest := b[4:]
	i := bytes.IndexByte(rest, 0)
	if i == -1 {
		return ErrorPacket{}, errors.New("error message not terminated")
	}
	if i != len(rest)-1 {
		return ErrorPacket{}, errors.New("unexpected zero inside error message")
	}
	return ErrorPacket{
		Code: binary.BigEndian.Uint16(b[2:4]),
		Msg:  string(rest[:i]), // string() copies the bytes
	}, nil
}

// MarshalBinary encodes the ERROR packet (RFC 1350 §5).
func (e ErrorPacket) MarshalBinary() ([]byte, error) {
	// A zero byte inside the message would end the string early, and our own
	// ParseError would reject the packet.
	if strings.IndexByte(e.Msg, 0) != -1 {
		return nil, errors.New("error message contains a zero byte")
	}
	// Keep the packet within 516 bytes: 4 header + message + 1 terminating zero.
	if len(e.Msg) > MaxPayload-1 {
		return nil, errors.New("error message too long")
	}
	// 2 bytes opcode + 2 bytes code + message + 1 terminating zero.
	b := make([]byte, 4+len(e.Msg)+1)
	// Bytes 0-1: the opcode (5), big endian.
	binary.BigEndian.PutUint16(b[0:2], uint16(OpError))
	// Bytes 2-3: the error code, big endian.
	binary.BigEndian.PutUint16(b[2:4], e.Code)
	// Bytes 4...: the message text. The last byte stays 0 because make zeroes memory.
	copy(b[4:], e.Msg)
	return b, nil
}

// MaxOptions limits how many option pairs we accept in one request.
const MaxOptions = 16

// MaxRequestLen is the largest RRQ/WRQ packet in bytes (RFC 2347).
const MaxRequestLen = 512

// Request is a read request (RRQ) or a write request (WRQ), RFC 1350 §5.
type Request struct {
	Op       Opcode // OpRRQ or OpWRQ
	Filename string
	Mode     string            // lower case: "netascii", "octet" or "mail"
	Options  map[string]string // RFC 2347 options; keys lower case; nil if none
}

// lowerASCII lower-cases only A-Z. RFC 1350 says case-insensitive ASCII;
// strings.ToLower is Unicode-aware and would accept odd spellings (e.g. a
// dotted capital I as "i") and can change the byte length.
func lowerASCII(s string) string {
	b := []byte(s)
	for i, c := range b {
		if 'A' <= c && c <= 'Z' {
			b[i] = c + 'a' - 'A'
		}
	}
	return string(b)
}

func validMode(m string) bool {
	switch m {
	case "netascii", "octet", "mail":
		return true
	}
	return false
}

// ParseRequest turns raw bytes into a Request.
func ParseRequest(b []byte) (Request, error) {
	// Smallest valid request: opcode(2) + name(1) + 0 + mode(1) + 0 = 6 bytes.
	if len(b) < 6 {
		return Request{}, errors.New("request too short")
	}
	// RFC 2347: a request packet is at most 512 octets.
	if len(b) > MaxRequestLen {
		return Request{}, errors.New("request too long")
	}

	op, err := ParseOpcode(b)
	if err != nil {
		return Request{}, err
	}
	if op != OpRRQ && op != OpWRQ {
		return Request{}, errors.New("not a RRQ or WRQ packet")
	}
	// Every field ends with a zero, so the last byte must be zero.
	if b[len(b)-1] != 0 {
		return Request{}, errors.New("request not terminated by zero")
	}
	// Drop opcode and the final zero, split the rest on zeros:
	// [filename, mode, opt1, val1, opt2, val2, ...]
	fields := bytes.Split(b[2:len(b)-1], []byte{0})
	if len(fields) < 2 || len(fields)%2 != 0 {
		return Request{}, errors.New("malformed request fields")
	}
	if len(fields[0]) == 0 {
		return Request{}, errors.New("empty filename")
	}
	mode := lowerASCII(string(fields[1]))
	if !validMode(mode) {
		return Request{}, errors.New("unknown transfer mode")
	}

	var opts map[string]string // stays nil when there are no options
	if pairs := (len(fields) - 2) / 2; pairs > 0 {
		if pairs > MaxOptions {
			return Request{}, errors.New("too many options")
		}
		opts = make(map[string]string, pairs)
		for i := 2; i < len(fields); i += 2 {
			key := lowerASCII(string(fields[i]))
			if key == "" {
				return Request{}, errors.New("empty option name")
			}
			if _, dup := opts[key]; dup {
				return Request{}, errors.New("duplicate option")
			}
			opts[key] = string(fields[i+1])
		}
	}
	return Request{Op: op, Filename: string(fields[0]), Mode: mode, Options: opts}, nil
}

// MarshalBinary encodes the request (RFC 1350 §5, options RFC 2347).
func (r Request) MarshalBinary() ([]byte, error) {
	if r.Op != OpRRQ && r.Op != OpWRQ {
		return nil, errors.New("request opcode must be RRQ or WRQ")
	}
	if r.Filename == "" {
		return nil, errors.New("empty filename")
	}
	if strings.IndexByte(r.Filename, 0) != -1 {
		return nil, errors.New("filename contains a zero byte")
	}
	mode := lowerASCII(r.Mode)
	if !validMode(mode) {
		return nil, errors.New("unknown transfer mode")
	}
	if len(r.Options) > MaxOptions {
		return nil, errors.New("too many options")
	}
	keys := make([]string, 0, len(r.Options))
	for k, v := range r.Options {
		if k == "" || k != lowerASCII(k) {
			return nil, errors.New("option name must be non-empty lower case")
		}
		if strings.IndexByte(k, 0) != -1 || strings.IndexByte(v, 0) != -1 {
			return nil, errors.New("option contains a zero byte")
		}
		keys = append(keys, k)
	}
	// Map iteration order is random in Go; sort so the same Request always
	// produces the same bytes.
	sort.Strings(keys)

	b := make([]byte, 2, 64)
	binary.BigEndian.PutUint16(b, uint16(r.Op))
	b = append(b, r.Filename...)
	b = append(b, 0)
	b = append(b, mode...)
	b = append(b, 0)
	for _, k := range keys {
		b = append(b, k...)
		b = append(b, 0)
		b = append(b, r.Options[k]...)
		b = append(b, 0)
	}

	if len(b) > MaxRequestLen {
		return nil, errors.New("request too long")
	}

	return b, nil
}

// Packet is any TFTP packet we can parse and send.
type Packet interface {
	Opcode() Opcode
	MarshalBinary() ([]byte, error)
}

func (r Request) Opcode() Opcode     { return r.Op }
func (a Ack) Opcode() Opcode         { return OpAck }
func (d Data) Opcode() Opcode        { return OpData }
func (e ErrorPacket) Opcode() Opcode { return OpError }

// Parse reads the opcode and calls the matching parser.
func Parse(b []byte) (Packet, error) {
	op, err := ParseOpcode(b)
	if err != nil {
		return nil, err
	}
	// Each case checks err itself and returns a literal nil on failure.
	// "return ParseAck(b)" would hand back a non-nil Packet holding a zero
	// Ack, so callers testing "p != nil" would be fooled.
	switch op {
	case OpRRQ, OpWRQ:
		p, err := ParseRequest(b)
		if err != nil {
			return nil, err
		}
		return p, nil
	case OpData:
		p, err := ParseData(b)
		if err != nil {
			return nil, err
		}
		return p, nil
	case OpAck:
		p, err := ParseAck(b)
		if err != nil {
			return nil, err
		}
		return p, nil
	case OpError:
		p, err := ParseError(b)
		if err != nil {
			return nil, err
		}
		return p, nil
	default:
		return nil, fmt.Errorf("unknown opcode %d", op)
	}
}
