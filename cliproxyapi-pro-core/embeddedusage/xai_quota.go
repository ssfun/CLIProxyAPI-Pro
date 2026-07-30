package embeddedusage

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
)

const XAIQuotaParserVersion = 5

type XAIQuotaObservation struct {
	FileName   string
	AuthIndex  string
	Email      string
	Label      string
	Model      string
	Status     int
	Header     http.Header
	Body       []byte
	ObservedAt time.Time
}

var (
	xaiQuotaCacheMu          sync.Mutex
	xaiFreeQuotaUsagePattern = regexp.MustCompile(`(?i)tokens\s*\(actual/limit\)\s*:\s*([0-9]+)\s*/\s*([0-9]+)`)
	xaiFreeQuotaModelPattern = regexp.MustCompile(`(?i)for\s+model\s+([a-z0-9._-]+)`)
)

func ObserveXAIQuotaResponse(ctx context.Context, observation XAIQuotaObservation) error {
	fileName := strings.TrimSpace(observation.FileName)
	if fileName == "" {
		return nil
	}
	observedAt := observation.ObservedAt
	if observedAt.IsZero() {
		observedAt = time.Now()
	}

	var freeQuota map[string]any
	if xaiQuotaHeadersStatus(observation.Status) {
		freeQuota = xaiRateLimitSnapshot(observation.Header, observation.Model, observedAt)
	}
	if xaiFreeQuotaExhausted(observation.Body) {
		freeQuota = xaiExhaustedQuotaSnapshot(observation.Body, observation.Model, observedAt)
	}
	if freeQuota == nil {
		return nil
	}

	now := observedAt.UnixMilli()
	state := map[string]any{
		"status":        "success",
		"schemaVersion": 2,
		"parserVersion": XAIQuotaParserVersion,
		"cachedAt":      now,
		"billing": map[string]any{
			"freeQuota": freeQuota,
		},
	}
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	fingerprintSource := strings.Join([]string{
		"xai",
		strings.ToLower(fileName),
		strings.ToLower(strings.TrimSpace(observation.Email)),
		strings.ToLower(strings.TrimSpace(observation.Label)),
	}, "|")
	fingerprint := sha256.Sum256([]byte(fingerprintSource))
	return MergeXAIQuotaCache(ctx, QuotaCacheEntry{
		ID:                  "xai:" + fileName,
		Provider:            "xai",
		FileName:            fileName,
		AuthIndex:           strings.TrimSpace(observation.AuthIndex),
		IdentityFingerprint: hex.EncodeToString(fingerprint[:]),
		Data:                raw,
		CachedAt:            now,
		ObservedAt:          now,
		AccessedAt:          now,
		Version:             2,
	})
}

func xaiQuotaHeadersStatus(status int) bool {
	return status == http.StatusSwitchingProtocols || (status >= 200 && status < 300)
}

// MergeXAIQuotaCache merges billing refreshes and request-path free quota observations.
// Neither writer is allowed to discard the other writer's latest fields.
func MergeXAIQuotaCache(ctx context.Context, entry QuotaCacheEntry) error {
	if !strings.EqualFold(strings.TrimSpace(entry.Provider), "xai") || strings.TrimSpace(entry.FileName) == "" {
		return SetQuotaCache(ctx, entry)
	}
	globalStateMu.RLock()
	defer globalStateMu.RUnlock()
	if globalService == nil || globalService.store == nil {
		return fmt.Errorf("usage service is not available")
	}
	return globalService.store.MergeXAIQuotaCache(ctx, entry)
}

func (s *Store) MergeXAIQuotaCache(ctx context.Context, entry QuotaCacheEntry) error {
	if !strings.EqualFold(strings.TrimSpace(entry.Provider), "xai") || strings.TrimSpace(entry.FileName) == "" {
		return s.SetQuotaCache(ctx, entry)
	}
	xaiQuotaCacheMu.Lock()
	defer xaiQuotaCacheMu.Unlock()

	incoming := map[string]any{}
	if len(entry.Data) > 0 {
		if err := json.Unmarshal(entry.Data, &incoming); err != nil {
			return err
		}
	}
	existingEntries, err := s.GetQuotaCache(ctx, "xai", entry.FileName)
	if err != nil {
		return err
	}
	if len(existingEntries) > 0 {
		existing := map[string]any{}
		if json.Unmarshal(existingEntries[0].Data, &existing) == nil {
			incoming = mergeXAIQuotaState(existing, incoming)
			if entry.AuthIndex == "" {
				entry.AuthIndex = existingEntries[0].AuthIndex
			}
			if entry.IdentityFingerprint == "" {
				entry.IdentityFingerprint = existingEntries[0].IdentityFingerprint
			}
		}
	}
	entry.Data, err = json.Marshal(incoming)
	if err != nil {
		return err
	}
	return s.SetQuotaCache(ctx, entry)
}

