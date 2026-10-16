package backup

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
)

func TestCreateProducesVerifiedBackup(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	store, err := sqlitestore.Open(ctx, filepath.Join(root, "source.db"))
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	service := New(store.DB, filepath.Join(root, "backups"), 2)
	path, err := service.Create(ctx)
	if err != nil {
		t.Fatalf("create backup: %v", err)
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat backup: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("backup is empty")
	}
}
