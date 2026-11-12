package slp

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"io"
	"net"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

// TestFallback_PrimaryAccepted_NoRetry simulates an e4mc-style
// proxy: it refuses the handshake when the client uses the
// protocol-agnostic -1 sentinel (drops the TCP connection), but
// responds normally when the client uses a real modern protocol
// (768). Since the primary protocol is 768, the very first
// attempt must succeed and there must be no fallback retry —
// this is critical for scanner throughput on the happy path.
func TestFallback_PrimaryAccepted_NoRetry(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			handleConditionally(c, func(protocol int32) bool {
				return protocol != -1
			})
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, _, err := Ping(ctx, host, port, 2*time.Second)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}
	if resp.Version.Name != "1.21.1" {
		t.Errorf("version.name = %q, want 1.21.1", resp.Version.Name)
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("server saw %d connections, want 1 (primary 768 succeeded, no retry)", got)
	}
}

// TestFallback_Rejects768_AcceptsMinus1 simulates the inverse case
// — a server that only accepts the protocol-agnostic -1 sentinel
// on the status path. The primary 768 attempt must be rejected
// (connection closed) and the -1 fallback must succeed.
func TestFallback_Rejects768_AcceptsMinus1(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			handleConditionally(c, func(protocol int32) bool {
				return protocol == -1
			})
			_ = c.Close()
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, _, err := Ping(ctx, host, port, 2*time.Second)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if resp == nil {
		t.Fatal("resp is nil")
	}
	if resp.Version.Name != "1.21.1" {
		t.Errorf("version.name = %q, want 1.21.1", resp.Version.Name)
	}
	if got := attempts.Load(); got != 2 {
		t.Errorf("server saw %d connections, want 2 (primary 768 + fallback -1)", got)
	}
}

// TestFallback_NoRetryOnDialFailure points Ping at a port nothing
// is listening on. The dial must fail immediately and Ping must
// not retry — the dial phase happens only once. We assert this by
// observing the elapsed time stays well below two full timeouts.
func TestFallback_NoRetryOnDialFailure(t *testing.T) {
	t.Parallel()

	// Bind a listener just to discover a port we can immediately
	// close; the OS won't reassign it instantly so the connect
	// below fails fast with ECONNREFUSED (not a timeout).
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen probe: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	host, port := splitHostPort(t, addr)
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	start := time.Now()
	_, _, err = Ping(ctx, host, port, 1500*time.Millisecond)
	elapsed := time.Since(start)

	if err == nil {
		t.Fatal("Ping succeeded against closed port; expected dial error")
	}
	if !strings.Contains(err.Error(), "dialing") {
		t.Errorf("err = %v, want dial error", err)
	}
	// A retry would have made elapsed >= 2*timeout roughly. Allow
	// generous slack but fail if it looks like a second attempt.
	if elapsed > 2200*time.Millisecond {
		t.Errorf("elapsed = %v, suggests a retry happened; want < 2.2s", elapsed)
	}
}

// TestFallback_NoRetryOnParseError covers the case where the
// server accepted the connection and replied, but the response is
// malformed. Ping must surface the parse error directly without
// retrying — a retry would not help. We verify by binding a
// server that replies with garbage on every connection.
func TestFallback_NoRetryOnParseError(t *testing.T) {
	t.Parallel()

	var attempts atomic.Int64
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer func() { _ = ln.Close() }()

	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			attempts.Add(1)
			_ = c.SetDeadline(time.Now().Add(2 * time.Second))
			// Drain the entire handshake + status request so the
			// client's read is unblocked.
			r := bufio.NewReader(c)
			_, _ = readHandshakeProtocol(r)
			_, _ = r.ReadByte()
			_, _ = r.ReadByte()
			// Send a well-framed packet whose packet id is not
			// 0x00. The client should report "unexpected response
			// packet id" without falling back.
			var resp bytes.Buffer
			_ = WriteVarInt(&resp, 2) // packet length
			_ = WriteVarInt(&resp, 7) // packet id != 0
			_ = WriteString(&resp, "x")
			_, _ = c.Write(resp.Bytes())
			_ = c.Close()
		}
	}()

	host, port := splitHostPort(t, ln.Addr().String())
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	_, _, err = Ping(ctx, host, port, 2*time.Second)
	if err == nil {
		t.Fatal("Ping succeeded with bad packet id; expected error")
	}
	if got := attempts.Load(); got != 1 {
		t.Errorf("server saw %d connections, want 1 (no fallback on parse)", got)
	}
}

