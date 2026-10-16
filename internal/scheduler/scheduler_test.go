package scheduler

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/jobs"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
)

func TestTickCreatesDeterministicJobsAndDeduplicates(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:scheduler-test?mode=memory&cache=shared")
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
	if err := ruleRepository.EnsureAccount(ctx, "account", "@account", "Asia/Shanghai"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	version, err := ruleRepository.Save(ctx, "account", raw, result)
	if err != nil {
		t.Fatalf("save rules: %v", err)
	}
	if err := ruleRepository.Activate(ctx, version.ID, time.Now()); err != nil {
		t.Fatalf("activate rules: %v", err)
	}

	jobRepository := jobs.NewRepository(store.DB)
	scheduler := New("account", ruleRepository, jobRepository)
	location, _ := time.LoadLocation("Asia/Shanghai")
	now := time.Date(2026, 10, 17, 0, 0, 0, 0, location)
	first, err := scheduler.Tick(ctx, now)
	if err != nil {
		t.Fatalf("first tick: %v", err)
	}
	if first.Enqueued == 0 {
		t.Fatal("first tick enqueued no jobs")
	}
	postJobs, err := jobRepository.List(ctx, jobs.Filter{
		AccountID: "account",
		Action:    "publish_post",
		Limit:     20,
	})
	if err != nil {
		t.Fatalf("list post jobs: %v", err)
	}
	if len(postJobs) != 10 {
		t.Fatalf("post jobs = %d, want 10", len(postJobs))
	}

	second, err := scheduler.Tick(ctx, now)
	if err != nil {
		t.Fatalf("second tick: %v", err)
	}
	if second.Enqueued != 0 || second.Deduplicated != first.Enqueued {
		t.Fatalf("second tick = %+v, want all jobs deduplicated", second)
	}
}
