package login

import (
	"testing"
)

func TestExtractVersionHint(t *testing.T) {
	tests := []struct {
		input    string
		expected string
	}{
		{"Please use version 1.21.11", "1.21.11"},
		{"Outdated client! Please use 1.21.11", "1.21.11"},
		{"Required version: 1.20.1", "1.20.1"},
		{"Use 1.21.11 to connect", "1.21.11"},
		{"multiplayer.disconnect.incompatible 1.21.11", "1.21.11"},
		{"Connection throttled! Please wait before reconnecting.", ""},
		{"Your client is outdated, please use 1.20.5", "1.20.5"},
		{"", ""},
	}

	for _, tt := range tests {
		got := ExtractVersionHint(tt.input)
		if got != tt.expected {
			t.Errorf("ExtractVersionHint(%q) = %q, want %q", tt.input, got, tt.expected)
		}
	}
}

func TestProtocolForVersion(t *testing.T) {
	tests := []struct {
		input    string
		expected int32
	}{
		{"1.21.11", 776},
		{"1.20.5", 766},
		{"", -1},
		{"9.9.9", -1}, // unknown
	}

	for _, tt := range tests {
		got := ProtocolForVersion(tt.input)
		if got != tt.expected {
			t.Errorf("ProtocolForVersion(%q) = %d, want %d", tt.input, got, tt.expected)
		}
	}
}
