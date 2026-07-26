// Package config loads and exposes user configuration.
//
// Resolution order (first set wins):
//
//  1. explicit viper.Set
//  2. bound flag (CLI)
//  3. env var (SWOOMPS_*)
//  4. config file (XDG_CONFIG_HOME/swoomps/config.yaml)
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
	"strings"
	"time"

	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/viper"
)

// Config is the resolved user configuration. Every field is populated
// from the layered sources, with sensible defaults applied.
type Config struct {
	QPS             int           `mapstructure:"qps"`
	Workers         int           `mapstructure:"workers"`
	Timeout         time.Duration `mapstructure:"timeout"`
	Ports           string        `mapstructure:"ports"`
	DatabasePath    string        `mapstructure:"database_path"`
	ConfigPath      string        `mapstructure:"config_path"`
	EnableQuery     bool          `mapstructure:"enable_query"`
	EnableExpansion bool          `mapstructure:"enable_expansion"`
	SeedsDir        string        `mapstructure:"seeds_dir"`
	GeoIPPath       string        `mapstructure:"geoip_path"`
	Verbose         bool          `mapstructure:"verbose"`
}

// Defaults applied when no other source provides a value.
const (
	defaultQPS         = 50
	defaultWorkers     = 64
	defaultTimeout     = 3 * time.Second
	defaultPorts       = "1-65535"
	defaultEnableQuery = true
	defaultExpand      = true
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
	v.SetEnvPrefix("SWOOMPS")
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
	return filepath.Join(xdgDataHome(), "swoomps.db")
}

// DefaultSeedsDir returns the canonical XDG location of the seeds
// directory. Seed files (one host per line) live in this directory; the
// scanner reads every regular file inside it. The directory is not
// created — the user is expected to drop files in.
func DefaultSeedsDir() string {
	return filepath.Join(xdgConfigHome(), "seeds")
}

func xdgConfigHome() string {
	if v := os.Getenv("SWOOMPS_CONFIG_PATH"); v != "" {
		return filepath.Dir(v)
	}
	if v := os.Getenv("XDG_CONFIG_HOME"); v != "" {
		return filepath.Join(v, "swoomps")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".config", "swoomps")
}

func xdgDataHome() string {
	if v := os.Getenv("SWOOMPS_DATA_PATH"); v != "" {
		return filepath.Dir(v)
	}
	if v := os.Getenv("XDG_DATA_HOME"); v != "" {
		return filepath.Join(v, "swoomps")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return filepath.Join(home, ".local", "share", "swoomps")
}

func setDefaults(v *viper.Viper) {
	v.SetDefault("qps", defaultQPS)
	v.SetDefault("workers", defaultWorkers)
	v.SetDefault("timeout", defaultTimeout)
	v.SetDefault("ports", defaultPorts)
	v.SetDefault("enable_query", defaultEnableQuery)
	v.SetDefault("enable_expansion", defaultExpand)
}
