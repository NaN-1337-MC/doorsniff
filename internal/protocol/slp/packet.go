// Package slp implements the Minecraft Server List Ping protocol for
// modern 1.7+ handshakes. It speaks TCP, sends the handshake + status
// request packets, and parses the JSON response.
package slp

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

// ErrVarIntTooLong is returned when a varint exceeds 5 bytes — the
// maximum for a 32-bit value.
var ErrVarIntTooLong = errors.New("varint: too many bytes")

// WriteVarInt encodes v as a Minecraft varint and writes it to w. The
// value is interpreted as the bit pattern of int32; -1 is encoded as
// the 5-byte varint for 0xFFFFFFFF, which the SLP handshake uses for
// "any protocol version".
func WriteVarInt(w io.Writer, v int32) error {
	u := uint32(v)
	for u >= 0x80 {
		if _, err := w.Write([]byte{byte(u) | 0x80}); err != nil {
			return fmt.Errorf("writing varint byte: %w", err)
		}
		u >>= 7
	}
	if _, err := w.Write([]byte{byte(u)}); err != nil {
		return fmt.Errorf("writing varint byte: %w", err)
	}
	return nil
}

// ReadVarInt reads a varint from r. r must be an io.ByteReader
// (bufio.Reader satisfies this).
func ReadVarInt(r io.ByteReader) (int32, error) {
	var result int32
	var shift uint
	for i := 0; i < 5; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, fmt.Errorf("reading varint byte: %w", err)
		}
		result |= int32(b&0x7F) << shift
		if b&0x80 == 0 {
			return result, nil
		}
		shift += 7
	}
	return 0, ErrVarIntTooLong
}

// WriteString writes a Minecraft string: a varint length followed by the
// UTF-8 bytes of s.
func WriteString(w io.Writer, s string) error {
	if err := WriteVarInt(w, int32(len(s))); err != nil {
		return fmt.Errorf("writing string length: %w", err)
	}
	if _, err := w.Write([]byte(s)); err != nil {
		return fmt.Errorf("writing string bytes: %w", err)
	}
	return nil
}

// ReadString reads a Minecraft string from r. r must be an io.ByteReader
// (varint length) AND an io.Reader (the bytes). bufio.Reader satisfies
// both.
func ReadString(r io.ByteReader) (string, error) {
	length, err := ReadVarInt(r)
	if err != nil {
		return "", fmt.Errorf("reading string length: %w", err)
	}
	if length < 0 || length > 32767 {
		return "", fmt.Errorf("string length out of range: %d", length)
	}
	buf := make([]byte, length)
	br, ok := r.(io.Reader)
	if !ok {
		return "", fmt.Errorf("reader does not implement io.Reader")
	}
	if _, err := io.ReadFull(br, buf); err != nil {
		return "", fmt.Errorf("reading string body: %w", err)
	}
	return string(buf), nil
}

// WriteUShort writes a big-endian unsigned 16-bit integer.
func WriteUShort(w io.Writer, v uint16) error {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	if _, err := w.Write(b[:]); err != nil {
		return fmt.Errorf("writing ushort: %w", err)
	}
	return nil
}
