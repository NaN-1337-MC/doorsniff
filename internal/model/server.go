// Package model defines the core domain types used across the scanner.
package model

import (
	"net"
	"strconv"
	"time"
)

// Target is a single probe destination. The scanner pipeline produces
// Targets from seeds, port ranges, and CIDR expansion.
type Target struct {
	IP   string
	Port uint16
}

// Address returns "ip:port", the form used by net.Dial.
func (t Target) Address() string {
	return net.JoinHostPort(t.IP, strconv.Itoa(int(t.Port)))
}

// SamplePlayer is one entry from the SLP `players.sample` array.
type SamplePlayer struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

// Plugin is one entry from the Query stat `plugins` list.
type Plugin struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
}

// Server is a discovered Minecraft server. The zero value is not a valid
// server — use IsZero to test.
type Server struct {
	IP              string         `json:"ip"`
	Port            uint16         `json:"port"`
	FirstSeen       time.Time      `json:"first_seen"`
	LastSeen        time.Time      `json:"last_seen"`
	LastLatencyMs   int64          `json:"last_latency_ms,omitempty"`
	VersionProtocol int            `json:"version_protocol,omitempty"`
	VersionName     string         `json:"version_name,omitempty"`
	MOTD            string         `json:"motd,omitempty"`
	NumPlayers      int            `json:"num_players,omitempty"`
	MaxPlayers      int            `json:"max_players,omitempty"`
	SamplePlayers   []SamplePlayer `json:"sample_players,omitempty"`
	QueryEnabled    bool           `json:"query_enabled"`
	Plugins         []Plugin       `json:"plugins,omitempty"`
	Map             string         `json:"map,omitempty"`
	GameType        string         `json:"gametype,omitempty"`
	Country         string         `json:"country,omitempty"`
	RawQuery        string         `json:"raw_query,omitempty"`
	ProbeKind       string         `json:"probe_kind,omitempty"`
	LoginMode       string         `json:"login_mode,omitempty"`
}

// Address returns "ip:port".
func (s Server) Address() string {
	return net.JoinHostPort(s.IP, strconv.Itoa(int(s.Port)))
}

// IsZero reports whether s is the zero Server value.
func (s Server) IsZero() bool {
	return s.IP == "" && s.Port == 0
}

// ProbeKind values, used to record how a server was discovered.
const (
	ProbeSLP     = "slp"
	ProbeQuery   = "query"
	ProbeSLPAndQ = "slp+query"
	ProbeLegacy  = "slp-legacy"
)

// Seed is a configured starting point. The MVP keeps seeds in a flat
// file; the Seed type is what the scanner reads into memory.
type Seed struct {
	Host    string
	Port    uint16
	AddedAt time.Time
}

// Address returns "host:port" (the port defaulting to 25565 if unset).
func (s Seed) Address() string {
	port := s.Port
	if port == 0 {
		port = 25565
	}
	return net.JoinHostPort(s.Host, strconv.Itoa(int(port)))
}
