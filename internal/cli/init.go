package cli

import (
	"context"
	"fmt"
	"os"
	"path/filepath"

	"github.com/renanbray/swoomps/internal/config"
	"github.com/renanbray/swoomps/internal/storage"
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
	return nil
}
