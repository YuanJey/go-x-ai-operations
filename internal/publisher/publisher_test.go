package publisher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/quota"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
	"github.com/YuanJey/go-x-ai-operations/internal/xapi"
)

func TestDryRunPublisherUsesOperationStateMachine(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:publisher-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	version := activeRule(t, ctx, store)

	body := "Persist the operation before an external write."
	sum := sha256.Sum256([]byte(body))
	item, err := content.NewRepository(store.DB).SaveApproved(ctx, content.Item{
		AccountID: "account", RuleVersionID: version.ID, Kind: content.KindPost,
		Body: body, BodyHash: hex.EncodeToString(sum[:]), Language: "en",
	}, content.Approval{
		Approved: true, FactCheckPassed: true, PolicyPassed: true,
		SimilarityPassed: true, DeterministicPassed: true,
	})
	if err != nil {
		t.Fatalf("save content: %v", err)
	}

	operations := NewOperationRepository(store.DB)
	publisher := NewPostPublisher(
		store.DB, xapi.NewDryRunAdapter("@account"),
		quota.NewCounter(store.DB), operations, "account",
	)
	operation, err := publisher.Publish(ctx, item.ID, version.ID)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if operation.State != OperationSucceeded || operation.XPostID == nil {
		t.Fatalf("operation = %+v", operation)
	}
	stored, err := operations.Get(ctx, operation.ID)
	if err != nil {
		t.Fatalf("get operation: %v", err)
	}
	if stored.State != OperationSucceeded || stored.XPostID == nil {
		t.Fatalf("stored operation = %+v", stored)
	}
	published, err := content.NewRepository(store.DB).Get(ctx, item.ID)
	if err != nil {
		t.Fatalf("get content: %v", err)
	}
	if published.State != content.StatePublished {
		t.Fatalf("content state = %s", published.State)
	}
}

func activeRule(
	t *testing.T,
	ctx context.Context,
	store *sqlitestore.Store,
) rules.Version {
	t.Helper()
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
		t.Fatalf("save rule: %v", err)
	}
	if err := repository.Activate(ctx, version.ID, time.Now()); err != nil {
		t.Fatalf("activate rule: %v", err)
	}
	return version
}
