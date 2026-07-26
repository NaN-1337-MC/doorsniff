package slp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

// Protocol version sent in the handshake. -1 means "any" and is
// accepted by every server; the server does not use this value to
// decide the SLP response, only next_state.
const handshakeProtocolVersion = int32(-1)

// Next-state value for a status (SLP) request.
const nextStateStatus = int32(1)

// Ping performs a Server List Ping against host:port and returns the
// parsed response, the round-trip latency, and any error. The call
// honours ctx for cancellation and uses timeout for the dial + read
// deadlines.
func Ping(ctx context.Context, host string, port uint16, timeout time.Duration) (*Response, time.Duration, error) {
	start := time.Now()

	dialer := &net.Dialer{Timeout: timeout}
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	// conn is a *net.TCPConn; it implements io.ReadWriter.
	type dialResult struct {
		conn net.Conn
		err  error
	}
	dialCh := make(chan dialResult, 1)
	go func() {
		c, err := dialer.DialContext(ctx, "tcp", addr)
		dialCh <- dialResult{conn: c, err: err}
	}()
	var conn net.Conn
	select {
	case <-ctx.Done():
		return nil, 0, fmt.Errorf("dialing %s: %w", addr, ctx.Err())
	case r := <-dialCh:
		if r.err != nil {
			return nil, 0, fmt.Errorf("dialing %s: %w", addr, r.err)
		}
		conn = r.conn
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, 0, fmt.Errorf("setting deadline: %w", err)
	}

	handshake, err := buildHandshake(host, port)
	if err != nil {
		return nil, 0, fmt.Errorf("building handshake: %w", err)
	}
	if _, err := conn.Write(handshake); err != nil {
		return nil, 0, fmt.Errorf("sending handshake: %w", err)
	}

	statusReq := buildStatusRequest()
	if _, err := conn.Write(statusReq); err != nil {
		return nil, 0, fmt.Errorf("sending status request: %w", err)
	}

	reader := bufio.NewReader(conn)
	packetLen, err := ReadVarInt(reader)
	if err != nil {
		return nil, 0, fmt.Errorf("reading response length: %w", err)
	}
	if packetLen <= 0 {
		return nil, 0, fmt.Errorf("invalid response length: %d", packetLen)
	}

	// Cap how much we are willing to read; reject absurd values early.
	if packetLen > 1<<20 {
		return nil, 0, fmt.Errorf("response length too large: %d", packetLen)
	}

	packetBuf := make([]byte, packetLen)
	if _, err := io.ReadFull(reader, packetBuf); err != nil {
		return nil, 0, fmt.Errorf("reading response body: %w", err)
	}

	packetReader := bufio.NewReader(bytes.NewReader(packetBuf))
	packetID, err := ReadVarInt(packetReader)
	if err != nil {
		return nil, 0, fmt.Errorf("reading response packet id: %w", err)
	}
	if packetID != 0x00 {
		return nil, 0, fmt.Errorf("unexpected response packet id: %d (want 0)", packetID)
	}

	body, err := ReadString(packetReader)
	if err != nil {
		return nil, 0, fmt.Errorf("reading response json: %w", err)
	}

	resp, err := Parse([]byte(body))
	if err != nil {
		return nil, 0, err
	}
	return resp, time.Since(start), nil
}

// buildHandshake returns the bytes to send for a status-handshake
// packet. The packet is length-prefixed: [varint length][varint 0x00]
// [varint protocol][string host][ushort port][varint next_state=1].
func buildHandshake(host string, port uint16) ([]byte, error) {
	var payload bytes.Buffer
	if err := WriteVarInt(&payload, 0x00); err != nil {
		return nil, err
	}
	if err := WriteVarInt(&payload, handshakeProtocolVersion); err != nil {
		return nil, err
	}
	if err := WriteString(&payload, host); err != nil {
		return nil, err
	}
	if err := WriteUShort(&payload, port); err != nil {
		return nil, err
	}
	if err := WriteVarInt(&payload, nextStateStatus); err != nil {
		return nil, err
	}

	out := bytes.NewBuffer(make([]byte, 0, payload.Len()+5))
	if err := WriteVarInt(out, int32(payload.Len())); err != nil {
		return nil, err
	}
	if _, err := out.Write(payload.Bytes()); err != nil {
		return nil, err
	}
	return out.Bytes(), nil
}

// buildStatusRequest returns the bytes for a status-request packet:
// [varint 1][varint 0x00].
func buildStatusRequest() []byte {
	return []byte{0x01, 0x00}
}
