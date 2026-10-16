package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

type secretMap map[string]string

func (s secretMap) Resolve(_ context.Context, reference string) (string, error) {
	return s[reference], nil
}

type usageSink struct {
	mu      sync.Mutex
	records []UsageRecord
}

func (s *usageSink) RecordLLMUsage(_ context.Context, record UsageRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.records = append(s.records, record)
	return nil
}

func TestGatewayFallsBackAndRedactsRemoteInput(t *testing.T) {
	primary := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		http.Error(response, `{"error":{"message":"temporarily unavailable"}}`, http.StatusServiceUnavailable)
	}))
	defer primary.Close()

	var received string
	backup := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(request.Body).Decode(&body); err != nil {
			t.Fatalf("decode provider request: %v", err)
		}
		encoded, _ := json.Marshal(body)
		received = string(encoded)
		response.Header().Set("Content-Type", "application/json")
		_, _ = response.Write([]byte(`{
			"choices":[{"message":{"content":"{\"text\":\"ok\"}"},"finish_reason":"stop"}],
			"usage":{"prompt_tokens":12,"completion_tokens":4}
		}`))
	}))
	defer backup.Close()

	sink := &usageSink{}
	adapter := NewOpenAICompatibleAdapter(http.DefaultClient)
	gateway, err := NewGateway(GatewayConfig{
		Profiles: []Profile{
			{
				Name: "primary", Protocol: ProtocolOpenAICompatible,
				BaseURL: primary.URL, Model: "primary-model", Location: "local",
				Capabilities: Capabilities{JSONObject: true}, Enabled: true,
			},
			{
				Name: "backup", Protocol: ProtocolOpenAICompatible,
				BaseURL: backup.URL, APIKeyRef: "keychain://test/backup",
				Model: "backup-model", Location: "remote",
				Capabilities: Capabilities{JSONObject: true}, Enabled: true,
			},
		},
		DefaultRoute:   []string{"primary", "backup"},
		RequestTimeout: time.Second,
		Secrets:        secretMap{"keychain://test/backup": "secret"},
		Usage:          sink,
		Adapters:       []Adapter{adapter},
	})
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	result, err := gateway.Generate(context.Background(), "agent-run", Request{
		Agent: "post_writer",
		Messages: []Message{{
			Role:    "user",
			Content: "read /Users/example/private and keychain://xops/oauth/token",
		}},
		Required:    Capabilities{JSONObject: true},
		AllowRemote: true,
	})
	if err != nil {
		t.Fatalf("generate: %v", err)
	}
	if result.Profile != "backup" || string(result.Content) != `{"text":"ok"}` {
		t.Fatalf("unexpected response: %+v", result)
	}
	if strings.Contains(received, "/Users/example") || strings.Contains(received, "keychain://") {
		t.Fatalf("remote request was not redacted: %s", received)
	}
	if len(sink.records) != 1 || sink.records[0].Usage.PromptTokens != 12 {
		t.Fatalf("usage records = %+v", sink.records)
	}
}

func TestGatewayRejectsRemoteWhenDisabled(t *testing.T) {
	gateway, err := NewGateway(GatewayConfig{
		Profiles: []Profile{{
			Name: "remote", Protocol: ProtocolOpenAICompatible,
			BaseURL: "https://example.invalid", Model: "model", Location: "remote",
			Capabilities: Capabilities{JSONObject: true}, Enabled: true,
		}},
		DefaultRoute: []string{"remote"},
		Adapters:     []Adapter{NewOpenAICompatibleAdapter(http.DefaultClient)},
	})
	if err != nil {
		t.Fatalf("create gateway: %v", err)
	}
	_, err = gateway.Generate(context.Background(), "", Request{
		Agent: "policy", Required: Capabilities{JSONObject: true}, AllowRemote: false,
	})
	if err == nil || !strings.Contains(err.Error(), "remote use is disabled") {
		t.Fatalf("error = %v, want remote privacy rejection", err)
	}
}
