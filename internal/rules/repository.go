package rules

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

var ErrNoActiveRule = errors.New("no active rule version")

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) EnsureAccount(
	ctx context.Context,
	accountID, handle, timezone string,
) error {
	now := formatTime(time.Now())
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO accounts(id, handle, timezone, state, created_at, updated_at)
		VALUES (?, ?, ?, 'active', ?, ?)
		ON CONFLICT(id) DO UPDATE SET
			handle = excluded.handle,
			timezone = excluded.timezone,
			updated_at = excluded.updated_at`,
		accountID, handle, timezone, now, now,
	)
	if err != nil {
		return fmt.Errorf("ensure account: %w", err)
	}
	return nil
}

func (r *Repository) Save(
	ctx context.Context,
	accountID string,
	raw []byte,
	result ValidationResult,
) (Version, error) {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Version{}, fmt.Errorf("begin save rule: %w", err)
	}
	defer tx.Rollback()

	var nextVersion int
	if err := tx.QueryRowContext(ctx,
		"SELECT COALESCE(MAX(version), 0) + 1 FROM rule_versions WHERE account_id = ?",
		accountID,
	).Scan(&nextVersion); err != nil {
		return Version{}, fmt.Errorf("allocate rule version: %w", err)
	}

	now := time.Now().UTC()
	version := Version{
		ID:             id.New("rule"),
		AccountID:      accountID,
		Version:        nextVersion,
		State:          "validated",
		SourceYAML:     string(raw),
		NormalizedJSON: result.NormalizedJSON,
		ConfigHash:     result.ConfigHash,
		SchemaVersion:  result.Document.Config.Version,
		EffectiveAt:    result.Document.Config.EffectiveAt.UTC(),
		CreatedAt:      now,
		Document:       result.Document,
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO rule_versions(
			id, account_id, version, state, source_yaml, normalized_json,
			config_hash, schema_version, effective_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		version.ID, version.AccountID, version.Version, version.State,
		version.SourceYAML, version.NormalizedJSON, version.ConfigHash,
		version.SchemaVersion, formatTime(version.EffectiveAt), formatTime(now),
	)
	if err != nil {
		return Version{}, fmt.Errorf("insert rule version: %w", err)
	}
	if err := insertAudit(
		ctx, tx, accountID, "operator", "local",
		"rule.created", "rule_version", version.ID,
		map[string]any{"version": nextVersion, "config_hash": version.ConfigHash},
	); err != nil {
		return Version{}, err
	}
	if err := tx.Commit(); err != nil {
		return Version{}, fmt.Errorf("commit rule version: %w", err)
	}
	return version, nil
}

func (r *Repository) Activate(ctx context.Context, versionID string, at time.Time) error {
	now := time.Now().UTC()
	if at.IsZero() {
		at = now
	}
	at = at.UTC()

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin activate rule: %w", err)
	}
	defer tx.Rollback()

	var accountID, state string
	if err := tx.QueryRowContext(ctx,
		"SELECT account_id, state FROM rule_versions WHERE id = ?", versionID,
	).Scan(&accountID, &state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("rule version %s not found", versionID)
		}
		return fmt.Errorf("load rule version: %w", err)
	}
	if state == "rejected" {
		return fmt.Errorf("rejected rule version cannot be activated")
	}

	targetState := "active"
	eventType := "rule.activated"
	if at.After(now) {
		targetState = "scheduled"
		eventType = "rule.scheduled"
	} else {
		if _, err := tx.ExecContext(ctx, `
			UPDATE rule_versions
			SET state = 'superseded'
			WHERE account_id = ? AND state = 'active' AND id <> ?`,
			accountID, versionID,
		); err != nil {
			return fmt.Errorf("supersede active rule: %w", err)
		}
	}

	var activatedAt any
	if targetState == "active" {
		activatedAt = formatTime(now)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE rule_versions
		SET state = ?, effective_at = ?, activated_at = ?
		WHERE id = ?`,
		targetState, formatTime(at), activatedAt, versionID,
	); err != nil {
		return fmt.Errorf("update target rule: %w", err)
	}
	details := map[string]any{
		"effective_at": formatTime(at),
		"state":        targetState,
	}
	if err := insertAudit(
		ctx, tx, accountID, "operator", "local",
		eventType, "rule_version", versionID, details,
	); err != nil {
		return err
	}
	if err := insertOutbox(
		ctx, tx, accountID, "rule_version", versionID, eventType, details, now,
	); err != nil {
		return err
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit activate rule: %w", err)
	}
	return nil
}

