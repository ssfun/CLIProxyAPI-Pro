package observability

import (
	"encoding/json"
	"strings"
)

// Legacy cache selection preserves the existing card/inspection precedence.
// Business consumers receive quota.PlanEvidence after this compatibility seam.
// isAuthCardQuotaSnapshotCompatible mirrors the Management auth-card
// persistence contract. Only rows that the card can hydrate participate in
// account-policy selection; provider-neutral plugin rows for other providers
// remain persisted without displacing a valid inspection snapshot.
func isAuthCardQuotaSnapshotCompatible(provider string, raw []byte) bool {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "gemini-cli" && isNormalizedGeminiQuotaSnapshot(raw) {
		return true
	}
	payload := map[string]any{}
	if json.Unmarshal(raw, &payload) != nil || payload == nil {
		return false
	}
	status, ok := payload["status"].(string)
	if !ok {
		return false
	}
	switch status {
	case "idle", "loading", "error":
		return true
	case "success":
	default:
		return false
	}
	switch provider {
	case "antigravity":
		groups, okGroups := payload["groups"].([]any)
		if !okGroups {
			return false
		}
		for _, rawGroup := range groups {
			group, okGroup := rawGroup.(map[string]any)
			if !okGroup {
				return false
			}
			if _, okBuckets := group["buckets"].([]any); !okBuckets {
				return false
			}
		}
		return true
	case "claude", "codex":
		_, ok = payload["windows"].([]any)
		return ok
	case "gemini-cli":
		_, ok = payload["buckets"].([]any)
		return ok
	case "kimi":
		_, ok = payload["rows"].([]any)
		return ok
	case "xai":
		_, ok = payload["billing"].(map[string]any)
		return ok
	default:
		return false
	}
}

// preferredQuotaCacheEntry mirrors Management's auth-card cache selection.
// Both consumers must resolve duplicate inspection/plugin rows to the same
// effective snapshot before applying provider-specific plan semantics.
func preferredQuotaCacheEntry(provider string, candidate, current QuotaCacheEntry) bool {
	if strings.EqualFold(strings.TrimSpace(provider), "gemini-cli") {
		candidateNormalized := isNormalizedGeminiQuotaSnapshot(candidate.Data)
		currentNormalized := isNormalizedGeminiQuotaSnapshot(current.Data)
		if candidateNormalized != currentNormalized {
			return candidateNormalized
		}
	}
	candidateFreshness := [...]int64{candidate.ObservedAt, candidate.CachedAt, candidate.StoredAt, candidate.Revision}
	currentFreshness := [...]int64{current.ObservedAt, current.CachedAt, current.StoredAt, current.Revision}
	for index := range candidateFreshness {
		if candidateFreshness[index] == currentFreshness[index] {
			continue
		}
		return candidateFreshness[index] > currentFreshness[index]
	}
	return false
}

func isNormalizedGeminiQuotaSnapshot(raw []byte) bool {
	payload := map[string]json.RawMessage{}
	if json.Unmarshal(raw, &payload) != nil {
		return false
	}
	if _, hasStatus := payload["status"]; hasStatus {
		return false
	}
	var items []json.RawMessage
	return json.Unmarshal(payload["items"], &items) == nil && items != nil
}
