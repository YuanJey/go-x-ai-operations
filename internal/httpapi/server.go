package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/account"
	"github.com/YuanJey/go-x-ai-operations/internal/alert"
	"github.com/YuanJey/go-x-ai-operations/internal/analytics"
	"github.com/YuanJey/go-x-ai-operations/internal/audit"
	"github.com/YuanJey/go-x-ai-operations/internal/content"
	"github.com/YuanJey/go-x-ai-operations/internal/engagement"
	"github.com/YuanJey/go-x-ai-operations/internal/jobs"
	"github.com/YuanJey/go-x-ai-operations/internal/llm"
	"github.com/YuanJey/go-x-ai-operations/internal/metering"
	"github.com/YuanJey/go-x-ai-operations/internal/oauth"
	"github.com/YuanJey/go-x-ai-operations/internal/orchestrator"
	"github.com/YuanJey/go-x-ai-operations/internal/rules"
)

type StatusProvider interface {
	Status(ctx context.Context) (any, error)
}

type Dependencies struct {
	AccountID    string
	DryRun       bool
	StartedAt    time.Time
	Account      *account.Service
	Rules        *rules.Service
	Jobs         *jobs.Repository
	Content      *content.Repository
	Engagement   *engagement.Repository
	Audit        *audit.Repository
	Metering     *metering.Repository
	Analytics    *analytics.Service
	Alerts       *alert.Service
	LLM          *llm.Gateway
	OAuth        *oauth.Manager
	PostWorkflow *orchestrator.PostWorkflow
}

type Server struct {
	deps   Dependencies
	csrf   string
	mux    *http.ServeMux
	logger *slog.Logger
}

func New(dependencies Dependencies, logger *slog.Logger) (*Server, error) {
	if logger == nil {
		logger = slog.Default()
	}
	token, err := randomToken(32)
	if err != nil {
		return nil, err
	}
	server := &Server{
		deps: dependencies, csrf: token,
		mux: http.NewServeMux(), logger: logger,
	}
	server.routes()
	return server, nil
}

func (s *Server) Handler() http.Handler {
	return s.securityHeaders(s.logging(s.mux))
}

func (s *Server) routes() {
	s.mux.HandleFunc("GET /{$}", s.handleConsole)
	s.mux.HandleFunc("GET /api/v1/status", s.handleStatus)
	s.mux.HandleFunc("GET /api/v1/providers", s.handleProviders)
	s.mux.HandleFunc("POST /api/v1/providers/validate", s.withCSRF(s.handleProviderValidate))
	s.mux.HandleFunc("POST /api/v1/oauth/start", s.withCSRF(s.handleOAuthStart))
	s.mux.HandleFunc("GET /api/v1/oauth/callback", s.handleOAuthCallback)
	s.mux.HandleFunc("GET /api/v1/rules", s.handleRules)
	s.mux.HandleFunc("POST /api/v1/rules/validate", s.withCSRF(s.handleRuleValidate))
	s.mux.HandleFunc("POST /api/v1/rules", s.withCSRF(s.handleRuleCreate))
	s.mux.HandleFunc("POST /api/v1/rules/{id}/activate", s.withCSRF(s.handleRuleActivate))
	s.mux.HandleFunc("POST /api/v1/rules/{id}/rollback", s.withCSRF(s.handleRuleRollback))
	s.mux.HandleFunc("POST /api/v1/accounts/{id}/pause", s.withCSRF(s.handlePause))
	s.mux.HandleFunc("POST /api/v1/accounts/{id}/resume", s.withCSRF(s.handleResume))
	s.mux.HandleFunc("GET /api/v1/jobs", s.handleJobs)
	s.mux.HandleFunc("GET /api/v1/content", s.handleContent)
	s.mux.HandleFunc("GET /api/v1/interactions", s.handleInteractions)
	s.mux.HandleFunc("GET /api/v1/audit", s.handleAudit)
	s.mux.HandleFunc("GET /api/v1/metering", s.handleMetering)
	s.mux.HandleFunc("GET /api/v1/metrics/account", s.handleAccountMetrics)
	s.mux.HandleFunc("GET /api/v1/alerts", s.handleAlerts)
	s.mux.HandleFunc("POST /api/v1/dry-run/posts", s.withCSRF(s.handleDryRunPost))
}

