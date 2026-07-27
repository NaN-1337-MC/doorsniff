package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/renanbray/doorsniff/internal/config"
	"github.com/renanbray/doorsniff/internal/storage"
)

func runInit(args []string) error {
	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("loading config: %w", err)
	}

	dbPath := cfg.DatabasePath
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		return fmt.Errorf("creating data directory: %w", err)
	}

	ctx := context.Background()
	db, err := storage.Open(ctx, dbPath)
	if err != nil {
		return fmt.Errorf("opening database: %w", err)
	}
	defer db.Close()

	if err := db.EnsureSchema(ctx); err != nil {
		return fmt.Errorf("applying schema: %w", err)
	}

	fmt.Printf("database created at %s\n", dbPath)

	configPath := config.DefaultConfigPath()
	if err := createConfigFile(configPath); err != nil {
		return fmt.Errorf("creating config file: %w", err)
	}

	fmt.Printf("config file created at %s\n", configPath)
	return nil
}

func createConfigFile(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	}

	configDir := filepath.Dir(path)
	if err := os.MkdirAll(configDir, 0o755); err != nil {
		return fmt.Errorf("creating config directory: %w", err)
	}

	configContent := `# doorsniff configuration
# https://github.com/renanbray/doorsniff

# Scan settings
ports: "25500-25700"

# Probe settings
probe_qps: 100
probe_workers: 128
probe_timeout: 1s

# Update settings
update_qps: 100
update_workers: 128
update_timeout: 1s
update_limit: 0

# List settings
list_sort: "recent"
list_limit: 100

# Common settings
login_user: "Herobrine"
ascii: false
resume: false
enable_query: true
enable_expansion: true
debug_login: false
`

	if err := os.WriteFile(path, []byte(configContent), 0o644); err != nil {
		return fmt.Errorf("writing config file: %w", err)
	}

	return nil
}
