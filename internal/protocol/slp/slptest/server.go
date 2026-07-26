// Package slptest provides a fake Minecraft SLP server for
// integration tests. It speaks both modern 1.7+ and legacy 1.6
// protocols and can be configured to RST the connection to trigger
// the scanner's legacy fallback path.
package slptest

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"unicode/utf16"

	"github.com/renanbray/swoomps/internal/protocol/slp"
)

// Config holds the creation parameters for a FakeServer.
type Config struct {
	// Addr is the address to listen on. When empty, defaults to
	// "127.0.0.1:0" (OS-assigned port).
	Addr string

	// Version is the modern (1.7+) SLP JSON response sent to clients.
	Version slp.Response

	// Legacy, when true, causes the server to RST modern handshakes
	// (first byte 0x00) and only respond to legacy 1.6 pings
	// (first byte 0xFE). This triggers the scanner's fallback path.
	Legacy bool

	// LegacyKick holds the kick packet content sent in response to
	// a legacy 1.6 ping. When zero-valued, a sensible default is used.
	LegacyKick LegacyKick
}

// FakeServer is a test-double Minecraft server that speaks SLP.
// Create one with Start; it listens on 127.0.0.1:0 (OS-assigned port).
type FakeServer struct {
	cfg  Config
	conn net.Listener
	wg   sync.WaitGroup
}

// LegacyKick holds the fields of a 1.6 legacy kick packet.
type LegacyKick struct {
	Protocol   string
	Version    string
	MOTD       string
	NumPlayers int
	MaxPlayers int
}

// Start creates a FakeServer, starts listening on 127.0.0.1:0, and
// returns it. The server is stopped automatically when the test ends.
func Start(t *testing.T, cfg Config) *FakeServer {
	t.Helper()

	addr := cfg.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	l, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("slptest: listen %s: %v", addr, err)
	}

	if cfg.LegacyKick == (LegacyKick{}) {
		cfg.LegacyKick = LegacyKick{
			Protocol:   "47",
			Version:    "1.8.9",
			MOTD:       "Test Server",
			NumPlayers: 5,
			MaxPlayers: 20,
		}
	}

	fs := &FakeServer{
		cfg:  cfg,
		conn: l,
	}

	go fs.accept()

	t.Cleanup(func() {
		_ = l.Close()
		fs.wg.Wait()
	})

	return fs
}

// Addr returns the host:port string suitable for seed files.
func (fs *FakeServer) Addr() string {
	return fs.conn.Addr().String()
}

// Port returns the OS-assigned port.
func (fs *FakeServer) Port() uint16 {
	return uint16(fs.conn.Addr().(*net.TCPAddr).Port)
}

func (fs *FakeServer) accept() {
	for {
		conn, err := fs.conn.Accept()
		if err != nil {
			return
		}
		fs.wg.Add(1)
		go func() {
			defer fs.wg.Done()
			fs.handle(conn)
		}()
	}
}

// handle processes one client connection. The first byte determines
// the protocol:
//
//	0xFE → Legacy SLP (1.6):  [0xFE][0x01]  (no length prefix)
//	other → Modern SLP (1.7+): [varint len][varint 0x00][handshake...][status request]
func (fs *FakeServer) handle(conn net.Conn) {
	defer conn.Close()

	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.SetNoDelay(true)
	}

	r := bufio.NewReader(conn)

	// Peek the first byte. Legacy pings start with 0xFE (no
	// length prefix); modern handshakes start with a varint
	// length prefix (the body always starts with 0x00).
	first, err := r.Peek(1)
	if err != nil {
		slog.Debug("slptest: peek first byte", "err", err)
		return
	}

	switch {
	case first[0] == 0xFE:
		// Legacy 1.6 ping: consume the 2-byte sequence 0xFE 0x01.
		_, _ = r.ReadByte()
		_, _ = r.ReadByte()
		fs.respondLegacy(conn)

	case first[0] == 0x00 && fs.cfg.Legacy:
		// Modern handshake on a legacy-only server: RST.
		forceRST(conn)

	default:
		// Modern SLP: first byte is the varint length prefix of
		// the handshake packet. readPacket reads [varint len]
		// [body], then we read the status request the same way.
		fs.readPacket(r) // handshake
		if fs.cfg.Legacy {
			forceRST(conn)
			return
		}
		fs.readPacket(r) // status request
		fs.respondModern(conn)
	}
}

