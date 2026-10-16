package metering

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Entry struct {
	ID            string    `json:"id"`
	AccountID     string    `json:"account_id"`
	OperationID   *string   `json:"operation_id,omitempty"`
	ResourceType  string    `json:"resource_type"`
	Endpoint      string    `json:"endpoint"`
	Units         float64   `json:"units"`
	EstimatedCost float64   `json:"estimated_cost"`
	ActualCost    *float64  `json:"actual_cost,omitempty"`
	Currency      string    `json:"currency"`
	CreatedAt     time.Time `json:"created_at"`
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Record(ctx context.Context, entry Entry) error {
	if entry.ID == "" {
		entry.ID = id.New("cost")
	}
	if entry.Currency == "" {
		entry.Currency = "USD"
	}
	if entry.CreatedAt.IsZero() {
		entry.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO cost_ledger(
			id, account_id, operation_id, resource_type, endpoint,
			units, estimated_cost, actual_cost, currency, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		entry.ID, entry.AccountID, entry.OperationID, entry.ResourceType,
		entry.Endpoint, entry.Units, entry.EstimatedCost, entry.ActualCost,
		entry.Currency, entry.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("record cost entry: %w", err)
	}
	return nil
}

func (r *Repository) List(
	ctx context.Context,
	accountID string,
	limit int,
) ([]Entry, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id, account_id, operation_id, resource_type, endpoint,
			units, estimated_cost, actual_cost, currency, created_at
		FROM cost_ledger
		WHERE account_id = ?
		ORDER BY created_at DESC LIMIT ?`,
		accountID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	entries := make([]Entry, 0)
	for rows.Next() {
		var entry Entry
		var operationID sql.NullString
		var actualCost sql.NullFloat64
		var createdAt string
		if err := rows.Scan(
			&entry.ID, &entry.AccountID, &operationID, &entry.ResourceType,
			&entry.Endpoint, &entry.Units, &entry.EstimatedCost, &actualCost,
			&entry.Currency, &createdAt,
		); err != nil {
			return nil, err
		}
		if operationID.Valid {
			entry.OperationID = &operationID.String
		}
		if actualCost.Valid {
			entry.ActualCost = &actualCost.Float64
		}
		entry.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
		if err != nil {
			return nil, err
		}
		entries = append(entries, entry)
	}
	return entries, rows.Err()
}
