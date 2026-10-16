package alert

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Alert struct {
	ID          string     `json:"id"`
	AccountID   string     `json:"account_id"`
	DedupeKey   string     `json:"dedupe_key"`
	Severity    string     `json:"severity"`
	Kind        string     `json:"kind"`
	Message     string     `json:"message"`
	DetailsJSON string     `json:"details_json"`
	State       string     `json:"state"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ResolvedAt  *time.Time `json:"resolved_at,omitempty"`
}

type Service struct {
	db        *sql.DB
	accountID string
}

func NewService(db *sql.DB, accountID string) *Service {
	return &Service{db: db, accountID: accountID}
}

func (s *Service) Evaluate(ctx context.Context) (int, error) {
	type condition struct {
		key, severity, kind, message, query string
	}
	conditions := []condition{
		{
			key: "dead_jobs", severity: "error", kind: "jobs",
			message: "One or more jobs are dead",
			query:   "SELECT COUNT(*) FROM jobs WHERE account_id = ? AND state = 'dead'",
		},
		{
			key: "uncertain_operations", severity: "critical", kind: "publisher",
			message: "One or more X writes require reconciliation",
			query: "SELECT COUNT(*) FROM api_operations WHERE account_id = ? " +
				"AND state IN ('uncertain', 'reconciling', 'failed_ambiguous')",
		},
		{
			key: "paused_account", severity: "warning", kind: "account",
			message: "Account automation is paused",
			query:   "SELECT COUNT(*) FROM accounts WHERE id = ? AND state <> 'active'",
		},
	}
	open := 0
	for _, condition := range conditions {
		var count int
		if err := s.db.QueryRowContext(ctx, condition.query, s.accountID).Scan(&count); err != nil {
			return open, fmt.Errorf("evaluate alert %s: %w", condition.key, err)
		}
		if count > 0 {
			if err := s.open(ctx, condition.key, condition.severity, condition.kind, condition.message, count); err != nil {
				return open, err
			}
			open++
		} else if err := s.resolve(ctx, condition.key); err != nil {
			return open, err
		}
	}
	return open, nil
}

func (s *Service) List(ctx context.Context, limit int) ([]Alert, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, account_id, dedupe_key, severity, kind, message,
			details_json, state, created_at, updated_at, resolved_at
		FROM alerts WHERE account_id = ?
		ORDER BY CASE state WHEN 'open' THEN 0 ELSE 1 END, updated_at DESC
		LIMIT ?`,
		s.accountID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	alerts := make([]Alert, 0)
	for rows.Next() {
		var item Alert
		var createdAt, updatedAt string
		var resolvedAt sql.NullString
		if err := rows.Scan(
			&item.ID, &item.AccountID, &item.DedupeKey, &item.Severity,
			&item.Kind, &item.Message, &item.DetailsJSON, &item.State,
			&createdAt, &updatedAt, &resolvedAt,
		); err != nil {
			return nil, err
		}
		item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, err
		}
		if resolvedAt.Valid {
			value, err := time.Parse(time.RFC3339Nano, resolvedAt.String)
			if err != nil {
				return nil, err
			}
			item.ResolvedAt = &value
		}
		alerts = append(alerts, item)
	}
	return alerts, rows.Err()
}

func (s *Service) open(
	ctx context.Context,
	key, severity, kind, message string,
	count int,
) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	details, _ := json.Marshal(map[string]int{"count": count})
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO alerts(
			id, account_id, dedupe_key, severity, kind, message,
			details_json, state, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, 'open', ?, ?)
		ON CONFLICT(account_id, dedupe_key) DO UPDATE SET
			severity = excluded.severity,
			message = excluded.message,
			details_json = excluded.details_json,
			state = 'open',
			updated_at = excluded.updated_at,
			resolved_at = NULL`,
		id.New("alert"), s.accountID, key, severity, kind, message,
		string(details), now, now,
	)
	if err != nil {
		return fmt.Errorf("open alert %s: %w", key, err)
	}
	return nil
}

func (s *Service) resolve(ctx context.Context, key string) error {
	now := time.Now().UTC().Format(time.RFC3339Nano)
	_, err := s.db.ExecContext(ctx, `
		UPDATE alerts SET state = 'resolved', resolved_at = ?, updated_at = ?
		WHERE account_id = ? AND dedupe_key = ? AND state = 'open'`,
		now, now, s.accountID, key,
	)
	return err
}
