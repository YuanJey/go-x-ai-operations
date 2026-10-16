package engagement

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
	"github.com/YuanJey/go-x-ai-operations/internal/jobs"
	"github.com/YuanJey/go-x-ai-operations/internal/xapi"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) SaveMention(
	ctx context.Context,
	accountID, authorHandle string,
	mention xapi.Mention,
	eligibility Eligibility,
) (Mention, bool, error) {
	participants := []string{mention.AuthorID}
	participantJSON, _ := json.Marshal(participants)
	now := time.Now().UTC()
	item := Mention{
		ID: id.New("mention"), AccountID: accountID, XPostID: mention.PostID,
		AuthorID: mention.AuthorID, AuthorHandle: authorHandle,
		ConversationID: mention.ConversationID, Text: mention.Text,
		ParticipantIDs: participants, Summoned: eligibility.Eligible,
		SummonEvidence: eligibility.Evidence, ReceivedAt: mention.CreatedAt,
		State: "received",
	}
	result, err := r.db.ExecContext(ctx, `
		INSERT INTO mentions(
			id, account_id, x_post_id, author_id, author_handle,
			conversation_id, text, participant_ids_json, summoned,
			summon_evidence, received_at, state
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'received')
		ON CONFLICT(account_id, x_post_id) DO NOTHING`,
		item.ID, item.AccountID, item.XPostID, item.AuthorID,
		nullableString(item.AuthorHandle), nullableString(item.ConversationID),
		item.Text, string(participantJSON), item.Summoned,
		nullableString(item.SummonEvidence), formatTime(item.ReceivedAt),
	)
	if err != nil {
		return Mention{}, false, fmt.Errorf("save mention: %w", err)
	}
	affected, _ := result.RowsAffected()
	item.ReceivedAt = now
	return item, affected == 1, nil
}

func (r *Repository) SaveDecision(
	ctx context.Context,
	decision ReplyDecision,
) error {
	if decision.ID == "" {
		decision.ID = id.New("decision")
	}
	if decision.CreatedAt.IsZero() {
		decision.CreatedAt = time.Now().UTC()
	}
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO reply_decisions(
			id, mention_id, decision, reason, risk_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		decision.ID, decision.MentionID, decision.Decision,
		decision.Reason, decision.RiskJSON, formatTime(decision.CreatedAt),
	)
	if err != nil {
		return fmt.Errorf("save reply decision: %w", err)
	}
	return nil
}

func (r *Repository) CreateReply(
	ctx context.Context,
	accountID, mentionID, contentID string,
) error {
	now := formatTime(time.Now())
	_, err := r.db.ExecContext(ctx, `
		INSERT INTO replies(
			id, account_id, mention_id, content_id, state, created_at, updated_at
		) VALUES (?, ?, ?, ?, 'approved_auto', ?, ?)`,
		id.New("reply"), accountID, mentionID, contentID, now, now,
	)
	if err != nil {
		return fmt.Errorf("create reply record: %w", err)
	}
	return nil
}

func (r *Repository) GetMention(ctx context.Context, mentionID string) (Mention, error) {
	var mention Mention
	var authorHandle, conversationID, evidence sql.NullString
	var participantsJSON, receivedAt string
	err := r.db.QueryRowContext(ctx, `
		SELECT id, account_id, x_post_id, author_id, author_handle,
			conversation_id, text, participant_ids_json, summoned,
			summon_evidence, received_at, state
		FROM mentions WHERE id = ?`,
		mentionID,
	).Scan(
		&mention.ID, &mention.AccountID, &mention.XPostID, &mention.AuthorID,
		&authorHandle, &conversationID, &mention.Text, &participantsJSON,
		&mention.Summoned, &evidence, &receivedAt, &mention.State,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return Mention{}, fmt.Errorf("mention %s not found", mentionID)
	}
	if err != nil {
		return Mention{}, fmt.Errorf("load mention: %w", err)
	}
	if authorHandle.Valid {
		mention.AuthorHandle = authorHandle.String
	}
	if conversationID.Valid {
		mention.ConversationID = conversationID.String
	}
	if evidence.Valid {
		mention.SummonEvidence = evidence.String
	}
	if err := json.Unmarshal([]byte(participantsJSON), &mention.ParticipantIDs); err != nil {
		return Mention{}, err
	}
	mention.ReceivedAt, err = time.Parse(time.RFC3339Nano, receivedAt)
	return mention, err
}

func (r *Repository) List(
	ctx context.Context,
	accountID string,
	limit int,
) ([]Mention, error) {
	if limit <= 0 || limit > 500 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, `
		SELECT id FROM mentions
		WHERE account_id = ?
		ORDER BY received_at DESC LIMIT ?`,
		accountID, limit,
	)
	if err != nil {
		return nil, fmt.Errorf("list mentions: %w", err)
	}
	var ids []string
	for rows.Next() {
		var mentionID string
		if err := rows.Scan(&mentionID); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, mentionID)
	}
	rows.Close()
	mentions := make([]Mention, 0)
	for _, mentionID := range ids {
		mention, err := r.GetMention(ctx, mentionID)
		if err != nil {
			return nil, err
		}
		mentions = append(mentions, mention)
	}
	return mentions, nil
}

func MentionJob(
	mention Mention,
	ruleVersionID string,
) jobs.NewJob {
	payload, _ := json.Marshal(map[string]string{"mention_id": mention.ID})
	deadline := time.Now().Add(24 * time.Hour)
	return jobs.NewJob{
		AccountID: mention.AccountID, Action: "publish_reply",
		PayloadJSON: string(payload), RuleVersionID: ruleVersionID,
		DedupeKey: "mention:" + mention.ID, RunAt: time.Now(),
		DeadlineAt: &deadline, MaxAttempts: 1,
	}
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
