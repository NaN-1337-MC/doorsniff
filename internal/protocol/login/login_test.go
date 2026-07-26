package login

import (
	"bufio"
	"bytes"
	"encoding/json"
	"testing"
)

// makeDisconnectPacket builds a Disconnect packet body (length VarInt +
// packet ID + JSON reason string) suitable for readFirstPacket.
func makeDisconnectPacket(t *testing.T, reason string) []byte {
	t.Helper()

	reasonJSON, err := json.Marshal(reason)
	if err != nil {
		t.Fatalf("marshal reason: %v", err)
	}

	// Disconnect packet body = packet ID (0x00) + string (VarInt len + bytes)
	var body bytes.Buffer
	if err := WriteVarInt(&body, packetIDDisconnect); err != nil {
		t.Fatalf("write id: %v", err)
	}
	if err := WriteString(&body, string(reasonJSON)); err != nil {
		t.Fatalf("write string: %v", err)
	}

	// Wrap in length-prefixed packet frame
	var pkt bytes.Buffer
	if err := WriteVarInt(&pkt, int32(body.Len())); err != nil {
		t.Fatalf("write length: %v", err)
	}
	pkt.Write(body.Bytes())

	return pkt.Bytes()
}

func TestReadFirstPacket_DisconnectExtractsReason(t *testing.T) {
	reason := `{"text":"You are not whitelisted on this server"}`
	pkt := makeDisconnectPacket(t, reason)

	result, _, err := readFirstPacket(bufio.NewReader(bytes.NewReader(pkt)), nil, "test")
	if err != nil {
		t.Fatalf("readFirstPacket: %v", err)
	}

	if result.Mode != LoginModeWhitelisted {
		t.Errorf("Mode = %q, want %q", result.Mode, LoginModeWhitelisted)
	}
	if result.DisconnectReason != reason {
		t.Errorf("DisconnectReason = %q, want %q", result.DisconnectReason, reason)
	}
}

func TestReadFirstPacket_DisconnectBanReason(t *testing.T) {
	reason := `{"text":"You are banned from this server!"}`
	pkt := makeDisconnectPacket(t, reason)

	result, _, err := readFirstPacket(bufio.NewReader(bytes.NewReader(pkt)), nil, "test")
	if err != nil {
		t.Fatalf("readFirstPacket: %v", err)
	}

	if result.Mode != LoginModeBanned {
		t.Errorf("Mode = %q, want %q", result.Mode, LoginModeBanned)
	}
}

func TestReadFirstPacket_DisconnectServerFull(t *testing.T) {
	reason := `Server is full`
	pkt := makeDisconnectPacket(t, reason)

	result, _, err := readFirstPacket(bufio.NewReader(bytes.NewReader(pkt)), nil, "test")
	if err != nil {
		t.Fatalf("readFirstPacket: %v", err)
	}

	if result.Mode != LoginModeFull {
		t.Errorf("Mode = %q, want %q", result.Mode, LoginModeFull)
	}
}

func TestReadFirstPacket_LoginSuccess(t *testing.T) {
	// LoginSuccess packet: length + id (0x02) + UUID + username
	var body bytes.Buffer
	if err := WriteVarInt(&body, packetIDLoginSuccess); err != nil {
		t.Fatalf("write id: %v", err)
	}
	// UUID (16 bytes) + username (VarInt len + bytes)
	body.Write(make([]byte, 16))
	if err := WriteString(&body, "TestPlayer"); err != nil {
		t.Fatalf("write username: %v", err)
	}

	var pkt bytes.Buffer
	if err := WriteVarInt(&pkt, int32(body.Len())); err != nil {
		t.Fatalf("write length: %v", err)
	}
	pkt.Write(body.Bytes())

	result, _, err := readFirstPacket(bufio.NewReader(bytes.NewReader(pkt.Bytes())), nil, "test")
	if err != nil {
		t.Fatalf("readFirstPacket: %v", err)
	}

	if result.Mode != LoginModeOffline {
		t.Errorf("Mode = %q, want %q", result.Mode, LoginModeOffline)
	}
}

func TestReadFirstPacket_EncryptionRequest(t *testing.T) {
	// EncryptionRequest packet: id (0x01) + string (server id) + bytes (pub key) + bytes (verify token)
	var body bytes.Buffer
	if err := WriteVarInt(&body, packetIDEncryptionReq); err != nil {
		t.Fatalf("write id: %v", err)
	}
	if err := WriteString(&body, ""); err != nil {
		t.Fatalf("write server id: %v", err)
	}
	if err := WriteVarInt(&body, 0); err != nil { // pub key length
		t.Fatalf("write pub key len: %v", err)
	}
	if err := WriteVarInt(&body, 0); err != nil { // verify token length
		t.Fatalf("write token len: %v", err)
	}

	var pkt bytes.Buffer
	if err := WriteVarInt(&pkt, int32(body.Len())); err != nil {
		t.Fatalf("write length: %v", err)
	}
	pkt.Write(body.Bytes())

	result, _, err := readFirstPacket(bufio.NewReader(bytes.NewReader(pkt.Bytes())), nil, "test")
	if err != nil {
		t.Fatalf("readFirstPacket: %v", err)
	}

	if result.Mode != LoginModeOnline {
		t.Errorf("Mode = %q, want %q", result.Mode, LoginModeOnline)
	}
}

func TestReadVarIntFromBytes(t *testing.T) {
	cases := []struct {
		name    string
		encoded []byte
		want    int32
		wantN   int
	}{
		{"single byte zero", []byte{0x00}, 0, 1},
		{"single byte 127", []byte{0x7f}, 127, 1},
		{"two bytes 128", []byte{0x80, 0x01}, 128, 2},
		{"two bytes 255", []byte{0xff, 0x01}, 255, 2},
		{"disconnect id 0x00", []byte{0x00}, 0, 1},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			br := bytes.NewReader(tc.encoded)
			got, gotN, err := readVarIntFromBytes(br)
			if err != nil {
				t.Fatalf("readVarIntFromBytes: %v", err)
			}
			if got != tc.want {
				t.Errorf("value = %d, want %d", got, tc.want)
			}
			if gotN != tc.wantN {
				t.Errorf("n = %d, want %d", gotN, tc.wantN)
			}
		})
	}
}

func TestReadPacket_DisconnectBody(t *testing.T) {
	reason := `{"text":"whitelist"}`
	pkt := makeDisconnectPacket(t, reason)

	p, err := ReadPacket(bufio.NewReader(bytes.NewReader(pkt)), "login")
	if err != nil {
		t.Fatalf("ReadPacket: %v", err)
	}

	if p.ID != packetIDDisconnect {
		t.Errorf("ID = %d, want %d", p.ID, packetIDDisconnect)
	}
	if len(p.Data) == 0 {
		t.Fatal("Data is empty, expected JSON reason body")
	}

	// Verify we can parse the body via parseDisconnectReason
	reasonText := parseDisconnectReason(p.Data)
	if reasonText == "" {
		t.Error("parseDisconnectReason returned empty string")
	}
	if !bytes.Contains([]byte(reasonText), []byte("whitelist")) {
		t.Errorf("reason text %q does not contain 'whitelist'", reasonText)
	}
}