// Package config loads and exposes user configuration.
//
// Resolution order (first set wins):
//
//  1. explicit viper.Set
//  2. bound flag (CLI)
//  3. env var (DOORSNIFF_*)
//  4. config file (XDG_CONFIG_HOME/doorsniff/config.yaml)
//  5. default in code
//
// All three of SetEnvPrefix, SetEnvKeyReplacer, and AutomaticEnv are
// wired together — missing any one of them silently breaks nested-key
// resolution.
package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Config is the resolved user configuration. Every field is populated
// from the layered sources, with sensible defaults applied.
type Config struct {
	// Scan settings
	Ports    string `mapstructure:"ports"`
	SeedsDir string `mapstructure:"seeds_dir"`

	// Probe settings
	ProbeQPS     int           `mapstructure:"probe_qps"`
	ProbeWorkers int           `mapstructure:"probe_workers"`
	ProbeTimeout time.Duration `mapstructure:"probe_timeout"`
	ProbeLogin   bool          `mapstructure:"probe_login"`
	ProbeLoginMode string       `mapstructure:"probe_login_mode"`
	ProbePlayerHistory bool     `mapstructure:"probe_player_history"`

	// Update settings
	UpdateQPS     int           `mapstructure:"update_qps"`
	UpdateWorkers int           `mapstructure:"update_workers"`
	UpdateTimeout time.Duration `mapstructure:"update_timeout"`
	UpdateLimit   int           `mapstructure:"update_limit"`
	UpdateLogin   bool         `mapstructure:"update_login"`
	UpdateLoginMode string       `mapstructure:"update_login_mode"`
	UpdatePlayerHistory bool     `mapstructure:"update_player_history"`

	// List settings
	ListSort      string   `mapstructure:"list_sort"`
	ListLimit     int      `mapstructure:"list_limit"`
	ListColumns   []string `mapstructure:"list_columns"`
	ListDateFormat string  `mapstructure:"list_date_format"`

	// Scanner settings
	ScannerResultChannelSize int           `mapstructure:"scanner_result_channel_size"`
	UpdateOpChannelSize      int           `mapstructure:"update_op_channel_size"`
	ProgressTickerInterval   time.Duration `mapstructure:"progress_ticker_interval"`

	// Protocol settings
	SLPMaxResponseBytes   int `mapstructure:"slp_max_response_bytes"`
	QueryMaxResponseBytes  int `mapstructure:"query_max_response_bytes"`
	LoginMaxPacketBytes   int `mapstructure:"login_max_packet_bytes"`
	MaxStringLength       int `mapstructure:"max_string_length"`

	// Display settings
	ProgressBarWidth          int `mapstructure:"progress_bar_width"`
	MaxMotdDisplayLen         int `mapstructure:"max_motd_display_len"`
	MaxPluginsDisplayLen      int `mapstructure:"max_plugins_display_len"`
	MaxPlayerNamesDisplayLen  int `mapstructure:"max_player_names_display_len"`

	// Storage settings
	SQLiteSyncMode string `mapstructure:"sqlite_sync_mode"`

	// Common settings
	DatabasePath    string `mapstructure:"database_path"`
	ConfigPath      string `mapstructure:"config_path"`
	GeoIPPath       string `mapstructure:"geoip_path"`
	LoginUser       string `mapstructure:"login_user"`
	Verbose         bool   `mapstructure:"verbose"`
	ASCII           bool   `mapstructure:"ascii"`
	Resume          bool   `mapstructure:"resume"`
	EnableQuery     bool   `mapstructure:"enable_query"`
	EnableExpansion bool   `mapstructure:"enable_expansion"`
	DebugLogin      bool   `mapstructure:"debug_login"`
}

// Defaults applied when no other source provides a value.
const (
	// Scan
	DefaultPorts = "25500-25700"

	// Probe
	DefaultProbeQPS      = 100
	DefaultProbeWorkers  = 128
	DefaultProbeTimeout  = 1 * time.Second
	DefaultProbeLogin    = true
	DefaultProbeLoginMode = "detect"
	DefaultProbePlayerHistory = true

	// Update
	DefaultUpdateQPS      = 100
	DefaultUpdateWorkers  = 128
	DefaultUpdateTimeout  = 1 * time.Second
	DefaultUpdateLimit    = 0
	DefaultUpdateLogin    = true
	DefaultUpdateLoginMode = "detect"
	DefaultUpdatePlayerHistory = true

	// List
	DefaultListSort      = "recent"
	DefaultListLimit     = 100
	DefaultListDateFormat = "02/01/2006 15:04:05" // DD/MM/YYYY HH:mm:ss

	// DefaultListColumns is defined as a var to avoid slice literal in const error

	// Scanner
	DefaultScannerResultChannelSize = 256
	DefaultUpdateOpChannelSize      = 128
	DefaultProgressTickerInterval   = 500 * time.Millisecond

	// Protocol
	DefaultSLPMaxResponseBytes   = 1 << 20 // 1 MiB
	DefaultQueryMaxResponseBytes = 4096     // 4 KiB
	DefaultLoginMaxPacketBytes   = 2 << 20  // 2 MiB
	DefaultMaxStringLength       = 32767

	// Display
	DefaultProgressBarWidth        = 30
	DefaultMaxMotdDisplayLen       = 70
	DefaultMaxPluginsDisplayLen    = 60
	DefaultMaxPlayerNamesDisplayLen = 30

	// Storage
	DefaultSQLiteSyncMode = "NORMAL"

	// Common
	DefaultEnableQuery = true
	DefaultExpand     = true
	DefaultLoginUser  = "Herobrine"
	DefaultLogin     = true
	DefaultASCII      = false
	DefaultResume    = false
	DefaultDebugLogin = false
)

