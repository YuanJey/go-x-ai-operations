package quota

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
)

var (
	ErrQuotaExceeded  = errors.New("action quota exceeded")
	ErrAccountPaused  = errors.New("account is paused")
	ErrActionDisabled = errors.New("action is disabled")
)

type Period struct {
	Kind  string    `json:"kind"`
	Start time.Time `json:"start"`
	Limit int       `json:"limit"`
	Count int       `json:"count"`
}

type Decision struct {
	AccountID     string    `json:"account_id"`
	RuleVersionID string    `json:"rule_version_id"`
	Action        string    `json:"action"`
	Admitted      bool      `json:"admitted"`
	Periods       []Period  `json:"periods"`
	DecidedAt     time.Time `json:"decided_at"`
}

type Counter struct {
	db *sql.DB
}

func NewCounter(db *sql.DB) *Counter {
	return &Counter{db: db}
}

func (c *Counter) Acquire(
	ctx context.Context,
	accountID, ruleVersionID, actionName string,
	now time.Time,
) (Decision, error) {
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return Decision{}, fmt.Errorf("begin quota decision: %w", err)
	}
	defer tx.Rollback()

	decision, err := c.AcquireTx(ctx, tx, accountID, ruleVersionID, actionName, now)
	if err != nil {
		return decision, err
	}
	if err := tx.Commit(); err != nil {
		return Decision{}, fmt.Errorf("commit quota decision: %w", err)
	}
	return decision, nil
}

func (c *Counter) AcquireTx(
	ctx context.Context,
	tx *sql.Tx,
	accountID, ruleVersionID, actionName string,
	now time.Time,
) (Decision, error) {
	var accountState string
	if err := tx.QueryRowContext(ctx,
		"SELECT state FROM accounts WHERE id = ?", accountID,
	).Scan(&accountState); err != nil {
		return Decision{}, fmt.Errorf("load account state: %w", err)
	}
	if accountState != "active" {
		return Decision{}, ErrAccountPaused
	}

	var ruleAccount, ruleState, normalized string
	if err := tx.QueryRowContext(ctx, `
		SELECT account_id, state, normalized_json
		FROM rule_versions WHERE id = ?`,
		ruleVersionID,
	).Scan(&ruleAccount, &ruleState, &normalized); err != nil {
		return Decision{}, fmt.Errorf("load rule for quota: %w", err)
	}
	if ruleAccount != accountID {
		return Decision{}, fmt.Errorf("rule version belongs to another account")
	}
	if ruleState != "active" && ruleState != "superseded" {
		return Decision{}, fmt.Errorf("rule version state %s does not allow execution", ruleState)
	}

	var document rules.Document
	if err := json.Unmarshal([]byte(normalized), &document); err != nil {
		return Decision{}, fmt.Errorf("decode rule for quota: %w", err)
	}
	action, ok := document.Actions[actionName]
	if !ok || !action.Enabled {
		return Decision{}, ErrActionDisabled
	}
	if action.Quota == nil {
		return Decision{}, fmt.Errorf("action %s has no quota", actionName)
	}
	location, err := time.LoadLocation(scheduleTimezone(document, action))
	if err != nil {
		return Decision{}, fmt.Errorf("load quota timezone: %w", err)
	}
	periods := quotaPeriods(*action.Quota, now.In(location))
	if len(periods) == 0 {
		return Decision{}, fmt.Errorf("action %s has no configured quota period", actionName)
	}

	for index := range periods {
		period := &periods[index]
		result, err := tx.ExecContext(ctx, `
			INSERT INTO action_counters(
				id, account_id, rule_version_id, action, period_kind,
				period_start, count, updated_at
			) VALUES (?, ?, ?, ?, ?, ?, 1, ?)
			ON CONFLICT(account_id, action, period_start, period_kind)
			DO UPDATE SET
				count = action_counters.count + 1,
				rule_version_id = excluded.rule_version_id,
				updated_at = excluded.updated_at
			WHERE action_counters.count < ?`,
			id.New("quota"), accountID, ruleVersionID, actionName, period.Kind,
			formatTime(period.Start), formatTime(now), period.Limit,
		)
		if err != nil {
			return Decision{}, fmt.Errorf("increment %s quota: %w", period.Kind, err)
		}
		affected, err := result.RowsAffected()
		if err != nil {
			return Decision{}, fmt.Errorf("inspect quota increment: %w", err)
		}
		if affected != 1 {
			return Decision{
				AccountID: accountID, RuleVersionID: ruleVersionID,
				Action: actionName, Admitted: false, Periods: periods, DecidedAt: now.UTC(),
			}, ErrQuotaExceeded
		}
		if err := tx.QueryRowContext(ctx, `
			SELECT count FROM action_counters
			WHERE account_id = ? AND action = ? AND period_kind = ? AND period_start = ?`,
			accountID, actionName, period.Kind, formatTime(period.Start),
		).Scan(&period.Count); err != nil {
			return Decision{}, fmt.Errorf("read quota count: %w", err)
		}
	}

	decision := Decision{
		AccountID: accountID, RuleVersionID: ruleVersionID, Action: actionName,
		Admitted: true, Periods: periods, DecidedAt: now.UTC(),
	}
	if err := appendAudit(ctx, tx, decision, "quota.admitted"); err != nil {
		return Decision{}, err
	}
	return decision, nil
}

