package slp

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"syscall"
	"time"
)

// Handshake protocol versions used by Ping.
//
// The handshake protocol field is only meaningful for login
// (next_state=2); vanilla servers ignore it for status requests.
// However, some frontends (notably e4mc LAN-world proxies and a
// few BungeeCord / Velocity setups) inspect the field even on the
// status path and reject unknown values. In particular:
//
//   - e4mc resets the connection (RST) when it sees protocol -1
//     but responds normally to a real protocol number such as 768.
//   - some older / version-strict setups do the opposite and only
//     accept -1 (or a value close to theirs) on the status path.
//
// To cover both cases, Ping sends a real modern protocol first
// (handshakeProtocolPrimary) and falls back to the protocol-
// agnostic sentinel (handshakeProtocolFallback) only when the
// server accepts the TCP connection but aborts it right after
// the handshake is sent — i.e. the rejection signal.
const (
	handshakeProtocolPrimary  int32 = 768 // Minecraft 1.21.1
	handshakeProtocolFallback int32 = -1  // "any version" sentinel
)

// Next-state value for a status (SLP) request.
const nextStateStatus = int32(1)

// Ping performs a Server List Ping against host:port and returns the
// parsed response, the round-trip latency, and any error. The call
// honours ctx for cancellation and uses timeout for the dial + read
// deadlines.
//
// If the server accepts the TCP connection but aborts it right
// after receiving the handshake (a "handshake rejection" — typically
// a TCP RST or an immediate EOF before any SLP payload arrives),
// Ping retries exactly once with handshakeProtocolFallback (-1).
// Dial failures and parse failures are not retried.
func Ping(ctx context.Context, host string, port uint16, timeout time.Duration) (*Response, time.Duration, error) {
	resp, latency, err := pingOnce(ctx, host, port, handshakeProtocolPrimary, timeout)
	if err == nil {
		return resp, latency, nil
	}
	if !isHandshakeRejection(err) {
		return nil, 0, err
	}

	resp, latency, err = pingOnce(ctx, host, port, handshakeProtocolFallback, timeout)
	if err != nil {
		return nil, 0, err
	}
	return resp, latency, nil
}

// pingOnce performs a single SLP attempt with the given handshake
// protocol version. It does not retry on failure.
func pingOnce(ctx context.Context, host string, port uint16, protocol int32, timeout time.Duration) (*Response, time.Duration, error) {
	start := time.Now()

	conn, err := dialTCP(ctx, host, port, timeout)
	if err != nil {
		return nil, 0, err
	}
	defer func() { _ = conn.Close() }()

	if err := conn.SetDeadline(time.Now().Add(timeout)); err != nil {
		return nil, 0, fmt.Errorf("setting deadline: %w", err)
	}

	handshake, err := buildHandshake(host, port, protocol)
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

// dialTCP opens a TCP connection with the given timeout and returns
// the underlying net.Conn, or an error tagged with errDialFailure
// so callers can distinguish dial failures from post-dial ones.
func dialTCP(ctx context.Context, host string, port uint16, timeout time.Duration) (net.Conn, error) {
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
	select {
	case <-ctx.Done():
		return nil, fmt.Errorf("dialing %s: %w", addr, ctx.Err())
	case r := <-dialCh:
		if r.err != nil {
			return nil, fmt.Errorf("dialing %s: %w", addr, r.err)
		}
		return r.conn, nil
	}
}

// isHandshakeRejection reports whether err signals that the remote
// peer accepted the TCP connection but refused to continue the
// handshake — typically by sending a TCP RST or closing the
// connection right after the handshake bytes were sent. The
// signal is dial-succeeded + write-or-read failed with one of
// the well-known connection-aborted errors.
//
// Dial failures (timeout, refused, no route) and protocol-level
// errors (parse, bad packet id, etc.) are NOT handshake
// rejections — retrying with a different protocol version would
// not help and would only waste time.
func isHandshakeRejection(err error) bool {
	if err == nil {
		return false
	}
	if isDialError(err) {
		return false
	}
	return isConnAborted(err)
}

// isDialError reports whether err originated in the TCP dial
// phase. We detect this by the "dialing %s" prefix that dialTCP
// uses, which is robust to any underlying network error (timeout,
// refused, no route, ...). Dial failures never trigger a fallback
// retry because the server is simply unreachable.
func isDialError(err error) bool {
	for e := err; e != nil; e = errors.Unwrap(e) {
		if msg := e.Error(); len(msg) >= 7 && msg[:7] == "dialing" {
			return true
		}
	}
	return false
}

// isConnAborted reports whether err matches one of the
// connection-aborted errors that indicate the peer rejected the
// handshake: ECONNRESET, ECONNABORTED, EPIPE, or a clean/early
// EOF on the response read.
func isConnAborted(err error) bool {
	if errors.Is(err, io.EOF) ||
		errors.Is(err, io.ErrUnexpectedEOF) ||
		errors.Is(err, syscall.ECONNRESET) ||
		errors.Is(err, syscall.ECONNABORTED) ||
		errors.Is(err, syscall.EPIPE) {
		return true
	}
	// Some platforms report connection resets wrapped in *net.OpError
	// with a non-syscall underlying error (e.g. "connection reset by
	// peer" as a plain string). Fall back to substring matching on
	// the error message so those are still caught.
	msg := err.Error()
	return containsAny(msg,
		"connection reset by peer",
		"broken pipe",
		"connection aborted",
	)
}

// containsAny is a small case-sensitive substring helper kept local
// to avoid pulling strings into this file for a single use.
func containsAny(s string, subs ...string) bool {
	for _, sub := range subs {
		if len(sub) <= len(s) && indexOf(s, sub) >= 0 {
			return true
		}
	}
	return false
}

func indexOf(s, sub string) int {
	n, m := len(s), len(sub)
	if m == 0 {
		return 0
	}
	for i := 0; i+m <= n; i++ {
		if s[i:i+m] == sub {
			return i
		}
	}
	return -1
}

// buildHandshake returns the bytes to send for a status-handshake
// packet. The packet is length-prefixed: [varint length][varint 0x00]
// [varint protocol][string host][ushort port][varint next_state=1].
func buildHandshake(host string, port uint16, protocol int32) ([]byte, error) {
	var payload bytes.Buffer
	if err := WriteVarInt(&payload, 0x00); err != nil {
		return nil, err
	}
	if err := WriteVarInt(&payload, protocol); err != nil {
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
