package slp

import (
	"bufio"
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"time"
	"unicode/utf16"
)

// LegacyResponse is the parsed result of a 1.6 legacy Server List
// Ping. Modern (1.7+) servers do not respond to this protocol; the
// caller must fall back to it only when the modern handshake is
// rejected.
type LegacyResponse struct {
	Protocol     string
	Version      string
	MOTD         string
	NumPlayers   int
	MaxPlayers   int
}

// PingLegacy performs a 1.6 Server List Ping. The wire format is:
//
//   Client -> Server: [0xFE] [0x01]                              (2 bytes)
//   Server -> Client: [0xFF] [short length] [UTF-16BE data]      (kick packet)
//
// The data string is a sequence of sections separated by null bytes:
//
//   §1\0<protocol>\0§2\0<version>\0§3\0<motd>\0§4\0<num>\0§5\0<max>\0
//
// BungeeCord proxies with `ping_passthrough: true` and pre-1.7
// servers respond to this protocol. Most modern servers do not.
func PingLegacy(ctx context.Context, host string, port uint16, timeout time.Duration) (*LegacyResponse, time.Duration, error) {
	start := time.Now()

	addr := net.JoinHostPort(host, strconv.Itoa(int(port)))
	dialer := &net.Dialer{Timeout: timeout}
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

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}
	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, 0, fmt.Errorf("setting deadline: %w", err)
	}

	// 1.6 ping: two bytes, no length prefix.
	if _, err := conn.Write([]byte{0xFE, 0x01}); err != nil {
		return nil, 0, fmt.Errorf("sending legacy ping: %w", err)
	}

	// Read until the server closes the connection. The kick packet
	// arrives as one or more reads; we collect them all.
	reader := bufio.NewReader(conn)
	body, err := io.ReadAll(reader)
	if err != nil {
		return nil, 0, fmt.Errorf("reading legacy response: %w", err)
	}
	if len(body) == 0 {
		return nil, 0, fmt.Errorf("empty legacy response")
	}

	resp, err := parseLegacyKick(body)
	if err != nil {
		return nil, 0, err
	}
	return resp, time.Since(start), nil
}

// parseLegacyKick decodes the body of a 1.6 kick response. The body
// starts with 0xFF followed by a short (BE) length and UTF-16BE data.
func parseLegacyKick(body []byte) (*LegacyResponse, error) {
	if len(body) < 1 || body[0] != 0xFF {
		return nil, fmt.Errorf("legacy response does not start with 0xFF (got 0x%02x)", body[0])
	}
	body = body[1:]

	if len(body) < 2 {
		return nil, fmt.Errorf("legacy response too short for length prefix: %d bytes", len(body))
	}
	strLen := int(binary.BigEndian.Uint16(body[:2]))
	body = body[2:]

	if len(body) < strLen {
		return nil, fmt.Errorf("legacy response truncated: have %d bytes, need %d", len(body), strLen)
	}
	body = body[:strLen]

	if len(body)%2 != 0 {
		// Some servers send a stray byte; trim it.
		body = body[:len(body)-1]
	}

	// Decode UTF-16BE into a Go string.
	u16 := make([]uint16, len(body)/2)
	for i := range u16 {
		u16[i] = binary.BigEndian.Uint16(body[2*i : 2*i+2])
	}
	decoded := string(utf16.Decode(u16))

	return parseLegacySections(decoded)
}

// parseLegacySections extracts the §1..§5 fields from a 1.6 response
// string. Sections are introduced by §N (where N is 1..5) and each
// section's value is terminated by a null byte.
func parseLegacySections(s string) (*LegacyResponse, error) {
	parts := strings.Split(s, string(rune(0)))
	resp := &LegacyResponse{}

	// Walk pairs of (marker, value).
	parts = parts[1:] // first element is empty (response starts with \0)
	for i := 0; i+1 < len(parts); i += 2 {
		switch parts[i] {
		case "§1":
			resp.Protocol = parts[i+1]
		case "§2":
			resp.Version = parts[i+1]
		case "§3":
			resp.MOTD = parts[i+1]
		case "§4":
			n, err := strconv.Atoi(parts[i+1])
			if err != nil {
				return nil, fmt.Errorf("parsing numplayers %q: %w", parts[i+1], err)
			}
			resp.NumPlayers = n
		case "§5":
			n, err := strconv.Atoi(parts[i+1])
			if err != nil {
				return nil, fmt.Errorf("parsing maxplayers %q: %w", parts[i+1], err)
			}
			resp.MaxPlayers = n
		}
	}
	if resp.MaxPlayers == 0 && resp.MOTD == "" && resp.Protocol == "" && resp.Version == "" {
		return nil, fmt.Errorf("legacy response empty: %q", s)
	}
	return resp, nil
}
