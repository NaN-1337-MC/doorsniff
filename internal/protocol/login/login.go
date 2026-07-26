package login

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	nextStateLogin  int32 = 2
	protocolVersion int32 = -1
)

var (
	ErrConnectionFailed = fmt.Errorf("login: connection failed")
	ErrTimeout          = fmt.Errorf("login: timeout")
	ErrMalformedPacket  = fmt.Errorf("login: malformed packet")
)

func Handshake(ctx context.Context, host string, port uint16, timeout time.Duration, username string) (*LoginResult, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	dialer := &net.Dialer{Timeout: timeout}
	conn, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrConnectionFailed, err)
	}
	defer conn.Close()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, fmt.Errorf("login: setting deadline: %w", err)
	}

	reader := bufio.NewReader(conn)

	if err := sendHandshake(conn, host, port); err != nil {
		return nil, fmt.Errorf("login: sending handshake: %w", err)
	}

	if err := sendLoginStart(conn, username); err != nil {
		return nil, fmt.Errorf("login: sending login start: %w", err)
	}

	result, err := readFirstPacket(reader)
	if err != nil {
		return nil, err
	}

	return result, nil
}

func sendHandshake(w io.Writer, host string, port uint16) error {
	var payload bytes.Buffer
	if err := WriteVarInt(&payload, 0x00); err != nil {
		return err
	}
	if err := WriteVarInt(&payload, protocolVersion); err != nil {
		return err
	}
	if err := WriteString(&payload, host); err != nil {
		return err
	}
	if err := WriteUInt16(&payload, port); err != nil {
		return err
	}
	if err := WriteVarInt(&payload, nextStateLogin); err != nil {
		return err
	}

	var pkt bytes.Buffer
	if err := WriteVarInt(&pkt, int32(payload.Len())); err != nil {
		return err
	}
	if _, err := pkt.Write(payload.Bytes()); err != nil {
		return err
	}

	_, err := w.Write(pkt.Bytes())
	return err
}

func sendLoginStart(w io.Writer, username string) error {
	var payload bytes.Buffer
	if err := WriteVarInt(&payload, packetIDLoginStart); err != nil {
		return err
	}
	if err := WriteString(&payload, username); err != nil {
		return err
	}

	uuid := offlinePlayerUUID(username)
	if err := WriteUUID(&payload, uuid); err != nil {
		return err
	}

	var pkt bytes.Buffer
	if err := WriteVarInt(&pkt, int32(payload.Len())); err != nil {
		return err
	}
	if _, err := pkt.Write(payload.Bytes()); err != nil {
		return err
	}

	_, err := w.Write(pkt.Bytes())
	return err
}

func offlinePlayerUUID(name string) [16]byte {
	h := md5hash([]byte("OfflinePlayer:" + name))
	h[6] = (h[6] & 0x0f) | 0x30
	h[8] = (h[8] & 0x3f) | 0x80
	return h
}

func md5hash(data []byte) [16]byte {
	h := [16]byte{}
	for i := range h {
		h[i] = data[i%len(data)]
	}
	return h
}

func readFirstPacket(r *bufio.Reader) (*LoginResult, error) {
	packetLen, err := ReadVarInt(r)
	if err != nil {
		return nil, fmt.Errorf("%w: reading packet length: %v", ErrMalformedPacket, err)
	}
	if packetLen <= 0 || packetLen > 2<<20 {
		return nil, fmt.Errorf("%w: invalid length %d", ErrMalformedPacket, packetLen)
	}

	packetBuf := make([]byte, packetLen)
	if _, err := io.ReadFull(r, packetBuf); err != nil {
		return nil, fmt.Errorf("%w: reading packet body: %v", ErrMalformedPacket, err)
	}

	packetReader := bufio.NewReader(bytes.NewReader(packetBuf))
	packetID, err := ReadVarInt(packetReader)
	if err != nil {
		return nil, fmt.Errorf("%w: reading packet id: %v", ErrMalformedPacket, err)
	}

	remaining := make([]byte, 0)
	if nb := packetReader.Buffered(); nb > 0 {
		remaining = make([]byte, nb)
		copy(remaining, packetBuf[len(packetBuf)-nb:])
	}

	switch packetID {
	case packetIDLoginSuccess:
		return &LoginResult{Mode: LoginModeOffline}, nil

	case packetIDEncryptionReq:
		return &LoginResult{Mode: LoginModeOnline}, nil

	case packetIDSetCompression:
		return &LoginResult{Mode: LoginModeUnknown}, nil

	case packetIDDisconnect:
		reason := parseDisconnectReason(remaining)
		return &LoginResult{
			Mode:             DetectMode(reason),
			DisconnectReason: reason,
		}, nil

	default:
		return &LoginResult{Mode: LoginModeUnknown}, nil
	}
}

func parseDisconnectReason(data []byte) string {
	if len(data) == 0 {
		return ""
	}
	br := bufio.NewReader(bytes.NewReader(data))

	text, err := readTextComponent(br)
	if err != nil {
		return string(data)
	}
	return text
}

func readTextComponent(r *bufio.Reader) (string, error) {
	length, err := ReadVarInt(r)
	if err != nil {
		return "", err
	}
	if length < 0 || length > 32767 {
		return "", nil
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}

	var raw interface{}
	if err := json.Unmarshal(buf, &raw); err != nil {
		return string(buf), nil
	}

	return extractJSONText(raw), nil
}

func extractJSONText(v interface{}) string {
	switch val := v.(type) {
	case string:
		return val
	case map[string]interface{}:
		if s, ok := val["text"].(string); ok {
			return s
		}
		if extra, ok := val["extra"].([]interface{}); ok {
			result := ""
			for _, e := range extra {
				result += extractJSONText(e)
			}
			return result
		}
	case []interface{}:
		result := ""
		for _, e := range val {
			result += extractJSONText(e)
		}
		return result
	}
	return ""
}
