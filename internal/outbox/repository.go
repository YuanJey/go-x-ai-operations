package outbox

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

type Repository struct {
	db *sql.DB
}

func NewRepository(db *sql.DB) *Repository {
	return &Repository{db: db}
}

func (r *Repository) Lease(
	ctx context.Context,
	owner string,
	now time.Time,
	duration time.Duration,
	limit int,
) ([]Event, error) {
	if owner == "" || duration <= 0 || limit < 1 {
		return nil, fmt.Errorf("owner, positive lease duration, and positive limit are required")
	}
	tx, err := r.db.BeginTx(ctx, nil)
	if err != nil {
		return nil, fmt.Errorf("begin outbox lease: %w", err)
	}
	defer tx.Rollback()

	if _, err := tx.ExecContext(ctx, `
		UPDATE outbox_events
		SET state = 'pending', lease_owner = NULL, lease_until = NULL
		WHERE state = 'leased' AND lease_until <= ?`,
		formatTime(now),
	); err != nil {
		return nil, fmt.Errorf("recover outbox leases: %w", err)
	}
	rows, err := tx.QueryContext(ctx, `
		SELECT event_id
		FROM outbox_events
		WHERE state = 'pending' AND available_at <= ?
		ORDER BY created_at
		LIMIT ?`,
		formatTime(now), limit,
	)
	if err != nil {
		return nil, fmt.Errorf("select outbox events: %w", err)
	}
	var ids []string
	for rows.Next() {
		var eventID string
		if err := rows.Scan(&eventID); err != nil {
			rows.Close()
			return nil, fmt.Errorf("scan outbox candidate: %w", err)
		}
		ids = append(ids, eventID)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}

	var events []Event
	leaseUntil := now.Add(duration)
	for _, eventID := range ids {
		result, err := tx.ExecContext(ctx, `
			UPDATE outbox_events
			SET state = 'leased', attempts = attempts + 1,
				lease_owner = ?, lease_until = ?
			WHERE event_id = ? AND state = 'pending'`,
			owner, formatTime(leaseUntil), eventID,
		)
		if err != nil {
			return nil, fmt.Errorf("lease outbox event: %w", err)
		}
		affected, _ := result.RowsAffected()
		if affected != 1 {
			continue
		}
		event, err := scanEvent(tx.QueryRowContext(ctx, eventSelect+" WHERE event_id = ?", eventID))
		if err != nil {
			return nil, fmt.Errorf("load leased outbox event: %w", err)
		}
		events = append(events, event)
	}
	if err := tx.Commit(); err != nil {
		return nil, fmt.Errorf("commit outbox lease: %w", err)
	}
	return events, nil
}

func (r *Repository) Complete(ctx context.Context, eventID, owner string) error {
	now := formatTime(time.Now())
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET state = 'published', published_at = ?, lease_owner = NULL, lease_until = NULL
		WHERE event_id = ? AND state = 'leased' AND lease_owner = ?`,
		now, eventID, owner,
	)
	if err != nil {
		return fmt.Errorf("complete outbox event: %w", err)
	}
	return requireOne(result)
}

func (r *Repository) Fail(
	ctx context.Context,
	eventID, owner, reason string,
	availableAt time.Time,
) error {
	result, err := r.db.ExecContext(ctx, `
		UPDATE outbox_events
		SET state = 'pending', available_at = ?, last_error = ?,
			lease_owner = NULL, lease_until = NULL
		WHERE event_id = ? AND state = 'leased' AND lease_owner = ?`,
		formatTime(availableAt), reason, eventID, owner,
	)
	if err != nil {
		return fmt.Errorf("fail outbox event: %w", err)
	}
	return requireOne(result)
}

const eventSelect = `
	SELECT event_id, account_id, aggregate_type, aggregate_id, event_type,
		payload_json, state, attempts, available_at, lease_owner, lease_until,
		last_error, created_at, published_at
	FROM outbox_events`

type rowScanner interface {
	Scan(dest ...any) error
}

func scanEvent(scanner rowScanner) (Event, error) {
	var event Event
	var availableAt, createdAt string
	var leaseOwner, leaseUntil, lastError, publishedAt sql.NullString
	err := scanner.Scan(
		&event.ID, &event.AccountID, &event.AggregateType, &event.AggregateID,
		&event.Type, &event.PayloadJSON, &event.State, &event.Attempts,
		&availableAt, &leaseOwner, &leaseUntil, &lastError, &createdAt, &publishedAt,
	)
	if err != nil {
		return Event{}, err
	}
	event.AvailableAt, err = parseTime(availableAt)
	if err != nil {
		return Event{}, err
	}
	event.CreatedAt, err = parseTime(createdAt)
	if err != nil {
		return Event{}, err
	}
	if leaseOwner.Valid {
		event.LeaseOwner = &leaseOwner.String
	}
	if lastError.Valid {
		event.LastError = &lastError.String
	}
	if leaseUntil.Valid {
		value, err := parseTime(leaseUntil.String)
		if err != nil {
			return Event{}, err
		}
		event.LeaseUntil = &value
	}
	if publishedAt.Valid {
		value, err := parseTime(publishedAt.String)
		if err != nil {
			return Event{}, err
		}
		event.PublishedAt = &value
	}
	return event, nil
}

func requireOne(result sql.Result) error {
	affected, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if affected != 1 {
		return errors.New("outbox lease is no longer owned")
	}
	return nil
}

func formatTime(value time.Time) string {
	return value.UTC().Format(time.RFC3339Nano)
}

func parseTime(value string) (time.Time, error) {
	parsed, err := time.Parse(time.RFC3339Nano, value)
	if err != nil {
		return time.Time{}, fmt.Errorf("parse timestamp %q: %w", value, err)
	}
	return parsed, nil
}
