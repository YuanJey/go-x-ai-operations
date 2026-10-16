package jobs

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Enqueue(ctx context.Context, input NewJob) (bool, error) {
	if input.AccountID == "" || input.Action == "" || input.RuleVersionID == "" || input.DedupeKey == "" {
		return false, fmt.Errorf("account, action, rule version, and dedupe key are required")
	}
	if input.RunAt.IsZero() {
		return false, fmt.Errorf("run_at is required")
	}
	if input.MaxAttempts < 1 {
		input.MaxAttempts = 1
	}
	if input.PayloadJSON == "" {
		input.PayloadJSON = "{}"
	}
	now := formatTime(time.Now())
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO jobs(
			id, account_id, action, payload_json, payload_ref, rule_version_id,
			dedupe_key, run_at, deadline_at, priority, max_attempts,
			state, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'pending', ?, ?)
		ON CONFLICT(dedupe_key) DO NOTHING`,
		id.New("job"), input.AccountID, input.Action, input.PayloadJSON, input.PayloadRef,
		input.RuleVersionID, input.DedupeKey, formatTime(input.RunAt),
		nullableTime(input.DeadlineAt), input.Priority, input.MaxAttempts, now, now,
	)
	if err != nil {
		return false, fmt.Errorf("enqueue job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect enqueue result: %w", err)
	}
	return affected == 1, nil
}

func (r *Repository) PendingCount(
	ctx context.Context,
	accountID, action string,
) (int, error) {
	var count int
	err := r.db.QueryRowContext(ctx, `
		SELECT COUNT(*) FROM jobs
		WHERE account_id = ? AND action = ? AND state IN ('pending', 'leased')`,
		accountID, action,
	).Scan(&count)
	if err != nil {
		return 0, fmt.Errorf("count pending jobs: %w", err)
	}
	return count, nil
}

func (r *Repository) CancelOldestPending(
	ctx context.Context,
	accountID, action, reason string,
) (bool, error) {
	result, err := r.db.ExecContext(ctx, `
		UPDATE jobs
		SET state = 'cancelled', last_error = ?, updated_at = ?
		WHERE id = (
			SELECT id FROM jobs
			WHERE account_id = ? AND action = ? AND state = 'pending'
			ORDER BY created_at
			LIMIT 1
		)`,
		reason, formatTime(time.Now()), accountID, action,
	)
	if err != nil {
		return false, fmt.Errorf("cancel oldest pending job: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil {
		return false, err
	}
	return affected == 1, nil
}

func (r *Repository) Lease(
	ctx context.Context,
	owner string,
	now time.Time,
	duration time.Duration,
	limit int,
) ([]Job, error) {
	if owner == "" || duration <= 0 || limit < 1 {
		return nil, fmt.Errorf("owner, positive lease duration, and positive limit are required")
	}
	now = now.UTC()
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin job lease: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs
		SET state = CASE WHEN attempts >= max_attempts THEN 'dead' ELSE 'pending' END,
			lease_owner = NULL,
			lease_until = NULL,
			updated_at = ?
		WHERE state = 'leased' AND lease_until <= ?`,
		formatTime(now), formatTime(now),
	); err != nil {
		return nil, fmt.Errorf("recover expired leases: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs
		SET state = 'expired', updated_at = ?
		WHERE state = 'pending' AND deadline_at IS NOT NULL AND deadline_at <= ?`,
		formatTime(now), formatTime(now),
	); err != nil {
		return nil, fmt.Errorf("expire stale jobs: %w", err)
	}

	rows, err := tx.QueryContext(ctx, `
		SELECT id
		FROM jobs
		WHERE state = 'pending'
			AND run_at <= ?
			AND (deadline_at IS NULL OR deadline_at > ?)
			AND attempts < max_attempts
		ORDER BY priority DESC, run_at, created_at
		LIMIT ?`,
		formatTime(now), formatTime(now), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("select jobs for lease: %w", err)
	}
	var ids []string
	for rows.Next() {
		var jobID string
		if err := rows.Scan(&jobID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan lease candidate: %w", err)
		}
		ids = append(ids, jobID)
	}
	if err := rows.Close(); err != nil {
		return nil, fmt.Errorf("close lease candidates: %w", err)
	}

	leaseUntil := now.Add(duration)
	leased := make([]Job, 0, len(ids))
	for _, jobID := range ids {
		result, err := tx.ExecContext(ctx, `
			UPDATE jobs
			SET state = 'leased', attempts = attempts + 1,
				lease_owner = ?, lease_until = ?, updated_at = ?
			WHERE id = ? AND state = 'pending'`,
			owner, formatTime(leaseUntil), formatTime(now), jobID,
		)
		if err != nil {
			return nil, fmt.Errorf("lease job %s: %w", jobID, err)
		}
		affected, err := result.RowsAffected()
		if err != nil || affected != 1 {
			continue
		}
		job, err := getTx(ctx, tx, jobID)
		if err != nil {
			return nil, err
		}
		leased = append(leased, job)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit job lease: %w", err)
	}
	return leased, nil
}

func (r *Repository) Renew(
	ctx context.Context,
	jobID, owner string,
	until time.Time,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE jobs SET lease_until = ?, updated_at = ?
		WHERE id = ? AND state = 'leased' AND lease_owner = ?`,
		formatTime(until), formatTime(time.Now()), jobID, owner,
	)
	if err != nil {
		return fmt.Errorf("renew job lease: %w", err)
	}
	return requireOne(result, "job lease is no longer owned")
}

