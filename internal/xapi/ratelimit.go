package xapi

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"time"
)

type RateLimitStore struct {
	db *sql.DB
}

func NewRateLimitStore(db *sql.DB) *RateLimitStore {
	return &RateLimitStore{db: db}
}

func (s *RateLimitStore) Admit(
	ctx context.Context,
	accountID, endpoint string,
	now time.Time,
) error {
	var remaining sql.NullInt64
	var resetAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
		SELECT remaining, reset_at FROM x_rate_limits
		WHERE account_id = ? AND endpoint = ?`,
		accountID, endpoint,
	).Scan(&remaining, &resetAt)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return fmt.Errorf("load X rate limit: %w", err)
	}
	if !remaining.Valid || remaining.Int64 > 0 || !resetAt.Valid {
		return nil
	}
	reset, err := time.Parse(time.RFC3339Nano, resetAt.String)
	if err != nil {
		return fmt.Errorf("parse X rate limit reset: %w", err)
	}
	if now.Before(reset) {
		return &Error{
			StatusCode: 429, Message: "X API rate limit is exhausted",
			Retryable: true, ResetAt: &reset,
		}
	}
	return nil
}

func (s *RateLimitStore) Observe(
	ctx context.Context,
	accountID, endpoint string,
	headers map[string]string,
	now time.Time,
) error {
	limit := parseOptionalInt(headers["x-rate-limit-limit"])
	remaining := parseOptionalInt(headers["x-rate-limit-remaining"])
	var reset any
	if value := headers["x-rate-limit-reset"]; value != "" {
		unix, err := strconv.ParseInt(value, 10, 64)
		if err == nil {
			reset = time.Unix(unix, 0).UTC().Format(time.RFC3339Nano)
		}
	}
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO x_rate_limits(
			account_id, endpoint, limit_value, remaining, reset_at, observed_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(account_id, endpoint) DO UPDATE SET
			limit_value = excluded.limit_value,
			remaining = excluded.remaining,
			reset_at = excluded.reset_at,
			observed_at = excluded.observed_at`,
		accountID, endpoint, limit, remaining, reset,
		now.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store X rate limit: %w", err)
	}
	return nil
}

func parseOptionalInt(value string) any {
	if value == "" {
		return nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil {
		return nil
	}
	return parsed
}
