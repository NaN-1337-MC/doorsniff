package slp

import (
	"encoding/binary"
	"strings"
	"testing"
	"unicode/utf16"
)

// buildLegacyKick returns the body of a 1.6 kick packet (everything
// from the leading 0xFF onwards) for the given section map. The map
// keys are the §N markers and values are the section contents.
func buildLegacyKick(t *testing.T, sections map[string]string) []byte {
	t.Helper()

	var sb strings.Builder
	sb.WriteByte(0x00) // leading null
	for _, key := range []string{"§1", "§2", "§3", "§4", "§5"} {
		sb.WriteString(key)
		sb.WriteByte(0x00)
		sb.WriteString(sections[key])
		sb.WriteByte(0x00)
	}
	decoded := sb.String()

	// Encode to UTF-16BE.
	u16 := utf16.Encode([]rune(decoded))
	body := make([]byte, 0, 2+len(u16)*2)
	lenBuf := make([]byte, 2)
	binary.BigEndian.PutUint16(lenBuf, uint16(len(u16)*2))
	body = append(body, lenBuf...)
	for _, r := range u16 {
		var rb [2]byte
		binary.BigEndian.PutUint16(rb[:], r)
		body = append(body, rb[:]...)
	}
	// Prepend 0xFF packet id.
	out := append([]byte{0xFF}, body...)
	return out
}

func TestParseLegacyKickHappy(t *testing.T) {
	kick := buildLegacyKick(t, map[string]string{
		"§1": "47",
		"§2": "1.8",
		"§3": "A Minecraft Server",
		"§4": "12",
		"§5": "100",
	})

	resp, err := parseLegacyKick(kick)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Protocol != "47" {
		t.Errorf("Protocol = %q, want 47", resp.Protocol)
	}
	if resp.Version != "1.8" {
		t.Errorf("Version = %q, want 1.8", resp.Version)
	}
	if resp.MOTD != "A Minecraft Server" {
		t.Errorf("MOTD = %q, want %q", resp.MOTD, "A Minecraft Server")
	}
	if resp.NumPlayers != 12 {
		t.Errorf("NumPlayers = %d, want 12", resp.NumPlayers)
	}
	if resp.MaxPlayers != 100 {
		t.Errorf("MaxPlayers = %d, want 100", resp.MaxPlayers)
	}
}

func TestParseLegacyKickWrongPacketID(t *testing.T) {
	body := []byte{0x00, 0x00, 0x00}
	_, err := parseLegacyKick(body)
	if err == nil {
		t.Errorf("err = nil for wrong packet id, want error")
	}
}

func TestParseLegacyKickEmpty(t *testing.T) {
	_, err := parseLegacyKick([]byte{0xFF, 0x00, 0x00})
	if err == nil {
		t.Errorf("err = nil for empty body, want error")
	}
}

func TestParseLegacyKickTruncated(t *testing.T) {
	// 0xFF + length that exceeds body
	body := []byte{0xFF, 0x00, 0x10, 0x00, 0x00}
	_, err := parseLegacyKick(body)
	if err == nil {
		t.Errorf("err = nil for truncated body, want error")
	}
}

func TestParseLegacySectionsIncomplete(t *testing.T) {
	// Section data with only §1 and §2 (missing the rest).
	decoded := "\x00§1\x0047\x00§2\x001.8"
	resp, err := parseLegacySections(decoded)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Protocol != "47" {
		t.Errorf("Protocol = %q, want 47", resp.Protocol)
	}
	if resp.Version != "1.8" {
		t.Errorf("Version = %q, want 1.8", resp.Version)
	}
}

func TestParseLegacySectionsMalformedNumber(t *testing.T) {
	decoded := "\x00§1\x0047\x00§2\x001.8\x00§3\x00M\x00§4\x00not_a_number\x00"
	_, err := parseLegacySections(decoded)
	if err == nil {
		t.Errorf("err = nil for malformed numplayers, want error")
	}
}
