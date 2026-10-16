package jobs

import "time"

const (
	StatePending   = "pending"
	StateLeased    = "leased"
	StateSucceeded = "succeeded"
	StateDead      = "dead"
	StateExpired   = "expired"
	StateCancelled = "cancelled"
)

type Job struct {
	ID            string     `json:"id"`
	AccountID     string     `json:"account_id"`
	Action        string     `json:"action"`
	PayloadJSON   string     `json:"payload_json"`
	PayloadRef    *string    `json:"payload_ref,omitempty"`
	RuleVersionID string     `json:"rule_version_id"`
	DedupeKey     string     `json:"dedupe_key"`
	RunAt         time.Time  `json:"run_at"`
	DeadlineAt    *time.Time `json:"deadline_at,omitempty"`
	Priority      int        `json:"priority"`
	Attempts      int        `json:"attempts"`
	MaxAttempts   int        `json:"max_attempts"`
	LeaseOwner    *string    `json:"lease_owner,omitempty"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
	State         string     `json:"state"`
	LastError     *string    `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	UpdatedAt     time.Time  `json:"updated_at"`
}

type NewJob struct {
	AccountID     string
	Action        string
	PayloadJSON   string
	PayloadRef    *string
	RuleVersionID string
	DedupeKey     string
	RunAt         time.Time
	DeadlineAt    *time.Time
	Priority      int
	MaxAttempts   int
}

type Filter struct {
	AccountID string
	State     string
	Action    string
	Limit     int
}
