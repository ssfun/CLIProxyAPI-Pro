package pluginhost

import (
	"context"
	"errors"
	"fmt"
	"strings"

	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

type QuotaResult struct {
	Handled        bool
	PluginID       string
	Snapshot       pluginapi.QuotaSnapshot
	Response       pluginapi.QuotaFetchResponse
	Auth           *coreauth.Auth
	UpstreamStatus int
	Err            error
}

type quotaHTTPStatusError interface {
	HTTPStatus() int
}

func quotaUpstreamStatus(err error) int {
	var statusError quotaHTTPStatusError
	if errors.As(err, &statusError) {
		return statusError.HTTPStatus()
	}
	return 0
}

func (h *Host) FetchProQuota(ctx context.Context, auth *coreauth.Auth, previous *pluginapi.QuotaSnapshot) QuotaResult {
	return h.FetchProQuotaWithSelection(ctx, auth, previous, "", "")
}

// FetchProQuotaWithSelection selects the upstream provider while retaining the auth's cache identity.
func (h *Host) FetchProQuotaWithSelection(ctx context.Context, auth *coreauth.Auth, previous *pluginapi.QuotaSnapshot, pluginID, provider string) QuotaResult {
	if h == nil || auth == nil {
		return QuotaResult{}
	}
	pluginID = strings.TrimSpace(pluginID)
	provider = strings.TrimSpace(provider)
	if provider == "" {
		provider = auth.Provider
	}
	if provider == "" {
		return QuotaResult{}
	}
	var record *capabilityRecord
	if pluginID != "" {
		record = h.quotaProviderRecordByPlugin(pluginID)
	} else {
		record = h.quotaProviderRecord(ctx, provider)
		if record == nil {
			record = h.quotaProviderRecordByPlugin(provider)
		}
	}
	if record != nil {
		resp, errFetch := h.callFetchQuota(ctx, *record, record.plugin.Capabilities.QuotaProvider, auth, previous, provider)
		if errFetch != nil {
			return QuotaResult{Handled: true, PluginID: record.id, UpstreamStatus: quotaUpstreamStatus(errFetch), Err: errFetch}
		}
		return h.quotaResultFromResponse(record.id, auth.Provider, auth, previous, resp)
	}
	// A missing explicit plugin must not silently select another plugin or legacy adapter.
	if pluginID != "" || normalizeProviderID(provider) != normalizeProviderID(auth.Provider) {
		return QuotaResult{}
	}
	if record, okLegacy := h.legacyQuotaAdapter(provider); okLegacy {
		resp, errFetch := h.fetchLegacyGeminiCLIQuota(ctx, auth)
		if errFetch != nil {
			return QuotaResult{Handled: true, PluginID: record.id, UpstreamStatus: quotaUpstreamStatus(errFetch), Err: errFetch}
		}
		return h.quotaResultFromResponse(record.id, provider, auth, previous, resp)
	}
	return QuotaResult{}
}

func (h *Host) quotaResultFromResponse(pluginID, provider string, auth *coreauth.Auth, previous *pluginapi.QuotaSnapshot, resp pluginapi.QuotaFetchResponse) QuotaResult {
	snapshot, err := NormalizeProQuotaSnapshot(provider, previous, resp)
	if err != nil {
		return QuotaResult{Handled: true, PluginID: pluginID, Err: err}
	}
	path := ""
	if auth.Attributes != nil {
		path = auth.Attributes["path"]
	}
	var updated *coreauth.Auth
	if authDataHasValue(resp.AuthUpdate) {
		updated = h.boundQuotaAuthUpdate(resp.AuthUpdate, auth, path)
	}
	return QuotaResult{Handled: true, PluginID: pluginID, Snapshot: snapshot, Response: resp, Auth: updated}
}

// NormalizeProQuotaSnapshot applies the same snapshot contract to plugin and declarative responses.
func NormalizeProQuotaSnapshot(provider string, previous *pluginapi.QuotaSnapshot, resp pluginapi.QuotaFetchResponse) (pluginapi.QuotaSnapshot, error) {
	if resp.Snapshot.SchemaVersion > pluginapi.QuotaSnapshotSchemaVersion {
		return pluginapi.QuotaSnapshot{}, fmt.Errorf("quota snapshot schema %d is newer than host schema %d", resp.Snapshot.SchemaVersion, pluginapi.QuotaSnapshotSchemaVersion)
	}
	return proquota.NormalizeSnapshot(proQuotaSnapshot(resp), normalizeProviderID(provider), previous, resp.PlanUnavailable, resp.PlanError), nil
}

// proQuotaSnapshot accepts upstream quota groups while retaining legacy Pro snapshots.
func proQuotaSnapshot(resp pluginapi.QuotaFetchResponse) pluginapi.QuotaSnapshot {
	snapshot := resp.Snapshot
	if snapshot.SchemaVersion != 0 || snapshot.Items != nil || snapshot.Plan != nil {
		return snapshot
	}
	for groupIndex, group := range resp.Groups {
		for bucketIndex, bucket := range group.Buckets {
			remaining := bucket.RemainingFraction
			label := strings.TrimSpace(group.DisplayName + " " + bucket.Window)
			snapshot.Items = append(snapshot.Items, pluginapi.QuotaItem{
				ID:    fmt.Sprintf("group:%d:bucket:%d", groupIndex, bucketIndex),
				Label: label, Kind: "quota", RemainingFraction: &remaining,
				ResetAt:  bucket.ResetTime,
				Metadata: map[string]any{"description": bucket.Description},
			})
		}
	}
	if sub := resp.Subscription; sub != nil {
		label := strings.TrimSpace(sub.TierName)
		if label == "" {
			label = sub.Plan
		}
		snapshot.Plan = &pluginapi.QuotaPlan{ID: sub.TierID, Label: label, Kind: sub.Plan}
	}
	return snapshot
}

func (h *Host) boundQuotaAuthUpdate(data pluginapi.AuthData, auth *coreauth.Auth, path string) *coreauth.Auth {
	if h == nil || auth == nil {
		return nil
	}
	data = authDataWithDefaults(data, auth)
	data.Provider = auth.Provider
	data.ID = auth.ID
	data.FileName = auth.FileName
	data.Attributes = cloneStringMap(auth.Attributes)
	updated := h.AuthDataToCoreAuth(data, path, auth.FileName)
	if updated == nil {
		return nil
	}
	updated.Provider = auth.Provider
	updated.ID = auth.ID
	updated.FileName = auth.FileName
	updated.Index = auth.Index
	updated.CreatedAt = auth.CreatedAt
	updated.Attributes = cloneStringMap(auth.Attributes)
	return updated
}

func (h *Host) callFetchQuota(ctx context.Context, record capabilityRecord, provider pluginapi.QuotaProvider, auth *coreauth.Auth, previous *pluginapi.QuotaSnapshot, selectedProvider string) (resp pluginapi.QuotaFetchResponse, err error) {
	if h == nil || provider == nil || auth == nil || h.isPluginFused(record.id) || !h.recordCurrent(record) {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("quota provider is unavailable")
	}
	resp, handled, err := h.callQuotaFetch(ctx, record, provider, pluginapi.QuotaFetchRequest{
		Plugin:       clonePluginMetadata(record.meta),
		AuthIndex:    auth.Index,
		Provider:     selectedProvider,
		AuthID:       auth.ID,
		AuthProvider: auth.Provider,
		StorageJSON:  storageJSONFromAuth(auth),
		Metadata:     cloneAnyMap(auth.Metadata),
		Attributes:   cloneStringMap(auth.Attributes),
		Previous:     proquota.CloneSnapshot(previous),
		Host:         h.hostConfigSummary(),
		HTTPClient:   h.newHTTPClient(auth, auth.Provider),
	})
	if err != nil {
		return pluginapi.QuotaFetchResponse{}, err
	}
	if !handled {
		return pluginapi.QuotaFetchResponse{}, fmt.Errorf("quota provider is unavailable")
	}
	return resp, nil
}
