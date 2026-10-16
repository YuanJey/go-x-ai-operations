package jobs

import (
	"context"
	"database/sql"
	"testing"
	"time"

	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
)

func TestLeaseRecoversExpiredJobWithoutDuplicating(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:jobs-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	ruleID := seedRule(t, ctx, store.DB)

	repository := NewRepository(store.DB)
	now := time.Now().UTC()
	created, err := repository.Enqueue(ctx, NewJob{
		AccountID:     "account",
		Action:        "publish_post",
		RuleVersionID: ruleID,
		DedupeKey:     "post-slot-1",
		RunAt:         now.Add(-time.Minute),
		MaxAttempts:   2,
	})
	if err != nil || !created {
		t.Fatalf("enqueue: created=%v err=%v", created, err)
	}
	duplicate, err := repository.Enqueue(ctx, NewJob{
		AccountID: "account", Action: "publish_post", RuleVersionID: ruleID,
		DedupeKey: "post-slot-1", RunAt: now, MaxAttempts: 2,
	})
	if err != nil || duplicate {
		t.Fatalf("duplicate enqueue: created=%v err=%v", duplicate, err)
	}

	first, err := repository.Lease(ctx, "worker-a", now, time.Minute, 1)
	if err != nil || len(first) != 1 {
		t.Fatalf("first lease: jobs=%d err=%v", len(first), err)
	}
	second, err := repository.Lease(ctx, "worker-b", now.Add(2*time.Minute), time.Minute, 1)
	if err != nil || len(second) != 1 {
		t.Fatalf("recovered lease: jobs=%d err=%v", len(second), err)
	}
	if second[0].Attempts != 2 || second[0].LeaseOwner == nil || *second[0].LeaseOwner != "worker-b" {
		t.Fatalf("unexpected recovered job: %+v", second[0])
	}
	if err := repository.Complete(ctx, second[0].ID, "worker-b"); err != nil {
		t.Fatalf("complete recovered job: %v", err)
	}
}

func seedRule(t *testing.T, ctx context.Context, db *sql.DB) string {
	t.Helper()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.ExecContext(ctx, `
		INSERT INTO accounts(id, handle, timezone, state, created_at, updated_at)
		VALUES ('account', '@account', 'UTC', 'active', ?, ?)`, now, now); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	if _, err := db.ExecContext(ctx, `
		INSERT INTO rule_versions(
			id, account_id, version, state, source_yaml, normalized_json,
			config_hash, schema_version, effective_at, activated_at, created_at
		) VALUES ('rule-1', 'account', 1, 'active', '', '{}', 'hash', 1, ?, ?, ?)`,
		now, now, now,
	); err != nil {
		t.Fatalf("insert rule: %v", err)
	}
	return "rule-1"
}
