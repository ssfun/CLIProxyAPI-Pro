package auth

import (
	"context"
	"net/http"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type codexRateLimitRequestScopedTestError struct {
	retryAfter time.Duration
}

func (e *codexRateLimitRequestScopedTestError) Error() string   { return "codex rate limit" }
func (e *codexRateLimitRequestScopedTestError) StatusCode() int { return http.StatusTooManyRequests }
func (e *codexRateLimitRequestScopedTestError) RetryAfter() *time.Duration {
	if e == nil {
		return nil
	}
	d := e.retryAfter
	return &d
}
func (e *codexRateLimitRequestScopedTestError) IsRequestScoped() bool { return e != nil }

func TestManager_Codex429StopsCredentialFailoverAndPreservesRetryAfter(t *testing.T) {
	m := NewManager(nil, nil, nil)
	m.SetRetryConfig(2, 30*time.Second, 0)
	executor := &authFallbackExecutor{
		id:            "codex",
		executeErrors: map[string]error{},
	}
	m.RegisterExecutor(executor)

	model := "codex-429-request-scoped-" + uuid.NewString()
	first := &Auth{ID: "aaa-" + uuid.NewString(), Provider: "codex"}
	second := &Auth{ID: "bbb-" + uuid.NewString(), Provider: "codex"}
	for _, auth := range []*Auth{first, second} {
		registry.GetGlobalRegistry().RegisterClient(auth.ID, auth.Provider, []*registry.ModelInfo{{ID: model}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
		if _, errRegister := m.Register(context.Background(), auth); errRegister != nil {
			t.Fatalf("register %s: %v", auth.ID, errRegister)
		}
	}
	executor.executeErrors[first.ID] = &codexRateLimitRequestScopedTestError{retryAfter: 37 * time.Second}

	_, errExecute := m.Execute(context.Background(), []string{"codex"}, cliproxyexecutor.Request{Model: model}, cliproxyexecutor.Options{})
	if errExecute == nil {
		t.Fatal("expected 429 error")
	}
	if got := statusCodeFromError(errExecute); got != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want %d", got, http.StatusTooManyRequests)
	}
	if retryAfter := retryAfterFromError(errExecute); retryAfter == nil || *retryAfter != 37*time.Second {
		t.Fatalf("Retry-After = %v, want 37s", retryAfter)
	}
	calls := executor.ExecuteCalls()
	if len(calls) != 1 || calls[0] != first.ID {
		t.Fatalf("credential calls = %v, want only %s", calls, first.ID)
	}
}
