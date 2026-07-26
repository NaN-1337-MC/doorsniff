package query

import (
	"bytes"
	"fmt"
	"strconv"
	"strings"
)

// Response is the parsed result of a full-stat Query. All fields come
// from the K/V section of the response; PlayerNames comes from the
// player list section.
type Response struct {
	MOTD        string
	GameType    string
	GameID      string
	Version     string
	Plugins     string // raw Bukkit-format string, e.g. "Paper 1.21.1: Essentials; Vault"
	Map         string
	NumPlayers  int
	MaxPlayers  int
	HostPort    uint16
	HostIP      string
	PlayerNames []string
}

// splitnum is the 11-byte padding that precedes the K/V section in a
// full-stat response: "splitnum\0" + 0x80 + 0x00.
var splitnum = []byte("splitnum\x00\x80\x00")

// playerSep is the 10-byte separator between the K/V section and the
// player list: 0x01 + "player_\0\0".
var playerSep = []byte{0x01, 'p', 'l', 'a', 'y', 'e', 'r', '_', 0x00, 0x00}

// ParseFullStat parses the raw payload of a full-stat Query response
// (the bytes after the 5-byte header). The expected layout is:
//
//	padding(11 bytes) + K/V pairs + playerSep(10 bytes) + player names
func ParseFullStat(data []byte) (*Response, error) {
	if len(data) < len(splitnum)+len(playerSep) {
		return nil, fmt.Errorf("%w: %d bytes", ErrResponseShort, len(data))
	}

	// Split around the player separator.
	kvPart, playersPart, ok := bytes.Cut(data, playerSep)
	if !ok {
		return nil, fmt.Errorf("%w: player separator not found", ErrResponseShort)
	}

	// Strip the 11-byte splitnum prefix from the K/V section.
	if !bytes.HasPrefix(kvPart, splitnum) {
		return nil, fmt.Errorf("%w: splitnum prefix missing", ErrResponseShort)
	}
	kvPart = kvPart[len(splitnum):]

	// Parse K/V pairs — a sequence of null-terminated strings, key then
	// value alternating, terminated by an empty key.
	kv := parseKVPairs(kvPart)

	resp := &Response{
		MOTD:       kv["hostname"],
		GameType:   kv["gametype"],
		GameID:     kv["game_id"],
		Version:    kv["version"],
		Plugins:    kv["plugins"],
		Map:        kv["map"],
		HostIP:     kv["hostip"],
		PlayerNames: parsePlayerNames(playersPart),
	}

	if v, ok := kv["numplayers"]; ok {
		resp.NumPlayers, _ = strconv.Atoi(v)
	}
	if v, ok := kv["maxplayers"]; ok {
		resp.MaxPlayers, _ = strconv.Atoi(v)
	}
	if v, ok := kv["hostport"]; ok {
		port, _ := strconv.ParseUint(v, 10, 16)
		resp.HostPort = uint16(port)
	}

	return resp, nil
}

// parseKVPairs splits a null-terminated string sequence into a map.
// The sequence is key\0value\0key\0value\0...\0 (empty key terminates).
func parseKVPairs(data []byte) map[string]string {
	out := make(map[string]string)
	for {
		key, rest, ok := readCString(data)
		if !ok || key == "" {
			break
		}
		val, rest, ok := readCString(rest)
		if !ok {
			break
		}
		out[key] = val
		data = rest
	}
	return out
}

// parsePlayerNames splits a null-terminated sequence of player names.
// The sequence ends with an empty string (double null).
func parsePlayerNames(data []byte) []string {
	var names []string
	for {
		name, rest, ok := readCString(data)
		if !ok || name == "" {
			break
		}
		names = append(names, name)
		data = rest
	}
	return names
}

// readCString reads a null-terminated string from data and returns
// the string, the remaining bytes, and whether the read was successful.
func readCString(data []byte) (string, []byte, bool) {
	i := bytes.IndexByte(data, 0)
	if i < 0 {
		return "", nil, false
	}
	return string(data[:i]), data[i+1:], true
}

// ParsePlugins parses the Bukkit-format plugins string into a
// structured slice. The format is:
//
//	ServerName on ServerVersion: Plugin1 v1.0; Plugin2 v2.0
//
// If the string is empty, nil is returned.
func ParsePlugins(raw string) []struct{ Name, Version string } {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}

	// Strip the "ServerName on ServerVersion: " prefix if present.
	if idx := strings.Index(raw, ": "); idx >= 0 {
		raw = raw[idx+2:]
	}

	var result []struct{ Name, Version string }
	for _, item := range strings.Split(raw, ";") {
		item = strings.TrimSpace(item)
		if item == "" {
			continue
		}
		parts := strings.SplitN(item, " ", 2)
		name := parts[0]
		version := ""
		if len(parts) > 1 {
			version = parts[1]
		}
		result = append(result, struct{ Name, Version string }{name, version})
	}
	return result
}
