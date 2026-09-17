package oauthpolicy

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"

	"github.com/gin-gonic/gin"
	modelconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/config"
)

func RegisterManagementRoutes(group *gin.RouterGroup, service *Service) {
	if group == nil {
		return
	}
	management := &managementHandler{service: service}
	group.GET("/pro/oauth-policy/config", management.getConfig)
	group.PUT("/pro/oauth-policy/config", management.putConfig)
	group.GET("/pro/oauth-policy/status", management.getStatus)
	group.GET("/pro/oauth-policy/effective", management.getEffective)
	group.POST("/pro/oauth-policy/refresh", management.refresh)
	// Deprecated compatibility aliases. New clients must use /pro/oauth-policy.
	group.GET("/pro/oauth-model-policy/config", management.deprecated, management.getConfig)
	group.PUT("/pro/oauth-model-policy/config", management.deprecated, management.putConfig)
	group.GET("/pro/oauth-model-policy/status", management.deprecated, management.getStatus)
}

type managementHandler struct{ service *Service }

func (h *managementHandler) deprecated(c *gin.Context) {
	c.Header("Deprecation", "true")
	c.Header("Link", `</v0/management/pro/oauth-policy/config>; rel="successor-version"`)
	c.Next()
}

func (h *managementHandler) available(c *gin.Context) bool {
	if h != nil && h.service != nil {
		return true
	}
	c.JSON(http.StatusServiceUnavailable, gin.H{"error": "pro_features_unavailable"})
	return false
}

func (h *managementHandler) getConfig(c *gin.Context) {
	if !h.available(c) {
		return
	}
	raw, err := modelconfig.Marshal(h.service.Config())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.Data(http.StatusOK, "application/json", raw)
}

func (h *managementHandler) putConfig(c *gin.Context) {
	if !h.available(c) {
		return
	}
	raw, err := io.ReadAll(io.LimitReader(c.Request.Body, 2<<20))
	if err != nil || len(raw) == 0 || !json.Valid(raw) {
		if err == nil {
			err = errors.New("request body must contain valid JSON")
		}
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_oauth_policy_config", "message": err.Error()})
		return
	}
	cfg, err := modelconfig.Parse(raw)
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_oauth_policy_config", "message": err.Error()})
		return
	}
	if err := h.service.UpdateConfig(c.Request.Context(), cfg); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid_oauth_policy_config", "message": err.Error()})
		return
	}
	normalized, err := modelconfig.Marshal(h.service.Config())
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	var responseConfig json.RawMessage = normalized
	c.JSON(http.StatusOK, gin.H{"config": responseConfig, "status": h.service.Status()})
}

func (h *managementHandler) getStatus(c *gin.Context) {
	if h.available(c) {
		c.JSON(http.StatusOK, h.service.Status())
	}
}

func (h *managementHandler) getEffective(c *gin.Context) {
	if h.available(c) {
		c.JSON(http.StatusOK, gin.H{"items": h.service.EffectivePolicies()})
	}
}

func (h *managementHandler) refresh(c *gin.Context) {
	if !h.available(c) {
		return
	}
	h.service.RefreshPlanDetection()
	c.JSON(http.StatusAccepted, gin.H{"status": "refreshing"})
}
