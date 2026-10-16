package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type OllamaAdapter struct {
	client HTTPClient
}

func NewOllamaAdapter(client HTTPClient) *OllamaAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &OllamaAdapter{client: client}
}

func (a *OllamaAdapter) Protocol() string {
	return ProtocolOllamaNative
}

func (a *OllamaAdapter) Generate(
	ctx context.Context,
	profile Profile,
	request ProviderRequest,
) (ProviderResponse, error) {
	input := map[string]any{
		"model":    profile.Model,
		"messages": request.Messages,
		"stream":   false,
		"options": map[string]any{
			"temperature": request.Temperature,
		},
	}
	if request.MaxOutputTokens > 0 {
		input["options"].(map[string]any)["num_predict"] = request.MaxOutputTokens
	}
	if len(request.ResponseSchema) > 0 {
		var schema any
		if err := json.Unmarshal(request.ResponseSchema, &schema); err != nil {
			return ProviderResponse{}, fmt.Errorf("decode response schema: %w", err)
		}
		input["format"] = schema
	} else if profile.Capabilities.JSONObject {
		input["format"] = "json"
	}
	var output struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		DoneReason      string `json:"done_reason"`
		PromptEvalCount int    `json:"prompt_eval_count"`
		EvalCount       int    `json:"eval_count"`
	}
	if err := postJSON(ctx, a.client, ollamaEndpoint(profile.BaseURL, "chat"), nil, input, &output); err != nil {
		return ProviderResponse{}, err
	}
	if output.Message.Content == "" {
		return ProviderResponse{}, &ProviderError{Message: "Ollama returned empty content"}
	}
	return ProviderResponse{
		Content:      []byte(output.Message.Content),
		FinishReason: output.DoneReason,
		Usage: Usage{
			PromptTokens:     output.PromptEvalCount,
			CompletionTokens: output.EvalCount,
			Currency:         "USD",
		},
	}, nil
}

func (a *OllamaAdapter) Validate(
	ctx context.Context,
	profile Profile,
	_ string,
) error {
	return get(ctx, a.client, ollamaEndpoint(profile.BaseURL, "tags"), nil)
}

func ollamaEndpoint(baseURL, path string) string {
	base := strings.TrimRight(baseURL, "/")
	if strings.HasSuffix(base, "/api") {
		return base + "/" + path
	}
	return base + "/api/" + path
}
