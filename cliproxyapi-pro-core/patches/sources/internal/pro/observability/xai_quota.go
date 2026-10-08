package observability

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
)

const XAIQuotaParserVersion = proquota.XAIParserVersion

type XAIQuotaObservation = proquota.XAIObservation

var xaiQuotaCacheMu sync.Mutex

func ObserveXAIQuotaResponse(ctx context.Context, observation XAIQuotaObservation) error {
	mutation, ok, err := proquota.BuildXAIMutation(observation)
	if err != nil || !ok {
		return err
	}
	return MergeXAIQuotaCache(ctx, QuotaCacheEntry{
		ID: mutation.ID, Provider: mutation.Provider, FileName: mutation.FileName,
		AuthIndex: mutation.AuthIndex, IdentityFingerprint: mutation.IdentityFingerprint,
		Data: mutation.Data, CachedAt: mutation.CachedAt, ObservedAt: mutation.ObservedAt,
		AccessedAt: mutation.AccessedAt, Version: mutation.Version,
	})
}

func xaiQuotaHeadersStatus(status int) bool { return proquota.XAIHeadersStatus(status) }

// MergeXAIQuotaCache merges billing refreshes and request-path free quota
// observations without allowing either writer to discard newer fields.
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
	if len(existingEntries) > 0 && entry.IdentityFingerprint != "" && entry.IdentityFingerprint == existingEntries[0].IdentityFingerprint {
		existing := map[string]any{}
		if json.Unmarshal(existingEntries[0].Data, &existing) == nil {
			incoming = proquota.MergeXAIState(existing, incoming)
			if entry.AuthIndex == "" {
				entry.AuthIndex = existingEntries[0].AuthIndex
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

// Compatibility helpers keep existing package-level tests and downstream
// patches stable while the policy implementation lives in pro/quota.
func mergeXAIQuotaState(existing, incoming map[string]any) map[string]any {
	return proquota.MergeXAIState(existing, incoming)
}

func xaiRateLimitSnapshot(header http.Header, model string, observedAt time.Time) map[string]any {
	return proquota.XAIRateLimitSnapshot(header, model, observedAt)
}

func xaiExhaustedQuotaSnapshot(body []byte, fallbackModel string, observedAt time.Time) map[string]any {
	return proquota.XAIExhaustedQuotaSnapshot(body, fallbackModel, observedAt)
}

func xaiFreeQuotaExhausted(body []byte) bool { return proquota.XAIFreeQuotaExhausted(body) }
