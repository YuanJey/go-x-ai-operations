package outbox

import (
	"context"
	"time"
)

type Event struct {
	ID            string     `json:"event_id"`
	AccountID     string     `json:"account_id"`
	AggregateType string     `json:"aggregate_type"`
	AggregateID   string     `json:"aggregate_id"`
	Type          string     `json:"event_type"`
	PayloadJSON   string     `json:"payload_json"`
	State         string     `json:"state"`
	Attempts      int        `json:"attempts"`
	AvailableAt   time.Time  `json:"available_at"`
	LeaseOwner    *string    `json:"lease_owner,omitempty"`
	LeaseUntil    *time.Time `json:"lease_until,omitempty"`
	LastError     *string    `json:"last_error,omitempty"`
	CreatedAt     time.Time  `json:"created_at"`
	PublishedAt   *time.Time `json:"published_at,omitempty"`
}

type Handler interface {
	HandleEvent(ctx context.Context, event Event) error
}

type HandlerFunc func(context.Context, Event) error

func (f HandlerFunc) HandleEvent(ctx context.Context, event Event) error {
	return f(ctx, event)
}
