package auth

import (
	"fmt"
	"net/http"
	"testing"
	"time"
)

type codexRetryAfterHeadersTestError struct {
	status     int
	retryAfter time.Duration
}

func (e codexRetryAfterHeadersTestError) Error() string { return "codex rate limit" }
func (e codexRetryAfterHeadersTestError) StatusCode() int {
	return e.status
}
func (e codexRetryAfterHeadersTestError) RetryAfter() *time.Duration {
	return &e.retryAfter
}

func TestCodexRetryAfterHeadersIncludesWrapped429(t *testing.T) {
	err := fmt.Errorf("wrapped: %w", codexRetryAfterHeadersTestError{
		status:     http.StatusTooManyRequests,
		retryAfter: 37 * time.Second,
	})
	if got := SafeResponseHeaders(err).Get("Retry-After"); got != "37" {
		t.Fatalf("Retry-After = %q, want 37", got)
	}
}

func TestCodexRetryAfterHeadersRejectsNon429(t *testing.T) {
	err := codexRetryAfterHeadersTestError{
		status:     http.StatusServiceUnavailable,
		retryAfter: 37 * time.Second,
	}
	if got := SafeResponseHeaders(err).Get("Retry-After"); got != "" {
		t.Fatalf("Retry-After = %q, want empty for non-429", got)
	}
}
