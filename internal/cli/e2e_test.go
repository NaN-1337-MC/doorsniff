//go:build integration

package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/renanbray/doorsniff/internal/model"
	"github.com/renanbray/doorsniff/internal/protocol/slp"
	"github.com/renanbray/doorsniff/internal/protocol/slp/slptest"
	"github.com/renanbray/doorsniff/internal/scanner"
	"github.com/renanbray/doorsniff/internal/storage"
)

func TestE2E_ParseProbeFlags(t *testing.T) {
	tests := []struct {
		name    string
		args    []string
		wantErr bool
		check   func(t *testing.T, pc *probeConfig)
	}{
		{
			name: "defaults",
			args: nil,
			check: func(t *testing.T, pc *probeConfig) {
				if pc.portsLo != 1 || pc.portsHi != 65535 {
					t.Errorf("ports = %d-%d, want 1-65535", pc.portsLo, pc.portsHi)
				}
				if pc.qps != 200 {
					t.Errorf("qps = %d, want 200", pc.qps)
				}
				if pc.workers != 256 {
					t.Errorf("workers = %d, want 256", pc.workers)
				}
				if pc.timeout != 1*time.Second {
					t.Errorf("timeout = %v, want 1s", pc.timeout)
				}
				if pc.verbose {
					t.Error("verbose = true, want false")
				}
				if pc.ascii {
					t.Error("ascii = true, want false")
				}
				if pc.resume {
					t.Error("resume = true, want false")
				}
			},
		},
		{
			name: "custom flags",
			args: []string{
				"--ports", "25565",
				"--qps", "500",
				"--workers", "128",
				"--timeout", "500ms",
				"--verbose",
				"--ascii",
				"--resume",
			},
			check: func(t *testing.T, pc *probeConfig) {
				if pc.portsLo != 25565 || pc.portsHi != 25565 {
					t.Errorf("ports = %d-%d, want 25565-25565", pc.portsLo, pc.portsHi)
				}
				if pc.qps != 500 {
					t.Errorf("qps = %d, want 500", pc.qps)
				}
				if pc.workers != 128 {
					t.Errorf("workers = %d, want 128", pc.workers)
				}
				if pc.timeout != 500*time.Millisecond {
					t.Errorf("timeout = %v, want 500ms", pc.timeout)
				}
				if !pc.verbose {
					t.Error("verbose = false, want true")
				}
				if !pc.ascii {
					t.Error("ascii = false, want true")
				}
				if !pc.resume {
					t.Error("resume = false, want true")
				}
			},
		},
		{
			name: "port range",
			args: []string{"--ports", "25500-25600"},
			check: func(t *testing.T, pc *probeConfig) {
				if pc.portsLo != 25500 || pc.portsHi != 25600 {
					t.Errorf("ports = %d-%d, want 25500-25600", pc.portsLo, pc.portsHi)
				}
			},
		},
		{
			name:    "invalid port range",
			args:    []string{"--ports", "abc"},
			wantErr: true,
		},
		{
			name: "short verbose flag",
			args: []string{"-v"},
			check: func(t *testing.T, pc *probeConfig) {
				if !pc.verbose {
					t.Error("verbose = false, want true")
				}
			},
		},
		{
			name: "help returns nil",
			args: []string{"--help"},
			check: func(t *testing.T, pc *probeConfig) {
				if pc != nil {
					t.Errorf("pc = %v, want nil (help was printed)", pc)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pc, err := parseProbeFlags(tt.args)
			if tt.wantErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if tt.check != nil {
				tt.check(t, pc)
			}
		})
	}
}

func TestE2E_RunAndSummarize(t *testing.T) {
	srv := slptest.Start(t, slptest.Config{
		Version: slp.Response{
			Version:     slp.Version{Name: "1.21.1", Protocol: 767},
			Players:     slp.Players{Max: 50, Online: 12},
			Description: "CLI E2E",
		},
	})

	// Write a seed file pointing to the fake server.
	dir := t.TempDir()
	seedPath := filepath.Join(dir, "seeds.txt")
	seedContent := fmt.Sprintf("%s:%d\n", "127.0.0.1", srv.Port())
	if err := os.WriteFile(seedPath, []byte(seedContent), 0o644); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	// Open an in-memory DB.
	ctx := context.Background()
	db, err := storage.Open(ctx, "file::memory:?cache=shared")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	defer func() { _ = db.Close() }()
	if err := db.EnsureSchema(ctx); err != nil {
		t.Fatalf("ensure schema: %v", err)
	}
	repo := storage.NewRepository(db)

	// Build a probeConfig manually.
	pc := &probeConfig{
		portsLo:  srv.Port(),
		portsHi:  srv.Port(),
		qps:      50,
		workers:  4,
		timeout:  2 * time.Second,
		verbose:  false,
		ascii:    false,
		resume:   false,
		fileFlag: seedPath,
		seedsDir: dir,
		dbPath:   "in-memory",
	}

	stats := runAndSummarize(os.Stdout, pc, repo)

	if stats == nil {
		t.Fatal("stats is nil")
	}
	if stats.Found.Load() != 1 {
		t.Errorf("found = %d, want 1", stats.Found.Load())
	}

	// Verify the server is in the DB.
	servers, err := repo.ListServers(ctx, storage.ListOptions{Limit: 10})
	if err != nil {
		t.Fatalf("ListServers: %v", err)
	}
	if len(servers) != 1 {
		t.Fatalf("servers in DB = %d, want 1", len(servers))
	}
	if servers[0].VersionName != "1.21.1" {
		t.Errorf("version = %q, want %q", servers[0].VersionName, "1.21.1")
	}
	if servers[0].MOTD != "CLI E2E" {
		t.Errorf("motd = %q, want %q", servers[0].MOTD, "CLI E2E")
	}
}

// Ensure unused imports are consumed.
var _ = model.Seed{}
var _ = scanner.Options{}
