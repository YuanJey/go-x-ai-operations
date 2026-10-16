package outbox

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
)

func TestDispatcherPublishesEventOnce(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:outbox-test?mode=memory&cache=shared")
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
	ruleRepository := rules.NewRepository(store.DB)
	if err := ruleRepository.EnsureAccount(ctx, "account", "@account", "UTC"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	version, err := ruleRepository.Save(ctx, "account", raw, result)
	if err != nil {
		t.Fatalf("save rule: %v", err)
	}
	if err := ruleRepository.Activate(ctx, version.ID, time.Now()); err != nil {
		t.Fatalf("activate rule: %v", err)
	}

	repository := NewRepository(store.DB)
	dispatcher := NewDispatcher(repository, "worker", time.Minute, 10)
	handled := 0
	dispatcher.Register("rule.activated", HandlerFunc(func(_ context.Context, event Event) error {
		handled++
		return nil
	}))
	completed, err := dispatcher.Dispatch(ctx, time.Now())
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if completed != 1 || handled != 1 {
		t.Fatalf("completed=%d handled=%d, want 1", completed, handled)
	}
	completed, err = dispatcher.Dispatch(ctx, time.Now())
	if err != nil {
		t.Fatalf("second dispatch: %v", err)
	}
	if completed != 0 || handled != 1 {
		t.Fatalf("event was dispatched more than once")
	}
}
