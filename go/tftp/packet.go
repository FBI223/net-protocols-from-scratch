package tftp

import (
	"encoding/binary" // reads numbers from raw bytes
	"errors"          // creates simple error values
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
