package query

import (
	"reflect"
	"testing"
)

// buildFullStatPayload builds a synthetic full-stat response payload
// from the given K/V pairs and player names. This mirrors what the
// server sends after the 5-byte header.
func buildFullStatPayload(kv map[string]string, players []string) []byte {
	var buf []byte

	// 11-byte splitnum prefix.
	buf = append(buf, splitnum...)

	// K/V pairs: key\0value\0...
	for k, v := range kv {
		buf = append(buf, k...)
		buf = append(buf, 0)
		buf = append(buf, v...)
		buf = append(buf, 0)
	}
	// Empty key terminates the K/V section.
	buf = append(buf, 0)

	// Player separator.
	buf = append(buf, playerSep...)

	// Player names.
	for _, p := range players {
		buf = append(buf, p...)
		buf = append(buf, 0)
	}
	// Empty name terminates the player section.
	buf = append(buf, 0)

	return buf
}

func TestParseFullStat(t *testing.T) {
	payload := buildFullStatPayload(
		map[string]string{
			"hostname":   "A Minecraft Server",
			"gametype":   "SMP",
			"game_id":    "MINECRAFT",
			"version":    "1.21.1",
			"plugins":    "Paper on 1.21.1-R0.1-SNAPSHOT: Essentials; Vault",
			"map":        "world",
			"numplayers": "5",
			"maxplayers": "100",
			"hostport":   "25565",
			"hostip":     "127.0.0.1",
		},
		[]string{"Steve", "Alex"},
	)

	resp, err := ParseFullStat(payload)
	if err != nil {
		t.Fatalf("ParseFullStat: %v", err)
	}

	tests := []struct {
		name string
		got  any
		want any
	}{
		{"MOTD", resp.MOTD, "A Minecraft Server"},
		{"GameType", resp.GameType, "SMP"},
		{"GameID", resp.GameID, "MINECRAFT"},
		{"Version", resp.Version, "1.21.1"},
		{"Plugins", resp.Plugins, "Paper on 1.21.1-R0.1-SNAPSHOT: Essentials; Vault"},
		{"Map", resp.Map, "world"},
		{"NumPlayers", resp.NumPlayers, 5},
		{"MaxPlayers", resp.MaxPlayers, 100},
		{"HostPort", resp.HostPort, uint16(25565)},
		{"HostIP", resp.HostIP, "127.0.0.1"},
		{"PlayerNames", resp.PlayerNames, []string{"Steve", "Alex"}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if tt.name == "PlayerNames" {
				if !reflect.DeepEqual(tt.got, tt.want) {
					t.Errorf("got %v, want %v", tt.got, tt.want)
				}
				return
			}
			if tt.got != tt.want {
				t.Errorf("got %v, want %v", tt.got, tt.want)
			}
		})
	}
}

func TestParseFullStatEmptyPlayers(t *testing.T) {
	payload := buildFullStatPayload(
		map[string]string{
			"hostname":   "Empty Server",
			"gametype":   "SMP",
			"numplayers": "0",
			"maxplayers": "20",
		},
		nil,
	)

	resp, err := ParseFullStat(payload)
	if err != nil {
		t.Fatalf("ParseFullStat: %v", err)
	}
	if resp.MOTD != "Empty Server" {
		t.Errorf("MOTD = %q, want %q", resp.MOTD, "Empty Server")
	}
	if resp.NumPlayers != 0 {
		t.Errorf("NumPlayers = %d, want 0", resp.NumPlayers)
	}
	if len(resp.PlayerNames) != 0 {
		t.Errorf("PlayerNames = %v, want empty", resp.PlayerNames)
	}
}

func TestParseFullStatShort(t *testing.T) {
	_, err := ParseFullStat([]byte{0x00, 0x00})
	if err == nil {
		t.Fatal("expected error for short payload")
	}
}

func TestParseFullStatMissingSeparator(t *testing.T) {
	// Valid splitnum prefix but no player separator.
	data := append([]byte{}, splitnum...)
	data = append(data, "hostname\x00Test\x00\x00"...)
	_, err := ParseFullStat(data)
	if err == nil {
		t.Fatal("expected error for missing player separator")
	}
}

func TestParsePlugins(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want int // number of plugins
	}{
		{"empty", "", 0},
		{"whitespace", "  ", 0},
		{"server prefix", "Paper on 1.21.1-R0.1-SNAPSHOT: Essentials; Vault", 2},
		{"no prefix", "Essentials; Vault", 2},
		{"single", "WorldEdit", 1},
		{"with versions", "Essentials 2.19.0; Vault 1.7.3", 2},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := ParsePlugins(tt.raw)
			if len(result) != tt.want {
				t.Errorf("ParsePlugins(%q) returned %d plugins, want %d: %v",
					tt.raw, len(result), tt.want, result)
			}
		})
	}
}

func TestParsePluginsStructure(t *testing.T) {
	result := ParsePlugins("Paper on 1.21.1-R0.1-SNAPSHOT: Essentials 2.19.0; Vault 1.7.3")
	if len(result) != 2 {
		t.Fatalf("got %d plugins, want 2", len(result))
	}
	if result[0].Name != "Essentials" || result[0].Version != "2.19.0" {
		t.Errorf("plugin 0 = %+v", result[0])
	}
	if result[1].Name != "Vault" || result[1].Version != "1.7.3" {
		t.Errorf("plugin 1 = %+v", result[1])
	}
}

func TestReadCString(t *testing.T) {
	data := []byte("hello\x00world\x00")
	s, rest, ok := readCString(data)
	if !ok || s != "hello" || len(rest) != 6 {
		t.Errorf("first read: %q, %v, %v", s, rest, ok)
	}
	s, rest, ok = readCString(rest)
	if !ok || s != "world" || len(rest) != 0 {
		t.Errorf("second read: %q, rest len=%d, ok=%v", s, len(rest), ok)
	}
	_, _, ok = readCString(rest)
	if ok {
		t.Error("expected ok=false for empty data")
	}
}
