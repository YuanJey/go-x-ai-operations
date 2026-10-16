package agent

import (
	"context"
	"encoding/json"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/llm"
)

type Gateway interface {
	Generate(ctx context.Context, agentRunID string, request llm.Request) (llm.Response, error)
}

type Definition struct {
	Type                string
	PromptVersion       string
	SystemPrompt        string
	InputSchemaVersion  string
	OutputSchemaVersion string
	OutputSchema        json.RawMessage
	Required            llm.Capabilities
	MaxOutputTokens     int
	Temperature         float64
}

type RunRequest struct {
	WorkflowID  string
	Definition  Definition
	Input       any
	AllowRemote bool
}

type RunResult struct {
	RunID    string          `json:"run_id"`
	Output   json.RawMessage `json:"output"`
	Provider string          `json:"provider"`
	Model    string          `json:"model"`
	Repaired bool            `json:"repaired"`
}

type Run struct {
	ID                  string
	WorkflowID          string
	AgentType           string
	PromptVersion       string
	InputSchemaVersion  string
	OutputSchemaVersion string
	ProviderProfile     string
	ProviderProtocol    string
	Model               string
	InputJSON           string
	InputHash           string
	OutputJSON          *string
	OutputHash          *string
	Status              string
	Error               *string
	StartedAt           time.Time
	FinishedAt          *time.Time
}
