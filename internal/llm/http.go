package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
)

const maxProviderResponseSize = 4 << 20

type HTTPClient interface {
	Do(request *http.Request) (*http.Response, error)
}

func postJSON(
	ctx context.Context,
	client HTTPClient,
	url string,
	headers map[string]string,
	input any,
	output any,
) error {
	body, err := json.Marshal(input)
	if err != nil {
		return fmt.Errorf("encode provider request: %w", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("create provider request: %w", err)
	}
	request.Header.Set("Content-Type", "application/json")
	for name, value := range headers {
		if value != "" {
			request.Header.Set(name, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return &ProviderError{Message: err.Error(), Retryable: true}
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseSize))
	if err != nil {
		return &ProviderError{Message: "read provider response: " + err.Error(), Retryable: true}
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return classifyHTTPError(response.StatusCode, responseBody)
	}
	if err := json.Unmarshal(responseBody, output); err != nil {
		return &ProviderError{Message: "decode provider response: " + err.Error(), Retryable: false}
	}
	return nil
}

func get(
	ctx context.Context,
	client HTTPClient,
	url string,
	headers map[string]string,
) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	for name, value := range headers {
		if value != "" {
			request.Header.Set(name, value)
		}
	}
	response, err := client.Do(request)
	if err != nil {
		return &ProviderError{Message: err.Error(), Retryable: true}
	}
	defer response.Body.Close()
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		body, _ := io.ReadAll(io.LimitReader(response.Body, maxProviderResponseSize))
		return classifyHTTPError(response.StatusCode, body)
	}
	return nil
}

func classifyHTTPError(status int, body []byte) error {
	message := http.StatusText(status)
	var envelope struct {
		Error any `json:"error"`
	}
	if json.Unmarshal(body, &envelope) == nil && envelope.Error != nil {
		switch value := envelope.Error.(type) {
		case string:
			message = value
		case map[string]any:
			if text, ok := value["message"].(string); ok {
				message = text
			}
		}
	} else if len(body) > 0 {
		message = string(body)
	}
	return &ProviderError{
		StatusCode: status,
		Message:    message,
		Retryable:  status == http.StatusRequestTimeout || status == http.StatusTooManyRequests || status >= 500,
	}
}
