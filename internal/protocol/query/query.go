// Package query implements the Minecraft Query protocol (UDP).
//
// Query is a UDP-based protocol introduced in Beta 1.9 that returns
// richer server information than SLP: map name, game type, and plugin
// list. It must be explicitly enabled on the server (enable-query=true
// in server.properties).
//
// The protocol has two stat modes (basic and full). swoomps always
// uses full stat because it includes the plugin list and player names.
package query

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"math/rand/v2"
	"net"
	"strconv"
	"strings"
	"time"
)

// magic is the 2-byte UDP header prefix shared by every Query packet.
var magic = []byte{0xFE, 0xFD}

// Packet type constants.
const (
	typeHandshake byte = 0x09
	typeStat      byte = 0x00
)

// maxResponseSize is the largest UDP payload we accept. A full-stat
// response is typically a few hundred bytes; 4 KiB is a safe ceiling.
const maxResponseSize = 4096

// Errors returned by the client.
var (
	ErrHandshake     = errors.New("query: handshake failed")
	ErrStat          = errors.New("query: stat request failed")
	ErrBadMagic      = errors.New("query: response magic mismatch")
	ErrBadSessionID  = errors.New("query: session ID mismatch")
	ErrBadChallenge  = errors.New("query: invalid challenge token")
	ErrUnsupported   = errors.New("query: unsupported response type")
	ErrResponseShort = errors.New("query: response too short")
)

// Query performs a full-stat Query against host:port. It returns the
// parsed Response, the round-trip latency, and any error.
//
// The caller should treat a non-nil error as "server did not respond
// to query" — this is distinct from "server is offline" because query
// may be disabled even on a running server.
func Query(ctx context.Context, host string, port uint16, timeout time.Duration) (*Response, time.Duration, error) {
	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))

	conn, err := net.DialTimeout("udp", addr, timeout)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: dial: %v", ErrStat, err)
	}
	defer conn.Close()

	// Set read/write deadline for the entire session.
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, 0, fmt.Errorf("%w: set deadline: %v", ErrStat, err)
	}

	// --- handshake ---
	sessionID := rand.Int32()

	challenge, err := handshake(conn, sessionID)
	if err != nil {
		return nil, 0, err
	}

	// --- full stat ---
	start := time.Now()
	raw, err := fullStat(conn, sessionID, challenge)
	latency := time.Since(start)
	if err != nil {
		return nil, latency, err
	}

	resp, err := ParseFullStat(raw)
	if err != nil {
		return nil, latency, err
	}
	return resp, latency, nil
}

// handshake sends a type-0x09 packet and reads the challenge token
// from the response. The token is returned as a string for the caller
// to convert to int32.
func handshake(conn net.Conn, sessionID int32) (int32, error) {
	// Build request: magic(2) + type(1) + sessionID(4) = 7 bytes.
	req := make([]byte, 7)
	copy(req[0:2], magic)
	req[2] = typeHandshake
	binary.BigEndian.PutUint32(req[3:7], uint32(sessionID))

	if _, err := conn.Write(req); err != nil {
		return 0, fmt.Errorf("%w: write: %v", ErrHandshake, err)
	}

	resp := make([]byte, maxResponseSize)
	n, err := conn.Read(resp)
	if err != nil {
		return 0, fmt.Errorf("%w: read: %v", ErrHandshake, err)
	}
	resp = resp[:n]

	// Response: type(1) + sessionID(4) + challenge_string(…\0)
	if len(resp) < 5 {
		return 0, fmt.Errorf("%w: response too short (%d bytes)", ErrHandshake, len(resp))
	}
	if resp[0] != typeHandshake {
		return 0, fmt.Errorf("%w: type=0x%02X, want 0x09", ErrHandshake, resp[0])
	}
	respSessionID := int32(binary.BigEndian.Uint32(resp[1:5]))
	if respSessionID != sessionID {
		return 0, fmt.Errorf("%w: got %d, want %d", ErrBadSessionID, respSessionID, sessionID)
	}

	// The challenge token is a null-terminated ASCII decimal string.
	tokenStr := string(resp[5:])
	if i := strings.IndexByte(tokenStr, 0); i >= 0 {
		tokenStr = tokenStr[:i]
	}
	token, err := strconv.ParseInt(tokenStr, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("%w: %q: %v", ErrBadChallenge, tokenStr, err)
	}
	return int32(token), nil
}

// fullStat sends a type-0x00 packet with the challenge token and
// padding, then returns the raw response body (after the 5-byte header
// and before the payload end).
func fullStat(conn net.Conn, sessionID, challenge int32) ([]byte, error) {
	// Request: magic(2) + type(1) + sessionID(4) + challenge(4) + padding(4) = 15 bytes.
	req := make([]byte, 15)
	copy(req[0:2], magic)
	req[2] = typeStat
	binary.BigEndian.PutUint32(req[3:7], uint32(sessionID))
	binary.BigEndian.PutUint32(req[7:11], uint32(challenge))
	// Last 4 bytes are padding (0x00).

	if _, err := conn.Write(req); err != nil {
		return nil, fmt.Errorf("%w: write: %v", ErrStat, err)
	}

	resp := make([]byte, maxResponseSize)
	n, err := conn.Read(resp)
	if err != nil {
		return nil, fmt.Errorf("%w: read: %v", ErrStat, err)
	}
	resp = resp[:n]

	// Response: type(1) + sessionID(4) + payload(…)
	if len(resp) < 5 {
		return nil, fmt.Errorf("%w: response too short (%d bytes)", ErrStat, len(resp))
	}
	if resp[0] != typeStat {
		return nil, fmt.Errorf("%w: type=0x%02X, want 0x00", ErrStat, resp[0])
	}
	respSessionID := int32(binary.BigEndian.Uint32(resp[1:5]))
	if respSessionID != sessionID {
		return nil, fmt.Errorf("%w: got %d, want %d", ErrBadSessionID, respSessionID, sessionID)
	}

	return resp[5:], nil
}
