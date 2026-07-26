package login

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
)

const (
	packetIDLoginStart    int32 = 0x00
	packetIDEncryptionReq  int32 = 0x01
	packetIDLoginSuccess  int32 = 0x02
	packetIDSetCompression int32 = 0x03
	packetIDDisconnect    int32 = 0x00
)

var ErrVarIntTooLong = fmt.Errorf("login: varint too many bytes")

func WriteVarInt(w io.Writer, v int32) error {
	u := uint32(v)
	for u >= 0x80 {
		if _, err := w.Write([]byte{byte(u) | 0x80}); err != nil {
			return err
		}
		u >>= 7
	}
	if _, err := w.Write([]byte{byte(u)}); err != nil {
		return err
	}
	return nil
}

func ReadVarInt(r io.ByteReader) (int32, error) {
	var result int32
	var shift uint
	for i := 0; i < 5; i++ {
		b, err := r.ReadByte()
		if err != nil {
			return 0, err
		}
		result |= int32(b&0x7F) << shift
		if b&0x80 == 0 {
			return result, nil
		}
		shift += 7
	}
	return 0, ErrVarIntTooLong
}

func WriteString(w io.Writer, s string) error {
	if err := WriteVarInt(w, int32(len(s))); err != nil {
		return err
	}
	if _, err := w.Write([]byte(s)); err != nil {
		return err
	}
	return nil
}

func ReadString(r io.ByteReader) (string, error) {
	length, err := ReadVarInt(r)
	if err != nil {
		return "", err
	}
	if length < 0 || length > 32767 {
		return "", fmt.Errorf("login: string length out of range: %d", length)
	}
	buf := make([]byte, length)
	br, ok := r.(io.Reader)
	if !ok {
		return "", fmt.Errorf("login: reader does not implement io.Reader")
	}
	if _, err := io.ReadFull(br, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

func WriteUUID(w io.Writer, uuid [16]byte) error {
	_, err := w.Write(uuid[:])
	return err
}

func ReadUUID(r io.Reader) ([16]byte, error) {
	var uuid [16]byte
	_, err := io.ReadFull(r, uuid[:])
	return uuid, err
}

type Packet struct {
	ID    int32
	State string
	Data  []byte
}

func ReadPacket(r *bufio.Reader, state string) (*Packet, error) {
	length, err := ReadVarInt(r)
	if err != nil {
		return nil, fmt.Errorf("login: reading packet length: %w", err)
	}
	if length <= 0 || length > 2<<20 {
		return nil, fmt.Errorf("login: invalid packet length: %d", length)
	}

	data := make([]byte, length)
	if _, err := io.ReadFull(r, data); err != nil {
		return nil, fmt.Errorf("login: reading packet data: %w", err)
	}

	packetReader := bufio.NewReader(bytes.NewReader(data))
	id, err := ReadVarInt(packetReader)
	if err != nil {
		return nil, fmt.Errorf("login: reading packet id: %w", err)
	}

	body := make([]byte, 0)
	if remaining := len(data) - int(packetReader.Buffered()); remaining > 0 {
		body = make([]byte, remaining)
		copy(body, data[len(data)-remaining:])
	} else {
		packetReader = bufio.NewReader(bytes.NewReader(data[1:]))
	}

	p := &Packet{ID: id, State: state}
	if len(body) > 0 {
		p.Data = body
	} else {
		p.Data = make([]byte, 0)
	}

	return p, nil
}

func WritePacket(w io.Writer, state string, id int32, payload []byte) error {
	var hdr bytes.Buffer
	if err := WriteVarInt(&hdr, id); err != nil {
		return err
	}
	if _, err := hdr.Write(payload); err != nil {
		return err
	}

	var pkt bytes.Buffer
	if err := WriteVarInt(&pkt, int32(hdr.Len())); err != nil {
		return err
	}
	if _, err := pkt.Write(hdr.Bytes()); err != nil {
		return err
	}

	_, err := w.Write(pkt.Bytes())
	return err
}

func WriteUInt16(w io.Writer, v uint16) error {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	_, err := w.Write(b[:])
	return err
}

func ReadUInt16(r io.Reader) (uint16, error) {
	var b [2]byte
	if _, err := io.ReadFull(r, b[:]); err != nil {
		return 0, err
	}
	return binary.BigEndian.Uint16(b[:]), nil
}

func ReadPrefixedArray(r io.ByteReader, maxLen int) ([]byte, error) {
	length, err := ReadVarInt(r)
	if err != nil {
		return nil, err
	}
	if length < 0 || length > int32(maxLen) {
		return nil, fmt.Errorf("login: array length out of range: %d", length)
	}
	buf := make([]byte, length)
	br, ok := r.(io.Reader)
	if !ok {
		return nil, fmt.Errorf("login: reader does not implement io.Reader")
	}
	if _, err := io.ReadFull(br, buf); err != nil {
		return nil, err
	}
	return buf, nil
}