// TestIsHandshakeRejectionClassification verifies the classifier
// directly so future refactors cannot accidentally retry on dial
// failures or on parse errors.
func TestIsHandshakeRejectionClassification(t *testing.T) {
	t.Parallel()

	wrappedEOF := func(suffix string) error {
		return fmt.Errorf("%s: %w", suffix, io.EOF)
	}
	wrappedUnexpectedEOF := func(suffix string) error {
		return fmt.Errorf("%s: %w", suffix, io.ErrUnexpectedEOF)
	}
	wrappedRST := func(suffix string) error {
		return fmt.Errorf("%s: %w", suffix, syscall.ECONNRESET)
	}

	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil", err: nil, want: false},
		{name: "dial refused", err: errStr("dialing 1.2.3.4:25565: connect: connection refused"), want: false},
		{name: "dial timeout", err: errStr("dialing 1.2.3.4:25565: i/o timeout"), want: false},
		{name: "wrapped ECONNRESET", err: wrappedRST("sending handshake"), want: true},
		{name: "string ECONNRESET", err: errStr("sending handshake: read tcp: connection reset by peer"), want: true},
		{name: "wrapped EOF", err: wrappedEOF("reading response length"), want: true},
		{name: "wrapped unexpected EOF", err: wrappedUnexpectedEOF("reading response body"), want: true},
		{name: "parse error", err: errStr("invalid character 'n' looking for beginning of value"), want: false},
		{name: "invalid length", err: errStr("response length too large: 99999999"), want: false},
		{name: "bad packet id", err: errStr("unexpected response packet id: 7 (want 0)"), want: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isHandshakeRejection(tt.err)
			if got != tt.want {
				t.Errorf("isHandshakeRejection(%q) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

// handleConditionally is a tiny fake SLP server. It reads the
// handshake packet, decides what to do based on the protocol
// version it sees, and either replies with a canned status or
// drops the connection (forcing the client to see ECONNRESET or
// EOF on its next read).
//
// accept returns true to respond, false to drop the connection.
// This emulates both the e4mc-style ("accept only real protocols")
// and the old-server-style ("accept only -1") proxies.
func handleConditionally(c net.Conn, accept func(protocol int32) bool) {
	_ = c.SetDeadline(time.Now().Add(2 * time.Second))

	reader := bufio.NewReader(c)
	protocol, err := readHandshakeProtocol(reader)
	if err != nil {
		// Could not parse handshake — drop the connection
		// without responding. That is still a "handshake
		// rejection" signal for the client.
		rejectConn(c)
		return
	}

	if !accept(protocol) {
		rejectConn(c)
		return
	}

	// Drain the status request (1 byte length, 1 byte 0x00).
	_, _ = reader.ReadByte()
	_, _ = reader.ReadByte()

	// Send a canned status response. The packet is
	// length-prefixed: [varint N][varint 0x00][string JSON].
	body := []byte(`{"version":{"name":"1.21.1","protocol":768},"players":{"max":1,"online":0},"description":"ok"}`)
	var inner bytes.Buffer
	if err := WriteVarInt(&inner, 0x00); err != nil {
		rejectConn(c)
		return
	}
	if err := WriteString(&inner, string(body)); err != nil {
		rejectConn(c)
		return
	}
	var resp bytes.Buffer
	if err := WriteVarInt(&resp, int32(inner.Len())); err != nil {
		rejectConn(c)
		return
	}
	resp.Write(inner.Bytes())
	if _, err := c.Write(resp.Bytes()); err != nil {
		return
	}
	// Clean close so the client sees a normal end-of-stream rather
	// than a RST.
	_ = c.Close()
}

// rejectConn closes the connection with Linger(0) so the peer
// observes ECONNRESET instead of a clean FIN. Used to emulate
// proxies that abort the handshake.
func rejectConn(c net.Conn) {
	if tcp, ok := c.(*net.TCPConn); ok {
		_ = tcp.SetLinger(0)
	}
	_ = c.Close()
}

// readHandshakeProtocol parses just enough of the handshake to
// extract the protocol version. It expects the framing that
// buildHandshake produces: a varint length, then packet id 0x00,
// then a varint protocol.
func readHandshakeProtocol(r *bufio.Reader) (int32, error) {
	_, err := ReadVarInt(r) // packet length
	if err != nil {
		return 0, err
	}
	pid, err := ReadVarInt(r) // packet id (0x00)
	if err != nil {
		return 0, err
	}
	if pid != 0x00 {
		return 0, errStr("unexpected packet id in handshake")
	}
	return ReadVarInt(r)
}

type stringError string

func (s stringError) Error() string { return string(s) }

func errStr(s string) error { return stringError(s) }

// splitHostPort returns host and uint16 port for a "host:port"
// address (the format net.Listen produces). It fails the test on
// parse error rather than panicking.
func splitHostPort(t *testing.T, addr string) (string, uint16) {
	t.Helper()
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		t.Fatalf("SplitHostPort(%q): %v", addr, err)
	}
	var port uint16
	for _, c := range portStr {
		if c < '0' || c > '9' {
			t.Fatalf("non-digit in port %q", portStr)
		}
		port = port*10 + uint16(c-'0')
	}
	return host, port
}
