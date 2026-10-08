package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pluginhost"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// FetchProPluginQuota asks the provider plugin for one auth's current quota and persists it.
func (h *Handler) FetchProPluginQuota(c *gin.Context) {
	var req credentialQuotaRequest
	if errBind := c.ShouldBindJSON(&req); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	authIndex := req.resolveAuthIndex()
	if authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
		return
	}

	auth := h.authByIndex(authIndex)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	ctx := c.Request.Context()
	previous := loadPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index)
	h.mu.Lock()
	host := h.pluginHost
	h.mu.Unlock()
	result := host.FetchProQuotaWithSelection(ctx, auth, previous, req.PluginID, req.Provider)
	// Explicit selections must never fall back to a credential-local probe.
	if !result.Handled && result.Err == nil && strings.TrimSpace(req.PluginID) == "" &&
		(strings.TrimSpace(req.Provider) == "" || strings.EqualFold(strings.TrimSpace(req.Provider), strings.TrimSpace(auth.Provider))) {
		result = h.fetchCredentialQuotaProbe(ctx, auth, previous)
	}
	if !result.Handled && result.Err == nil {
		c.JSON(http.StatusNotImplemented, gin.H{"error": "no quota provider available for credential"})
		return
	}
	h.writeProQuotaResult(c, auth, result)
}

// fetchProQuotaForPlugin retains the upstream URL selection and never falls back
// to another provider or a credential-local declarative probe.
func (h *Handler) fetchProQuotaForPlugin(c *gin.Context, pluginID, authIndex string) {
	auth := h.authByIndex(authIndex)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	h.mu.Lock()
	host := h.pluginHost
	h.mu.Unlock()
	if host == nil || !host.HasQuotaProviderForPlugin(pluginID) {
		c.JSON(http.StatusNotFound, gin.H{"error": "quota provider not found for plugin"})
		return
	}
	ctx := c.Request.Context()
	previous := loadPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index)
	result := host.FetchProQuotaWithSelection(ctx, auth, previous, pluginID, "")
	h.writeProQuotaResult(c, auth, result)
}

func (h *Handler) writeProQuotaResult(c *gin.Context, auth *coreauth.Auth, result pluginhost.QuotaResult) {
	result, statusCode, errorLabel, errFetch := h.persistPluginQuotaResult(c.Request.Context(), auth, result)
	if errFetch != nil {
		c.JSON(statusCode, gin.H{"error": errorLabel, "message": errFetch.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"auth_index":         auth.Index,
		"plugin_id":          result.PluginID,
		"snapshot":           result.Snapshot,
		"subscription":       result.Response.Subscription,
		"summary":            result.Response.Summary,
		"groups":             result.Response.Groups,
		"serverTimeOffsetMs": result.Response.ServerTimeOffsetMs,
	})
}

func (h *Handler) fetchAndPersistPluginQuota(ctx context.Context, auth *coreauth.Auth) (pluginhost.QuotaResult, int, string, error) {
	if h == nil || auth == nil {
		return pluginhost.QuotaResult{}, http.StatusServiceUnavailable, "plugin quota service unavailable", fmt.Errorf("plugin quota service unavailable")
	}
	h.mu.Lock()
	host := h.pluginHost
	manager := h.authManager
	h.mu.Unlock()
	if manager == nil {
		return pluginhost.QuotaResult{}, http.StatusServiceUnavailable, "plugin quota service unavailable", fmt.Errorf("plugin quota service unavailable")
	}
	previous := loadPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index)
	result := host.FetchProQuota(ctx, auth, previous)
	if !result.Handled && result.Err == nil {
		result = h.fetchCredentialQuotaProbe(ctx, auth, previous)
	}
	return h.persistPluginQuotaResult(ctx, auth, result)
}

