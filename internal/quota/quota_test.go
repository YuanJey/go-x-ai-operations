package quota

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
)

func TestDailyQuotaIsAtomicAndReleasable(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:quota-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	raw, err := os.ReadFile(filepath.Join("..", "..", "config", "rules.yaml"))
	if err != nil {
		t.Fatalf("read rules: %v", err)
	}
	validator := rules.NewValidator(rules.DefaultRegistry())
	result, err := validator.ParseAndValidate(raw)
	if err != nil {
		t.Fatalf("validate rules: %v", err)
	}
	repository := rules.NewRepository(store.DB)
	if err := repository.EnsureAccount(ctx, "account", "@account", "Asia/Shanghai"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	version, err := repository.Save(ctx, "account", raw, result)
	if err != nil {
		t.Fatalf("save rules: %v", err)
	}
	if err := repository.Activate(ctx, version.ID, time.Now()); err != nil {
		t.Fatalf("activate rules: %v", err)
	}

	counter := NewCounter(store.DB)
	now := time.Date(2026, 10, 17, 4, 0, 0, 0, time.UTC)
	var last Decision
	for index := 0; index < 10; index++ {
		last, err = counter.Acquire(ctx, "account", version.ID, "publish_post", now)
		if err != nil {
			t.Fatalf("acquire %d: %v", index+1, err)
		}
	}
	_, err = counter.Acquire(ctx, "account", version.ID, "publish_post", now)
	if !errors.Is(err, ErrQuotaExceeded) {
		t.Fatalf("eleventh acquire error = %v, want quota exceeded", err)
	}
	if err := counter.Release(ctx, last); err != nil {
		t.Fatalf("release: %v", err)
	}
	if _, err := counter.Acquire(ctx, "account", version.ID, "publish_post", now); err != nil {
		t.Fatalf("acquire after release: %v", err)
	}
}
