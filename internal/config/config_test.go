package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// chdir changes to a temp dir for the duration of the test so that XDG
// path lookups resolve to a known location.
func chdir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	old, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	if err := os.Chdir(dir); err != nil {
		t.Fatalf("chdir: %v", err)
	}
	t.Cleanup(func() { _ = os.Chdir(old) })
	return dir
}

func TestLoadDefaults(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DOORSNIFF_CONFIG_PATH", "")
	t.Setenv("DOORSNIFF_DATA_PATH", "")

	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "missing.yaml"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if cfg.ProbeQPS != defaultProbeQPS {
		t.Errorf("ProbeQPS = %d, want %d", cfg.ProbeQPS, defaultProbeQPS)
	}
	if cfg.ProbeWorkers != defaultProbeWorkers {
		t.Errorf("ProbeWorkers = %d, want %d", cfg.ProbeWorkers, defaultProbeWorkers)
	}
	if cfg.ProbeTimeout != defaultProbeTimeout {
		t.Errorf("ProbeTimeout = %v, want %v", cfg.ProbeTimeout, defaultProbeTimeout)
	}
	if cfg.Ports != defaultPorts {
		t.Errorf("Ports = %q, want %q", cfg.Ports, defaultPorts)
	}
	if !cfg.EnableQuery {
		t.Errorf("EnableQuery = false, want true")
	}
	if !cfg.EnableExpansion {
		t.Errorf("EnableExpansion = false, want true")
	}
}

func TestLoadFromYAMLFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DOORSNIFF_CONFIG_PATH", "")
	t.Setenv("DOORSNIFF_DATA_PATH", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	yaml := `
probe_qps: 100
probe_workers: 128
probe_timeout: 5s
ports: 25565
enable_query: false
enable_expansion: false
verbose: true
`
	if err := os.WriteFile(path, []byte(yaml), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}

	if cfg.ProbeQPS != 100 {
		t.Errorf("ProbeQPS = %d, want 100", cfg.ProbeQPS)
	}
	if cfg.ProbeWorkers != 128 {
		t.Errorf("ProbeWorkers = %d, want 128", cfg.ProbeWorkers)
	}
	if cfg.ProbeTimeout != 5*time.Second {
		t.Errorf("ProbeTimeout = %v, want 5s", cfg.ProbeTimeout)
	}
	if cfg.Ports != "25565" {
		t.Errorf("Ports = %q, want 25565", cfg.Ports)
	}
	if cfg.EnableQuery {
		t.Errorf("EnableQuery = true, want false")
	}
	if cfg.EnableExpansion {
		t.Errorf("EnableExpansion = true, want false")
	}
	if !cfg.Verbose {
		t.Errorf("Verbose = false, want true")
	}
}

func TestEnvOverridesFile(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DOORSNIFF_CONFIG_PATH", "")
	t.Setenv("DOORSNIFF_DATA_PATH", "")

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("probe_qps: 10\n"), 0o600); err != nil {
		t.Fatalf("write yaml: %v", err)
	}

	t.Setenv("DOORSNIFF_PROBE_QPS", "999")

	cfg, err := LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if cfg.ProbeQPS != 999 {
		t.Errorf("ProbeQPS = %d, want 999 (env override)", cfg.ProbeQPS)
	}
}

func TestDefaultPaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DOORSNIFF_CONFIG_PATH", "")
	t.Setenv("DOORSNIFF_DATA_PATH", "")

	if got, want := DefaultConfigPath(), filepath.Join(home, ".config", "doorsniff", "config.yaml"); got != want {
		t.Errorf("DefaultConfigPath = %q, want %q", got, want)
	}
	if got, want := DefaultDatabasePath(), filepath.Join(home, ".local", "share", "doorsniff", "doorsniff.db"); got != want {
		t.Errorf("DefaultDatabasePath = %q, want %q", got, want)
	}
}

func TestXDGEnvOverride(t *testing.T) {
	t.Setenv("HOME", "/should/not/be/used")
	t.Setenv("DOORSNIFF_CONFIG_PATH", "")
	t.Setenv("DOORSNIFF_DATA_PATH", "")

	customCfg := t.TempDir()
	customData := t.TempDir()
	t.Setenv("XDG_CONFIG_HOME", customCfg)
	t.Setenv("XDG_DATA_HOME", customData)

	if got, want := DefaultConfigPath(), filepath.Join(customCfg, "doorsniff", "config.yaml"); got != want {
		t.Errorf("DefaultConfigPath = %q, want %q", got, want)
	}
	if got, want := DefaultDatabasePath(), filepath.Join(customData, "doorsniff", "doorsniff.db"); got != want {
		t.Errorf("DefaultDatabasePath = %q, want %q", got, want)
	}
}

func TestLoadMissingConfigFileIsNotError(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("XDG_CONFIG_HOME", "")
	t.Setenv("XDG_DATA_HOME", "")
	t.Setenv("DOORSNIFF_CONFIG_PATH", "")
	t.Setenv("DOORSNIFF_DATA_PATH", "")

	// Path that does not exist — should not error.
	cfg, err := LoadFrom(filepath.Join(t.TempDir(), "nope.yaml"))
	if err != nil {
		t.Fatalf("LoadFrom with missing file: %v", err)
	}
	if cfg.ProbeQPS != defaultProbeQPS {
		t.Errorf("ProbeQPS = %d, want default %d", cfg.ProbeQPS, defaultProbeQPS)
	}
}
