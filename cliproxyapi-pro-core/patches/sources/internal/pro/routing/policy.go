package routing

import (
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	ModeObserve = "observe"
	ModeEnforce = "enforce"
)

// ProviderPolicy is the provider-neutral request-protection policy consumed by
// the Management host adapter.
type ProviderPolicy struct {
	Enabled                   bool  `yaml:"enabled" json:"enabled"`
	StatusCodes               []int `yaml:"status-codes,omitempty" json:"statusCodes,omitempty"`
	Confirmations             int   `yaml:"confirmations,omitempty" json:"confirmations,omitempty"`
	ConfirmationWindowSeconds int   `yaml:"confirmation-window-seconds,omitempty" json:"confirmationWindowSeconds,omitempty"`
	AutoEnable                bool  `yaml:"auto-enable" json:"autoEnable"`
	FallbackDisableMinutes    int   `yaml:"fallback-disable-minutes,omitempty" json:"fallbackDisableMinutes,omitempty"`
	RequireQuotaEvidence      bool  `yaml:"require-quota-evidence" json:"requireQuotaEvidence"`
}

type RequestProtectionConfig struct {
	Enabled   bool                      `yaml:"enabled" json:"enabled"`
	Mode      string                    `yaml:"mode,omitempty" json:"mode,omitempty"`
	Providers map[string]ProviderPolicy `yaml:"providers,omitempty" json:"providers,omitempty"`
}

func NormalizeConfig(input RequestProtectionConfig, providers []string) RequestProtectionConfig {
	input.Mode = NormalizeMode(input.Mode)
	normalized := make(map[string]ProviderPolicy, len(providers))
	for _, provider := range providers {
		provider = strings.ToLower(strings.TrimSpace(provider))
		if provider == "" {
			continue
		}
		policy := input.Providers[provider]
		policy.StatusCodes = NormalizeStatusCodes(policy.StatusCodes)
		if len(policy.StatusCodes) == 0 {
			policy.StatusCodes = []int{http.StatusPaymentRequired, http.StatusTooManyRequests}
		}
		if policy.Confirmations <= 0 {
			policy.Confirmations = 1
		}
		if policy.Confirmations > 5 {
			policy.Confirmations = 5
		}
		if policy.ConfirmationWindowSeconds <= 0 {
			policy.ConfirmationWindowSeconds = 600
		}
		if policy.ConfirmationWindowSeconds > 86400 {
			policy.ConfirmationWindowSeconds = 86400
		}
		if policy.FallbackDisableMinutes < 0 {
			policy.FallbackDisableMinutes = 0
		}
		if policy.FallbackDisableMinutes > 10080 {
			policy.FallbackDisableMinutes = 10080
		}
		normalized[provider] = policy
	}
	input.Providers = normalized
	return input
}

func NormalizeMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), ModeEnforce) {
		return ModeEnforce
	}
	return ModeObserve
}

