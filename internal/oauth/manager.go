package oauth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

type CredentialStore interface {
	Resolve(ctx context.Context, reference string) (string, error)
	Save(ctx context.Context, reference, secret string) error
}

type Config struct {
	ClientID        string
	AuthorizeURL    string
	TokenURL        string
	RedirectURL     string
	RefreshTokenRef string
	Scopes          []string
	HTTPClient      *http.Client
	SessionTTL      time.Duration
}

type authorizationSession struct {
	Verifier  string
	ExpiresAt time.Time
}

type tokenState struct {
	AccessToken string
	ExpiresAt   time.Time
	Scopes      map[string]bool
}

type Manager struct {
	config   Config
	store    CredentialStore
	client   *http.Client
	mu       sync.Mutex
	sessions map[string]authorizationSession
	token    tokenState
}

func NewManager(config Config, store CredentialStore) (*Manager, error) {
	if config.ClientID == "" || config.AuthorizeURL == "" || config.TokenURL == "" ||
		config.RedirectURL == "" || config.RefreshTokenRef == "" {
		return nil, fmt.Errorf("OAuth client ID, endpoints, redirect URL, and refresh token reference are required")
	}
	if config.HTTPClient == nil {
		config.HTTPClient = http.DefaultClient
	}
	if config.SessionTTL <= 0 {
		config.SessionTTL = 10 * time.Minute
	}
	return &Manager{
		config: config, store: store, client: config.HTTPClient,
		sessions: make(map[string]authorizationSession),
	}, nil
}

func (m *Manager) Start() (string, error) {
	state, err := randomURLToken(32)
	if err != nil {
		return "", err
	}
	verifier, err := randomURLToken(64)
	if err != nil {
		return "", err
	}
	challengeSum := sha256.Sum256([]byte(verifier))
	challenge := base64.RawURLEncoding.EncodeToString(challengeSum[:])
	m.mu.Lock()
	m.sessions[state] = authorizationSession{
		Verifier: verifier, ExpiresAt: time.Now().Add(m.config.SessionTTL),
	}
	m.removeExpiredSessionsLocked(time.Now())
	m.mu.Unlock()

	query := url.Values{
		"response_type":         {"code"},
		"client_id":             {m.config.ClientID},
		"redirect_uri":          {m.config.RedirectURL},
		"scope":                 {strings.Join(m.config.Scopes, " ")},
		"state":                 {state},
		"code_challenge":        {challenge},
		"code_challenge_method": {"S256"},
	}
	return m.config.AuthorizeURL + "?" + query.Encode(), nil
}

func (m *Manager) Callback(
	ctx context.Context,
	state, code string,
) error {
	m.mu.Lock()
	session, ok := m.sessions[state]
	delete(m.sessions, state)
	m.mu.Unlock()
	if !ok || session.ExpiresAt.Before(time.Now()) {
		return fmt.Errorf("OAuth state is invalid or expired")
	}
	if code == "" {
		return fmt.Errorf("OAuth authorization code is missing")
	}
	token, err := m.exchange(ctx, url.Values{
		"grant_type":    {"authorization_code"},
		"client_id":     {m.config.ClientID},
		"code":          {code},
		"redirect_uri":  {m.config.RedirectURL},
		"code_verifier": {session.Verifier},
	})
	if err != nil {
		return err
	}
	if token.RefreshToken == "" {
		return fmt.Errorf("OAuth token response did not include a refresh token")
	}
	if err := m.store.Save(ctx, m.config.RefreshTokenRef, token.RefreshToken); err != nil {
		return fmt.Errorf("save OAuth refresh token: %w", err)
	}
	m.setToken(token)
	return nil
}

func (m *Manager) AccessToken(ctx context.Context) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.token.AccessToken != "" && time.Until(m.token.ExpiresAt) > time.Minute {
		return m.token.AccessToken, nil
	}
	refreshToken, err := m.store.Resolve(ctx, m.config.RefreshTokenRef)
	if err != nil {
		return "", fmt.Errorf("load OAuth refresh token: %w", err)
	}
	token, err := m.exchange(ctx, url.Values{
		"grant_type":    {"refresh_token"},
		"client_id":     {m.config.ClientID},
		"refresh_token": {refreshToken},
	})
	if err != nil {
		return "", err
	}
	if token.RefreshToken != "" && token.RefreshToken != refreshToken {
		if err := m.store.Save(ctx, m.config.RefreshTokenRef, token.RefreshToken); err != nil {
			return "", fmt.Errorf("rotate OAuth refresh token: %w", err)
		}
	}
	m.setTokenLocked(token)
	return m.token.AccessToken, nil
}

func (m *Manager) HasScopes(required ...string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, scope := range required {
		if !m.token.Scopes[scope] {
			return false
		}
	}
	return true
}

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
	TokenType    string `json:"token_type"`
}

func (m *Manager) exchange(ctx context.Context, values url.Values) (tokenResponse, error) {
	request, err := http.NewRequestWithContext(
		ctx, http.MethodPost, m.config.TokenURL,
		strings.NewReader(values.Encode()),
	)
	if err != nil {
		return tokenResponse{}, err
	}
	request.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	response, err := m.client.Do(request)
	if err != nil {
		return tokenResponse{}, fmt.Errorf("call OAuth token endpoint: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return tokenResponse{}, fmt.Errorf("read OAuth response: %w", err)
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return tokenResponse{}, fmt.Errorf("OAuth token endpoint returned HTTP %d", response.StatusCode)
	}
	var token tokenResponse
	if err := json.Unmarshal(body, &token); err != nil {
		return tokenResponse{}, fmt.Errorf("decode OAuth token response: %w", err)
	}
	if token.AccessToken == "" {
		return tokenResponse{}, fmt.Errorf("OAuth token response did not include an access token")
	}
	return token, nil
}

func (m *Manager) setToken(token tokenResponse) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.setTokenLocked(token)
}

func (m *Manager) setTokenLocked(token tokenResponse) {
	expiresIn := token.ExpiresIn
	if expiresIn <= 0 {
		expiresIn = 3600
	}
	scopes := make(map[string]bool)
	for _, scope := range strings.Fields(token.Scope) {
		scopes[scope] = true
	}
	m.token = tokenState{
		AccessToken: token.AccessToken,
		ExpiresAt:   time.Now().Add(time.Duration(expiresIn) * time.Second),
		Scopes:      scopes,
	}
}

func (m *Manager) removeExpiredSessionsLocked(now time.Time) {
	for state, session := range m.sessions {
		if session.ExpiresAt.Before(now) {
			delete(m.sessions, state)
		}
	}
}

func randomURLToken(size int) (string, error) {
	body := make([]byte, size)
	if _, err := rand.Read(body); err != nil {
		return "", fmt.Errorf("generate OAuth random token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}
