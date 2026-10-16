package audit

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

type Event struct {
	ID          string    `json:"id"`
	AccountID   string    `json:"account_id"`
	ActorType   string    `json:"actor_type"`
	ActorID     string    `json:"actor_id"`
	EventType   string    `json:"event_type"`
	SubjectType string    `json:"subject_type"`
	SubjectID   string    `json:"subject_id"`
	DetailsJSON string    `json:"details_json"`
	CreatedAt   time.Time `json:"created_at"`
}

type Filter struct {
	AccountID string
	EventType string
	Limit     int
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) List(ctx context.Context, filter Filter) ([]Event, error) {
	query := `
		SELECT id, account_id, actor_type, actor_id, event_type,
			subject_type, subject_id, details_json, created_at
		FROM audit_logs WHERE account_id = ?`
	args := []any{filter.AccountID}
	if filter.EventType != "" {
		query += " AND event_type = ?"
		args = append(args, filter.EventType)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	if filter.Limit <= 0 || filter.Limit > 500 {
		filter.Limit = 100
	}
	args = append(args, filter.Limit)

	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list audit events: %w", err)
	}
	defer rows.Close()
	events := make([]Event, 0)
	for rows.Next() {
		var event Event
		var createdAt string
		if err := rows.Scan(
			&event.ID, &event.AccountID, &event.ActorType, &event.ActorID,
			&event.EventType, &event.SubjectType, &event.SubjectID,
			&event.DetailsJSON, &createdAt,
		); err != nil {
			return nil, fmt.Errorf("scan audit event: %w", err)
		}
		event.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, fmt.Errorf("parse audit time: %w", err)
		}
		events = append(events, event)
	}
	return events, rows.Err()
}