func NormalizeStatusCodes(values []int) []int {
	seen := make(map[int]struct{})
	out := make([]int, 0, len(values))
	for _, value := range values {
		if value < 100 || value > 599 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}

// UsageFailure is the stable input needed by the protection engine. It keeps
// upstream usage.Record details at the host boundary.
type UsageFailure struct {
	StatusCode int
	Headers    http.Header
	Body       string
}

type confirmation struct {
	count   int
	firstAt time.Time
}

// ConfirmationTracker owns the bounded-window confirmation state used before
// a protection decision is enforced.
type ConfirmationTracker struct {
	mu     sync.Mutex
	states map[string]confirmation
}

var (
	routingBearerPattern = regexp.MustCompile(`(?i)bearer\s+[a-z0-9._~+/=-]+`)
	routingSecretPattern = regexp.MustCompile(`(?i)("?(?:authorization|api[_-]?key|(?:[a-z0-9]+[_-])*token|password|secret|credential|cookie)"?)(\s*[:=]\s*)(?:"(?:\\.|[^"])*"|'(?:\\.|[^'])*'|[^\s,;&}]+)`)
)

func NewConfirmationTracker() *ConfirmationTracker {
	return &ConfirmationTracker{states: make(map[string]confirmation)}
}

func (t *ConfirmationTracker) Confirm(authID, provider string, statusCode int, policy ProviderPolicy, now time.Time) (bool, int, int) {
	required := policy.Confirmations
	if required <= 1 {
		return true, 1, 1
	}
	window := time.Duration(policy.ConfirmationWindowSeconds) * time.Second
	if window <= 0 {
		window = 10 * time.Minute
	}
	key := strings.Join([]string{authID, provider, strconv.Itoa(statusCode)}, "|")
	if t == nil {
		return false, 0, required
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	state := t.states[key]
	if state.firstAt.IsZero() || now.Sub(state.firstAt) > window {
		state = confirmation{firstAt: now}
	}
	state.count++
	t.states[key] = state
	return state.count >= required, state.count, required
}

func (t *ConfirmationTracker) Clear(authID, provider string) {
	if t == nil {
		return
	}
	prefix := authID + "|" + provider + "|"
	t.mu.Lock()
	for key := range t.states {
		if strings.HasPrefix(key, prefix) {
			delete(t.states, key)
		}
	}
	t.mu.Unlock()
}

func (t *ConfirmationTracker) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	clear(t.states)
	t.mu.Unlock()
}

func StatusMatches(values []int, status int) bool {
	for _, value := range values {
		if value == status {
			return true
		}
	}
	return false
}

func HasQuotaEvidence(failure UsageFailure) bool {
	if failure.Headers.Get("Retry-After") != "" {
		return true
	}
	for _, key := range []string{"x-codex-primary-used-percent", "x-codex-secondary-used-percent"} {
		if value, err := strconv.ParseFloat(strings.TrimSpace(failure.Headers.Get(key)), 64); err == nil && value >= 99.5 {
			return true
		}
	}
	body := strings.ToLower(strings.TrimSpace(failure.Body))
	for _, marker := range []string{
		"usage_limit_reached", "rate_limit_exceeded", "insufficient_quota",
		"free-usage-exhausted", "quota exceeded", "quota_exceeded",
		"used all the included free usage", "resource_exhausted",
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func ReleaseAt(failure UsageFailure, policy ProviderPolicy, now time.Time) time.Time {
	if !policy.AutoEnable {
		return time.Time{}
	}
	candidates := make([]time.Time, 0, 4)
	if retryAt := retryAfter(failure.Headers.Get("Retry-After"), now); !retryAt.IsZero() {
		candidates = append(candidates, retryAt)
	}
	for _, key := range []string{"x-codex-primary-reset-at", "x-codex-secondary-reset-at"} {
		if unix, err := strconv.ParseInt(strings.TrimSpace(failure.Headers.Get(key)), 10, 64); err == nil && unix > now.Unix() {
			candidates = append(candidates, time.Unix(unix, 0))
		}
	}
	if bodyAt := bodyResetAt(failure.Body, now); !bodyAt.IsZero() {
		candidates = append(candidates, bodyAt)
	}
	var releaseAt time.Time
	for _, candidate := range candidates {
		if candidate.After(releaseAt) {
			releaseAt = candidate
		}
	}
	if releaseAt.IsZero() && policy.FallbackDisableMinutes > 0 {
		releaseAt = now.Add(time.Duration(policy.FallbackDisableMinutes) * time.Minute)
	}
	return releaseAt
}

func Reason(failure UsageFailure) string {
	if body := strings.TrimSpace(failure.Body); body != "" {
		return redactReason(body)
	}
	return fmt.Sprintf("HTTP %d", failure.StatusCode)
}

func redactReason(value string) string {
	var payload any
	if json.Unmarshal([]byte(value), &payload) == nil {
		if redactReasonJSON(payload) {
			if encoded, err := json.Marshal(payload); err == nil {
				return string(encoded)
			}
		}
		return value
	}
	return redactReasonText(value)
}

func redactReasonText(value string) string {
	value = routingBearerPattern.ReplaceAllString(value, "Bearer [REDACTED]")
	return routingSecretPattern.ReplaceAllString(value, "${1}${2}[REDACTED]")
}

func redactReasonJSON(value any) bool {
	changed := false
	switch typed := value.(type) {
	case map[string]any:
		for key, child := range typed {
			normalized := strings.ToLower(strings.NewReplacer("-", "_", " ", "_").Replace(key))
			if sensitiveReasonKey(normalized) {
				typed[key] = "[REDACTED]"
				changed = true
				continue
			}
			if text, ok := child.(string); ok {
				redacted := redactReasonText(text)
				if redacted != text {
					typed[key] = redacted
					changed = true
				}
			} else if redactReasonJSON(child) {
				changed = true
			}
		}
	case []any:
		for index, child := range typed {
			if text, ok := child.(string); ok {
				redacted := redactReasonText(text)
				if redacted != text {
					typed[index] = redacted
					changed = true
				}
			} else if redactReasonJSON(child) {
				changed = true
			}
		}
	}
	return changed
}

func sensitiveReasonKey(normalized string) bool {
	return strings.Contains(normalized, "token") ||
		strings.Contains(normalized, "secret") ||
		strings.Contains(normalized, "password") ||
		strings.Contains(normalized, "credential") ||
		strings.Contains(normalized, "authorization") ||
		strings.Contains(normalized, "api_key") ||
		normalized == "apikey" ||
		strings.Contains(normalized, "cookie")
}

func MetadataInt64(metadata map[string]any, key string) int64 {
	value, _ := anyInt64(metadata[key])
	return value
}

func retryAfter(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(value); err == nil && parsed.After(now) {
		return parsed
	}
	return time.Time{}
}

func bodyResetAt(body string, now time.Time) time.Time {
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil {
		return time.Time{}
	}
	errorPayload, _ := payload["error"].(map[string]any)
	for _, source := range []map[string]any{errorPayload, payload} {
		if unix, ok := anyInt64(source["resets_at"]); ok && unix > now.Unix() {
			return time.Unix(unix, 0)
		}
		if seconds, ok := anyInt64(source["resets_in_seconds"]); ok && seconds > 0 {
			return now.Add(time.Duration(seconds) * time.Second)
		}
	}
	return time.Time{}
}

func anyInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), typed > 0
	case int64:
		return typed, typed > 0
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil && parsed > 0
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil && parsed > 0
	default:
		return 0, false
	}
}
