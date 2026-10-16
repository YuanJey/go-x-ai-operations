package scheduler

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/jobs"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	"github.com/robfig/cron/v3"
)

type Result struct {
	RuleVersionID string `json:"rule_version_id,omitempty"`
	Enqueued      int    `json:"enqueued"`
	Deduplicated  int    `json:"deduplicated"`
	Dropped       int    `json:"dropped"`
}

type Scheduler struct {
	accountID string
	rules     *rules.Repository
	jobs      *jobs.Repository
}

func New(
	accountID string,
	ruleRepository *rules.Repository,
	jobRepository *jobs.Repository,
) *Scheduler {
	return &Scheduler{
		accountID: accountID,
		rules:     ruleRepository,
		jobs:      jobRepository,
	}
}

func (s *Scheduler) Tick(ctx context.Context, now time.Time) (Result, error) {
	if _, err := s.rules.ActivateDue(ctx, now); err != nil {
		return Result{}, err
	}
	version, err := s.rules.Active(ctx, s.accountID)
	if errors.Is(err, rules.ErrNoActiveRule) {
		return Result{}, nil
	}
	if err != nil {
		return Result{}, err
	}
	result := Result{RuleVersionID: version.ID}
	for name, action := range version.Document.Actions {
		if !action.Enabled {
			continue
		}
		var inputs []jobs.NewJob
		switch action.Trigger {
		case rules.TriggerQuota:
			// Replies are event-driven. Their schedule and quota are enforced by
			// ReplyPublisher when an eligible mention reaches the queue.
			if name == "publish_reply" {
				continue
			}
			inputs, err = s.quotaJobs(version, name, action, now)
		case rules.TriggerInterval:
			inputs, err = s.intervalJobs(version, name, action, now)
		case rules.TriggerCron:
			inputs, err = s.cronJobs(version, name, action, now)
		case rules.TriggerDelayed:
			continue
		default:
			err = fmt.Errorf("unsupported trigger %s for action %s", action.Trigger, name)
		}
		if err != nil {
			return result, err
		}
		for _, input := range inputs {
			if action.Queue.MaxPending > 0 {
				pending, err := s.jobs.PendingCount(ctx, s.accountID, name)
				if err != nil {
					return result, err
				}
				if pending >= action.Queue.MaxPending {
					if action.Queue.Overflow != "drop_oldest" {
						result.Dropped++
						continue
					}
					cancelled, err := s.jobs.CancelOldestPending(
						ctx, s.accountID, name, "queue overflow: dropped oldest",
					)
					if err != nil {
						return result, err
					}
					if !cancelled {
						result.Dropped++
						continue
					}
					result.Dropped++
				}
			}
			created, err := s.jobs.Enqueue(ctx, input)
			if err != nil {
				return result, err
			}
			if created {
				result.Enqueued++
			} else {
				result.Deduplicated++
			}
		}
	}
	return result, nil
}

func (s *Scheduler) quotaJobs(
	version rules.Version,
	name string,
	action rules.Action,
	now time.Time,
) ([]jobs.NewJob, error) {
	slots, err := rules.DailySlots(version.Document, name, now)
	if err != nil {
		return nil, err
	}
	var result []jobs.NewJob
	for _, slot := range slots {
		if slot.Before(now.UTC()) {
			continue
		}
		deadline := slot.Add(action.Queue.MaxAge.Value())
		if action.Queue.MaxAge.Value() <= 0 {
			deadline = slot.Add(24 * time.Hour)
		}
		payload, _ := json.Marshal(map[string]any{
			"slot_at": slot,
			"action":  name,
		})
		result = append(result, jobs.NewJob{
			AccountID:     s.accountID,
			Action:        name,
			PayloadJSON:   string(payload),
			RuleVersionID: version.ID,
			DedupeKey:     fmt.Sprintf("slot:%s:%s:%s", version.ID, name, slot.Format(time.RFC3339)),
			RunAt:         slot,
			DeadlineAt:    &deadline,
			MaxAttempts:   max(1, action.Retry.MaxAttempts),
		})
	}
	return result, nil
}

func (s *Scheduler) intervalJobs(
	version rules.Version,
	name string,
	action rules.Action,
	now time.Time,
) ([]jobs.NewJob, error) {
	interval := action.Every.Value()
	if interval <= 0 {
		return nil, fmt.Errorf("action %s has invalid interval", name)
	}
	runAt := now.UTC().Truncate(interval)
	deadline := runAt.Add(interval)
	payload, _ := json.Marshal(map[string]any{
		"scheduled_at": runAt,
		"action":       name,
	})
	return []jobs.NewJob{{
		AccountID:     s.accountID,
		Action:        name,
		PayloadJSON:   string(payload),
		RuleVersionID: version.ID,
		DedupeKey:     fmt.Sprintf("interval:%s:%s:%s", version.ID, name, runAt.Format(time.RFC3339Nano)),
		RunAt:         runAt,
		DeadlineAt:    &deadline,
		MaxAttempts:   max(1, action.Retry.MaxAttempts),
	}}, nil
}

func (s *Scheduler) cronJobs(
	version rules.Version,
	name string,
	action rules.Action,
	now time.Time,
) ([]jobs.NewJob, error) {
	schedule, err := cron.ParseStandard(action.Expression)
	if err != nil {
		return nil, fmt.Errorf("parse cron for action %s: %w", name, err)
	}
	timezone := action.Schedule.Timezone
	if timezone == "" {
		timezone = version.Document.Account.Timezone
	}
	location, err := time.LoadLocation(timezone)
	if err != nil {
		return nil, err
	}
	currentMinute := now.In(location).Truncate(time.Minute)
	if !schedule.Next(currentMinute.Add(-time.Minute)).Equal(currentMinute) {
		return nil, nil
	}
	runAt := currentMinute.UTC()
	deadline := runAt.Add(time.Minute)
	payload, _ := json.Marshal(map[string]any{
		"scheduled_at": runAt,
		"action":       name,
	})
	return []jobs.NewJob{{
		AccountID:     s.accountID,
		Action:        name,
		PayloadJSON:   string(payload),
		RuleVersionID: version.ID,
		DedupeKey:     fmt.Sprintf("cron:%s:%s:%s", version.ID, name, runAt.Format(time.RFC3339)),
		RunAt:         runAt,
		DeadlineAt:    &deadline,
		MaxAttempts:   max(1, action.Retry.MaxAttempts),
	}}, nil
}
