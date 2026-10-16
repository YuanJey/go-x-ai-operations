package llm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/id"
)

type Gateway struct {
	profiles       map[string]Profile
	adapters       map[string]Adapter
	defaultRoute   []string
	agentRoutes    map[string][]string
	secrets        SecretResolver
	usage          UsageRecorder
	requestTimeout time.Duration
}

type GatewayConfig struct {
	Profiles       []Profile
	DefaultRoute   []string
	AgentRoutes    map[string][]string
	RequestTimeout time.Duration
	Secrets        SecretResolver
	Usage          UsageRecorder
	Adapters       []Adapter
}

type ProfileStatus struct {
	Profile Profile `json:"profile"`
	Healthy bool    `json:"healthy"`
	Error   string  `json:"error,omitempty"`
}

func NewGateway(config GatewayConfig) (*Gateway, error) {
	if config.RequestTimeout <= 0 {
		config.RequestTimeout = 90 * time.Second
	}
	gateway := &Gateway{
		profiles:       make(map[string]Profile),
		adapters:       make(map[string]Adapter),
		defaultRoute:   append([]string(nil), config.DefaultRoute...),
		agentRoutes:    config.AgentRoutes,
		secrets:        config.Secrets,
		usage:          config.Usage,
		requestTimeout: config.RequestTimeout,
	}
	for _, profile := range config.Profiles {
		if profile.Name == "" || profile.Protocol == "" || profile.Model == "" || profile.BaseURL == "" {
			return nil, fmt.Errorf("provider profile is incomplete")
		}
		if _, exists := gateway.profiles[profile.Name]; exists {
			return nil, fmt.Errorf("duplicate provider profile %s", profile.Name)
		}
		gateway.profiles[profile.Name] = profile
	}
	for _, adapter := range config.Adapters {
		gateway.adapters[adapter.Protocol()] = adapter
	}
	for _, profileName := range gateway.defaultRoute {
		if _, ok := gateway.profiles[profileName]; !ok {
			return nil, fmt.Errorf("default route references unknown profile %s", profileName)
		}
	}
	return gateway, nil
}

func (g *Gateway) Generate(
	ctx context.Context,
	agentRunID string,
	request Request,
) (Response, error) {
	route := g.agentRoutes[request.Agent]
	if len(route) == 0 {
		route = g.defaultRoute
	}
	if len(route) == 0 {
		return Response{}, fmt.Errorf("no provider route configured for agent %s", request.Agent)
	}

	var attempts []string
	for _, profileName := range route {
		profile, ok := g.profiles[profileName]
		if !ok || !profile.Enabled {
			attempts = append(attempts, profileName+": unavailable")
			continue
		}
		if profile.Location == "remote" && !request.AllowRemote {
			attempts = append(attempts, profileName+": remote use is disabled")
			continue
		}
		if !profile.Capabilities.Satisfies(request.Required) {
			attempts = append(attempts, profileName+": required capabilities are missing")
			continue
		}
		adapter, ok := g.adapters[profile.Protocol]
		if !ok {
			attempts = append(attempts, profileName+": adapter is not registered")
			continue
		}
		apiKey, err := g.resolveSecret(ctx, profile.APIKeyRef)
		if err != nil {
			attempts = append(attempts, profileName+": "+err.Error())
			continue
		}
		providerRequest := ProviderRequest{
			Messages:        cloneMessages(request.Messages),
			ResponseSchema:  request.ResponseSchema,
			MaxOutputTokens: request.MaxOutputTokens,
			Temperature:     request.Temperature,
			APIKey:          apiKey,
		}
		if profile.Location == "remote" {
			providerRequest.Messages = redactMessages(providerRequest.Messages)
		}

		callCtx, cancel := context.WithTimeout(ctx, g.requestTimeout)
		started := time.Now()
		providerResponse, err := adapter.Generate(callCtx, profile, providerRequest)
		cancel()
		latency := time.Since(started)
		if err != nil {
			attempts = append(attempts, profileName+": "+err.Error())
			var providerError *ProviderError
			if errors.As(err, &providerError) && !providerError.Retryable {
				return Response{}, err
			}
			continue
		}

		var compact bytes.Buffer
		if err := json.Compact(&compact, providerResponse.Content); err != nil {
			attempts = append(attempts, profileName+": invalid JSON response")
			continue
		}
		response := Response{
			Profile:      profile.Name,
			Protocol:     profile.Protocol,
			Model:        profile.Model,
			Content:      json.RawMessage(compact.Bytes()),
			FinishReason: providerResponse.FinishReason,
			Usage:        providerResponse.Usage,
			Latency:      latency,
		}
		g.recordUsage(ctx, agentRunID, profile, response)
		return response, nil
	}
	return Response{}, fmt.Errorf("all provider routes failed: %s", strings.Join(attempts, "; "))
}

func (g *Gateway) ValidateProfile(ctx context.Context, name string) error {
	profile, ok := g.profiles[name]
	if !ok {
		return fmt.Errorf("provider profile %s not found", name)
	}
	adapter, ok := g.adapters[profile.Protocol]
	if !ok {
		return fmt.Errorf("adapter %s is not registered", profile.Protocol)
	}
	apiKey, err := g.resolveSecret(ctx, profile.APIKeyRef)
	if err != nil {
		return err
	}
	callCtx, cancel := context.WithTimeout(ctx, g.requestTimeout)
	defer cancel()
	return adapter.Validate(callCtx, profile, apiKey)
}

func (g *Gateway) Profiles(ctx context.Context, probe bool) []ProfileStatus {
	statuses := make([]ProfileStatus, 0, len(g.profiles))
	for _, profile := range g.profiles {
		status := ProfileStatus{Profile: profile, Healthy: profile.Enabled}
		status.Profile.APIKeyRef = ""
		if probe && profile.Enabled {
			if err := g.ValidateProfile(ctx, profile.Name); err != nil {
				status.Healthy = false
				status.Error = err.Error()
			}
		}
		statuses = append(statuses, status)
	}
	return statuses
}

func (g *Gateway) resolveSecret(ctx context.Context, reference string) (string, error) {
	if reference == "" {
		return "", nil
	}
	if g.secrets == nil {
		return "", fmt.Errorf("secret resolver is not configured")
	}
	return g.secrets.Resolve(ctx, reference)
}

func (g *Gateway) recordUsage(
	ctx context.Context,
	agentRunID string,
	profile Profile,
	response Response,
) {
	if g.usage == nil {
		return
	}
	_ = g.usage.RecordLLMUsage(ctx, UsageRecord{
		ID:              id.New("llm"),
		ProviderProfile: profile.Name,
		AgentRunID:      agentRunID,
		Model:           profile.Model,
		Usage:           response.Usage,
		Latency:         response.Latency,
		CreatedAt:       time.Now().UTC(),
	})
}

func cloneMessages(messages []Message) []Message {
	return append([]Message(nil), messages...)
}

var (
	keychainReference = regexp.MustCompile(`keychain://[^\s"']+`)
	bearerToken       = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`)
	localPath         = regexp.MustCompile(`/(?:Users|home)/[^\s"']+`)
)

func redactMessages(messages []Message) []Message {
	result := cloneMessages(messages)
	for index := range result {
		content := keychainReference.ReplaceAllString(result[index].Content, "[REDACTED_CREDENTIAL_REF]")
		content = bearerToken.ReplaceAllString(content, "Bearer [REDACTED]")
		content = localPath.ReplaceAllString(content, "[REDACTED_LOCAL_PATH]")
		result[index].Content = content
	}
	return result
}