func (r *Repository) Complete(ctx context.Context, jobID, owner string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE jobs
		SET state = 'succeeded', lease_owner = NULL, lease_until = NULL, updated_at = ?
		WHERE id = ? AND state = 'leased' AND lease_owner = ?`,
		formatTime(time.Now()), jobID, owner,
	)
	if err != nil {
		return fmt.Errorf("complete job: %w", err)
	}
	return requireOne(result, "job lease is no longer owned")
}

func (r *Repository) Fail(
	ctx context.Context,
	jobID, owner, reason string,
	retryAt time.Time,
	retryable bool,
) error {
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin fail job: %w", err)
	}
	defer tx.Rollback()

	var attempts, maxAttempts int
	if err := tx.QueryRowContext(ctx, `
		SELECT attempts, max_attempts FROM jobs
		WHERE id = ? AND state = 'leased' AND lease_owner = ?`,
		jobID, owner,
	).Scan(&attempts, &maxAttempts); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("job lease is no longer owned")
		}
		return fmt.Errorf("load failed job: %w", err)
	}
	state := StateDead
	if retryable && attempts < maxAttempts {
		state = StatePending
	}
	if retryAt.IsZero() {
		retryAt = time.Now()
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE jobs
		SET state = ?, run_at = ?, lease_owner = NULL, lease_until = NULL,
			last_error = ?, updated_at = ?
		WHERE id = ?`,
		state, formatTime(retryAt), reason, formatTime(time.Now()), jobID,
	); err != nil {
		return fmt.Errorf("update failed job: %w", err)
	}
	return tx.Commit()
}

func (r *Repository) Get(ctx context.Context, jobID string) (Job, error) {
	return scanJob(r.db.QueryRowContext(ctx, jobSelect+" WHERE id = ?", jobID))
}

func (r *Repository) List(ctx context.Context, filter Filter) ([]Job, error) {
	query := jobSelect + " WHERE 1 = 1"
	var args []any
	if filter.AccountID != "" {
		query += " AND account_id = ?"
		args = append(args, filter.AccountID)
	}
	if filter.State != "" {
		query += " AND state = ?"
		args = append(args, filter.State)
	}
	if filter.Action != "" {
		query += " AND action = ?"
		args = append(args, filter.Action)
	}
	query += " ORDER BY created_at DESC"
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	query += " LIMIT ?"
	args = append(args, filter.Limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list jobs: %w", err)
	}
	defer rows.Close()
	result := make([]Job, 0)
	for rows.Next() {
		job, err := scanJob(rows)
		if err != nil {
			return nil, fmt.Errorf("scan job: %w", err)
		}
		result = append(result, job)
	}
	return result, rows.Err()
}

const jobSelect = `
	SELECT id, account_id, action, payload_json, payload_ref, rule_version_id,
		dedupe_key, run_at, deadline_at, priority, attempts, max_attempts,
		lease_owner, lease_until, state, last_error, created_at, updated_at
	FROM jobs`

type rowScanner interface {
	Scan(dest ...any) error
}

func getTx(ctx context.Context, tx *sql.Tx, jobID string) (Job, error) {
	job, err := scanJob(tx.QueryRowContext(ctx, jobSelect+" WHERE id = ?", jobID))
	if err != nil {
		return Job{}, fmt.Errorf("load leased job: %w", err)
	}
	return job, nil
}

func scanJob(scanner rowScanner) (Job, error) {
	var job Job
	var payloadRef, leaseOwner, leaseUntil, deadline, lastError sql.NullString
	var runAt, createdAt, updatedAt string
	err := scanner.Scan(
		&job.ID, &job.AccountID, &job.Action, &job.PayloadJSON, &payloadRef,
		&job.RuleVersionID, &job.DedupeKey, &runAt, &deadline, &job.Priority,
		&job.Attempts, &job.MaxAttempts, &leaseOwner, &leaseUntil, &job.State,
		&lastError, &createdAt, &updatedAt,
	)
	if err != nil {
		return Job{}, err
	}
	job.RunAt, err = parseTime(runAt)
	if err != nil {
		return Job{}, err
	}
	job.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Job{}, err
	}
	job.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Job{}, err
	}
	if payloadRef.Valid {
		job.PayloadRef = &payloadRef.String
	}
	if leaseOwner.Valid {
		job.LeaseOwner = &leaseOwner.String
	}
	if lastError.Valid {
		job.LastError = &lastError.String
	}
	if deadline.Valid {
		value, err := parseTime(deadline.String)
		if err != nil {
			return Job{}, err
		}
		job.DeadlineAt = &value
	}
	if leaseUntil.Valid {
		value, err := parseTime(leaseUntil.String)
		if err != nil {
			return Job{}, err
		}
		job.LeaseUntil = &value
	}
	return job, nil
}

func requireOne(result sql.Result, message string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New(message)
	}
	return nil
}

func nullableTime(value *time.Time) any {
	if value == nil {
		return nil
	}
	return formatTime(*value)
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

func NormalizeError(value string) string {
	return strings.TrimSpace(value)
}
