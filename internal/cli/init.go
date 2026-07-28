package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/renanbray/doorsniff/internal/config"
	"github.com/renanbray/doorsniff/internal/storage"
)

func runInit(args []string) error {
	fs := flag.NewFlagSet("init", flag.ContinueOnError)
	fs.SetOutput(io.Discard)

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			fmt.Print(initUsage())
			return nil
		}
		return fmt.Errorf("parsing flags: %w", err)
	}

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
probe_login: true

# Update settings
update_qps: 100
update_workers: 128
update_timeout: 1s
update_limit: 0
update_login: true

# List settings
# Available columns: ordinal, address, version, login, players, latency,
#                   last_seen, last_activity, motd, map, country, plugins,
#                   protocol, player_names, last_login
list_sort: "recent"
list_limit: 100
list_columns:
  - ordinal
  - address
  - version
  - login
  - players
  - latency
  - last_seen
  - motd
  - map

# Date format (use Go reference date format)
# 02/01/2006 = DD/MM/YYYY, 01/02/2006 = MM/DD/YYYY, 2006-01-02 = YYYY-MM-DD
# Add time: "02/01/2006 15:04:05" = DD/MM/YYYY HH:mm:ss
list_date_format: "02/01/2006 15:04:05"

# Max players shown in 'list server <ip>' PLAYERS column.
# 0 = no truncation (table wraps to multiple lines). Set to e.g. 5 to
# show the first 5 players followed by "+N more".
list_server_player_max: 0

# Scanner settings
scanner_result_channel_size: 256
update_op_channel_size: 128
progress_ticker_interval: 500ms

# Protocol settings
slp_max_response_bytes: 1048576
query_max_response_bytes: 4096
login_max_packet_bytes: 2097152
max_string_length: 32767

# Display settings
progress_bar_width: 30
max_motd_display_len: 70
max_plugins_display_len: 60
max_player_names_display_len: 30

# Storage settings
sqlite_sync_mode: "NORMAL"

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
