package outbox

import (
	"context"
	"fmt"
	"time"
)

type Dispatcher struct {
	repository *Repository
	owner      string
	lease      time.Duration
	batchSize  int
	handlers   map[string]Handler
}

func NewDispatcher(
	repository *Repository,
	owner string,
	lease time.Duration,
	batchSize int,
) *Dispatcher {
	return &Dispatcher{
		repository: repository,
		owner:      owner,
		lease:      lease,
		batchSize:  batchSize,
		handlers:   make(map[string]Handler),
	}
}

func (d *Dispatcher) Register(eventType string, handler Handler) {
	d.handlers[eventType] = handler
}

func (d *Dispatcher) Dispatch(ctx context.Context, now time.Time) (int, error) {
	events, err := d.repository.Lease(ctx, d.owner, now, d.lease, d.batchSize)
	if err != nil {
		return 0, err
	}
	completed := 0
	for _, event := range events {
		handler, ok := d.handlers[event.Type]
		if !ok {
			if err := d.repository.Complete(ctx, event.ID, d.owner); err != nil {
				return completed, err
			}
			completed++
			continue
		}
		if err := handler.HandleEvent(ctx, event); err != nil {
			retryAt := now.Add(backoff(event.Attempts))
			if failErr := d.repository.Fail(ctx, event.ID, d.owner, err.Error(), retryAt); failErr != nil {
				return completed, fmt.Errorf("handle event: %v; release lease: %w", err, failErr)
			}
			continue
		}
		if err := d.repository.Complete(ctx, event.ID, d.owner); err != nil {
			return completed, err
		}
		completed++
	}
	return completed, nil
}

func backoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<(attempt-1)) * time.Second
}
