package content

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
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

func (r *Repository) SaveApproved(
	ctx context.Context,
	item Item,
	approval Approval,
) (Item, error) {
	if !approval.Approved {
		return Item{}, fmt.Errorf("cannot save content with a rejected approval")
	}
	if item.ID == "" {
		item.ID = id.New("content")
	}
	item.State = StateApprovedAuto
	now := time.Now().UTC()
	item.CreatedAt = now
	item.UpdatedAt = now
	approvalBody, err := json.Marshal(approval)
	if err != nil {
		return Item{}, fmt.Errorf("encode content approval: %w", err)
	}
	item.ApprovalJSON = string(approvalBody)

	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return Item{}, fmt.Errorf("begin save approved content: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.ExecContext(ctx, `
		INSERT INTO content_items(
			id, account_id, topic_id, rule_version_id, kind,
			body, body_hash, language, state, approval_json, created_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		item.ID, item.AccountID, item.TopicID, item.RuleVersionID, item.Kind,
		item.Body, item.BodyHash, item.Language, item.State, item.ApprovalJSON,
		formatTime(item.CreatedAt), formatTime(item.UpdatedAt),
	)
	if err != nil {
		return Item{}, fmt.Errorf("insert approved content: %w", err)
	}
	payload, _ := json.Marshal(map[string]any{
		"content_id": item.ID,
		"kind":       item.Kind,
	})
	_, err = tx.ExecContext(ctx, `
		INSERT INTO outbox_events(
			event_id, account_id, aggregate_type, aggregate_id,
			event_type, payload_json, state, available_at, created_at
		) VALUES (?, ?, 'content', ?, 'content.approved', ?, 'pending', ?, ?)`,
		id.New("evt"), item.AccountID, item.ID, string(payload),
		formatTime(now), formatTime(now),
	)
	if err != nil {
		return Item{}, fmt.Errorf("insert content outbox event: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return Item{}, fmt.Errorf("commit approved content: %w", err)
	}
	return item, nil
}

func (r *Repository) Transition(
	ctx context.Context,
	contentID, from, to string,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE content_items
		SET state = ?, updated_at = ?
		WHERE id = ? AND state = ?`,
		to, formatTime(time.Now()), contentID, from,
	)
	if err != nil {
		return fmt.Errorf("transition content: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("content state is not %s", from)
	}
	return nil
}

func (r *Repository) Get(ctx context.Context, contentID string) (Item, error) {
	item, err := scanItem(r.db.QueryRowContext(ctx, itemSelect+" WHERE id = ?", contentID))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, fmt.Errorf("content %s not found", contentID)
	}
	return item, err
}

func (r *Repository) NextApproved(
	ctx context.Context,
	accountID, kind, ruleVersionID string,
) (Item, error) {
	item, err := scanItem(r.db.QueryRowContext(ctx, itemSelect+`
		WHERE account_id = ? AND kind = ? AND rule_version_id = ?
			AND state = 'approved_auto'
		ORDER BY created_at
		LIMIT 1`,
		accountID, kind, ruleVersionID,
	))
	if errors.Is(err, sql.ErrNoRows) {
		return Item{}, fmt.Errorf("no approved %s content is available", kind)
	}
	return item, err
}

func (r *Repository) List(
	ctx context.Context,
	accountID, kind, state string,
	limit int,
) ([]Item, error) {
	query := itemSelect + " WHERE account_id = ?"
	args := []any{accountID}
	if kind != "" {
		query += " AND kind = ?"
		args = append(args, kind)
	}
	if state != "" {
		query += " AND state = ?"
		args = append(args, state)
	}
	query += " ORDER BY created_at DESC LIMIT ?"
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	args = append(args, limit)
	rows, err := r.db.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("list content: %w", err)
	}
	defer rows.Close()
	items := make([]Item, 0)
	for rows.Next() {
		item, err := scanItem(rows)
		if err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

const itemSelect = `
	SELECT id, account_id, topic_id, rule_version_id, kind, body,
		body_hash, language, state, approval_json, created_at, updated_at
	FROM content_items`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanItem(scanner rowScanner) (Item, error) {
	var item Item
	var topicID, approval sql.NullString
	var createdAt, updatedAt string
	err := scanner.Scan(
		&item.ID, &item.AccountID, &topicID, &item.RuleVersionID, &item.Kind,
		&item.Body, &item.BodyHash, &item.Language, &item.State, &approval,
		&createdAt, &updatedAt,
	)
	if err != nil {
		return Item{}, err
	}
	item.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Item{}, err
	}
	item.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Item{}, err
	}
	if topicID.Valid {
		item.TopicID = &topicID.String
	}
	if approval.Valid {
		item.ApprovalJSON = approval.String
	}
	return item, nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