func (s *Server) handleStatus(response http.ResponseWriter, request *http.Request) {
	accountState, err := s.deps.Account.Get(request.Context(), s.deps.AccountID)
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	active, activeErr := s.deps.Rules.Active(request.Context())
	ruleID := ""
	if activeErr == nil {
		ruleID = active.ID
	} else if !errors.Is(activeErr, rules.ErrNoActiveRule) {
		writeError(response, activeErr, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{
		"mode":                map[bool]string{true: "dry_run", false: "live"}[s.deps.DryRun],
		"uptime_seconds":      int(time.Since(s.deps.StartedAt).Seconds()),
		"account":             accountState,
		"active_rule_version": ruleID,
		"csrf_token":          s.csrf,
	})
}

func (s *Server) handleProviders(response http.ResponseWriter, request *http.Request) {
	writeJSON(response, http.StatusOK, s.deps.LLM.Profiles(request.Context(), false))
}

func (s *Server) handleProviderValidate(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Name string `json:"name"`
	}
	if !decodeJSON(response, request, &input) {
		return
	}
	if err := s.deps.LLM.ValidateProfile(request.Context(), input.Name); err != nil {
		writeError(response, err, http.StatusBadGateway)
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"valid": true})
}

func (s *Server) handleOAuthStart(response http.ResponseWriter, _ *http.Request) {
	if s.deps.OAuth == nil {
		writeError(response, fmt.Errorf("OAuth is not configured in dry-run mode"), http.StatusConflict)
		return
	}
	authorizationURL, err := s.deps.OAuth.Start()
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, map[string]string{"authorization_url": authorizationURL})
}

func (s *Server) handleOAuthCallback(response http.ResponseWriter, request *http.Request) {
	if s.deps.OAuth == nil {
		writeError(response, fmt.Errorf("OAuth is not configured"), http.StatusConflict)
		return
	}
	if err := s.deps.OAuth.Callback(
		request.Context(), request.URL.Query().Get("state"), request.URL.Query().Get("code"),
	); err != nil {
		writeError(response, err, http.StatusBadRequest)
		return
	}
	http.Redirect(response, request, "/", http.StatusSeeOther)
}

func (s *Server) handleRules(response http.ResponseWriter, request *http.Request) {
	versions, err := s.deps.Rules.List(request.Context())
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, versions)
}

func (s *Server) handleRuleValidate(response http.ResponseWriter, request *http.Request) {
	raw, ok := readBody(response, request)
	if !ok {
		return
	}
	result, err := s.deps.Rules.Validate(raw)
	if err != nil {
		writeError(response, err, http.StatusBadRequest)
		return
	}
	writeJSON(response, http.StatusOK, result)
}

func (s *Server) handleRuleCreate(response http.ResponseWriter, request *http.Request) {
	raw, ok := readBody(response, request)
	if !ok {
		return
	}
	version, err := s.deps.Rules.Create(request.Context(), raw)
	if err != nil {
		writeError(response, err, http.StatusBadRequest)
		return
	}
	writeJSON(response, http.StatusCreated, version)
}

func (s *Server) handleRuleActivate(response http.ResponseWriter, request *http.Request) {
	var input struct {
		EffectiveAt *time.Time `json:"effective_at"`
	}
	if request.ContentLength > 0 && !decodeJSON(response, request, &input) {
		return
	}
	at := time.Now()
	if input.EffectiveAt != nil {
		at = *input.EffectiveAt
	}
	if err := s.deps.Rules.Activate(request.Context(), request.PathValue("id"), at); err != nil {
		writeError(response, err, http.StatusBadRequest)
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"activated": true})
}

func (s *Server) handleRuleRollback(response http.ResponseWriter, request *http.Request) {
	if err := s.deps.Rules.Rollback(request.Context(), request.PathValue("id")); err != nil {
		writeError(response, err, http.StatusBadRequest)
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"rolled_back": true})
}

func (s *Server) handlePause(response http.ResponseWriter, request *http.Request) {
	var input struct {
		Reason string `json:"reason"`
	}
	if request.ContentLength > 0 && !decodeJSON(response, request, &input) {
		return
	}
	if err := s.deps.Account.Pause(request.Context(), request.PathValue("id"), input.Reason); err != nil {
		writeError(response, err, http.StatusBadRequest)
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"paused": true})
}

func (s *Server) handleResume(response http.ResponseWriter, request *http.Request) {
	if err := s.deps.Account.Resume(request.Context(), request.PathValue("id")); err != nil {
		writeError(response, err, http.StatusBadRequest)
		return
	}
	writeJSON(response, http.StatusOK, map[string]bool{"resumed": true})
}