// DefaultListColumns is defined as a var to avoid slice literal in const error
var DefaultListColumns = []string{"ordinal", "address", "version", "login", "players", "latency", "last_seen", "motd", "map", "player_names"}

// Internal aliases for setDefaults
var (
	defaultPorts                  = DefaultPorts
	defaultProbeQPS              = DefaultProbeQPS
	defaultProbeWorkers          = DefaultProbeWorkers
	defaultProbeTimeout          = DefaultProbeTimeout
	defaultUpdateQPS              = DefaultUpdateQPS
	defaultUpdateWorkers          = DefaultUpdateWorkers
	defaultUpdateTimeout          = DefaultUpdateTimeout
	defaultUpdateLimit            = DefaultUpdateLimit
	defaultListSort               = DefaultListSort
	defaultListLimit              = DefaultListLimit
	defaultListColumns            = DefaultListColumns
	defaultListDateFormat        = DefaultListDateFormat
	defaultScannerResultChannelSize = DefaultScannerResultChannelSize
	defaultUpdateOpChannelSize     = DefaultUpdateOpChannelSize
	defaultProgressTickerInterval   = DefaultProgressTickerInterval
	defaultSLPMaxResponseBytes     = DefaultSLPMaxResponseBytes
	defaultQueryMaxResponseBytes   = DefaultQueryMaxResponseBytes
	defaultLoginMaxPacketBytes     = DefaultLoginMaxPacketBytes
	defaultMaxStringLength         = DefaultMaxStringLength
	defaultProgressBarWidth        = DefaultProgressBarWidth
	defaultMaxMotdDisplayLen       = DefaultMaxMotdDisplayLen
	defaultMaxPluginsDisplayLen    = DefaultMaxPluginsDisplayLen
	defaultMaxPlayerNamesDisplayLen = DefaultMaxPlayerNamesDisplayLen
	defaultSQLiteSyncMode          = DefaultSQLiteSyncMode
	defaultEnableQuery            = DefaultEnableQuery
	defaultExpand                 = DefaultExpand
	defaultLoginUser              = DefaultLoginUser
	defaultProbeLogin            = DefaultProbeLogin
	defaultUpdateLogin          = DefaultUpdateLogin
	defaultProbeLoginMode        = DefaultProbeLoginMode
	defaultUpdateLoginMode       = DefaultUpdateLoginMode
	defaultProbePlayerHistory    = DefaultProbePlayerHistory
	defaultUpdatePlayerHistory   = DefaultUpdatePlayerHistory
	defaultASCII                  = DefaultASCII
	defaultResume                = DefaultResume
	defaultDebugLogin            = DefaultDebugLogin
)

// Load reads configuration from the layered sources and returns a
// fully-populated Config. A missing config file is not an error —
// defaults still apply.
func Load() (*Config, error) {
	return LoadFrom("")
}

// LoadFrom is like Load but accepts an explicit config-file path. The
// empty string falls back to the XDG path. A missing file is not an
// error; defaults are applied instead.
func LoadFrom(explicitPath string) (*Config, error) {
	v := viper.New()

	if explicitPath != "" {
		v.SetConfigFile(explicitPath)
	} else {
		v.SetConfigName("config")
		v.SetConfigType("yaml")
		v.AddConfigPath(xdgConfigHome())
	}

	setDefaults(v)
	v.SetEnvPrefix("DOORSNIFF")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))
	v.AutomaticEnv()

	// Skip ReadInConfig entirely when the explicit path is missing;
	// when searching the XDG path, viper returns a typed
	// ConfigFileNotFoundError that we also accept.
	if explicitPath == "" || fileExists(explicitPath) {
		if err := v.ReadInConfig(); err != nil {
			var notFound viper.ConfigFileNotFoundError
			if !errors.As(err, &notFound) {
				return nil, fmt.Errorf("reading config: %w", err)
			}
		}
	}

	cfg := &Config{
		DatabasePath: DefaultDatabasePath(),
		ConfigPath:   DefaultConfigPath(),
		SeedsDir:     DefaultSeedsDir(),
	}
	if err := v.Unmarshal(cfg, viper.DecodeHook(mapstructure.ComposeDecodeHookFunc(
		mapstructure.StringToTimeDurationHookFunc(),
	))); err != nil {
		return nil, fmt.Errorf("unmarshal config: %w", err)
	}

	return cfg, nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// DefaultConfigPath returns the canonical XDG location of the config