// Keep the unselected manual endpoint and inspection gateway on the same
// declarative fallback. Selected plugin requests never reach this helper.
func (h *Handler) fetchCredentialQuotaProbe(ctx context.Context, auth *coreauth.Auth, previous *pluginapi.QuotaSnapshot) pluginhost.QuotaResult {
	probe, ok := auth.Metadata["quota_probe"].(map[string]any)
	if !ok {
		return pluginhost.QuotaResult{}
	}
	c := &gin.Context{Request: (&http.Request{}).WithContext(ctx)}
	resp, handled, err := h.executeQuotaProbe(c, auth, probe)
	result := pluginhost.QuotaResult{Handled: handled, Response: resp, Err: err}
	if handled && err == nil {
		// Declarative responses supply quota data, never credential mutations.
		result.Snapshot, result.Err = pluginhost.NormalizeProQuotaSnapshot(auth.Provider, previous, resp)
	}
	return result
}

func (h *Handler) persistPluginQuotaResult(ctx context.Context, auth *coreauth.Auth, result pluginhost.QuotaResult) (pluginhost.QuotaResult, int, string, error) {
	if result.Err != nil {
		return result, http.StatusBadGateway, "quota fetch failed", result.Err
	}
	if !result.Handled {
		return result, http.StatusNotFound, "quota provider not found", fmt.Errorf("quota provider not found")
	}
	if result.Auth != nil {
		h.mu.Lock()
		manager := h.authManager
		h.mu.Unlock()
		if manager == nil {
			return result, http.StatusServiceUnavailable, "auth manager unavailable", fmt.Errorf("auth manager unavailable")
		}
		updated, errUpdate := manager.Update(ctx, result.Auth)
		if errUpdate != nil {
			return result, http.StatusInternalServerError, "auth update failed", fmt.Errorf("auth update failed: %w", errUpdate)
		}
		if updated == nil {
			return result, http.StatusConflict, "auth update target no longer exists", fmt.Errorf("auth update target no longer exists")
		}
	}
	if errPersist := persistPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index, result.Snapshot); errPersist != nil {
		return result, http.StatusInternalServerError, "quota persistence failed", fmt.Errorf("quota persistence failed: %w", errPersist)
	}
	if application := h.proApplication(); application != nil {
		application.RefreshAccountPolicies()
	}
	return result, http.StatusOK, "", nil
}

func loadPluginQuotaSnapshot(ctx context.Context, provider, fileName, authIndex string) *pluginapi.QuotaSnapshot {
	entries, errGet := embeddedusage.GetQuotaCache(ctx, provider, quotaFileName(fileName, authIndex))
	if errGet != nil {
		return nil
	}
	for _, entry := range entries {
		if entry.AuthIndex != "" && entry.AuthIndex != authIndex {
			continue
		}
		var snapshot pluginapi.QuotaSnapshot
		if json.Unmarshal(entry.Data, &snapshot) == nil && snapshot.SchemaVersion > 0 {
			return &snapshot
		}
	}
	return nil
}

func persistPluginQuotaSnapshot(ctx context.Context, provider, fileName, authIndex string, snapshot pluginapi.QuotaSnapshot) error {
	raw, errMarshal := json.Marshal(snapshot)
	if errMarshal != nil {
		return fmt.Errorf("marshal quota snapshot: %w", errMarshal)
	}
	now := time.Now().UnixMilli()
	observedAt := snapshot.ObservedAtMS
	if observedAt <= 0 {
		observedAt = now
	}
	provider = strings.TrimSpace(provider)
	fileName = quotaFileName(fileName, authIndex)
	fingerprint := sha256.Sum256([]byte(strings.ToLower(provider + "|" + authIndex)))
	return embeddedusage.SetQuotaCache(ctx, embeddedusage.QuotaCacheEntry{
		ID:                  "quota-provider:" + provider + ":" + authIndex,
		Provider:            provider,
		FileName:            fileName,
		AuthIndex:           authIndex,
		IdentityFingerprint: hex.EncodeToString(fingerprint[:]),
		Data:                raw,
		CachedAt:            observedAt,
		ObservedAt:          observedAt,
		AccessedAt:          now,
		Version:             snapshot.SchemaVersion,
	})
}

func quotaFileName(fileName, authIndex string) string {
	if fileName = strings.TrimSpace(fileName); fileName != "" {
		return fileName
	}
	return strings.TrimSpace(authIndex)
}