func GetXAIQuotaState(ctx context.Context, fileName string) (map[string]any, bool, error) {
	entries, err := GetQuotaCache(ctx, "xai", strings.TrimSpace(fileName))
	if err != nil || len(entries) == 0 {
		return nil, false, err
	}
	state := map[string]any{}
	if err := json.Unmarshal(entries[0].Data, &state); err != nil {
		return nil, false, err
	}
	return state, true, nil
}

func mergeXAIQuotaState(existing, incoming map[string]any) map[string]any {
	merged := cloneXAIMap(existing)
	for key, value := range incoming {
		merged[key] = value
	}
	existingBilling, _ := existing["billing"].(map[string]any)
	incomingBilling, _ := incoming["billing"].(map[string]any)
	if existingBilling == nil && incomingBilling == nil {
		return merged
	}
	billing := cloneXAIMap(existingBilling)
	for key, value := range incomingBilling {
		billing[key] = value
	}
	existingFree, _ := existingBilling["freeQuota"].(map[string]any)
	incomingFree, _ := incomingBilling["freeQuota"].(map[string]any)
	if existingFree != nil && (incomingFree == nil || xaiObservedAt(existingFree) > xaiObservedAt(incomingFree)) {
		billing["freeQuota"] = existingFree
	}
	merged["billing"] = billing
	return merged
}

func cloneXAIMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = value
	}
	return cloned
}

func xaiObservedAt(value map[string]any) int64 {
	switch typed := value["observedAt"].(type) {
	case float64:
		return int64(typed)
	case int64:
		return typed
	case int:
		return int64(typed)
	case string:
		parsed, _ := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed
	default:
		return 0
	}
}

func xaiRateLimitSnapshot(header http.Header, model string, observedAt time.Time) map[string]any {
	limitTokens, okLimit := xaiHeaderInt64(header, "x-ratelimit-limit-tokens")
	remainingTokens, okRemaining := xaiHeaderInt64(header, "x-ratelimit-remaining-tokens")
	if !okLimit || !okRemaining || limitTokens <= 0 {
		return nil
	}
	if remainingTokens > limitTokens {
		remainingTokens = limitTokens
	}
	usedTokens := limitTokens - remainingTokens
	snapshot := map[string]any{
		"source":          "rate_limit_headers",
		"windowKind":      "rolling_24h",
		"usedTokens":      usedTokens,
		"limitTokens":     limitTokens,
		"remainingTokens": remainingTokens,
		"observedAt":      observedAt.UnixMilli(),
		"exhausted":       remainingTokens == 0,
	}
	if value, ok := xaiHeaderInt64(header, "x-ratelimit-limit-requests"); ok {
		snapshot["limitRequests"] = value
	}
	if value, ok := xaiHeaderInt64(header, "x-ratelimit-remaining-requests"); ok {
		snapshot["remainingRequests"] = value
	}
	if model = strings.TrimSpace(model); model != "" {
		snapshot["model"] = model
	}
	return snapshot
}

func xaiExhaustedQuotaSnapshot(body []byte, fallbackModel string, observedAt time.Time) map[string]any {
	snapshot := map[string]any{
		"source":     "free_usage_exhausted",
		"windowKind": "rolling_24h",
		"observedAt": observedAt.UnixMilli(),
		"exhausted":  true,
	}
	if matches := xaiFreeQuotaUsagePattern.FindSubmatch(body); len(matches) == 3 {
		used, usedErr := strconv.ParseInt(string(matches[1]), 10, 64)
		limit, limitErr := strconv.ParseInt(string(matches[2]), 10, 64)
		if usedErr == nil && limitErr == nil && limit > 0 {
			snapshot["usedTokens"] = used
			snapshot["limitTokens"] = limit
			snapshot["remainingTokens"] = int64(0)
		}
	}
	model := strings.TrimSpace(xaiJSONField(body, "model"))
	if model == "" {
		if matches := xaiFreeQuotaModelPattern.FindSubmatch(body); len(matches) == 2 {
			model = string(matches[1])
		}
	}
	if model == "" {
		model = strings.TrimSpace(fallbackModel)
	}
	model = strings.TrimRight(model, ".,;:!?\"'()[]{}")
	if model != "" {
		snapshot["model"] = model
	}
	return snapshot
}

func xaiFreeQuotaExhausted(body []byte) bool {
	lower := strings.ToLower(string(body))
	return strings.Contains(lower, "subscription:free-usage-exhausted") ||
		strings.Contains(lower, "used all the included free usage")
}

func xaiHeaderInt64(header http.Header, key string) (int64, bool) {
	if header == nil {
		return 0, false
	}
	value, err := strconv.ParseInt(strings.TrimSpace(header.Get(key)), 10, 64)
	return value, err == nil && value >= 0
}

func xaiJSONField(body []byte, key string) string {
	var value map[string]any
	if json.Unmarshal(body, &value) != nil {
		return ""
	}
	text, _ := value[key].(string)
	return text
}
