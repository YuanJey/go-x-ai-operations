package publisher

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/id"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
)

const (
	OperationPrepared        = "prepared"
	OperationSending         = "sending"
	OperationSucceeded       = "succeeded"
	OperationRejected        = "rejected"
	OperationUncertain       = "uncertain"
	OperationReconciling     = "reconciling"
	OperationFailedAmbiguous = "failed_ambiguous"
)

type Operation struct {
	ID           string     `json:"operation_id"`
	AccountID    string     `json:"account_id"`
	Action       string     `json:"action"`
	SubjectID    string     `json:"subject_id"`
	RequestHash  string     `json:"request_hash"`
	RequestJSON  string     `json:"request_json"`
	State        string     `json:"state"`
	XPostID      *string    `json:"x_post_id,omitempty"`
	ResponseJSON *string    `json:"response_json,omitempty"`
	ErrorCode    *string    `json:"error_code,omitempty"`
	ErrorMessage *string    `json:"error_message,omitempty"`
	PreparedAt   time.Time  `json:"prepared_at"`
	SentAt       *time.Time `json:"sent_at,omitempty"`
	ResolvedAt   *time.Time `json:"resolved_at,omitempty"`
	UpdatedAt    time.Time  `json:"updated_at"`
}

type OperationRepository struct {
	db *sql.DB
}

func NewOperationRepository(db *sql.DB) *OperationRepository {
	return &OperationRepository{db: db}
}

