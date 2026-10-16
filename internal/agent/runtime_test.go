package agent

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/YuanJey/go-x-ai-operations/internal/llm"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
	"github.com/YuanJey/go-x-ai-operations/internal/workflow"
)

type queuedGateway struct {
	responses []string
	calls     int
}

func (g *queuedGateway) Generate(
	_ context.Context,
	_ string,
	_ llm.Request,
) (llm.Response, error) {
	response := g.responses[g.calls]
	g.calls++
	return llm.Response{
		Profile: "fake", Protocol: "fake", Model: "fake",
		Content: json.RawMessage(response),
	}, nil
}

func TestRuntimeRepairsInvalidOutputOnce(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:agent-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	version := seedWorkflowRule(t, ctx, store)
	workflowRepository := workflow.NewRepository(store.DB)
	run, err := workflowRepository.Start(ctx, "account", "test", version.ID, map[string]any{})
	if err != nil {
		t.Fatalf("start workflow: %v", err)
	}
	gateway := &queuedGateway{responses: []string{
		`{"body":123}`,
		`{"body":"valid text"}`,
	}}
	runtime := NewRuntime(gateway, NewRepository(store.DB))
	result, err := runtime.Run(ctx, RunRequest{
		WorkflowID:  run.ID,
		Definition:  PostEditorDefinition(),
		Input:       map[string]string{"body": "draft"},
		AllowRemote: true,
	})
	if err != nil {
		t.Fatalf("run agent: %v", err)
	}
	if !result.Repaired || gateway.calls != 2 {
		t.Fatalf("result=%+v calls=%d, want one repair", result, gateway.calls)
	}
}

func seedWorkflowRule(
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
	return version
}
