package slptest

import (
	"context"
	"testing"
	"time"

	"github.com/renanbray/doorsniff/internal/protocol/slp"
)

func TestFakeServer_Modern(t *testing.T) {
	srv := Start(t, Config{
		Version: slp.Response{
			Version:     slp.Version{Name: "1.20.4", Protocol: 765},
			Players:     slp.Players{Max: 100, Online: 42},
			Description: "A test server",
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, latency, err := slp.Ping(ctx, "127.0.0.1", srv.Port(), 1*time.Second)
	if err != nil {
		t.Fatalf("Ping: %v", err)
	}
	if resp.Version.Name != "1.20.4" {
		t.Errorf("version.name = %q, want %q", resp.Version.Name, "1.20.4")
	}
	if resp.Version.Protocol != 765 {
		t.Errorf("version.protocol = %d, want 765", resp.Version.Protocol)
	}
	if resp.Players.Max != 100 {
		t.Errorf("players.max = %d, want 100", resp.Players.Max)
	}
	if resp.Players.Online != 42 {
		t.Errorf("players.online = %d, want 42", resp.Players.Online)
	}
	if resp.Description != "A test server" {
		t.Errorf("description = %q, want %q", resp.Description, "A test server")
	}
	if latency <= 0 {
		t.Errorf("latency = %v, want > 0", latency)
	}
}

func TestFakeServer_Legacy(t *testing.T) {
	srv := Start(t, Config{
		Legacy: true,
		LegacyKick: LegacyKick{
			Protocol:   "47",
			Version:    "1.8.9",
			MOTD:       "Legacy Test",
			NumPlayers: 7,
			MaxPlayers: 50,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	resp, latency, err := slp.PingLegacy(ctx, "127.0.0.1", srv.Port(), 1*time.Second)
	if err != nil {
		t.Fatalf("PingLegacy: %v", err)
	}
	if resp.Version != "1.8.9" {
		t.Errorf("version = %q, want %q", resp.Version, "1.8.9")
	}
	if resp.MOTD != "Legacy Test" {
		t.Errorf("motd = %q, want %q", resp.MOTD, "Legacy Test")
	}
	if resp.NumPlayers != 7 {
		t.Errorf("numPlayers = %d, want 7", resp.NumPlayers)
	}
	if resp.MaxPlayers != 50 {
		t.Errorf("maxPlayers = %d, want 50", resp.MaxPlayers)
	}
	if latency <= 0 {
		t.Errorf("latency = %v, want > 0", latency)
	}
}

func TestFakeServer_LegacyRST(t *testing.T) {
	srv := Start(t, Config{
		Legacy: true,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Modern handshake should get RST.
	_, _, err := slp.Ping(ctx, "127.0.0.1", srv.Port(), 1*time.Second)
	if err == nil {
		t.Fatal("expected error from modern handshake on legacy server")
	}
	t.Logf("got expected error: %v", err)
}

func TestFakeServer_LegacyFallbackIntegration(t *testing.T) {
	srv := Start(t, Config{
		Legacy: true,
		LegacyKick: LegacyKick{
			Protocol:   "47",
			Version:    "1.8.9",
			MOTD:       "Fallback Test",
			NumPlayers: 3,
			MaxPlayers: 20,
		},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	// Modern fails → legacy succeeds.
	_, _, modernErr := slp.Ping(ctx, "127.0.0.1", srv.Port(), 1*time.Second)
	if modernErr == nil {
		t.Fatal("expected modern ping to fail on legacy server")
	}

	resp, _, legacyErr := slp.PingLegacy(ctx, "127.0.0.1", srv.Port(), 1*time.Second)
	if legacyErr != nil {
		t.Fatalf("PingLegacy: %v", legacyErr)
	}
	if resp.Version != "1.8.9" {
		t.Errorf("version = %q, want %q", resp.Version, "1.8.9")
	}
}
