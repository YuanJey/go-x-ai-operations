package backup

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

type Service struct {
	db        *sql.DB
	directory string
	retain    int
}

func New(db *sql.DB, directory string, retain int) *Service {
	return &Service{db: db, directory: directory, retain: retain}
}

func (s *Service) Create(ctx context.Context) (string, error) {
	if err := os.MkdirAll(s.directory, 0o700); err != nil {
		return "", fmt.Errorf("create backup directory: %w", err)
	}
	path := filepath.Join(
		s.directory,
		"xops-"+time.Now().UTC().Format("20060102T150405Z")+".db",
	)
	escaped := strings.ReplaceAll(path, "'", "''")
	if _, err := s.db.ExecContext(ctx, "VACUUM INTO '"+escaped+"'"); err != nil {
		return "", fmt.Errorf("create SQLite backup: %w", err)
	}
	if err := checkIntegrity(ctx, path); err != nil {
		_ = os.Remove(path)
		return "", err
	}
	if err := s.prune(); err != nil {
		return path, err
	}
	return path, nil
}

func checkIntegrity(ctx context.Context, path string) error {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open backup for verification: %w", err)
	}
	defer db.Close()
	var result string
	if err := db.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return fmt.Errorf("verify backup integrity: %w", err)
	}
	if result != "ok" {
		return fmt.Errorf("backup integrity check returned %s", result)
	}
	return nil
}

func (s *Service) prune() error {
	entries, err := filepath.Glob(filepath.Join(s.directory, "xops-*.db"))
	if err != nil {
		return err
	}
	sort.Strings(entries)
	excess := len(entries) - s.retain
	for index := 0; index < excess; index++ {
		if err := os.Remove(entries[index]); err != nil {
			return fmt.Errorf("remove expired backup: %w", err)
		}
	}
	return nil
}
