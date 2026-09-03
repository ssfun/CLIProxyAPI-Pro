package helps

import (
	"net/http"
	"strconv"
	"strings"
	"time"
)

// ParseRetryAfterHeader parses the standard Retry-After formats into a positive delay.
// Invalid, expired, and empty values return nil so callers can apply a safe fallback.
func ParseRetryAfterHeader(raw string, now time.Time) *time.Duration {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	if seconds, err := strconv.ParseFloat(raw, 64); err == nil && seconds > 0 {
		delay := time.Duration(seconds * float64(time.Second))
		if delay > 0 {
			return &delay
		}
	}
	if resetAt, err := http.ParseTime(raw); err == nil {
		if delay := resetAt.Sub(now); delay > 0 {
			return &delay
		}
	}
	if resetAt, err := time.Parse(time.RFC3339, raw); err == nil {
		if delay := resetAt.Sub(now); delay > 0 {
			return &delay
		}
	}
	return nil
}
