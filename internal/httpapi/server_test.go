package httpapi_test

import (
	"bytes"
	"context"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/YuanJey/go-x-ai-operations/internal/app"
	"github.com/YuanJey/go-x-ai-operations/internal/config"
)

func TestMutationsRequireCSRF(t *testing.T) {
	cfg, err := config.Load(filepath.Join("..", "..", "config", "default.yaml"))
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	root := t.TempDir()
	cfg.Storage.DatabasePath = filepath.Join(root, "xops.db")
	cfg.Backup.Directory = filepath.Join(root, "backups")
	cfg.Rules.BootstrapFile = filepath.Join("..", "..", "config", "rules.yaml")
	application, err := app.Build(
		context.Background(), cfg,
		slog.New(slog.NewTextHandler(os.Stderr, nil)),
	)
	if err != nil {
		t.Fatalf("build app: %v", err)
	}
	defer application.Close()
	server := httptest.NewServer(application.HTTP.Handler())
	defer server.Close()

	statusResponse, err := http.Get(server.URL + "/api/v1/status")
	if err != nil {
		t.Fatalf("get status: %v", err)
	}
	if statusResponse.StatusCode != http.StatusOK {
		t.Fatalf("status endpoint returned %d", statusResponse.StatusCode)
	}
	statusResponse.Body.Close()

	request, _ := http.NewRequest(
		http.MethodPost,
		server.URL+"/api/v1/accounts/default/pause",
		bytes.NewBufferString(`{"reason":"test"}`),
	)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		t.Fatalf("pause without CSRF: %v", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusForbidden {
		t.Fatalf("pause status = %d, want 403", response.StatusCode)
	}
}
