package management

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	"github.com/router-for-me/CLIProxyAPI/v7/sdk/pluginapi"
)

// RegisterPluginQuotaRoutes registers the host-owned normalized quota endpoint.
func (h *Handler) RegisterPluginQuotaRoutes(group *gin.RouterGroup) {
	if h == nil || group == nil {
		return
	}
	group.POST("/quota/fetch", h.FetchPluginQuota)
}

// FetchPluginQuota asks the provider plugin for one auth's current quota and persists it.
func (h *Handler) FetchPluginQuota(c *gin.Context) {
	var req struct {
		AuthIndex string `json:"auth_index"`
	}
	if errBind := c.ShouldBindJSON(&req); errBind != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	authIndex := strings.TrimSpace(req.AuthIndex)
	if authIndex == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "auth_index is required"})
		return
	}

	h.mu.Lock()
	host := h.pluginHost
	manager := h.authManager
	h.mu.Unlock()
	if host == nil || manager == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "plugin quota service unavailable"})
		return
	}
	auth := h.authByIndex(authIndex)
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	previous := loadPluginQuotaSnapshot(c, auth.Provider, auth.FileName, auth.Index)
	result := host.FetchQuota(c.Request.Context(), auth, previous)
	if !result.Handled {
		c.JSON(http.StatusNotFound, gin.H{"error": "quota provider not found"})
		return
	}
	if result.Err != nil {
		c.JSON(http.StatusBadGateway, gin.H{"error": "quota fetch failed", "message": result.Err.Error()})
		return
	}
	if result.Auth != nil {
		updated, errUpdate := manager.Update(c.Request.Context(), result.Auth)
		if errUpdate != nil {
			c.JSON(http.StatusInternalServerError, gin.H{"error": "auth update failed", "message": errUpdate.Error()})
			return
		}
		if updated == nil {
			c.JSON(http.StatusConflict, gin.H{"error": "auth update target no longer exists"})
			return
		}
	}
	if errPersist := persistPluginQuotaSnapshot(c, auth.Provider, auth.FileName, auth.Index, result.Snapshot); errPersist != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": "quota persistence failed", "message": errPersist.Error()})
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"auth_index": auth.Index,
		"plugin_id":  result.PluginID,
		"snapshot":   result.Snapshot,
	})
}

func loadPluginQuotaSnapshot(c *gin.Context, provider, fileName, authIndex string) *pluginapi.QuotaSnapshot {
	entries, errGet := embeddedusage.GetQuotaCache(c.Request.Context(), provider, quotaFileName(fileName, authIndex))
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

func persistPluginQuotaSnapshot(c *gin.Context, provider, fileName, authIndex string, snapshot pluginapi.QuotaSnapshot) error {
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
	return embeddedusage.SetQuotaCache(c.Request.Context(), embeddedusage.QuotaCacheEntry{
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
