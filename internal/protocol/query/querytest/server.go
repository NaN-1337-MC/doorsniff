// Package querytest provides a fake Minecraft Query server for
// integration tests. It speaks the UDP Query protocol (handshake +
// full stat) and can be configured with custom server properties.
package querytest

import (
	"encoding/binary"
	"fmt"
	"log/slog"
	"net"
	"strconv"
	"testing"
	"time"
)

// Config holds the creation parameters for a FakeServer.
type Config struct {
	// Addr is the address to listen on. When empty, defaults to
	// "127.0.0.1:0" (OS-assigned port).
	Addr string

	// ChallengeToken is the token sent in the handshake response.
	// When 0, defaults to 1234567.
	ChallengeToken int32

	// MOTD is the hostname key in the full-stat response.
	MOTD string

	// GameType is the gametype key (always "SMP" in practice).
	GameType string

	// Version is the version key (e.g. "1.21.1").
	Version string

	// Plugins is the raw Bukkit-format plugins string.
	Plugins string

	// Map is the map key (e.g. "world").
	Map string

	// NumPlayers is the numplayers key.
	NumPlayers int

	// MaxPlayers is the maxplayers key.
	MaxPlayers int

	// HostPort is the hostport key (should match the listening port).
	HostPort uint16

	// HostIP is the hostip key.
	HostIP string

	// PlayerNames is the list of player names in the full-stat response.
	PlayerNames []string
}

// FakeServer is a test-double Minecraft server that speaks Query (UDP).
// Create one with Start; it listens on 127.0.0.1:0 (OS-assigned port).
type FakeServer struct {
	cfg  Config
	conn *net.UDPConn
}

// Start creates a FakeServer, starts listening on 127.0.0.1:0, and
// returns it. The server is stopped automatically when the test ends.
func Start(t *testing.T, cfg Config) *FakeServer {
	t.Helper()

	addr := cfg.Addr
	if addr == "" {
		addr = "127.0.0.1:0"
	}

	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		t.Fatalf("querytest: resolve %s: %v", addr, err)
	}

	conn, err := net.ListenUDP("udp", udpAddr)
	if err != nil {
		t.Fatalf("querytest: listen %s: %v", addr, err)
	}

	if cfg.ChallengeToken == 0 {
		cfg.ChallengeToken = 1234567
	}
	if cfg.MOTD == "" {
		cfg.MOTD = "A Minecraft Server"
	}
	if cfg.GameType == "" {
		cfg.GameType = "SMP"
	}
	if cfg.Version == "" {
		cfg.Version = "1.21.1"
	}
	if cfg.Map == "" {
		cfg.Map = "world"
	}
	if cfg.HostIP == "" {
		cfg.HostIP = "127.0.0.1"
	}
	if cfg.HostPort == 0 {
		cfg.HostPort = uint16(conn.LocalAddr().(*net.UDPAddr).Port)
	}

	fs := &FakeServer{
		cfg:  cfg,
		conn: conn,
	}

	go fs.serve()

	t.Cleanup(func() {
		_ = conn.Close()
	})

	return fs
}

// Addr returns the host:port string suitable for dialling.
func (fs *FakeServer) Addr() string {
	return fs.conn.LocalAddr().String()
}

// Host returns just the host portion (127.0.0.1).
func (fs *FakeServer) Host() string {
	return "127.0.0.1"
}

// Port returns the OS-assigned port.
func (fs *FakeServer) Port() uint16 {
	return uint16(fs.conn.LocalAddr().(*net.UDPAddr).Port)
}

func (fs *FakeServer) serve() {
	buf := make([]byte, 4096)
	for {
		n, clientAddr, err := fs.conn.ReadFromUDP(buf)
		if err != nil {
			return
		}
		go fs.handlePacket(buf[:n], clientAddr)
	}
}

