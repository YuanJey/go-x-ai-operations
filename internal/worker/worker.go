package worker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/jobs"
)

type Handler interface {
	Handle(ctx context.Context, job jobs.Job) error
}

type HandlerFunc func(context.Context, jobs.Job) error

func (f HandlerFunc) Handle(ctx context.Context, job jobs.Job) error {
	return f(ctx, job)
}

type Runner struct {
	repository   *jobs.Repository
	owner        string
	lease        time.Duration
	pollInterval time.Duration
	batchSize    int
	handlers     map[string]Handler
	logger       *slog.Logger
}

func New(
	repository *jobs.Repository,
	owner string,
	lease, pollInterval time.Duration,
	batchSize int,
	logger *slog.Logger,
) *Runner {
	if logger == nil {
		logger = slog.Default()
	}
	return &Runner{
		repository: repository, owner: owner, lease: lease,
		pollInterval: pollInterval, batchSize: batchSize,
		handlers: make(map[string]Handler), logger: logger,
	}
}

func (r *Runner) Register(action string, handler Handler) {
	r.handlers[action] = handler
}

func (r *Runner) Run(ctx context.Context) error {
	ticker := time.NewTicker(r.pollInterval)
	defer ticker.Stop()
	for {
		if err := r.Poll(ctx); err != nil {
			r.logger.Error("worker poll failed", "error", err)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}
	}
}

func (r *Runner) Poll(ctx context.Context) error {
	leased, err := r.repository.Lease(ctx, r.owner, time.Now(), r.lease, r.batchSize)
	if err != nil {
		return err
	}
	for _, job := range leased {
		handler, ok := r.handlers[job.Action]
		if !ok {
			if err := r.repository.Fail(
				ctx, job.ID, r.owner, "no handler registered",
				time.Now(), false,
			); err != nil {
				return err
			}
			continue
		}
		if err := handler.Handle(ctx, job); err != nil {
			retryable := job.Attempts < job.MaxAttempts
			retryAt := time.Now().Add(retryBackoff(job.Attempts))
			var directive interface {
				JobRetry() (bool, time.Time)
			}
			if errors.As(err, &directive) {
				directiveRetryable, directiveRetryAt := directive.JobRetry()
				retryable = retryable && directiveRetryable
				if !directiveRetryAt.IsZero() {
					retryAt = directiveRetryAt
				}
			}
			if failErr := r.repository.Fail(
				ctx, job.ID, r.owner, err.Error(), retryAt, retryable,
			); failErr != nil {
				return fmt.Errorf("handle job: %v; fail job: %w", err, failErr)
			}
			continue
		}
		if err := r.repository.Complete(ctx, job.ID, r.owner); err != nil {
			return err
		}
	}
	return nil
}

func retryBackoff(attempt int) time.Duration {
	if attempt < 1 {
		attempt = 1
	}
	if attempt > 6 {
		attempt = 6
	}
	return time.Duration(1<<(attempt-1)) * time.Minute
}
