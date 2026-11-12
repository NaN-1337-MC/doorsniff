package slp

import (
	"bytes"
	"errors"
	"testing"
)

func TestVarIntRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		v    int32
	}{
		{name: "zero", v: 0},
		{name: "one", v: 1},
		{name: "127", v: 127},
		{name: "128", v: 128},
		{name: "255", v: 255},
		{name: "max int32", v: 2147483647},
		{name: "short", v: 42},
		{name: "protocol 765", v: 765},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteVarInt(&buf, tt.v); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := ReadVarInt(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != tt.v {
				t.Errorf("roundtrip = %d, want %d", got, tt.v)
			}
		})
	}
}

func TestVarIntRoundtripNegative(t *testing.T) {
	// -1 in varint is the 5-byte encoding of 0xFFFFFFFF; roundtrips.
	var buf bytes.Buffer
	if err := WriteVarInt(&buf, -1); err != nil {
		t.Fatalf("write -1: %v", err)
	}
	if buf.Len() != 5 {
		t.Errorf("varint(-1) length = %d, want 5", buf.Len())
	}
	got, err := ReadVarInt(bytes.NewReader(buf.Bytes()))
	if err != nil {
		t.Fatalf("read: %v", err)
	}
	if got != -1 {
		t.Errorf("roundtrip = %d, want -1", got)
	}
}

func TestVarIntTooLong(t *testing.T) {
	// 5 bytes with continuation bit set would need a 6th byte.
	data := []byte{0x80, 0x80, 0x80, 0x80, 0x80}
	_, err := ReadVarInt(bytes.NewReader(data))
	if !errors.Is(err, ErrVarIntTooLong) {
		t.Errorf("err = %v, want ErrVarIntTooLong", err)
	}
}

func TestStringRoundtrip(t *testing.T) {
	tests := []struct {
		name string
		s    string
	}{
		{name: "empty", s: ""},
		{name: "ascii", s: "A Minecraft Server"},
		{name: "unicode", s: "Olá mundo"},
		{name: "long", s: "this is a long string with many characters to test multi-byte varint length"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := WriteString(&buf, tt.s); err != nil {
				t.Fatalf("write: %v", err)
			}
			got, err := ReadString(bytes.NewReader(buf.Bytes()))
			if err != nil {
				t.Fatalf("read: %v", err)
			}
			if got != tt.s {
				t.Errorf("roundtrip = %q, want %q", got, tt.s)
			}
		})
	}
}

func TestParseStringDescription(t *testing.T) {
	body := []byte(`{
  "version": {"name": "1.20.4", "protocol": 765},
  "players": {"max": 100, "online": 42, "sample": [{"name": "Notch", "id": "uuid-1"}]},
  "description": "A Minecraft Server"
}`)
	resp, err := Parse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Version.Name != "1.20.4" {
		t.Errorf("version.name = %q, want 1.20.4", resp.Version.Name)
	}
	if resp.Version.Protocol != 765 {
		t.Errorf("version.protocol = %d, want 765", resp.Version.Protocol)
	}
	if resp.Players.Online != 42 {
		t.Errorf("players.online = %d, want 42", resp.Players.Online)
	}
	if resp.Players.Max != 100 {
		t.Errorf("players.max = %d, want 100", resp.Players.Max)
	}
	if len(resp.Players.Sample) != 1 || resp.Players.Sample[0].Name != "Notch" {
		t.Errorf("sample mismatch: %+v", resp.Players.Sample)
	}
	if resp.Description != "A Minecraft Server" {
		t.Errorf("description = %q, want plain string", resp.Description)
	}
}

func TestParseChatObjectDescription(t *testing.T) {
	body := []byte(`{
  "version": {"name": "1.20.4", "protocol": 765},
  "players": {"max": 100, "online": 42},
  "description": {"text": "Hypixel Network", "color": "gold"}
}`)
	resp, err := Parse(body)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if resp.Description != "Hypixel Network" {
		t.Errorf("description = %q, want %q (extracted from chat text)", resp.Description, "Hypixel Network")
	}
}

func TestParseMalformedErrors(t *testing.T) {
	_, err := Parse([]byte(`{not json`))
	if err == nil {
		t.Errorf("parse of malformed = nil, want error")
	}
}

func TestBuildHandshake(t *testing.T) {
	b, err := buildHandshake("mc.example.com", 25565, handshakeProtocolPrimary)
	if err != nil {
		t.Fatalf("buildHandshake: %v", err)
	}
	// Expect at least: length varint (1 byte) + packet id (1 byte) +
	// protocol varint + host (varint length + 14 bytes) +
	// port (2 bytes) + next_state (1 byte). For protocol 768 the
	// protocol varint is 2 bytes; for -1 it is 5 bytes.
	if len(b) < 10 {
		t.Errorf("handshake too short: %d bytes", len(b))
	}
}

func TestBuildStatusRequest(t *testing.T) {
	got := buildStatusRequest()
	want := []byte{0x01, 0x00}
	if !bytes.Equal(got, want) {
		t.Errorf("buildStatusRequest = %v, want %v", got, want)
	}
}
