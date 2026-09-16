package routing

import (
	"encoding/json"
	"hash/fnv"
	"path"
	"strings"
	"time"
)

const QuotaProtectionNamespace = "quota-protection"
const QuotaProtectionMetadataKey = "pro_quota_protection"

// QuotaProtection is independent of Disabled and upstream error state. A zero
// RetryAt requires explicit release. Recheck holds need fresh quota evidence.
type QuotaProtection struct {
	Source   string          `json:"source"`
	Revision int64           `json:"revision"`
	RetryAt  int64           `json:"retryAt"`
	Recheck  bool            `json:"recheck"`
	Model    string          `json:"model,omitempty"`
	Reason   string          `json:"reason"`
	Failures int             `json:"failures,omitempty"`
	Settings json.RawMessage `json:"settings,omitempty"`
}

func QuotaProtections(metadata map[string]any) map[string]QuotaProtection {
	result := make(map[string]QuotaProtection)
	if raw, ok := metadata[QuotaProtectionMetadataKey].(string); ok {
		_ = json.Unmarshal([]byte(raw), &result)
	}
	if result == nil {
		result = make(map[string]QuotaProtection)
	}
	return result
}

func ProtectionBlocks(p QuotaProtection, model string, now time.Time) bool {
	if p.Model != "" {
		matches := false
		for _, pattern := range strings.Split(p.Model, ",") {
			if ok, _ := path.Match(strings.ToLower(pattern), strings.ToLower(model)); ok {
				matches = true
			}
		}
		if !matches {
			return false
		}
	}
	return p.Recheck || p.RetryAt == 0 || p.RetryAt > now.UnixMilli()
}

// Bound unknown resets and failures; stable per-account jitter spreads wakeups.
func RecoveryBackoff(authID string, failures int) time.Duration {
	if failures < 0 {
		failures = 0
	}
	if failures > 5 {
		failures = 5
	}
	delay := time.Minute * time.Duration(1<<failures)
	if delay > 30*time.Minute {
		delay = 30 * time.Minute
	}
	h := fnv.New32a()
	_, _ = h.Write([]byte(authID))
	return delay + time.Duration(h.Sum32()%15000)*time.Millisecond
}
