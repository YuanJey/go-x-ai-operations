package rules

import (
	"fmt"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	if node.Value == "" {
		return nil
	}
	value, err := time.ParseDuration(node.Value)
	if err != nil {
		return fmt.Errorf("invalid duration %q: %w", node.Value, err)
	}
	*d = Duration(value)
	return nil
}

func (d Duration) Value() time.Duration {
	return time.Duration(d)
}

type Document struct {
	Config        MetaConfig        `yaml:"config" json:"config"`
	Account       AccountConfig     `yaml:"account" json:"account"`
	ContentPolicy ContentPolicy     `yaml:"content_policy" json:"content_policy"`
	Engagement    EngagementConfig  `yaml:"engagement" json:"engagement"`
	Automation    AutomationConfig  `yaml:"automation" json:"automation"`
	Actions       map[string]Action `yaml:"actions" json:"actions"`
}

type MetaConfig struct {
	Version     int       `yaml:"version" json:"version"`
	EffectiveAt time.Time `yaml:"effective_at" json:"effective_at"`
}

type AccountConfig struct {
	Handle          string   `yaml:"handle" json:"handle"`
	Language        string   `yaml:"language" json:"language"`
	Timezone        string   `yaml:"timezone" json:"timezone"`
	Audience        string   `yaml:"audience" json:"audience"`
	Promise         string   `yaml:"promise" json:"promise"`
	Voice           []string `yaml:"voice" json:"voice"`
	ForbiddenTopics []string `yaml:"forbidden_topics" json:"forbidden_topics"`
}

type ContentPolicy struct {
	AllowURLs    bool     `yaml:"allow_urls" json:"allow_urls"`
	AllowMedia   bool     `yaml:"allow_media" json:"allow_media"`
	AllowThreads bool     `yaml:"allow_threads" json:"allow_threads"`
	ApprovalMode string   `yaml:"approval_mode" json:"approval_mode"`
	Pillars      []Pillar `yaml:"pillars" json:"pillars"`
}

type Pillar struct {
	Name          string  `yaml:"name" json:"name"`
	InitialWeight float64 `yaml:"initial_weight" json:"initial_weight"`
}

type EngagementConfig struct {
	Enabled                     bool   `yaml:"enabled" json:"enabled"`
	ReplyMode                   string `yaml:"reply_mode" json:"reply_mode"`
	RequireOriginalAuthorSummon bool   `yaml:"require_original_author_summon" json:"require_original_author_summon"`
	AllowUnsolicitedReplies     bool   `yaml:"allow_unsolicited_replies" json:"allow_unsolicited_replies"`
	AllowDirectMessages         bool   `yaml:"allow_direct_messages" json:"allow_direct_messages"`
	AllowQuotePosts             bool   `yaml:"allow_quote_posts" json:"allow_quote_posts"`
	AllowMentionsOutsideThread  bool   `yaml:"allow_mentions_outside_thread" json:"allow_mentions_outside_thread"`
}

type AutomationConfig struct {
	Mode          string   `yaml:"mode" json:"mode"`
	XWriteChannel string   `yaml:"x_write_channel" json:"x_write_channel"`
	OAuthScopes   []string `yaml:"oauth_scopes" json:"oauth_scopes"`
}

type Action struct {
	Enabled            bool       `yaml:"enabled" json:"enabled"`
	Trigger            string     `yaml:"trigger" json:"trigger"`
	Every              Duration   `yaml:"every" json:"every"`
	Expression         string     `yaml:"expression" json:"expression"`
	AfterPublish       []Duration `yaml:"after_publish" json:"after_publish"`
	Quota              *Quota     `yaml:"quota" json:"quota,omitempty"`
	Schedule           Schedule   `yaml:"schedule" json:"schedule"`
	Cooldown           Cooldown   `yaml:"cooldown" json:"cooldown"`
	Queue              Queue      `yaml:"queue" json:"queue"`
	Retry              Retry      `yaml:"retry" json:"retry"`
	MaxConcurrency     int        `yaml:"max_concurrency" json:"max_concurrency"`
	MaxResourcesPerDay *int       `yaml:"max_resources_per_day" json:"max_resources_per_day,omitempty"`
}

type Quota struct {
	PerMinute *int `yaml:"per_minute" json:"per_minute,omitempty"`
	PerHour   *int `yaml:"per_hour" json:"per_hour,omitempty"`
	PerDay    *int `yaml:"per_day" json:"per_day,omitempty"`
}

type Schedule struct {
	Timezone string   `yaml:"timezone" json:"timezone"`
	Days     []string `yaml:"days" json:"days"`
	Windows  []string `yaml:"windows" json:"windows"`
}

type Cooldown struct {
	MinInterval Duration `yaml:"min_interval" json:"min_interval"`
	SameTopic   Duration `yaml:"same_topic" json:"same_topic"`
	SameUser    Duration `yaml:"same_user" json:"same_user"`
}

type Queue struct {
	MaxPending int      `yaml:"max_pending" json:"max_pending"`
	MaxAge     Duration `yaml:"max_age" json:"max_age"`
	Overflow   string   `yaml:"overflow" json:"overflow"`
}

type Retry struct {
	MaxAttempts int    `yaml:"max_attempts" json:"max_attempts"`
	Backoff     string `yaml:"backoff" json:"backoff"`
}

type Version struct {
	ID             string     `json:"id"`
	AccountID      string     `json:"account_id"`
	Version        int        `json:"version"`
	State          string     `json:"state"`
	SourceYAML     string     `json:"-"`
	NormalizedJSON string     `json:"normalized_json"`
	ConfigHash     string     `json:"config_hash"`
	SchemaVersion  int        `json:"schema_version"`
	EffectiveAt    time.Time  `json:"effective_at"`
	ActivatedAt    *time.Time `json:"activated_at,omitempty"`
	CreatedAt      time.Time  `json:"created_at"`
	Document       Document   `json:"document"`
}

type ValidationResult struct {
	Document       Document       `json:"document"`
	NormalizedJSON string         `json:"normalized_json"`
	ConfigHash     string         `json:"config_hash"`
	Slots          map[string]int `json:"daily_slot_capacity"`
}
