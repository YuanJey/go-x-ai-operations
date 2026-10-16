package llm

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) SyncProfiles(ctx context.Context, profiles []Profile) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin provider profile sync: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	for _, profile := range profiles {
		capabilities, err := json.Marshal(profile.Capabilities)
		if err != nil {
			return fmt.Errorf("encode provider capabilities: %w", err)
		}
		_, err = tx.ExecContext(ctx, `
			INSERT INTO provider_profiles(
				id, name, protocol, base_url, model, location,
				capabilities_json, api_key_ref, enabled, created_at, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			ON CONFLICT(name) DO UPDATE SET
				protocol = excluded.protocol,
				base_url = excluded.base_url,
				model = excluded.model,
				location = excluded.location,
				capabilities_json = excluded.capabilities_json,
				api_key_ref = excluded.api_key_ref,
				enabled = excluded.enabled,
				updated_at = excluded.updated_at`,
			id.New("provider"), profile.Name, profile.Protocol, profile.BaseURL,
			profile.Model, profile.Location, string(capabilities), nullableString(profile.APIKeyRef),
			profile.Enabled, now, now,
		)
		if err != nil {
			return fmt.Errorf("sync provider profile %s: %w", profile.Name, err)
		}
	}
	return tx.Commit()
}

func (r *Repository) RecordLLMUsage(ctx context.Context, record UsageRecord) error {
	var profileID string
	if err := r.db.QueryRowContext(ctx,
		"SELECT id FROM provider_profiles WHERE name = ?", record.ProviderProfile,
	).Scan(&profileID); err != nil {
		return fmt.Errorf("resolve provider profile: %w", err)
	}
	var agentRunID any
	if record.AgentRunID != "" {
		agentRunID = record.AgentRunID
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO llm_usage(
			id, provider_profile_id, agent_run_id, model,
			prompt_tokens, completion_tokens, estimated_cost,
			currency, latency_ms, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		record.ID, profileID, agentRunID, record.Model,
		record.Usage.PromptTokens, record.Usage.CompletionTokens,
		record.Usage.EstimatedCost, defaultCurrency(record.Usage.Currency),
		record.Latency.Milliseconds(), record.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("record LLM usage: %w", err)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func defaultCurrency(value string) string {
	if value == "" {
		return "USD"
	}
	return value
}
