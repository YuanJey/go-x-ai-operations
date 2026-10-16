package analytics

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
	"github.com/YuanJey/go-x-ai-operations/internal/xapi"
)

type Service struct {
	db        *sql.DB
	client    xapi.Client
	accountID string
}

func NewService(db *sql.DB, client xapi.Client, accountID string) *Service {
	return &Service{db: db, client: client, accountID: accountID}
}

func (s *Service) SyncAccount(ctx context.Context) (xapi.AccountSnapshot, error) {
	snapshot, err := s.client.GetAccount(ctx)
	if err != nil {
		return xapi.AccountSnapshot{}, err
	}
	body, err := json.Marshal(snapshot)
	if err != nil {
		return xapi.AccountSnapshot{}, err
	}
	_, err = s.db.ExecContext(ctx, `
		INSERT INTO metric_snapshots(
			id, account_id, subject_type, subject_id, metrics_json, captured_at
		) VALUES (?, ?, 'account', ?, ?, ?)`,
		id.New("metric"), s.accountID, snapshot.ID, string(body),
		snapshot.CapturedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return xapi.AccountSnapshot{}, fmt.Errorf("save account metric snapshot: %w", err)
	}
	return snapshot, nil
}

func (s *Service) AccountHistory(
	ctx context.Context,
	limit int,
) ([]xapi.AccountSnapshot, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT metrics_json FROM metric_snapshots
		WHERE account_id = ? AND subject_type = 'account'
		ORDER BY captured_at DESC LIMIT ?`,
		s.accountID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var snapshots []xapi.AccountSnapshot
	for rows.Next() {
		var body string
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		var snapshot xapi.AccountSnapshot
		if err := json.Unmarshal([]byte(body), &snapshot); err != nil {
			return nil, err
		}
		snapshots = append(snapshots, snapshot)
	}
	return snapshots, rows.Err()
}

func (s *Service) SyncPosts(
	ctx context.Context,
	userID string,
	since time.Time,
) (int, error) {
	posts, err := s.client.GetOwnPosts(ctx, userID, since)
	if err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, fmt.Errorf("begin post metrics sync: %w", err)
	}
	defer tx.Rollback()
	for _, post := range posts {
		body, err := json.Marshal(post.Metrics)
		if err != nil {
			return 0, err
		}
		capturedAt := time.Now().UTC().Truncate(time.Minute)
		_, err = tx.ExecContext(ctx, `
			INSERT INTO metric_snapshots(
				id, account_id, subject_type, subject_id, metrics_json, captured_at
			) VALUES (?, ?, 'post', ?, ?, ?)
			ON CONFLICT(account_id, subject_type, subject_id, captured_at)
			DO UPDATE SET metrics_json = excluded.metrics_json`,
			id.New("metric"), s.accountID, post.PostID, string(body),
			capturedAt.Format(time.RFC3339Nano),
		)
		if err != nil {
			return 0, fmt.Errorf("save post metrics: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return len(posts), nil
}