func (r *Repository) ActivateDue(ctx context.Context, now time.Time) (int, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM rule_versions
		WHERE state = 'scheduled' AND effective_at <= ?
		ORDER BY effective_at, version`,
		formatTime(now),
	)
	if err != nil {
		return 0, fmt.Errorf("list due rules: %w", err)
	}
	var ids []string
	for rows.Next() {
		var versionID string
		if err := rows.Scan(&versionID); err != nil {
			rows.Close()
			return 0, fmt.Errorf("scan due rule: %w", err)
		}
		ids = append(ids, versionID)
	}
	if err := rows.Close(); err != nil {
		return 0, fmt.Errorf("close due rules: %w", err)
	}
	for _, versionID := range ids {
		if err := r.Activate(ctx, versionID, now); err != nil {
			return 0, err
		}
	}
	return len(ids), nil
}

func (r *Repository) Active(ctx context.Context, accountID string) (Version, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, version, state, source_yaml, normalized_json,
			config_hash, schema_version, effective_at, activated_at, created_at
		FROM rule_versions
		WHERE account_id = ? AND state = 'active'`,
		accountID,
	)
	version, err := scanVersion(row)
	if errors.Is(err, sql.ErrNoRows) {
		return Version{}, ErrNoActiveRule
	}
	if err != nil {
		return Version{}, fmt.Errorf("load active rule: %w", err)
	}
	return version, nil
}

func (r *Repository) ByHash(
	ctx context.Context,
	accountID, hash string,
) (Version, error) {
	row := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, version, state, source_yaml, normalized_json,
			config_hash, schema_version, effective_at, activated_at, created_at
		FROM rule_versions
		WHERE account_id = ? AND config_hash = ?
		ORDER BY version DESC LIMIT 1`,
		accountID, hash,
	)
	return scanVersion(row)
}

func (r *Repository) List(ctx context.Context, accountID string) ([]Version, error) {
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, version, state, source_yaml, normalized_json,
			config_hash, schema_version, effective_at, activated_at, created_at
		FROM rule_versions
		WHERE account_id = ?
		ORDER BY version DESC`,
		accountID,
	)
	if err != nil {
		return nil, fmt.Errorf("list rule versions: %w", err)
	}
	defer rows.Close()

	var versions []Version
	for rows.Next() {
		version, err := scanVersion(rows)
		if err != nil {
			return nil, fmt.Errorf("scan rule version: %w", err)
		}
		versions = append(versions, version)
	}
	return versions, rows.Err()
}

type rowScanner interface {
	Scan(dest ...any) error
}

func scanVersion(scanner rowScanner) (Version, error) {
	var version Version
	var effectiveAt, createdAt string
	var activatedAt sql.NullString
	err := scanner.Scan(
		&version.ID, &version.AccountID, &version.Version, &version.State,
		&version.SourceYAML, &version.NormalizedJSON, &version.ConfigHash,
		&version.SchemaVersion, &effectiveAt, &activatedAt, &createdAt,
	)
	if err != nil {
		return Version{}, err
	}
	version.EffectiveAt, err = parseTime(effectiveAt)
	if err != nil {
		return Version{}, err
	}
	version.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Version{}, err
	}
	if activatedAt.Valid {
		value, err := parseTime(activatedAt.String)
		if err != nil {
			return Version{}, err
		}
		version.ActivatedAt = &value
	}
	if err := json.Unmarshal([]byte(version.NormalizedJSON), &version.Document); err != nil {
		return Version{}, fmt.Errorf("decode normalized rule: %w", err)
	}
	return version, nil
}

func insertAudit(
	ctx context.Context,
	tx *sql.Tx,
	accountID, actorType, actorID, eventType, subjectType, subjectID string,
	details any,
) error {
	body, err := json.Marshal(details)
	if err != nil {
		return fmt.Errorf("encode audit details: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_logs(
			id, account_id, actor_type, actor_id, event_type,
			subject_type, subject_id, details_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		id.New("audit"), accountID, actorType, actorID, eventType,
		subjectType, subjectID, string(body), formatTime(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("insert audit log: %w", err)
	}
	return nil
}

func insertOutbox(
	ctx context.Context,
	tx *sql.Tx,
	accountID, aggregateType, aggregateID, eventType string,
	payload any,
	availableAt time.Time,
) error {
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode outbox payload: %w", err)
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox_events(
			event_id, account_id, aggregate_type, aggregate_id,
			event_type, payload_json, state, available_at, created_at
		) VALUES (?, ?, ?, ?, ?, ?, 'pending', ?, ?)`,
		id.New("evt"), accountID, aggregateType, aggregateID,
		eventType, string(body), formatTime(availableAt), formatTime(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("insert outbox event: %w", err)
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", value, err)
	}
	return parsed, nil
}
