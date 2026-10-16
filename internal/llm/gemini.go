package llm

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

type GeminiAdapter struct {
	client HTTPClient
}

func NewGeminiAdapter(client HTTPClient) *GeminiAdapter {
	if client == nil {
		client = http.DefaultClient
	}
	return &GeminiAdapter{client: client}
}

func (a *GeminiAdapter) Protocol() string {
	return ProtocolGemini
}

func (a *GeminiAdapter) Generate(
	ctx context.Context,
	profile Profile,
	request ProviderRequest,
) (ProviderResponse, error) {
	var systemParts []map[string]string
	var contents []map[string]any
	for _, message := range request.Messages {
		if message.Role == "system" {
			systemParts = append(systemParts, map[string]string{"text": message.Content})
			continue
		}
		role := "user"
		if message.Role == "assistant" {
			role = "model"
		}
		contents = append(contents, map[string]any{
			"role":  role,
			"parts": []map[string]string{{"text": message.Content}},
		})
	}
	generation := map[string]any{
		"temperature":      request.Temperature,
		"responseMimeType": "application/json",
	}
	if request.MaxOutputTokens > 0 {
		generation["maxOutputTokens"] = request.MaxOutputTokens
	}
	if len(request.ResponseSchema) > 0 && profile.Capabilities.JSONSchema {
		var schema any
		if err := json.Unmarshal(request.ResponseSchema, &schema); err != nil {
			return ProviderResponse{}, fmt.Errorf("decode response schema: %w", err)
		}
		generation["responseSchema"] = schema
	}
	input := map[string]any{
		"contents":         contents,
		"generationConfig": generation,
	}
	if len(systemParts) > 0 {
		input["systemInstruction"] = map[string]any{"parts": systemParts}
	}
	var output struct {
		Candidates []struct {
			Content struct {
				Parts []struct {
					Text string `json:"text"`
				} `json:"parts"`
			} `json:"content"`
			FinishReason string `json:"finishReason"`
		} `json:"candidates"`
		UsageMetadata struct {
			PromptTokenCount     int `json:"promptTokenCount"`
			CandidatesTokenCount int `json:"candidatesTokenCount"`
		} `json:"usageMetadata"`
	}
	headers := map[string]string{"x-goog-api-key": request.APIKey}
	if err := postJSON(
		ctx, a.client, geminiEndpoint(profile.BaseURL, profile.Model)+":generateContent",
		headers, input, &output,
	); err != nil {
		return ProviderResponse{}, err
	}
	if len(output.Candidates) == 0 {
		return ProviderResponse{}, &ProviderError{Message: "Gemini returned no candidates"}
	}
	var text string
	for _, part := range output.Candidates[0].Content.Parts {
		text += part.Text
	}
	if text == "" {
		return ProviderResponse{}, &ProviderError{Message: "Gemini returned empty content"}
	}
	return ProviderResponse{
		Content:      []byte(text),
		FinishReason: output.Candidates[0].FinishReason,
		Usage: Usage{
			PromptTokens:     output.UsageMetadata.PromptTokenCount,
			CompletionTokens: output.UsageMetadata.CandidatesTokenCount,
			Currency:         "USD",
		},
	}, nil
}

func (a *GeminiAdapter) Validate(
	ctx context.Context,
	profile Profile,
	apiKey string,
) error {
	return get(ctx, a.client, geminiEndpoint(profile.BaseURL, profile.Model), map[string]string{
		"x-goog-api-key": apiKey,
	})
}

func geminiEndpoint(baseURL, model string) string {
	base := strings.TrimRight(baseURL, "/")
	if !strings.HasSuffix(base, "/v1") && !strings.HasSuffix(base, "/v1beta") {
		base += "/v1beta"
	}
	return base + "/models/" + model
}
