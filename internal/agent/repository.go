package agent

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Start(ctx context.Context, run Run) error {
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO agent_runs(
			id, workflow_id, agent_type, prompt_version,
			input_schema_version, output_schema_version,
			input_json, input_hash, status, started_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		run.ID, run.WorkflowID, run.AgentType, run.PromptVersion,
		run.InputSchemaVersion, run.OutputSchemaVersion,
		run.InputJSON, run.InputHash, run.Status, formatTime(run.StartedAt),
	)
	if err != nil {
		return fmt.Errorf("start agent run: %w", err)
	}
	return nil
}

func (r *Repository) Succeed(
	ctx context.Context,
	runID string,
	responseProvider, responseProtocol, model, outputJSON, outputHash string,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET provider_profile = ?, provider_protocol = ?, model = ?,
			output_json = ?, output_hash = ?, status = 'succeeded',
			finished_at = ?
		WHERE id = ? AND status = 'running'`,
		responseProvider, responseProtocol, model, outputJSON, outputHash,
		formatTime(time.Now()), runID,
	)
	if err != nil {
		return fmt.Errorf("complete agent run: %w", err)
	}
	return requireOne(result)
}

func (r *Repository) Fail(ctx context.Context, runID string, runError error) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE agent_runs
		SET status = 'failed', error = ?, finished_at = ?
		WHERE id = ? AND status = 'running'`,
		runError.Error(), formatTime(time.Now()), runID,
	)
	if err != nil {
		return fmt.Errorf("fail agent run: %w", err)
	}
	return requireOne(result)
}

func requireOne(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("agent run is no longer running")
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