func (s *Server) handleJobs(response http.ResponseWriter, request *http.Request) {
	items, err := s.deps.Jobs.List(request.Context(), jobs.Filter{
		AccountID: s.deps.AccountID, State: request.URL.Query().Get("state"),
		Action: request.URL.Query().Get("action"), Limit: queryLimit(request),
	})
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (s *Server) handleContent(response http.ResponseWriter, request *http.Request) {
	items, err := s.deps.Content.List(
		request.Context(), s.deps.AccountID,
		request.URL.Query().Get("kind"), request.URL.Query().Get("state"), queryLimit(request),
	)
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (s *Server) handleInteractions(response http.ResponseWriter, request *http.Request) {
	items, err := s.deps.Engagement.List(request.Context(), s.deps.AccountID, queryLimit(request))
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (s *Server) handleAudit(response http.ResponseWriter, request *http.Request) {
	items, err := s.deps.Audit.List(request.Context(), audit.Filter{
		AccountID: s.deps.AccountID, EventType: request.URL.Query().Get("event_type"),
		Limit: queryLimit(request),
	})
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (s *Server) handleMetering(response http.ResponseWriter, request *http.Request) {
	items, err := s.deps.Metering.List(request.Context(), s.deps.AccountID, queryLimit(request))
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (s *Server) handleAccountMetrics(response http.ResponseWriter, request *http.Request) {
	items, err := s.deps.Analytics.AccountHistory(request.Context(), queryLimit(request))
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (s *Server) handleAlerts(response http.ResponseWriter, request *http.Request) {
	items, err := s.deps.Alerts.List(request.Context(), queryLimit(request))
	if err != nil {
		writeError(response, err, http.StatusInternalServerError)
		return
	}
	writeJSON(response, http.StatusOK, items)
}

func (s *Server) handleDryRunPost(response http.ResponseWriter, request *http.Request) {
	if !s.deps.DryRun {
		writeError(response, fmt.Errorf("manual post generation is only enabled in dry-run mode"), http.StatusConflict)
		return
	}
	var input orchestrator.PostInput
	if !decodeJSON(response, request, &input) {
		return
	}
	active, err := s.deps.Rules.Active(request.Context())
	if err != nil {
		writeError(response, err, http.StatusConflict)
		return
	}
	item, err := s.deps.PostWorkflow.Run(request.Context(), active.ID, input, true)
	if err != nil {
		writeError(response, err, http.StatusUnprocessableEntity)
		return
	}
	writeJSON(response, http.StatusCreated, item)
}

func (s *Server) withCSRF(handler http.HandlerFunc) http.HandlerFunc {
	return func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-CSRF-Token") != s.csrf {
			writeError(response, fmt.Errorf("invalid CSRF token"), http.StatusForbidden)
			return
		}
		handler(response, request)
	}
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		next.ServeHTTP(response, request)
		s.logger.Info("http request", "method", request.Method, "path", request.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *Server) securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline'; script-src 'self' 'unsafe-inline'; connect-src 'self'")
		response.Header().Set("X-Content-Type-Options", "nosniff")
		response.Header().Set("X-Frame-Options", "DENY")
		next.ServeHTTP(response, request)
	})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}

func writeError(response http.ResponseWriter, err error, status int) {
	writeJSON(response, status, map[string]string{"error": err.Error()})
}

func decodeJSON(response http.ResponseWriter, request *http.Request, target any) bool {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		writeError(response, fmt.Errorf("invalid JSON: %w", err), http.StatusBadRequest)
		return false
	}
	return true
}

func readBody(response http.ResponseWriter, request *http.Request) ([]byte, bool) {
	body, err := io.ReadAll(io.LimitReader(request.Body, 2<<20))
	if err != nil {
		writeError(response, err, http.StatusBadRequest)
		return nil, false
	}
	if len(strings.TrimSpace(string(body))) == 0 {
		writeError(response, fmt.Errorf("request body is empty"), http.StatusBadRequest)
		return nil, false
	}
	return body, true
}

func queryLimit(request *http.Request) int {
	value, _ := strconv.Atoi(request.URL.Query().Get("limit"))
	if value <= 0 {
		return 100
	}
	return value
}

func randomToken(size int) (string, error) {
	body := make([]byte, size)
	if _, err := rand.Read(body); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(body), nil
}
