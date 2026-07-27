//go:build integration

package query_test

import (
	"context"
	"testing"
	"time"

	"github.com/renanbray/doorsniff/internal/protocol/query"
	"github.com/renanbray/doorsniff/internal/protocol/query/querytest"
)

func TestQuery_FullStat(t *testing.T) {
	srv := querytest.Start(t, querytest.Config{
		MOTD:       "Test Server",
		GameType:   "SMP",
		Version:    "1.21.1",
		Plugins:    "Paper on 1.21.1-R0.1-SNAPSHOT: Essentials; Vault",
		Map:        "world",
		NumPlayers: 7,
		MaxPlayers: 100,
		PlayerNames: []string{"Steve", "Alex", "Notch"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, latency, err := query.Query(ctx, "127.0.0.1", srv.Port(), 2*time.Second)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	t.Logf("latency: %v", latency)

	if resp.MOTD != "Test Server" {
		t.Errorf("MOTD = %q, want %q", resp.MOTD, "Test Server")
	}
	if resp.GameType != "SMP" {
		t.Errorf("GameType = %q, want %q", resp.GameType, "SMP")
	}
	if resp.Version != "1.21.1" {
		t.Errorf("Version = %q, want %q", resp.Version, "1.21.1")
	}
	if resp.Map != "world" {
		t.Errorf("Map = %q, want %q", resp.Map, "world")
	}
	if resp.NumPlayers != 7 {
		t.Errorf("NumPlayers = %d, want 7", resp.NumPlayers)
	}
	if resp.MaxPlayers != 100 {
		t.Errorf("MaxPlayers = %d, want 100", resp.MaxPlayers)
	}
	if resp.HostIP != "127.0.0.1" {
		t.Errorf("HostIP = %q, want %q", resp.HostIP, "127.0.0.1")
	}
	if len(resp.PlayerNames) != 3 {
		t.Errorf("PlayerNames len = %d, want 3", len(resp.PlayerNames))
	}
	if resp.Plugins != "Paper on 1.21.1-R0.1-SNAPSHOT: Essentials; Vault" {
		t.Errorf("Plugins = %q", resp.Plugins)
	}
}

func TestQuery_TimeoutOnClosedPort(t *testing.T) {
	// No server listening — should timeout.
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	_, _, err := query.Query(ctx, "127.0.0.1", 19999, 500*time.Millisecond)
	if err == nil {
		t.Fatal("expected error for unreachable server")
	}
	t.Logf("expected error: %v", err)
}

func TestQuery_PluginParsing(t *testing.T) {
	srv := querytest.Start(t, querytest.Config{
		MOTD:     "Plugin Test",
		Plugins:  "Paper on 1.21.1: WorldEdit 7.3; Vault 1.7.3",
		PlayerNames: []string{"Player1"},
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, _, err := query.Query(ctx, "127.0.0.1", srv.Port(), 2*time.Second)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}

	plugins := query.ParsePlugins(resp.Plugins)
	if len(plugins) != 2 {
		t.Fatalf("plugins = %d, want 2", len(plugins))
	}
	if plugins[0].Name != "WorldEdit" || plugins[0].Version != "7.3" {
		t.Errorf("plugin 0 = %+v", plugins[0])
	}
	if plugins[1].Name != "Vault" || plugins[1].Version != "1.7.3" {
		t.Errorf("plugin 1 = %+v", plugins[1])
	}
}

func TestQuery_EmptyServer(t *testing.T) {
	srv := querytest.Start(t, querytest.Config{
		MOTD:        "Empty",
		NumPlayers:  0,
		PlayerNames: nil,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	resp, _, err := query.Query(ctx, "127.0.0.1", srv.Port(), 2*time.Second)
	if err != nil {
		t.Fatalf("Query: %v", err)
	}
	if resp.NumPlayers != 0 {
		t.Errorf("NumPlayers = %d, want 0", resp.NumPlayers)
	}
	if len(resp.PlayerNames) != 0 {
		t.Errorf("PlayerNames = %v, want empty", resp.PlayerNames)
	}
}
