package xapi

import (
	"context"
	"errors"
	"strconv"
	"testing"
	"time"

	"github.com/YuanJey/go-x-ai-operations/internal/rules"
	sqlitestore "github.com/YuanJey/go-x-ai-operations/internal/store/sqlite"
)

func TestRateLimitAdmissionUsesObservedReset(t *testing.T) {
	ctx := context.Background()
	store, err := sqlitestore.Open(ctx, "file:rate-limit-test?mode=memory&cache=shared")
	if err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer store.Close()
	if err := rules.NewRepository(store.DB).EnsureAccount(ctx, "account", "@account", "UTC"); err != nil {
		t.Fatalf("ensure account: %v", err)
	}
	rateLimits := NewRateLimitStore(store.DB)
	now := time.Now().UTC()
	reset := now.Add(time.Hour)
	if err := rateLimits.Observe(ctx, "account", "POST /tweets", map[string]string{
		"x-rate-limit-limit":     "10",
		"x-rate-limit-remaining": "0",
		"x-rate-limit-reset":     formatUnix(reset),
	}, now); err != nil {
		t.Fatalf("observe limit: %v", err)
	}
	err = rateLimits.Admit(ctx, "account", "POST /tweets", now)
	var xError *Error
	if !errors.As(err, &xError) || xError.ResetAt == nil {
		t.Fatalf("admission error = %v, want rate limit with reset", err)
	}
	if err := rateLimits.Admit(ctx, "account", "POST /tweets", reset.Add(time.Second)); err != nil {
		t.Fatalf("admission after reset: %v", err)
	}
}

func formatUnix(value time.Time) string {
	return strconv.FormatInt(value.Unix(), 10)
}
