package login

import (
	"bytes"
	"crypto/md5"
	"encoding/hex"
	"testing"
)

// TestSendHandshake_ProtoVersionEncoded verifies the handshake packet
// embeds the requested protocol version. This is the regression test
// for the protocol-spoofing change: sendHandshake used to always
// emit -1, which made modern servers respond with
// "outdated_client" disconnect because the protocol in the handshake
// didn't match the protocol the server was running.
func TestSendHandshake_ProtoVersionEncoded(t *testing.T) {
	cases := []struct {
		name    string
		version int32
	}{
		{"legacy -1", -1},
		{"modern 1.21.11 (770)", 770},
		{"modern 1.20.1 (47)", 47},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var buf bytes.Buffer
			if err := sendHandshake(&buf, "test.example", 25565, tc.version); err != nil {
				t.Fatalf("sendHandshake: %v", err)
			}

			// Parse the packet back: length VarInt + packet body.
			br := bytes.NewReader(buf.Bytes())
			// Outer length
			if _, err := ReadVarInt(br); err != nil {
				t.Fatalf("read outer length: %v", err)
			}
			// Inner packet ID 0x00 (Handshake intent)
			if _, err := ReadVarInt(br); err != nil {
				t.Fatalf("read packet id: %v", err)
			}
			// Now the protocol version should be tc.version (VarInt).
			gotProto, err := ReadVarInt(br)
			if err != nil {
				t.Fatalf("read protocol version: %v", err)
			}
			if gotProto != tc.version {
				t.Errorf("embedded proto version = %d, want %d", gotProto, tc.version)
			}
		})
	}
}

// TestProtocolVersionLegacyConstant ensures the legacy constant is -1,
// matching the protocol convention for "no protocol specified".
func TestProtocolVersionLegacyConstant(t *testing.T) {
	if got := ProtocolVersionLegacy(); got != -1 {
		t.Errorf("ProtocolVersionLegacy() = %d, want -1", got)
	}
}

// TestOfflinePlayerUUID_RealMD5 verifies that offlinePlayerUUID is
// actually MD5("OfflinePlayer:" + name) with version=3 and variant=10b
// bits set. The previous implementation had a placeholder
// md5hash() that just repeated input bytes — which made every login
// handshake carry a bogus UUID and triggered
// "io.netty.handler.codec.DecoderException: IndexOutOfBoundsException"
// on modern Forge servers.
func TestOfflinePlayerUUID_RealMD5(t *testing.T) {
	got := offlinePlayerUUID("Notch")
	want := md5.Sum([]byte("OfflinePlayer:Notch"))
	want[6] = (want[6] & 0x0f) | 0x30
	want[8] = (want[8] & 0x3f) | 0x80

	if got != want {
		t.Errorf("offlinePlayerUUID(Notch) = %s, want %s",
			hex.EncodeToString(got[:]),
			hex.EncodeToString(want[:]))
	}

	// version nibble (high 4 bits of byte 6) must be 3
	if got[6]&0xf0 != 0x30 {
		t.Errorf("version nibble = %#x, want 0x30 (v3)", got[6]&0xf0)
	}
	// variant (high 2 bits of byte 8) must be 10b = 0x80
	if got[8]&0xc0 != 0x80 {
		t.Errorf("variant = %#x, want 0x80 (RFC4122)", got[8]&0xc0)
	}
}

// TestOfflinePlayerUUID_NotConstant guards against the placeholder
// regression: the broken md5hash() returned the same bytes regardless
// of input length. Real MD5 must vary with input.
func TestOfflinePlayerUUID_NotConstant(t *testing.T) {
	a := offlinePlayerUUID("a")
	b := offlinePlayerUUID("b")
	if a == b {
		t.Error("offlinePlayerUUID(a) == offlinePlayerUUID(b) — likely the placeholder md5hash() regressed")
	}
}

// dummy