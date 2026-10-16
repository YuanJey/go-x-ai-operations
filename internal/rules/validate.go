package rules

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type ValidationError struct {
	Problems []string `json:"problems"`
}

func (e *ValidationError) Error() string {
	return strings.Join(e.Problems, "; ")
}

type Validator struct {
	registry Registry
}

func NewValidator(registry Registry) *Validator {
	return &Validator{registry: registry}
}

func (v *Validator) ParseAndValidate(raw []byte) (ValidationResult, error) {
	var document Document
	decoder := yaml.NewDecoder(bytes.NewReader(raw))
	decoder.KnownFields(true)
	if err := decoder.Decode(&document); err != nil {
		return ValidationResult{}, &ValidationError{Problems: []string{"parse YAML: " + err.Error()}}
	}

	problems := v.validate(document)
	if len(problems) > 0 {
		sort.Strings(problems)
		return ValidationResult{}, &ValidationError{Problems: problems}
	}

	normalized, err := json.Marshal(document)
	if err != nil {
		return ValidationResult{}, fmt.Errorf("normalize rule document: %w", err)
	}
	sum := sha256.Sum256(normalized)
	capacities := make(map[string]int)
	for name, action := range document.Actions {
		if !action.Enabled || action.Trigger != TriggerQuota || action.Quota == nil || action.Quota.PerDay == nil {
			continue
		}
		capacity, err := MaxDailyCapacity(document, name, document.Config.EffectiveAt)
		if err != nil {
			return ValidationResult{}, err
		}
		capacities[name] = capacity
	}

	return ValidationResult{
		Document:       document,
		NormalizedJSON: string(normalized),
		ConfigHash:     hex.EncodeToString(sum[:]),
		Slots:          capacities,
	}, nil
}

func (v *Validator) validate(document Document) []string {
	var problems []string
	if document.Config.Version < 1 {
		problems = append(problems, "config.version must be at least 1")
	}
	if document.Config.EffectiveAt.IsZero() {
		problems = append(problems, "config.effective_at is required")
	}
	if document.Account.Handle == "" || document.Account.Language == "" || document.Account.Timezone == "" {
		problems = append(problems, "account handle, language, and timezone are required")
	}
	if _, err := time.LoadLocation(document.Account.Timezone); err != nil {
		problems = append(problems, "account.timezone is invalid")
	}
	if document.ContentPolicy.ApprovalMode != "automatic_policy_gate" {
		problems = append(problems, "content_policy.approval_mode must be automatic_policy_gate")
	}
	if len(document.ContentPolicy.Pillars) == 0 {
		problems = append(problems, "content_policy.pillars must not be empty")
	} else {
		var weight float64
		for _, pillar := range document.ContentPolicy.Pillars {
			if pillar.Name == "" || pillar.InitialWeight < 0 {
				problems = append(problems, "content pillars require a name and non-negative weight")
			}
			weight += pillar.InitialWeight
		}
		if math.Abs(weight-1) > 0.0001 {
			problems = append(problems, "content pillar weights must sum to 1")
		}
	}
	problems = append(problems, validateHardConstraints(document)...)

	scopes := make(map[string]bool, len(document.Automation.OAuthScopes))
	for _, scope := range document.Automation.OAuthScopes {
		scopes[scope] = true
	}
	if len(document.Actions) == 0 {
		problems = append(problems, "actions must not be empty")
	}
	for name, action := range document.Actions {
		definition, ok := v.registry.Get(name)
		if !ok {
			problems = append(problems, "unsupported action: "+name)
			continue
		}
		if !contains(definition.TriggerKinds, action.Trigger) {
			problems = append(problems, fmt.Sprintf("action %s does not support trigger %s", name, action.Trigger))
		}
		if action.Enabled {
			for _, scope := range definition.RequiredScopes {
				if !scopes[scope] {
					problems = append(problems, fmt.Sprintf("action %s requires OAuth scope %s", name, scope))
				}
			}
		}
		problems = append(problems, validateAction(document, name, action)...)
	}
	return problems
}

func validateHardConstraints(document Document) []string {
	var problems []string
	if document.Automation.Mode != "full_auto_rule_driven" {
		problems = append(problems, "automation.mode must be full_auto_rule_driven")
	}
	if document.Automation.XWriteChannel != "official_api" {
		problems = append(problems, "automation.x_write_channel must be official_api")
	}
	if document.Engagement.ReplyMode != "summoned_only" ||
		!document.Engagement.RequireOriginalAuthorSummon ||
		document.Engagement.AllowUnsolicitedReplies ||
		document.Engagement.AllowDirectMessages ||
		document.Engagement.AllowQuotePosts ||
		document.Engagement.AllowMentionsOutsideThread {
		problems = append(problems, "engagement settings violate summoned-only reply policy")
	}
	return problems
}

func validateAction(document Document, name string, action Action) []string {
	var problems []string
	if !action.Enabled {
		return problems
	}
	if action.MaxConcurrency < 0 {
		problems = append(problems, "action "+name+" max_concurrency cannot be negative")
	}
	if action.Retry.MaxAttempts < 0 {
		problems = append(problems, "action "+name+" retry.max_attempts cannot be negative")
	}
	switch action.Trigger {
	case TriggerInterval:
		if action.Every.Value() <= 0 {
			problems = append(problems, "action "+name+" interval must be positive")
		}
	case TriggerQuota:
		if action.Quota == nil || action.Quota.PerDay == nil || *action.Quota.PerDay < 1 {
			problems = append(problems, "action "+name+" requires a positive daily quota")
			break
		}
		if len(action.Schedule.Windows) == 0 {
			problems = append(problems, "action "+name+" requires at least one schedule window")
			break
		}
		if _, err := MaxDailyCapacity(document, name, document.Config.EffectiveAt); err != nil {
			problems = append(problems, err.Error())
		} else if capacity, _ := MaxDailyCapacity(document, name, document.Config.EffectiveAt); *action.Quota.PerDay > capacity {
			problems = append(problems,
				fmt.Sprintf("action %s quota %d exceeds daily capacity %d", name, *action.Quota.PerDay, capacity))
		}
	case TriggerCron:
		if len(strings.Fields(action.Expression)) != 5 {
			problems = append(problems, "action "+name+" cron expression must have five fields")
		}
	case TriggerDelayed:
		if len(action.AfterPublish) == 0 {
			problems = append(problems, "action "+name+" requires after_publish delays")
		}
		for _, delay := range action.AfterPublish {
			if delay.Value() <= 0 {
				problems = append(problems, "action "+name+" after_publish delays must be positive")
			}
		}
	default:
		problems = append(problems, "action "+name+" has an invalid trigger")
	}
	if action.Queue.MaxPending < 0 || action.Queue.MaxAge.Value() < 0 {
		problems = append(problems, "action "+name+" queue limits cannot be negative")
	}
	if action.Queue.MaxPending > 0 &&
		action.Queue.Overflow != "drop_oldest" &&
		action.Queue.Overflow != "drop_newest" {
		problems = append(problems, "action "+name+" queue.overflow must be drop_oldest or drop_newest")
	}
	return problems
}

func contains(items []string, target string) bool {
	for _, item := range items {
		if item == target {
			return true
		}
	}
	return false
}
