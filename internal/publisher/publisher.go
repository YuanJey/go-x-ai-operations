package publisher

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/id"
	"github.com/YuanJey/go-x-ai-operations/internal/metering"
	"github.com/YuanJey/go-x-ai-operations/internal/quota"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	"github.com/YuanJey/go-x-ai-operations/internal/xapi"
)

type PostPublisher struct {
	executor *executor
}

type ReplyPublisher struct {
	executor *executor
}

type executor struct {
	db         *sql.DB
	client     xapi.Client
	quota      *quota.Counter
	operations *OperationRepository
	accountID  string
	metering   *metering.Repository
	unitCost   map[string]float64
	currency   string
}

func (p *PostPublisher) ConfigureMetering(
	repository *metering.Repository,
	unitCost map[string]float64,
	currency string,
) {
	p.executor.configureMetering(repository, unitCost, currency)
}

func (p *ReplyPublisher) ConfigureMetering(
	repository *metering.Repository,
	unitCost map[string]float64,
	currency string,
) {
	p.executor.configureMetering(repository, unitCost, currency)
}

func (e *executor) configureMetering(
	repository *metering.Repository,
	unitCost map[string]float64,
	currency string,
) {
	e.metering = repository
	e.unitCost = unitCost
	e.currency = currency
}

func NewPostPublisher(
	db *sql.DB,
	client xapi.Client,
	counter *quota.Counter,
	operations *OperationRepository,
	accountID string,
) *PostPublisher {
	return &PostPublisher{executor: &executor{
		db: db, client: client, quota: counter,
		operations: operations, accountID: accountID,
	}}
}

func NewReplyPublisher(
	db *sql.DB,
	client xapi.Client,
	counter *quota.Counter,
	operations *OperationRepository,
	accountID string,
) *ReplyPublisher {
	return &ReplyPublisher{executor: &executor{
		db: db, client: client, quota: counter,
		operations: operations, accountID: accountID,
	}}
}

func (p *PostPublisher) Publish(
	ctx context.Context,
	contentID, ruleVersionID string,
) (Operation, error) {
	return p.executor.publish(ctx, contentID, ruleVersionID, content.KindPost, "", true)
}

func (p *ReplyPublisher) Publish(
	ctx context.Context,
	contentID, ruleVersionID, parentPostID string,
	eligible bool,
) (Operation, error) {
	if !eligible {
		return Operation{}, fmt.Errorf("reply eligibility must be verified before publishing")
	}
	if parentPostID == "" {
		return Operation{}, fmt.Errorf("reply parent post ID is required")
	}
	return p.executor.publish(ctx, contentID, ruleVersionID, content.KindReply, parentPostID, eligible)
}

func (e *executor) publish(
	ctx context.Context,
	contentID, ruleVersionID, kind, parentPostID string,
	_ bool,
) (Operation, error) {
	action := "publish_post"
	if kind == content.KindReply {
		action = "publish_reply"
	}
	operation, body, bodyHash, err := e.prepare(
		ctx, contentID, ruleVersionID, kind, action, parentPostID,
	)
	if err != nil {
		return Operation{}, err
	}
	if err := e.operations.MarkSending(ctx, operation.ID); err != nil {
		return operation, err
	}
	operation.State = OperationSending
	sentAt := time.Now().UTC()
	operation.SentAt = &sentAt

	var result xapi.PostResult
	if kind == content.KindReply {
		result, err = e.client.CreateReply(ctx, xapi.CreateReplyRequest{
			Text: body, ParentPostID: parentPostID,
		})
	} else {
		result, err = e.client.CreatePost(ctx, xapi.CreatePostRequest{Text: body})
	}
	if err != nil {
		state := OperationUncertain
		var xError *xapi.Error
		code := ""
		if errors.As(err, &xError) {
			code = xError.Code
			if !xError.Uncertain && xError.StatusCode < 500 {
				state = OperationRejected
			}
		}
		if failErr := e.operations.Fail(ctx, operation.ID, state, code, err.Error()); failErr != nil {
			return operation, fmt.Errorf("publish failed: %v; persist failure: %w", err, failErr)
		}
		operation.State = state
		e.recordCost(ctx, operation, kind)
		return operation, err
	}
	if err := e.operations.Succeed(
		ctx, operation, result.PostID, result, kind, parentPostID,
		bodyHash, result.PublishedAt,
	); err != nil {
		return operation, err
	}
	operation.State = OperationSucceeded
	operation.XPostID = &result.PostID
	if !result.DryRun {
		e.recordCost(ctx, operation, kind)
	}
	return operation, nil
}

