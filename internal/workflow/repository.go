package workflow

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Run struct {
	ID                string          `json:"id"`
	AccountID         string          `json:"account_id"`
	Kind              string          `json:"kind"`
	RuleVersionID     string          `json:"rule_version_id"`
	StrategyVersionID *string         `json:"strategy_version_id,omitempty"`
	State             string          `json:"state"`
	Input             json.RawMessage `json:"input"`
	Output            json.RawMessage `json:"output,omitempty"`
	Error             string          `json:"error,omitempty"`
	StartedAt         time.Time       `json:"started_at"`
	FinishedAt        *time.Time      `json:"finished_at,omitempty"`
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Start(
	ctx context.Context,
	accountID, kind, ruleVersionID string,
	input any,
) (Run, error) {
	body, err := json.Marshal(input)
	if err != nil {
		return Run{}, fmt.Errorf("encode workflow input: %w", err)
	}
	run := Run{
		ID: id.New("wf"), AccountID: accountID, Kind: kind,
		RuleVersionID: ruleVersionID, State: "running",
		Input: body, StartedAt: time.Now().UTC(),
	}
	_, err = r.db.ExecContext(ctx, `
		INSERT INTO workflow_runs(
			id, account_id, kind, rule_version_id, state,
			input_json, started_at
		) VALUES (?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.AccountID, run.Kind, run.RuleVersionID,
		run.State, string(run.Input), formatTime(run.StartedAt),
	)
	if err != nil {
		return Run{}, fmt.Errorf("start workflow: %w", err)
	}
	return run, nil
}

func (r *Repository) Succeed(ctx context.Context, runID string, output any) error {
	body, err := json.Marshal(output)
	if err != nil {
		return fmt.Errorf("encode workflow output: %w", err)
	}
	result, err := r.db.ExecContext(ctx, `
		UPDATE workflow_runs
		SET state = 'succeeded', output_json = ?, finished_at = ?
		WHERE id = ? AND state = 'running'`,
		string(body), formatTime(time.Now()), runID,
	)
	if err != nil {
		return fmt.Errorf("complete workflow: %w", err)
	}
	return requireOne(result)
}

func (r *Repository) Fail(ctx context.Context, runID string, runError error) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE workflow_runs
		SET state = 'failed', error = ?, finished_at = ?
		WHERE id = ? AND state = 'running'`,
		runError.Error(), formatTime(time.Now()), runID,
	)
	if err != nil {
		return fmt.Errorf("fail workflow: %w", err)
	}
	return requireOne(result)
}

func requireOne(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("workflow is no longer running")
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
