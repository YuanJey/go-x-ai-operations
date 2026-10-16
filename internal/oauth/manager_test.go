package oauth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"
)

type memoryStore struct {
	mu     sync.Mutex
	values map[string]string
}

func (s *memoryStore) Resolve(_ context.Context, reference string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.values[reference], nil
}

func (s *memoryStore) Save(_ context.Context, reference, secret string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.values[reference] = secret
	return nil
}

func TestPKCECallbackAndRefresh(t *testing.T) {
	var authorizationVerifier string
	tokenServer := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if err := request.ParseForm(); err != nil {
			t.Fatalf("parse token form: %v", err)
		}
		switch request.Form.Get("grant_type") {
		case "authorization_code":
			authorizationVerifier = request.Form.Get("code_verifier")
			_, _ = response.Write([]byte(`{
				"access_token":"first","refresh_token":"refresh-1",
				"expires_in":1,"scope":"tweet.read tweet.write offline.access"
			}`))
		case "refresh_token":
			_, _ = response.Write([]byte(`{
				"access_token":"second","refresh_token":"refresh-2",
				"expires_in":3600,"scope":"tweet.read tweet.write offline.access"
			}`))
		default:
			http.Error(response, "unexpected grant", http.StatusBadRequest)
		}
	}))
	defer tokenServer.Close()

	store := &memoryStore{values: make(map[string]string)}
	manager, err := NewManager(Config{
		ClientID: "client", AuthorizeURL: "https://x.example/authorize",
		TokenURL: tokenServer.URL, RedirectURL: "http://127.0.0.1/callback",
		RefreshTokenRef: "keychain://xops/refresh",
		Scopes:          []string{"tweet.read", "tweet.write", "offline.access"},
	}, store)
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	authorizationURL, err := manager.Start()
	if err != nil {
		t.Fatalf("start OAuth: %v", err)
	}
	parsed, _ := url.Parse(authorizationURL)
	state := parsed.Query().Get("state")
	if parsed.Query().Get("code_challenge_method") != "S256" || state == "" {
		t.Fatalf("invalid authorization URL: %s", authorizationURL)
	}
	if err := manager.Callback(context.Background(), state, "code"); err != nil {
		t.Fatalf("OAuth callback: %v", err)
	}
	if authorizationVerifier == "" || store.values["keychain://xops/refresh"] != "refresh-1" {
		t.Fatalf("PKCE or refresh token was not persisted")
	}
	manager.mu.Lock()
	manager.token.ExpiresAt = manager.token.ExpiresAt.Add(-2 * time.Hour)
	manager.mu.Unlock()
	token, err := manager.AccessToken(context.Background())
	if err != nil {
		t.Fatalf("refresh access token: %v", err)
	}
	if token != "second" || store.values["keychain://xops/refresh"] != "refresh-2" {
		t.Fatalf("token=%s store=%v", token, store.values)
	}
}

func TestCallbackRejectsUnknownState(t *testing.T) {
	manager, err := NewManager(Config{
		ClientID: "client", AuthorizeURL: "https://x.example/authorize",
		TokenURL: "https://x.example/token", RedirectURL: "http://127.0.0.1/callback",
		RefreshTokenRef: "keychain://xops/refresh",
	}, &memoryStore{values: make(map[string]string)})
	if err != nil {
		t.Fatalf("new manager: %v", err)
	}
	err = manager.Callback(context.Background(), "unknown", "code")
	if err == nil || !strings.Contains(err.Error(), "invalid or expired") {
		t.Fatalf("error = %v", err)
	}
}