// readPacket reads one length-prefixed packet and discards the body.
// It reads [varint length][length bytes of body].
func (fs *FakeServer) readPacket(r *bufio.Reader) int32 {
	length, err := slp.ReadVarInt(r)
	if err != nil {
		slog.Debug("slptest: readPacket ReadVarInt", "err", err)
		return -1
	}
	if length <= 0 || length > 4096 {
		slog.Debug("slptest: readPacket invalid length", "length", length)
		return -1
	}
	n, err := io.CopyN(io.Discard, r, int64(length))
	if err != nil {
		slog.Debug("slptest: readPacket body", "err", err, "n", n, "want", length)
	}
	return length
}

// respondModern writes a modern SLP JSON response.
func (fs *FakeServer) respondModern(conn net.Conn) {
	data, err := json.Marshal(fs.cfg.Version)
	if err != nil {
		slog.Debug("slptest: marshal response", "err", err)
		return
	}

	// Inner packet: [varint 0x00][string JSON]
	var payload bytes.Buffer
	if err := slp.WriteVarInt(&payload, 0x00); err != nil {
		slog.Debug("slptest: write packet id", "err", err)
		return
	}
	if err := slp.WriteString(&payload, string(data)); err != nil {
		slog.Debug("slptest: write json string", "err", err)
		return
	}

	// Outer: [varint length][payload]
	if err := slp.WriteVarInt(conn, int32(payload.Len())); err != nil {
		slog.Debug("slptest: write length", "err", err)
		return
	}
	if _, err := conn.Write(payload.Bytes()); err != nil {
		slog.Debug("slptest: write payload", "err", err)
	}
}

// respondLegacy writes a 1.6 legacy kick packet (UTF-16BE).
// The format expected by parseLegacySections is:
//
//	\0§1\0<proto>\0§2\0<ver>\0§3\0<motd>\0§4\0<num>\0§5\0<max>\0
//
// The leading \0 is because the decoded string starts with a null
// byte (from the wire encoding), and parseLegacySections splits on
// null and discards the first empty element.
func (fs *FakeServer) respondLegacy(conn net.Conn) {
	lk := fs.cfg.LegacyKick

	// Build the sections string with leading null byte.
	s := "\x00"
	s += "\u00a71" + "\x00" + lk.Protocol
	s += "\x00" + "\u00a72" + "\x00" + lk.Version
	s += "\x00" + "\u00a73" + "\x00" + lk.MOTD
	s += "\x00" + "\u00a74" + "\x00" + itoa(lk.NumPlayers)
	s += "\x00" + "\u00a75" + "\x00" + itoa(lk.MaxPlayers)
	s += "\x00"

	// Encode as UTF-16BE.
	u16 := utf16.Encode([]rune(s))
	body := make([]byte, len(u16)*2)
	for i, r := range u16 {
		binary.BigEndian.PutUint16(body[i*2:], r)
	}

	// Kick packet: [0xFF][ushort BE length][UTF-16BE data]
	var resp []byte
	resp = append(resp, 0xFF)
	var lenBuf [2]byte
	binary.BigEndian.PutUint16(lenBuf[:], uint16(len(body)))
	resp = append(resp, lenBuf[:]...)
	resp = append(resp, body...)

	if _, err := conn.Write(resp); err != nil {
		slog.Debug("slptest: write legacy response", "err", err)
	}
}

// forceRST closes the connection with SO_LINGER=0, which causes the
// kernel to send a TCP RST instead of a graceful FIN. The client
// sees ECONNRESET.
func forceRST(conn net.Conn) {
	if tc, ok := conn.(*net.TCPConn); ok {
		_ = tc.SetLinger(0)
	}
	_ = conn.Close()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}
