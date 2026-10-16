package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type OpenAICompatibleAdapter struct {
	client HTTPClient
}

func NewOpenAICompatibleAdapter(client HTTPClient) *OpenAICompatibleAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &OpenAICompatibleAdapter{client: client}
}

func (a *OpenAICompatibleAdapter) Protocol() string {
	return ProtocolOpenAICompatible
}

func (a *OpenAICompatibleAdapter) Generate(
	ctx context.Context,
	profile Profile,
	request ProviderRequest,
) (ProviderResponse, error) {
	input := map[string]any{
		"model":       profile.Model,
		"messages":    request.Messages,
		"temperature": request.Temperature,
	}
	if request.MaxOutputTokens > 0 {
		input["max_tokens"] = request.MaxOutputTokens
	}
	if len(request.ResponseSchema) > 0 && profile.Capabilities.JSONSchema {
		var schema any
		if err := json.Unmarshal(request.ResponseSchema, &schema); err != nil {
			return ProviderResponse{}, fmt.Errorf("decode response schema: %w", err)
		}
		input["response_format"] = map[string]any{
			"type": "json_schema",
			"json_schema": map[string]any{
				"name":   "agent_response",
				"strict": true,
				"schema": schema,
			},
		}
	} else if profile.Capabilities.JSONObject {
		input["response_format"] = map[string]any{"type": "json_object"}
	}

	var output struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
		} `json:"usage"`
	}
	headers := map[string]string{}
	if request.APIKey != "" {
		headers["Authorization"] = "Bearer " + request.APIKey
	}
	if err := postJSON(
		ctx,
		a.client,
		strings.TrimRight(profile.BaseURL, "/")+"/chat/completions",
		headers,
		input,
		&output,
	); err != nil {
		return ProviderResponse{}, err
	}
	if len(output.Choices) == 0 {
		return ProviderResponse{}, &ProviderError{Message: "provider returned no choices"}
	}
	return ProviderResponse{
		Content:      []byte(output.Choices[0].Message.Content),
		FinishReason: output.Choices[0].FinishReason,
		Usage: Usage{
			PromptTokens:     output.Usage.PromptTokens,
			CompletionTokens: output.Usage.CompletionTokens,
			Currency:         "USD",
		},
	}, nil
}

func (a *OpenAICompatibleAdapter) Validate(
	ctx context.Context,
	profile Profile,
	apiKey string,
) error {
	headers := map[string]string{}
	if apiKey != "" {
		headers["Authorization"] = "Bearer " + apiKey
	}
	return get(ctx, a.client, strings.TrimRight(profile.BaseURL, "/")+"/models", headers)
}
