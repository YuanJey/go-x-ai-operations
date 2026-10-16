package rules

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
)

func TestDefaultRulesValidateAndGenerateSlots(t *testing.T) {
	raw := readDefaultRules(t)
	validator := NewValidator(DefaultRegistry())

	result, err := validator.ParseAndValidate(raw)
	if err != nil {
		t.Fatalf("validate default rules: %v", err)
	}
	if result.Slots["publish_post"] != 16 {
		t.Fatalf("publish capacity = %d, want 16", result.Slots["publish_post"])
	}

	slots, err := DailySlots(
		result.Document,
		"publish_post",
		time.Date(2026, 10, 17, 0, 0, 0, 0, time.UTC),
	)
	if err != nil {
		t.Fatalf("generate slots: %v", err)
	}
	if len(slots) != 10 {
		t.Fatalf("slot count = %d, want 10", len(slots))
	}
	for index := 1; index < len(slots); index++ {
		if slots[index].Sub(slots[index-1]) < time.Hour {
			t.Fatalf("slots %d and %d violate cooldown", index-1, index)
		}
	}
}

func TestRulesRejectInfeasibleQuota(t *testing.T) {
	raw := strings.Replace(string(readDefaultRules(t)), "quota: {per_day: 10}", "quota: {per_day: 17}", 1)
	validator := NewValidator(DefaultRegistry())

	_, err := validator.ParseAndValidate([]byte(raw))
	if err == nil || !strings.Contains(err.Error(), "exceeds daily capacity 16") {
		t.Fatalf("error = %v, want capacity validation", err)
	}
}

func TestRepositoryActivationIsAtomic(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:rules-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()

	repo := NewRepository(store.DB)
	if err := repo.EnsureAccount(ctx, "account", "@account", "UTC"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	validator := NewValidator(DefaultRegistry())
	result, err := validator.ParseAndValidate(readDefaultRules(t))
	if err != nil {
		t.Fatalf("validate: %v", err)
	}

	first, err := repo.Save(ctx, "account", readDefaultRules(t), result)
	if err != nil {
		t.Fatalf("save first rule: %v", err)
	}
	if err := repo.Activate(ctx, first.ID, time.Now()); err != nil {
		t.Fatalf("activate first rule: %v", err)
	}
	second, err := repo.Save(ctx, "account", readDefaultRules(t), result)
	if err != nil {
		t.Fatalf("save second rule: %v", err)
	}
	if err := repo.Activate(ctx, second.ID, time.Now()); err != nil {
		t.Fatalf("activate second rule: %v", err)
	}

	active, err := repo.Active(ctx, "account")
	if err != nil {
		t.Fatalf("get active rule: %v", err)
	}
	if active.ID != second.ID {
		t.Fatalf("active rule = %s, want %s", active.ID, second.ID)
	}
	versions, err := repo.List(ctx, "account")
	if err != nil {
		t.Fatalf("list rules: %v", err)
	}
	if versions[1].State != "superseded" {
		t.Fatalf("first rule state = %s, want superseded", versions[1].State)
	}
}

func readDefaultRules(t *testing.T) []byte {
	t.Helper()
	path := filepath.Join("..", "..", "config", "rules.yaml")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return raw
}
