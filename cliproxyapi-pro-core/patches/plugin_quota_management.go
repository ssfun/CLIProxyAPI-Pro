package management

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
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
	// Explicit upstream plugin/provider selection keeps its original response contract.
	if strings.TrimSpace(req.PluginID) != "" || strings.TrimSpace(req.Provider) != "" {
		h.forwardCredentialQuota(c, req)
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
	result, statusCode, errorLabel, errFetch := h.fetchAndPersistPluginQuota(c.Request.Context(), auth)
	if !result.Handled && (statusCode == http.StatusNotFound || statusCode == http.StatusServiceUnavailable) {
		// Upstream also supports declarative metadata probes without a quota plugin.
		h.forwardCredentialQuota(c, req)
		return
	}
	if errFetch != nil {
		c.JSON(statusCode, gin.H{"error": errorLabel, "message": errFetch.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"auth_index":         auth.Index,
		"plugin_id":          result.PluginID,
		"snapshot":           result.Snapshot,
		"subscription":       result.Response.Subscription,
		"groups":             result.Response.Groups,
		"serverTimeOffsetMs": result.Response.ServerTimeOffsetMs,
	})
}

func (h *Handler) forwardCredentialQuota(c *gin.Context, req credentialQuotaRequest) {
	raw, _ := json.Marshal(req)
	c.Request.Body = io.NopCloser(strings.NewReader(string(raw)))
	c.Request.ContentLength = int64(len(raw))
	h.FetchCredentialQuota(c)
}

func (h *Handler) fetchAndPersistPluginQuota(ctx context.Context, auth *coreauth.Auth) (pluginhost.QuotaResult, int, string, error) {
	if h == nil || auth == nil {
		return pluginhost.QuotaResult{}, http.StatusServiceUnavailable, "plugin quota service unavailable", fmt.Errorf("plugin quota service unavailable")
	}
	h.mu.Lock()
	host := h.pluginHost
	manager := h.authManager
	h.mu.Unlock()
	if host == nil || manager == nil {
		return pluginhost.QuotaResult{}, http.StatusServiceUnavailable, "plugin quota service unavailable", fmt.Errorf("plugin quota service unavailable")
	}
	previous := loadPluginQuotaSnapshot(ctx, auth.Provider, auth.FileName, auth.Index)
	result := host.FetchProQuota(ctx, auth, previous)
	if !result.Handled {
		return result, http.StatusNotFound, "quota provider not found", fmt.Errorf("quota provider not found")
	}
	if result.Err != nil {
		return result, http.StatusBadGateway, "quota fetch failed", result.Err
	}
	if result.Auth != nil {
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
