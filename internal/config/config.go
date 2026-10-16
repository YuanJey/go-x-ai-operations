package config

import (
	"bytes"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Duration time.Duration

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
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

type Config struct {
	Server   ServerConfig   `yaml:"server"`
	Storage  StorageConfig  `yaml:"storage"`
	Runtime  RuntimeConfig  `yaml:"runtime"`
	Account  AccountConfig  `yaml:"account"`
	Sources  []SourceConfig `yaml:"sources"`
	Rules    RulesConfig    `yaml:"rules"`
	LLM      LLMConfig      `yaml:"llm"`
	X        XConfig        `yaml:"x"`
	Metering MeteringConfig `yaml:"metering"`
	Backup   BackupConfig   `yaml:"backup"`
}

type ServerConfig struct {
	Listen          string   `yaml:"listen"`
	ShutdownTimeout Duration `yaml:"shutdown_timeout"`
}

type StorageConfig struct {
	DatabasePath string `yaml:"database_path"`
}

type RuntimeConfig struct {
	DryRun        bool     `yaml:"dry_run"`
	WorkerID      string   `yaml:"worker_id"`
	PollInterval  Duration `yaml:"poll_interval"`
	LeaseDuration Duration `yaml:"lease_duration"`
	BatchSize     int      `yaml:"batch_size"`
}

type AccountConfig struct {
	ID       string `yaml:"id"`
	Handle   string `yaml:"handle"`
	Timezone string `yaml:"timezone"`
}

type SourceConfig struct {
	Name    string `yaml:"name"`
	FeedURL string `yaml:"feed_url"`
	Enabled bool   `yaml:"enabled"`
}

type RulesConfig struct {
	BootstrapFile string `yaml:"bootstrap_file"`
}

type LLMConfig struct {
	DefaultProfile string                    `yaml:"default_profile"`
	RequestTimeout Duration                  `yaml:"request_timeout"`
	Profiles       map[string]ProviderConfig `yaml:"profiles"`
	Routing        RoutingConfig             `yaml:"routing"`
}

type ProviderConfig struct {
	Protocol     string   `yaml:"protocol"`
	BaseURL      string   `yaml:"base_url"`
	APIKeyRef    string   `yaml:"api_key_ref"`
	Model        string   `yaml:"model"`
	Location     string   `yaml:"location"`
	Capabilities []string `yaml:"capabilities"`
}

type RoutingConfig struct {
	Default []string            `yaml:"default"`
	Agents  map[string][]string `yaml:"agents"`
}

type XConfig struct {
	BaseURL           string `yaml:"base_url"`
	OAuthAuthorizeURL string `yaml:"oauth_authorize_url"`
	OAuthTokenURL     string `yaml:"oauth_token_url"`
	OAuthRedirectURL  string `yaml:"oauth_redirect_url"`
	ClientID          string `yaml:"client_id"`
	CredentialRef     string `yaml:"credential_ref"`
}

type MeteringConfig struct {
	Currency  string             `yaml:"currency"`
	XUnitCost map[string]float64 `yaml:"x_unit_cost"`
}

type BackupConfig struct {
	Enabled   bool     `yaml:"enabled"`
	Directory string   `yaml:"directory"`
	Interval  Duration `yaml:"interval"`
	Retain    int      `yaml:"retain"`
}

func Load(path string) (Config, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}

	var cfg Config
	decoder := yaml.NewDecoder(bytes.NewReader(body))
	decoder.KnownFields(true)
	if err := decoder.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config %s: %w", path, err)
	}
	applyEnvironment(&cfg)
	if err := cfg.Validate(); err != nil {
		return Config{}, fmt.Errorf("validate config: %w", err)
	}
	return cfg, nil
}

func (c Config) Validate() error {
	var problems []string
	if c.Server.Listen == "" {
		problems = append(problems, "server.listen is required")
	}
	if c.Server.ShutdownTimeout.Value() <= 0 {
		problems = append(problems, "server.shutdown_timeout must be positive")
	}
	if c.Storage.DatabasePath == "" {
		problems = append(problems, "storage.database_path is required")
	}
	if c.Runtime.WorkerID == "" {
		problems = append(problems, "runtime.worker_id is required")
	}
	if c.Runtime.PollInterval.Value() <= 0 || c.Runtime.LeaseDuration.Value() <= 0 {
		problems = append(problems, "runtime poll and lease durations must be positive")
	}
	if c.Runtime.BatchSize < 1 {
		problems = append(problems, "runtime.batch_size must be positive")
	}
	if c.Account.ID == "" || c.Account.Handle == "" || c.Account.Timezone == "" {
		problems = append(problems, "account id, handle, and timezone are required")
	} else if _, err := time.LoadLocation(c.Account.Timezone); err != nil {
		problems = append(problems, "account.timezone is invalid")
	}
	if c.Rules.BootstrapFile == "" {
		problems = append(problems, "rules.bootstrap_file is required")
	}
	if len(c.LLM.Profiles) == 0 {
		problems = append(problems, "at least one llm profile is required")
	}
	if _, ok := c.LLM.Profiles[c.LLM.DefaultProfile]; !ok {
		problems = append(problems, "llm.default_profile does not exist")
	}
	for name, profile := range c.LLM.Profiles {
		if profile.Protocol == "" || profile.BaseURL == "" || profile.Model == "" {
			problems = append(problems, "llm profile "+name+" is incomplete")
		}
		if profile.Location != "local" && profile.Location != "remote" {
			problems = append(problems, "llm profile "+name+" location must be local or remote")
		}
	}
	for index, source := range c.Sources {
		if source.Enabled && (source.Name == "" || source.FeedURL == "") {
			problems = append(problems, fmt.Sprintf("sources[%d] is incomplete", index))
		}
	}
	if c.Metering.Currency == "" {
		problems = append(problems, "metering.currency is required")
	}
	for resource, cost := range c.Metering.XUnitCost {
		if cost < 0 {
			problems = append(problems, "metering cost for "+resource+" cannot be negative")
		}
	}
	if c.Backup.Enabled {
		if c.Backup.Directory == "" || c.Backup.Interval.Value() <= 0 || c.Backup.Retain < 1 {
			problems = append(problems, "enabled backup requires directory, positive interval, and retention")
		}
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s", strings.Join(problems, "; "))
	}
	return nil
}

func applyEnvironment(cfg *Config) {
	if value := os.Getenv("XOPS_LISTEN"); value != "" {
		cfg.Server.Listen = value
	}
	if value := os.Getenv("XOPS_DATABASE_PATH"); value != "" {
		cfg.Storage.DatabasePath = value
	}
	if value := os.Getenv("XOPS_DRY_RUN"); value != "" {
		if parsed, err := strconv.ParseBool(value); err == nil {
			cfg.Runtime.DryRun = parsed
		}
	}
}
