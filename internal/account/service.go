package account

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Account struct {
	ID          string    `json:"id"`
	Handle      string    `json:"handle"`
	Timezone    string    `json:"timezone"`
	State       string    `json:"state"`
	PauseReason *string   `json:"pause_reason,omitempty"`
	CreatedAt   time.Time `json:"created_at"`
	UpdatedAt   time.Time `json:"updated_at"`
}

type Service struct {
	db *sql.DB
}

func NewService(db *sql.DB) *Service {
	return &Service{db: db}
}

func (s *Service) Get(ctx context.Context, accountID string) (Account, error) {
	var account Account
	var reason sql.NullString
	var createdAt, updatedAt string
	err := s.db.QueryRowContext(ctx, `
		SELECT id, handle, timezone, state, pause_reason, created_at, updated_at
		FROM accounts WHERE id = ?`,
		accountID,
	).Scan(
		&account.ID, &account.Handle, &account.Timezone, &account.State,
		&reason, &createdAt, &updatedAt,
	)
	if err != nil {
		return Account{}, fmt.Errorf("load account: %w", err)
	}
	account.CreatedAt, err = time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return Account{}, err
	}
	account.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return Account{}, err
	}
	if reason.Valid {
		account.PauseReason = &reason.String
	}
	return account, nil
}

func (s *Service) Pause(ctx context.Context, accountID, reason string) error {
	if reason == "" {
		reason = "operator requested"
	}
	return s.setState(ctx, accountID, "paused", &reason)
}

func (s *Service) Resume(ctx context.Context, accountID string) error {
	return s.setState(ctx, accountID, "active", nil)
}

func (s *Service) setState(
	ctx context.Context,
	accountID, state string,
	reason *string,
) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("begin account state change: %w", err)
	}
	defer tx.Rollback()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	result, err := tx.ExecContext(ctx, `
		UPDATE accounts
		SET state = ?, pause_reason = ?, updated_at = ?
		WHERE id = ?`,
		state, reason, now, accountID,
	)
	if err != nil {
		return fmt.Errorf("update account state: %w", err)
	}
	affected, _ := result.RowsAffected()
	if affected != 1 {
		return fmt.Errorf("account %s not found", accountID)
	}
	details, _ := json.Marshal(map[string]any{"state": state, "reason": reason})
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO audit_logs(
			id, account_id, actor_type, actor_id, event_type,
			subject_type, subject_id, details_json, created_at
		) VALUES (?, ?, 'operator', 'local', ?, 'account', ?, ?, ?)`,
		id.New("audit"), accountID, "account."+state, accountID, string(details), now,
	); err != nil {
		return fmt.Errorf("audit account state: %w", err)
	}
	return tx.Commit()
}