func (r *OperationRepository) MarkSending(ctx context.Context, operationID string) error {
	now := formatTime(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE api_operations
		SET state = 'sending', sent_at = ?, updated_at = ?
		WHERE operation_id = ? AND state = 'prepared'`,
		now, now, operationID,
	)
	if err != nil {
		return fmt.Errorf("mark operation sending: %w", err)
	}
	return requireOne(result, "operation is not prepared")
}

func (r *OperationRepository) Succeed(
	ctx context.Context,
	operation Operation,
	postID string,
	response any,
	kind, parentPostID, bodyHash string,
	publishedAt time.Time,
) error {
	responseBody, err := json.Marshal(response)
	if err != nil {
		return fmt.Errorf("encode X response: %w", err)
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin operation success: %w", err)
	}
	defer tx.Rollback()
	now := formatTime(time.Now())
	result, err := tx.ExecContext(ctx, `
		UPDATE api_operations
		SET state = 'succeeded', x_post_id = ?, response_json = ?,
			resolved_at = ?, updated_at = ?
		WHERE operation_id = ? AND state IN ('sending', 'reconciling', 'uncertain')`,
		postID, string(responseBody), now, now, operation.ID,
	)
	if err != nil {
		return fmt.Errorf("complete API operation: %w", err)
	}
	if err := requireOne(result, "operation cannot transition to succeeded"); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE content_items
		SET state = 'published', updated_at = ?
		WHERE id = ? AND state = 'rule_admitted'`,
		now, operation.SubjectID,
	); err != nil {
		return fmt.Errorf("mark content published: %w", err)
	}
	var parent any
	if parentPostID != "" {
		parent = parentPostID
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO x_posts(
			id, account_id, content_id, operation_id, x_post_id,
			kind, parent_x_post_id, body_hash, published_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		"xpost_"+operation.ID, operation.AccountID, operation.SubjectID,
		operation.ID, postID, kind, parent, bodyHash, formatTime(publishedAt),
	); err != nil {
		return fmt.Errorf("save X post: %w", err)
	}
	if kind == content.KindReply {
		if _, err := tx.ExecContext(ctx, `
			UPDATE replies
			SET x_post_id = ?, state = 'published', updated_at = ?
			WHERE content_id = ?`,
			postID, now, operation.SubjectID,
		); err != nil {
			return fmt.Errorf("mark reply published: %w", err)
		}
	}
	if err := schedulePostMetricJobs(
		ctx, tx, operation.AccountID, operation.SubjectID,
		postID, publishedAt,
	); err != nil {
		return err
	}
	if err := insertAudit(ctx, tx, operation.AccountID, "publish.succeeded", operation.SubjectID, map[string]any{
		"operation_id": operation.ID, "x_post_id": postID, "kind": kind,
	}); err != nil {
		return err
	}
	return tx.Commit()
}

func schedulePostMetricJobs(
	ctx context.Context,
	tx *sql.Tx,
	accountID, contentID, postID string,
	publishedAt time.Time,
) error {
	var ruleVersionID, normalized string
	if err := tx.QueryRowContext(ctx, `
		SELECT ci.rule_version_id, rv.normalized_json
		FROM content_items ci
		JOIN rule_versions rv ON rv.id = ci.rule_version_id
		WHERE ci.id = ?`,
		contentID,
	).Scan(&ruleVersionID, &normalized); err != nil {
		return fmt.Errorf("load metric sync rules: %w", err)
	}
	var document rules.Document
	if err := json.Unmarshal([]byte(normalized), &document); err != nil {
		return fmt.Errorf("decode metric sync rules: %w", err)
	}
	action, ok := document.Actions["sync_post_metrics"]
	if !ok || !action.Enabled {
		return nil
	}
	maxAttempts := action.Retry.MaxAttempts
	if maxAttempts < 1 {
		maxAttempts = 1
	}
	for _, delay := range action.AfterPublish {
		runAt := publishedAt.Add(delay.Value())
		deadline := runAt.Add(24 * time.Hour)
		payload, _ := json.Marshal(map[string]string{"x_post_id": postID})
		now := formatTime(time.Now())
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO jobs(
				id, account_id, action, payload_json, rule_version_id,
				dedupe_key, run_at, deadline_at, max_attempts,
				state, created_at, updated_at
			) VALUES (?, ?, 'sync_post_metrics', ?, ?, ?, ?, ?, ?,
				'pending', ?, ?)
			ON CONFLICT(dedupe_key) DO NOTHING`,
			id.New("job"), accountID, string(payload), ruleVersionID,
			fmt.Sprintf("post-metrics:%s:%s", postID, delay.Value()),
			formatTime(runAt), formatTime(deadline), maxAttempts, now, now,
		); err != nil {
			return fmt.Errorf("schedule post metric sync: %w", err)
		}
	}
	return nil
}

func (r *OperationRepository) Fail(
	ctx context.Context,
	operationID, state, code, message string,
) error {
	if state != OperationRejected && state != OperationUncertain && state != OperationFailedAmbiguous {
		return fmt.Errorf("invalid operation failure state %s", state)
	}
	now := formatTime(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE api_operations
		SET state = ?, error_code = ?, error_message = ?,
			resolved_at = CASE WHEN ? = 'uncertain' THEN NULL ELSE ? END,
			updated_at = ?
		WHERE operation_id = ? AND state IN ('prepared', 'sending', 'uncertain', 'reconciling')`,
		state, nullableString(code), message, state, now, now, operationID,
	)
	if err != nil {
		return fmt.Errorf("fail API operation: %w", err)
	}
	return requireOne(result, "operation cannot transition to failure state")
}

func (r *OperationRepository) MarkReconciling(ctx context.Context, operationID string) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE api_operations SET state = 'reconciling', updated_at = ?
		WHERE operation_id = ? AND state = 'uncertain'`,
		formatTime(time.Now()), operationID,
	)
	if err != nil {
		return err
	}
	return requireOne(result, "operation is not uncertain")
}

func (r *OperationRepository) Get(ctx context.Context, operationID string) (Operation, error) {
	return scanOperation(r.db.QueryRowContext(ctx, operationSelect+" WHERE operation_id = ?", operationID))
}

func (r *OperationRepository) ListUnresolved(
	ctx context.Context,
	accountID string,
	limit int,
) ([]Operation, error) {
	if limit <= 0 {
		limit = 100
	}
	rows, err := r.db.QueryContext(ctx, operationSelect+`
		WHERE account_id = ? AND state IN ('uncertain', 'reconciling')
		ORDER BY updated_at LIMIT ?`,
		accountID, limit,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var operations []Operation
	for rows.Next() {
		operation, err := scanOperation(rows)
		if err != nil {
			return nil, err
		}
		operations = append(operations, operation)
	}
	return operations, rows.Err()
}

const operationSelect = `
	SELECT operation_id, account_id, action, subject_id, request_hash,
		request_json, state, x_post_id, response_json, error_code, error_message,
		prepared_at, sent_at, resolved_at, updated_at
	FROM api_operations`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanOperation(scanner rowScanner) (Operation, error) {
	var operation Operation
	var xPostID, response, errorCode, errorMessage sql.NullString
	var preparedAt, updatedAt string
	var sentAt, resolvedAt sql.NullString
	err := scanner.Scan(
		&operation.ID, &operation.AccountID, &operation.Action, &operation.SubjectID,
		&operation.RequestHash, &operation.RequestJSON, &operation.State,
		&xPostID, &response, &errorCode, &errorMessage, &preparedAt,
		&sentAt, &resolvedAt, &updatedAt,
	)
	if err != nil {
		return Operation{}, err
	}
	operation.PreparedAt, err = parseTime(preparedAt)
	if err != nil {
		return Operation{}, err
	}
	operation.UpdatedAt, err = parseTime(updatedAt)
	if err != nil {
		return Operation{}, err
	}
	setString := func(value sql.NullString) *string {
		if !value.Valid {
			return nil
		}
		return &value.String
	}
	operation.XPostID = setString(xPostID)
	operation.ResponseJSON = setString(response)
	operation.ErrorCode = setString(errorCode)
	operation.ErrorMessage = setString(errorMessage)
	if sentAt.Valid {
		value, err := parseTime(sentAt.String)
		if err != nil {
			return Operation{}, err
		}
		operation.SentAt = &value
	}
	if resolvedAt.Valid {
		value, err := parseTime(resolvedAt.String)
		if err != nil {
			return Operation{}, err
		}
		operation.ResolvedAt = &value
	}
	return operation, nil
}

func requireOne(result sql.Result, message string) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return fmt.Errorf("%s", message)
	}
	return nil
}

func nullableString(value string) any {
	if value == "" {
		return nil
	}
	return value
}

func parseTime(value string) (time.Time, error) {
	return time.Parse(time.RFC3339Nano, value)
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
