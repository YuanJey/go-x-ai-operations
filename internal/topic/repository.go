package topic

import (
	"context"
	"database/sql"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Topic struct {
	ID        string    `json:"id"`
	AccountID string    `json:"account_id"`
	Title     string    `json:"title"`
	Pillar    string    `json:"pillar"`
	Score     float64   `json:"score"`
	Rationale string    `json:"rationale"`
	State     string    `json:"state"`
	CreatedAt time.Time `json:"created_at"`
}

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Save(ctx context.Context, topic Topic) (Topic, error) {
	if topic.ID == "" {
		topic.ID = id.New("topic")
	}
	if topic.State == "" {
		topic.State = "selected"
	}
	if topic.CreatedAt.IsZero() {
		topic.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO topics(
			id, account_id, title, pillar, score, rationale, state, created_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?)`,
		topic.ID, topic.AccountID, topic.Title, topic.Pillar,
		topic.Score, topic.Rationale, topic.State,
		topic.CreatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return Topic{}, fmt.Errorf("save topic: %w", err)
	}
	return topic, nil
}
