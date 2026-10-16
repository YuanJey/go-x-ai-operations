package orchestrator

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/agent"
	"github.com/YuanJey/go-x-ai-operations/internal/approval"
	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/llm"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
	"github.com/YuanJey/go-x-ai-operations/internal/topic"
	"github.com/YuanJey/go-x-ai-operations/internal/workflow"
)

type workflowGateway struct {
	responses []string
	index     int
}

func (g *workflowGateway) Generate(
	_ context.Context,
	_ string,
	_ llm.Request,
) (llm.Response, error) {
	response := g.responses[g.index]
	g.index++
	return llm.Response{
		Profile: "fake", Protocol: "fake", Model: "fake",
		Content: json.RawMessage(response),
	}, nil
}

func TestPostWorkflowPersistsApprovedContentAndOutbox(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:post-workflow-test?mode=memory&cache=shared")
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
		t.Fatalf("save rule: %v", err)
	}
	if err := ruleRepository.Activate(ctx, version.ID, time.Now()); err != nil {
		t.Fatalf("activate rule: %v", err)
	}

	gateway := &workflowGateway{responses: []string{
		`{"title":"可靠任务","pillar":"实战方法","audience_question":"如何避免重复发布？","score":0.9}`,
		`{"candidates":[{"body":"先持久化发布操作，再调用外部 API。","claims":[]},{"body":"外部写入前，先保存操作状态。","claims":[]}]}`,
		`{"passed":true,"reasons":[]}`,
		`{"passed":true,"reasons":[]}`,
		`{"passed":true,"reasons":[]}`,
		`{"body":"先持久化发布操作，再调用外部 API。"}`,
	}}
	agentRuntime := agent.NewRuntime(gateway, agent.NewRepository(store.DB))
	contentRepository := content.NewRepository(store.DB)
	postWorkflow := NewPostWorkflow(
		"account", agentRuntime, workflow.NewRepository(store.DB),
		contentRepository, approval.NewGate(280), ruleRepository,
		topic.NewRepository(store.DB),
	)
	item, err := postWorkflow.Run(ctx, version.ID, PostInput{
		Evidence: []EvidenceItem{{
			Title:   "Architecture",
			Summary: "External writes are prepared in a local operation record before sending.",
		}},
	}, true)
	if err != nil {
		t.Fatalf("run post workflow: %v", err)
	}
	if item.State != content.StateApprovedAuto {
		t.Fatalf("content state = %s", item.State)
	}
	var outboxCount int
	if err := store.DB.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM outbox_events
		WHERE event_type = 'content.approved' AND aggregate_id = ?`,
		item.ID,
	).Scan(&outboxCount); err != nil {
		t.Fatalf("count outbox events: %v", err)
	}
	if outboxCount != 1 {
		t.Fatalf("content outbox events = %d, want 1", outboxCount)
	}
}