func (c *Counter) Release(ctx context.Context, decision Decision) error {
	if !decision.Admitted {
		return nil
	}
	tx, err := c.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin quota release: %w", err)
	}
	defer tx.Rollback()
	for _, period := range decision.Periods {
		if _, err := tx.ExecContext(ctx, `
			UPDATE action_counters
			SET count = count - 1, updated_at = ?
			WHERE account_id = ? AND action = ? AND period_kind = ?
				AND period_start = ? AND count > 0`,
			formatTime(time.Now()), decision.AccountID, decision.Action,
			period.Kind, formatTime(period.Start),
		); err != nil {
			return fmt.Errorf("release %s quota: %w", period.Kind, err)
		}
	}
	if err := appendAudit(ctx, tx, decision, "quota.released"); err != nil {
		return err
	}
	return tx.Commit()
}

func quotaPeriods(config rules.Quota, local time.Time) []Period {
	var periods []Period
	if config.PerMinute != nil {
		periods = append(periods, Period{
			Kind:  "minute",
			Start: time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), local.Minute(), 0, 0, local.Location()).UTC(),
			Limit: *config.PerMinute,
		})
	}
	if config.PerHour != nil {
		periods = append(periods, Period{
			Kind:  "hour",
			Start: time.Date(local.Year(), local.Month(), local.Day(), local.Hour(), 0, 0, 0, local.Location()).UTC(),
			Limit: *config.PerHour,
		})
	}
	if config.PerDay != nil {
		periods = append(periods, Period{
			Kind:  "day",
			Start: time.Date(local.Year(), local.Month(), local.Day(), 0, 0, 0, 0, local.Location()).UTC(),
			Limit: *config.PerDay,
		})
	}
	return periods
}

func scheduleTimezone(document rules.Document, action rules.Action) string {
	if action.Schedule.Timezone != "" {
		return action.Schedule.Timezone
	}
	return document.Account.Timezone
}

func appendAudit(ctx context.Context, tx *sql.Tx, decision Decision, eventType string) error {
	body, err := json.Marshal(decision)
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `
		INSERT INTO audit_logs(
			id, account_id, actor_type, actor_id, event_type,
			subject_type, subject_id, details_json, created_at
		) VALUES (?, ?, 'system', 'quota', ?, 'action', ?, ?, ?)`,
		id.New("audit"), decision.AccountID, eventType, decision.Action,
		string(body), formatTime(time.Now()),
	)
	if err != nil {
		return fmt.Errorf("write quota audit: %w", err)
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}
