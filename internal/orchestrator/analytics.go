package orchestrator

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/agent"
	"github.com/YuanJey/go-x-ai-operations/internal/id"
	"github.com/YuanJey/go-x-ai-operations/internal/workflow"
)

type AnalyticsWorkflow struct {
	db        *sql.DB
	accountID string
	agents    *agent.Runtime
	workflows *workflow.Repository
}

func NewAnalyticsWorkflow(
	db *sql.DB,
	accountID string,
	agents *agent.Runtime,
	workflows *workflow.Repository,
) *AnalyticsWorkflow {
	return &AnalyticsWorkflow{
		db: db, accountID: accountID,
		agents: agents, workflows: workflows,
	}
}

func (w *AnalyticsWorkflow) Run(
	ctx context.Context,
	ruleVersionID string,
	allowRemote bool,
) (string, error) {
	metrics, err := w.recentMetrics(ctx, 200)
	if err != nil {
		return "", err
	}
	run, err := w.workflows.Start(ctx, w.accountID, "analytics", ruleVersionID, map[string]any{
		"metric_count": len(metrics),
	})
	if err != nil {
		return "", err
	}
	fail := func(runErr error) (string, error) {
		_ = w.workflows.Fail(ctx, run.ID, runErr)
		return "", runErr
	}
	analysisResult, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: run.ID, Definition: agent.MetricsAnalystDefinition(),
		Input: map[string]any{"metrics": metrics}, AllowRemote: allowRemote,
	})
	if err != nil {
		return fail(err)
	}
	var analysis struct {
		Observations []string           `json:"observations"`
		Weights      map[string]float64 `json:"recommended_pillar_weights"`
		Confidence   float64            `json:"confidence"`
	}
	if err := json.Unmarshal(analysisResult.Output, &analysis); err != nil {
		return fail(err)
	}
	if err := validateWeights(analysis.Weights); err != nil {
		return fail(err)
	}
	experimentResult, err := w.agents.Run(ctx, agent.RunRequest{
		WorkflowID: run.ID, Definition: agent.ExperimentDefinition(),
		Input: map[string]any{
			"analysis":     analysis,
			"sample_count": len(metrics),
		},
		AllowRemote: allowRemote,
	})
	if err != nil {
		return fail(err)
	}
	var experiment map[string]any
	if err := json.Unmarshal(experimentResult.Output, &experiment); err != nil {
		return fail(err)
	}
	strategy := map[string]any{
		"analysis":            analysis,
		"experiment":          experiment,
		"source_metric_count": len(metrics),
		"rule_version_id":     ruleVersionID,
	}
	strategyID, err := w.saveRecommendation(ctx, strategy)
	if err != nil {
		return fail(err)
	}
	if err := w.workflows.Succeed(ctx, run.ID, map[string]string{
		"strategy_version_id": strategyID,
	}); err != nil {
		return "", err
	}
	return strategyID, nil
}

func (w *AnalyticsWorkflow) recentMetrics(
	ctx context.Context,
	limit int,
) ([]json.RawMessage, error) {
	rows, err := w.db.QueryContext(ctx, `
		SELECT metrics_json FROM metric_snapshots
		WHERE account_id = ?
		ORDER BY captured_at DESC LIMIT ?`,
		w.accountID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("load analytics metrics: %w", err)
	}
	defer rows.Close()
	metrics := make([]json.RawMessage, 0)
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		metrics = append(metrics, json.RawMessage(body))
	}
	return metrics, rows.Err()
}

func (w *AnalyticsWorkflow) saveRecommendation(
	ctx context.Context,
	strategy any,
) (string, error) {
	body, err := json.Marshal(strategy)
	if err != nil {
		return "", err
	}
	tx, err := w.db.BeginTx(ctx, nil)
	if err != nil {
		return "", err
	}
	defer tx.Rollback()
	var version int
	if err := tx.QueryRowContext(ctx, `
		SELECT COALESCE(MAX(version), 0) + 1
		FROM strategy_versions WHERE account_id = ?`,
		w.accountID,
	).Scan(&version); err != nil {
		return "", err
	}
	strategyID := id.New("strategy")
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO strategy_versions(
			id, account_id, version, state, strategy_json, created_at
		) VALUES (?, ?, ?, 'recommended', ?, ?)`,
		strategyID, w.accountID, version, string(body),
		time.Now().UTC().Format(time.RFC3339Nano),
	); err != nil {
		return "", err
	}
	return strategyID, tx.Commit()
}

func validateWeights(weights map[string]float64) error {
	if len(weights) == 0 {
		return fmt.Errorf("analytics returned no content-pillar weights")
	}
	total := 0.0
	for name, weight := range weights {
		if name == "" || weight < 0 || weight > 1 {
			return fmt.Errorf("invalid content-pillar weight")
		}
		total += weight
	}
	if math.Abs(total-1) > 0.01 {
		return fmt.Errorf("recommended content-pillar weights must sum to 1")
	}
	return nil
}
