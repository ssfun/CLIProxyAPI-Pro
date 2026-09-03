package executor

import (
	"net/http"
	"testing"
	"time"
)

func TestNewCodexStatusErrRetryAfterPrecedence(t *testing.T) {
	body := []byte(`{"error":{"type":"usage_limit_reached","resets_in_seconds":120}}`)
	err := newCodexStatusErr(http.StatusTooManyRequests, body, http.Header{"Retry-After": []string{"5"}})

	if retryAfter := err.RetryAfter(); retryAfter == nil || *retryAfter != 120*time.Second {
		t.Fatalf("body reset did not take precedence: RetryAfter() = %v, want 2m", retryAfter)
	}
}

func TestNewCodexStatusErrUsesRetryAfterHeader(t *testing.T) {
	err := newCodexStatusErr(
		http.StatusTooManyRequests,
		[]byte(`{"error":{"type":"rate_limit_error","code":"rate_limit_exceeded"}}`),
		http.Header{"Retry-After": []string{"37"}},
	)

	if retryAfter := err.RetryAfter(); retryAfter == nil || *retryAfter != 37*time.Second {
		t.Fatalf("RetryAfter() = %v, want 37s", retryAfter)
	}
}

func TestNewCodexStatusErrUsesSafeFallback(t *testing.T) {
	err := newCodexStatusErr(http.StatusTooManyRequests, []byte(`{"detail":"Rate limit exceeded"}`))

	if retryAfter := err.RetryAfter(); retryAfter == nil || *retryAfter != 10*time.Second {
		t.Fatalf("RetryAfter() = %v, want 10s fallback", retryAfter)
	}
}