// file. The directory is not created — only the path is returned.
func DefaultConfigPath() string {
	return filepath.Join(xdgConfigHome(), "config.yaml")
}

// DefaultDatabasePath returns the canonical XDG location of the SQLite
// database.
func DefaultDatabasePath() string {
	return filepath.Join(xdgDataHome(), "doorsniff.db")
}

// DefaultSeedsDir returns the canonical XDG location of the seeds
// directory. Seed files (one host per line) live in this directory; the
// scanner reads every regular file inside it. The directory is not
// created — the user is expected to drop files in.
func DefaultSeedsDir() string {
	return filepath.Join(xdgConfigHome(), "seeds")
}

func xdgConfigHome() string {
	if v := os.Getenv("DOORSNIFF_CONFIG_PATH"); v != "" {
		return filepath.Dir(v)
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("APPDATA"); v != "" {
			return filepath.Join(v, "doorsniff")
		}
	} else {
		if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
			return filepath.Join(v, "doorsniff")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Roaming", "doorsniff")
	}
	return filepath.Join(home, ".config", "doorsniff")
}

func xdgDataHome() string {
	if v := os.Getenv("DOORSNIFF_DATA_PATH"); v != "" {
		return filepath.Dir(v)
	}
	if runtime.GOOS == "windows" {
		if v := os.Getenv("LOCALAPPDATA"); v != "" {
			return filepath.Join(v, "doorsniff")
		}
	} else {
		if v := os.Getenv("XDG_DATA_HOME"); v != "" {
			return filepath.Join(v, "doorsniff")
		}
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	if runtime.GOOS == "windows" {
		return filepath.Join(home, "AppData", "Local", "doorsniff")
	}
	return filepath.Join(home, ".local", "share", "doorsniff")
}

func setDefaults(v *viper.Viper) {
	// Scan
	v.SetDefault("ports", defaultPorts)

	// Probe
	v.SetDefault("probe_qps", defaultProbeQPS)
	v.SetDefault("probe_workers", defaultProbeWorkers)
	v.SetDefault("probe_timeout", defaultProbeTimeout)
	v.SetDefault("probe_login", defaultProbeLogin)
	v.SetDefault("probe_login_mode", defaultProbeLoginMode)
	v.SetDefault("probe_player_history", defaultProbePlayerHistory)

	// Update
	v.SetDefault("update_qps", defaultUpdateQPS)
	v.SetDefault("update_workers", defaultUpdateWorkers)
	v.SetDefault("update_timeout", defaultUpdateTimeout)
	v.SetDefault("update_limit", defaultUpdateLimit)
	v.SetDefault("update_login", defaultUpdateLogin)
	v.SetDefault("update_login_mode", defaultUpdateLoginMode)
	v.SetDefault("update_player_history", defaultUpdatePlayerHistory)

	// List
	v.SetDefault("list_sort", defaultListSort)
	v.SetDefault("list_limit", defaultListLimit)
	v.SetDefault("list_columns", defaultListColumns)
	v.SetDefault("list_date_format", defaultListDateFormat)

	// Scanner
	v.SetDefault("scanner_result_channel_size", defaultScannerResultChannelSize)
	v.SetDefault("update_op_channel_size", defaultUpdateOpChannelSize)
	v.SetDefault("progress_ticker_interval", defaultProgressTickerInterval)

	// Protocol
	v.SetDefault("slp_max_response_bytes", defaultSLPMaxResponseBytes)
	v.SetDefault("query_max_response_bytes", defaultQueryMaxResponseBytes)
	v.SetDefault("login_max_packet_bytes", defaultLoginMaxPacketBytes)
	v.SetDefault("max_string_length", defaultMaxStringLength)

	// Display
	v.SetDefault("progress_bar_width", defaultProgressBarWidth)
	v.SetDefault("max_motd_display_len", defaultMaxMotdDisplayLen)
	v.SetDefault("max_plugins_display_len", defaultMaxPluginsDisplayLen)
	v.SetDefault("max_player_names_display_len", defaultMaxPlayerNamesDisplayLen)

	// Storage
	v.SetDefault("sqlite_sync_mode", defaultSQLiteSyncMode)

	// Common
	v.SetDefault("enable_query", defaultEnableQuery)
	v.SetDefault("enable_expansion", defaultExpand)
	v.SetDefault("login_user", defaultLoginUser)
	v.SetDefault("ascii", defaultASCII)
	v.SetDefault("resume", defaultResume)
	v.SetDefault("debug_login", defaultDebugLogin)
}
