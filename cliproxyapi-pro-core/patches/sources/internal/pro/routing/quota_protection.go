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
const LegacyRoutingQuotaSourcePrefix = "routing:"

// QuotaProtection is independent of Disabled and upstream error state. All
// holds require explicit release. RetryAt schedules either a quota recheck or
// one real probe request; it never makes the selector release traffic itself.
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

func ParseQuotaProtections(metadata map[string]any) map[string]QuotaProtection {
	result := make(map[string]QuotaProtection)
	if raw, ok := metadata[QuotaProtectionMetadataKey].(string); ok {
		_ = json.Unmarshal([]byte(raw), &result)
	}
	if result == nil {
		result = make(map[string]QuotaProtection)
	}
	return result
}

func QuotaProtections(metadata map[string]any) map[string]QuotaProtection {
	result, _ := WithoutLegacyRoutingQuotaProtections(ParseQuotaProtections(metadata))
	if result == nil {
		result = make(map[string]QuotaProtection)
	}
	return result
}

func IsLegacyRoutingQuotaSource(source string) bool {
	return strings.HasPrefix(strings.TrimSpace(source), LegacyRoutingQuotaSourcePrefix)
}

func WithoutLegacyRoutingQuotaProtections(protections map[string]QuotaProtection) (map[string]QuotaProtection, bool) {
	if len(protections) == 0 {
		return protections, false
	}
	dropped := false
	out := make(map[string]QuotaProtection, len(protections))
	for source, hold := range protections {
		if IsLegacyRoutingQuotaSource(source) || IsLegacyRoutingQuotaSource(hold.Source) {
			dropped = true
			continue
		}
		out[source] = hold
	}
	if !dropped {
		return protections, false
	}
	return out, true
}

func ProtectionBlocks(p QuotaProtection, model string, now time.Time) bool {
	if p.Model != "" && !ModelMatchesProtection(p.Model, model) {
		return false
	}
	return true
}

func ModelMatchesProtection(patterns, model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	if model == "" {
		return false
	}
	for _, pattern := range strings.Split(patterns, ",") {
		pattern = strings.ToLower(strings.TrimSpace(pattern))
		if pattern == "" {
			continue
		}
		if ok, _ := path.Match(pattern, model); ok {
			return true
		}
	}
	return false
}

func RecoveryJitter(authID string) time.Duration {
	h := fnv.New32a()
	_, _ = h.Write([]byte(authID))
	return time.Duration(h.Sum32()%15000) * time.Millisecond
}

func ScheduleRecheckAt(resetAt int64, authID string, now time.Time) int64 {
	jitter := RecoveryJitter(authID)
	if resetAt > now.UnixMilli() {
		return resetAt + int64(jitter/time.Millisecond)
	}
	return now.Add(time.Minute + jitter).UnixMilli()
}

func NextRecheckAt(resetAt int64, authID string, failures int, now time.Time) int64 {
	if resetAt > now.UnixMilli() {
		return ScheduleRecheckAt(resetAt, authID, now)
	}
	return now.Add(RecoveryBackoff(authID, failures)).UnixMilli()
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
	return delay + RecoveryJitter(authID)
}
