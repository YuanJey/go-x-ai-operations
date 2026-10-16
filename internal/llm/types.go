package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"time"
)

const (
	ProtocolOpenAICompatible = "openai_compatible"
	ProtocolOllamaNative     = "ollama_native"
	ProtocolAnthropic        = "anthropic"
	ProtocolGemini           = "gemini"
)

type Capabilities struct {
	JSONSchema       bool `json:"json_schema"`
	JSONObject       bool `json:"json_object"`
	ToolCalling      bool `json:"tool_calling"`
	Vision           bool `json:"vision"`
	MaxContextTokens int  `json:"max_context_tokens"`
	MaxOutputTokens  int  `json:"max_output_tokens"`
}

func (c Capabilities) Satisfies(required Capabilities) bool {
	if required.JSONSchema && !c.JSONSchema {
		return false
	}
	if required.JSONObject && !c.JSONObject && !c.JSONSchema {
		return false
	}
	if required.ToolCalling && !c.ToolCalling {
		return false
	}
	if required.Vision && !c.Vision {
		return false
	}
	if required.MaxContextTokens > 0 &&
		c.MaxContextTokens > 0 &&
		c.MaxContextTokens < required.MaxContextTokens {
		return false
	}
	if required.MaxOutputTokens > 0 &&
		c.MaxOutputTokens > 0 &&
		c.MaxOutputTokens < required.MaxOutputTokens {
		return false
	}
	return true
}

type Profile struct {
	Name         string       `json:"name"`
	Protocol     string       `json:"protocol"`
	BaseURL      string       `json:"base_url"`
	APIKeyRef    string       `json:"api_key_ref,omitempty"`
	Model        string       `json:"model"`
	Location     string       `json:"location"`
	Capabilities Capabilities `json:"capabilities"`
	Enabled      bool         `json:"enabled"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type Request struct {
	Agent           string          `json:"agent"`
	Messages        []Message       `json:"messages"`
	ResponseSchema  json.RawMessage `json:"response_schema,omitempty"`
	Required        Capabilities    `json:"required"`
	MaxOutputTokens int             `json:"max_output_tokens,omitempty"`
	Temperature     float64         `json:"temperature"`
	AllowRemote     bool            `json:"allow_remote"`
}

type Response struct {
	Profile      string          `json:"profile"`
	Protocol     string          `json:"protocol"`
	Model        string          `json:"model"`
	Content      json.RawMessage `json:"content"`
	FinishReason string          `json:"finish_reason"`
	Usage        Usage           `json:"usage"`
	Latency      time.Duration   `json:"latency"`
}

type Usage struct {
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	EstimatedCost    float64 `json:"estimated_cost"`
	Currency         string  `json:"currency"`
}

type ProviderRequest struct {
	Messages        []Message
	ResponseSchema  json.RawMessage
	MaxOutputTokens int
	Temperature     float64
	APIKey          string
}

type ProviderResponse struct {
	Content      []byte
	FinishReason string
	Usage        Usage
}

type Adapter interface {
	Protocol() string
	Generate(ctx context.Context, profile Profile, request ProviderRequest) (ProviderResponse, error)
	Validate(ctx context.Context, profile Profile, apiKey string) error
}

type SecretResolver interface {
	Resolve(ctx context.Context, reference string) (string, error)
}

type UsageRecord struct {
	ID              string
	ProviderProfile string
	AgentRunID      string
	Model           string
	Usage           Usage
	Latency         time.Duration
	CreatedAt       time.Time
}

type UsageRecorder interface {
	RecordLLMUsage(ctx context.Context, record UsageRecord) error
}

type ProviderError struct {
	StatusCode int
	Code       string
	Message    string
	Retryable  bool
}

func (e *ProviderError) Error() string {
	if e.StatusCode > 0 {
		return fmt.Sprintf("provider returned HTTP %d: %s", e.StatusCode, e.Message)
	}
	return e.Message
}
