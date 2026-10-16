package llm

import (
	"context"
	"net/http"
	"strings"
)

type AnthropicAdapter struct {
	client HTTPClient
}

func NewAnthropicAdapter(client HTTPClient) *AnthropicAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &AnthropicAdapter{client: client}
}

func (a *AnthropicAdapter) Protocol() string {
	return ProtocolAnthropic
}

func (a *AnthropicAdapter) Generate(
	ctx context.Context,
	profile Profile,
	request ProviderRequest,
) (ProviderResponse, error) {
	var systemParts []string
	var messages []Message
	for _, message := range request.Messages {
		if message.Role == "system" {
			systemParts = append(systemParts, message.Content)
			continue
		}
		messages = append(messages, message)
	}
	if len(request.ResponseSchema) > 0 {
		systemParts = append(systemParts, "Return only one JSON object that conforms to the supplied response contract.")
	}
	maxTokens := request.MaxOutputTokens
	if maxTokens <= 0 {
		maxTokens = 1024
	}
	input := map[string]any{
		"model":       profile.Model,
		"max_tokens":  maxTokens,
		"temperature": request.Temperature,
		"system":      strings.Join(systemParts, "\n\n"),
		"messages":    messages,
	}
	var output struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		StopReason string `json:"stop_reason"`
		Usage      struct {
			InputTokens  int `json:"input_tokens"`
			OutputTokens int `json:"output_tokens"`
		} `json:"usage"`
	}
	headers := map[string]string{
		"x-api-key":         request.APIKey,
		"anthropic-version": "2023-06-01",
	}
	if err := postJSON(
		ctx, a.client, versionedEndpoint(profile.BaseURL, "messages"),
		headers, input, &output,
	); err != nil {
		return ProviderResponse{}, err
	}
	var text string
	for _, block := range output.Content {
		if block.Type == "text" {
			text += block.Text
		}
	}
	if text == "" {
		return ProviderResponse{}, &ProviderError{Message: "Anthropic returned empty content"}
	}
	return ProviderResponse{
		Content:      []byte(text),
		FinishReason: output.StopReason,
		Usage: Usage{
			PromptTokens:     output.Usage.InputTokens,
			CompletionTokens: output.Usage.OutputTokens,
			Currency:         "USD",
		},
	}, nil
}

func (a *AnthropicAdapter) Validate(
	ctx context.Context,
	profile Profile,
	apiKey string,
) error {
	return get(ctx, a.client, versionedEndpoint(profile.BaseURL, "models"), map[string]string{
		"x-api-key":         apiKey,
		"anthropic-version": "2023-06-01",
	})
}

func versionedEndpoint(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/v1") {
		return base + "/" + path
	}
	return base + "/v1/" + path
}
