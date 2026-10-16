package sqlite

import (
	"context"
	"path/filepath"
	"testing"
)

func TestOpenAppliesMigrationsAndPragmas(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, filepath.Join(t.TempDir(), "xops.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	var journalMode string
	if err := store.DB.QueryRowContext(ctx, "PRAGMA journal_mode").Scan(&journalMode); err != nil {
		t.Fatalf("read journal mode: %v", err)
	}
	if journalMode != "wal" {
		t.Fatalf("journal mode = %s, want wal", journalMode)
	}
	var migrationCount int
	if err := store.DB.QueryRowContext(ctx, "SELECT COUNT(*) FROM schema_migrations").Scan(&migrationCount); err != nil {
		t.Fatalf("count migrations: %v", err)
	}
	if migrationCount != 2 {
		t.Fatalf("migration count = %d, want 2", migrationCount)
	}
	var alertTable string
	if err := store.DB.QueryRowContext(ctx, `
		SELECT name FROM sqlite_master WHERE type = 'table' AND name = 'alerts'`,
	).Scan(&alertTable); err != nil {
		t.Fatalf("alerts table was not created: %v", err)
	}
}
