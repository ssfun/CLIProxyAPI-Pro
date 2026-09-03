package auth

import (
	"net/http"
	"testing"
	"time"
)

type codexRetryAfterHeadersTestError struct {
	retryAfter time.Duration
}

func (e codexRetryAfterHeadersTestError) Error() string { return "codex rate limit" }
func (e codexRetryAfterHeadersTestError) StatusCode() int {
	return http.StatusTooManyRequests
}
func (e codexRetryAfterHeadersTestError) RetryAfter() *time.Duration {
	return &e.retryAfter
}

func TestSafeResponseHeadersIncludesRetryAfterProvider(t *testing.T) {
	err := codexRetryAfterHeadersTestError{retryAfter: 10 * time.Second}
	if got := SafeResponseHeaders(err).Get("Retry-After"); got != "10" {
		t.Fatalf("Retry-After = %q, want 10", got)
	}
}