func (e *executor) recordCost(
	ctx context.Context,
	operation Operation,
	kind string,
) {
	if e.metering == nil {
		return
	}
	resource := "post_create"
	if kind == content.KindReply {
		resource = "reply_create"
	}
	operationID := operation.ID
	_ = e.metering.Record(ctx, metering.Entry{
		AccountID: e.accountID, OperationID: &operationID,
		ResourceType: resource, Endpoint: "POST /2/tweets",
		Units: 1, EstimatedCost: e.unitCost[resource], Currency: e.currency,
	})
}

func (e *executor) prepare(
	ctx context.Context,
	contentID, ruleVersionID, kind, action, parentPostID string,
) (Operation, string, string, error) {
	tx, err := e.db.BeginTx(ctx, nil)
	if err != nil {
		return Operation{}, "", "", fmt.Errorf("begin publish preparation: %w", err)
	}
	defer tx.Rollback()

	var accountID, itemRuleVersion, itemKind, body, bodyHash, state string
	var topicID sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT account_id, rule_version_id, kind, body, body_hash, state, topic_id
		FROM content_items WHERE id = ?`,
		contentID,
	).Scan(
		&accountID, &itemRuleVersion, &itemKind, &body, &bodyHash, &state, &topicID,
	); err != nil {
		return Operation{}, "", "", fmt.Errorf("load content for publish: %w", err)
	}
	if accountID != e.accountID || itemRuleVersion != ruleVersionID ||
		itemKind != kind || state != content.StateApprovedAuto {
		return Operation{}, "", "", fmt.Errorf("content is not eligible for %s", action)
	}
	var normalizedRules string
	if err := tx.QueryRowContext(ctx, `
		SELECT normalized_json FROM rule_versions
		WHERE id = ? AND account_id = ?`,
		ruleVersionID, e.accountID,
	).Scan(&normalizedRules); err != nil {
		return Operation{}, "", "", fmt.Errorf("load publish rules: %w", err)
	}
	var document rules.Document
	if err := json.Unmarshal([]byte(normalizedRules), &document); err != nil {
		return Operation{}, "", "", fmt.Errorf("decode publish rules: %w", err)
	}
	now := time.Now().UTC()
	withinSchedule, err := rules.IsWithinSchedule(document, action, now)
	if err != nil {
		return Operation{}, "", "", err
	}
	if !withinSchedule {
		return Operation{}, "", "", fmt.Errorf("%s is outside its configured schedule", action)
	}
	if err := checkCooldown(ctx, tx, e.accountID, kind, document.Actions[action].Cooldown.MinInterval.Value(), now); err != nil {
		return Operation{}, "", "", err
	}
	if kind == content.KindPost && topicID.Valid {
		if err := checkTopicCooldown(
			ctx, tx, e.accountID, topicID.String,
			document.Actions[action].Cooldown.SameTopic.Value(), now,
		); err != nil {
			return Operation{}, "", "", err
		}
	}
	if kind == content.KindReply {
		if err := checkUserCooldown(
			ctx, tx, e.accountID, contentID,
			document.Actions[action].Cooldown.SameUser.Value(), now,
		); err != nil {
			return Operation{}, "", "", err
		}
	}
	if _, err := e.quota.AcquireTx(
		ctx, tx, e.accountID, ruleVersionID, action, now,
	); err != nil {
		return Operation{}, "", "", err
	}
	request := map[string]any{"text": body}
	if parentPostID != "" {
		request["reply"] = map[string]string{"in_reply_to_tweet_id": parentPostID}
	}
	requestBody, _ := json.Marshal(request)
	requestSum := sha256.Sum256(requestBody)
	operation := Operation{
		ID: id.New("op"), AccountID: e.accountID, Action: action,
		SubjectID: contentID, RequestHash: hex.EncodeToString(requestSum[:]),
		RequestJSON: string(requestBody), State: OperationPrepared,
		PreparedAt: now, UpdatedAt: now,
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE content_items
		SET state = 'rule_admitted', updated_at = ?
		WHERE id = ? AND state = 'approved_auto'`,
		formatTime(now), contentID,
	); err != nil {
		return Operation{}, "", "", fmt.Errorf("admit content for publish: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO api_operations(
			operation_id, account_id, action, subject_id,
			request_hash, request_json, state, prepared_at, updated_at
		) VALUES (?, ?, ?, ?, ?, ?, 'prepared', ?, ?)`,
		operation.ID, operation.AccountID, operation.Action, operation.SubjectID,
		operation.RequestHash, operation.RequestJSON,
		formatTime(now), formatTime(now),
	); err != nil {
		return Operation{}, "", "", fmt.Errorf("insert API operation: %w", err)
	}
	if err := insertAudit(ctx, tx, e.accountID, "publish.prepared", contentID, map[string]any{
		"operation_id": operation.ID, "action": action,
	}); err != nil {
		return Operation{}, "", "", err
	}
	if err := tx.Commit(); err != nil {
		return Operation{}, "", "", fmt.Errorf("commit publish preparation: %w", err)
	}
	return operation, body, bodyHash, nil
}

func checkCooldown(
	ctx context.Context,
	tx *sql.Tx,
	accountID, kind string,
	minimum time.Duration,
	now time.Time,
) error {
	if minimum <= 0 {
		return nil
	}
	var lastPublished sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(published_at) FROM x_posts
		WHERE account_id = ? AND kind = ?`,
		accountID, kind,
	).Scan(&lastPublished); err != nil {
		return fmt.Errorf("load publish cooldown: %w", err)
	}
	if !lastPublished.Valid {
		return nil
	}
	last, err := parseTime(lastPublished.String)
	if err != nil {
		return fmt.Errorf("parse publish cooldown: %w", err)
	}
	if now.Before(last.Add(minimum)) {
		return fmt.Errorf("%s cooldown is active until %s", kind, last.Add(minimum).Format(time.RFC3339))
	}
	return nil
}

