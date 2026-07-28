package login

import (
	"errors"
	"testing"
)

// TestParseStealthMode_Defaults ensures the empty string resolves to
// the package default (detect). Tools and config files should keep
// working even when a user has not set --login-mode.
func TestParseStealthMode_Defaults(t *testing.T) {
	got, err := ParseStealthMode("")
	if err != nil {
		t.Fatalf("ParseStealthMode(\"\"): %v", err)
	}
	if got != DefaultStealthMode {
		t.Errorf("ParseStealthMode(\"\") = %q, want %q", got, DefaultStealthMode)
	}
	if DefaultStealthMode != StealthDetect {
		t.Errorf("DefaultStealthMode = %q, want %q", DefaultStealthMode, StealthDetect)
	}
}

// TestParseStealthMode_Valid covers the three real options plus
// case-insensitivity and surrounding whitespace.
func TestParseStealthMode_Valid(t *testing.T) {
	cases := []struct {
		in   string
		want StealthMode
	}{
		{"off", StealthOff},
		{"detect", StealthDetect},
		{"intrusive", StealthIntrusive},
		{"OFF", StealthOff},
		{"Detect", StealthDetect},
		{"  intrusive  ", StealthIntrusive},
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			got, err := ParseStealthMode(tc.in)
			if err != nil {
				t.Fatalf("ParseStealthMode(%q): %v", tc.in, err)
			}
			if got != tc.want {
				t.Errorf("ParseStealthMode(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

// TestParseStealthMode_Invalid guards the user-facing error path.
func TestParseStealthMode_Invalid(t *testing.T) {
	for _, in := range []string{"silent", "DETECT-but-not", "offish", "1"} {
		t.Run(in, func(t *testing.T) {
			_, err := ParseStealthMode(in)
			if err == nil {
				t.Fatalf("ParseStealthMode(%q): expected error, got nil", in)
			}
			if !errors.Is(err, err) {
				// Just ensure it is a non-nil error; the message is
				// cosmetic.
				t.Errorf("ParseStealthMode(%q): expected non-nil error", in)
			}
		})
	}
}

// TestStealthMode_String ensures the (un)marshalling defaulting
// behaviour is consistent — an unset mode (zero value) should
// stringify to the default rather than to "".
func TestStealthMode_String(t *testing.T) {
	if got := (StealthMode("")).String(); got != string(DefaultStealthMode) {
		t.Errorf("(StealthMode)(\"\").String() = %q, want %q", got, DefaultStealthMode)
	}
	if got := StealthOff.String(); got != "off" {
		t.Errorf("StealthOff.String() = %q, want %q", got, "off")
	}
	if got := StealthDetect.String(); got != "detect" {
		t.Errorf("StealthDetect.String() = %q, want %q", got, "detect")
	}
	if got := StealthIntrusive.String(); got != "intrusive" {
		t.Errorf("StealthIntrusive.String() = %q, want %q", got, "intrusive")
	}
}

// TestStealthMode_Valid locks the valid-mode set so a typo in the
// constant declarations fails fast.
func TestStealthMode_Valid(t *testing.T) {
	for _, m := range []StealthMode{StealthOff, StealthDetect, StealthIntrusive} {
		if !m.Valid() {
			t.Errorf("expected %q to be Valid", m)
		}
	}
	if (StealthMode("nope")).Valid() {
		t.Error("expected \"nope\" to be invalid")
	}
}

// TestHandshake_OffSkips ensures Handshake with StealthOff returns
// ErrLoginSkipped without ever opening a socket. We point at a port
// that must not be bound (test-only port = "0.0.0.0:1"); if the
// function tried to dial we would get a connection refused or
// timeout, neither of which is ErrLoginSkipped.
func TestHandshake_OffSkips(t *testing.T) {
	_, err := Handshake(t.Context(), "127.0.0.1", 1, 10, "test", ProtocolVersionLegacy(), StealthOff, nil)
	if err == nil {
		t.Fatal("Handshake(off): expected error, got nil")
	}
	if !errors.Is(err, ErrLoginSkipped) {
		t.Fatalf("Handshake(off): err = %v, want ErrLoginSkipped", err)
	}
}