// handlePacket processes one incoming UDP packet.
func (fs *FakeServer) handlePacket(data []byte, clientAddr *net.UDPAddr) {
	// Minimum packet: magic(2) + type(1) + sessionID(4) = 7 bytes.
	if len(data) < 7 {
		slog.Debug("querytest: packet too short", "len", len(data))
		return
	}
	// Verify magic.
	if data[0] != 0xFE || data[1] != 0xFD {
		slog.Debug("querytest: bad magic", "first", fmt.Sprintf("0x%02X%02X", data[0], data[1]))
		return
	}

	pktType := data[2]
	sessionID := int32(binary.BigEndian.Uint32(data[3:7]))

	switch pktType {
	case 0x09:
		fs.respondHandshake(sessionID, clientAddr)
	case 0x00:
		fs.respondFullStat(sessionID, clientAddr)
	default:
		slog.Debug("querytest: unknown type", "type", fmt.Sprintf("0x%02X", pktType))
	}
}

// respondHandshake sends the challenge token as a null-terminated string.
func (fs *FakeServer) respondHandshake(sessionID int32, clientAddr *net.UDPAddr) {
	// Response: type(1) + sessionID(4) + token_string\0
	tokenStr := strconv.FormatInt(int64(fs.cfg.ChallengeToken), 10)
	resp := make([]byte, 5+len(tokenStr)+1)
	resp[0] = 0x09
	binary.BigEndian.PutUint32(resp[1:5], uint32(sessionID))
	copy(resp[5:], tokenStr)
	resp[5+len(tokenStr)] = 0 // null terminator

	if _, err := fs.conn.WriteToUDP(resp, clientAddr); err != nil {
		slog.Debug("querytest: write handshake response", "err", err)
	}
}

// respondFullStat sends a full-stat response payload.
func (fs *FakeServer) respondFullStat(sessionID int32, clientAddr *net.UDPAddr) {
	var payload []byte

	// 11-byte splitnum prefix: "splitnum\0" + 0x80 + 0x00.
	payload = append(payload, "splitnum\x00\x80\x00"...)

	// K/V pairs.
	kv := map[string]string{
		"hostname":   fs.cfg.MOTD,
		"gametype":   fs.cfg.GameType,
		"game_id":    "MINECRAFT",
		"version":    fs.cfg.Version,
		"plugins":    fs.cfg.Plugins,
		"map":        fs.cfg.Map,
		"numplayers": strconv.Itoa(fs.cfg.NumPlayers),
		"maxplayers": strconv.Itoa(fs.cfg.MaxPlayers),
		"hostport":   strconv.FormatUint(uint64(fs.cfg.HostPort), 10),
		"hostip":     fs.cfg.HostIP,
	}

	// Write K/V in deterministic order for testability.
	kvOrder := []string{
		"hostname", "gametype", "game_id", "version", "plugins",
		"map", "numplayers", "maxplayers", "hostport", "hostip",
	}
	for _, k := range kvOrder {
		v := kv[k]
		payload = append(payload, k...)
		payload = append(payload, 0)
		payload = append(payload, v...)
		payload = append(payload, 0)
	}
	// Empty key terminates the K/V section.
	payload = append(payload, 0)

	// Player separator: 0x01 + "player_\0\0"
	payload = append(payload, 0x01, 'p', 'l', 'a', 'y', 'e', 'r', '_', 0x00, 0x00)

	// Player names.
	for _, name := range fs.cfg.PlayerNames {
		payload = append(payload, name...)
		payload = append(payload, 0)
	}
	// Empty name terminates the player section.
	payload = append(payload, 0)

	// Full response: type(1) + sessionID(4) + payload
	resp := make([]byte, 5+len(payload))
	resp[0] = 0x00
	binary.BigEndian.PutUint32(resp[1:5], uint32(sessionID))
	copy(resp[5:], payload)

	if _, err := fs.conn.WriteToUDP(resp, clientAddr); err != nil {
		slog.Debug("querytest: write full stat response", "err", err)
	}
}

// AddrPort returns the address as host:port (like net.JoinHostPort).
func (fs *FakeServer) AddrPort() string {
	return net.JoinHostPort(fs.cfg.HostIP, strconv.Itoa(int(fs.Port())))
}

// WaitForReady blocks until the server is ready or the timeout expires.
// This is a no-op for UDP (always ready after Listen), but matches the
// pattern of TCP servers for API consistency.
func (fs *FakeServer) WaitForReady(timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		// Try to dial to verify the port is open.
		conn, err := net.DialTimeout("udp", fs.conn.LocalAddr().String(), 100*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		time.Sleep(10 * time.Millisecond)
	}
	return fmt.Errorf("querytest: server not ready after %v", timeout)
}