func checkTopicCooldown(
	ctx context.Context,
	tx *sql.Tx,
	accountID, topicID string,
	minimum time.Duration,
	now time.Time,
) error {
	if minimum <= 0 {
		return nil
	}
	var lastPublished sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(xp.published_at)
		FROM x_posts xp
		JOIN content_items ci ON ci.id = xp.content_id
		JOIN topics t ON t.id = ci.topic_id
		WHERE xp.account_id = ? AND xp.kind = 'post'
			AND t.title = (SELECT title FROM topics WHERE id = ?)`,
		accountID, topicID,
	).Scan(&lastPublished); err != nil {
		return fmt.Errorf("load same-topic cooldown: %w", err)
	}
	return enforceCooldown("same-topic", lastPublished, minimum, now)
}

func checkUserCooldown(
	ctx context.Context,
	tx *sql.Tx,
	accountID, contentID string,
	minimum time.Duration,
	now time.Time,
) error {
	if minimum <= 0 {
		return nil
	}
	var lastPublished sql.NullString
	if err := tx.QueryRowContext(ctx, `
		SELECT MAX(xp.published_at)
		FROM x_posts xp
		JOIN replies previous_reply ON previous_reply.content_id = xp.content_id
		JOIN mentions previous_mention ON previous_mention.id = previous_reply.mention_id
		WHERE xp.account_id = ? AND xp.kind = 'reply'
			AND previous_mention.author_id = (
				SELECT current_mention.author_id
				FROM replies current_reply
				JOIN mentions current_mention ON current_mention.id = current_reply.mention_id
				WHERE current_reply.content_id = ?
			)`,
		accountID, contentID,
	).Scan(&lastPublished); err != nil {
		return fmt.Errorf("load same-user cooldown: %w", err)
	}
	return enforceCooldown("same-user", lastPublished, minimum, now)
}

func enforceCooldown(
	name string,
	lastPublished sql.NullString,
	minimum time.Duration,
	now time.Time,
) error {
	if !lastPublished.Valid {
		return nil
	}
	last, err := parseTime(lastPublished.String)
	if err != nil {
		return fmt.Errorf("parse %s cooldown: %w", name, err)
	}
	if now.Before(last.Add(minimum)) {
		return fmt.Errorf("%s cooldown is active until %s", name, last.Add(minimum).Format(time.RFC3339))
	}
	return nil
}

func insertAudit(
	ctx context.Context,
	tx *sql.Tx,
	accountID, eventType, subjectID string,
	details any,
) error {
	body, err := json.Marshal(details)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_logs(
			id, account_id, actor_type, actor_id, event_type,
			subject_type, subject_id, details_json, created_at
		) VALUES (?, ?, 'system', 'publisher', ?, 'content', ?, ?, ?)`,
		id.New("audit"), accountID, eventType, subjectID,
		string(body), formatTime(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("insert publish audit: %w", err)
	}
	return nil
}
