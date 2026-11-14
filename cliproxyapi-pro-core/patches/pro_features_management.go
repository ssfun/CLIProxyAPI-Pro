package management

import (
	"github.com/gin-gonic/gin"
	proapp "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/app"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/proxypool"
)

func (h *Handler) SetProApp(application *proapp.App) {
	if h == nil {
		return
	}
	h.mu.Lock()
	h.proApp = application
	var keys []string
	if h.cfg != nil {
		keys = append(keys, h.cfg.APIKeys...)
	}
	h.mu.Unlock()
	if application != nil && application.APIKeyPolicy() != nil {
		application.APIKeyPolicy().SetConfiguredAPIKeys(keys)
	}
}

func (h *Handler) proApplication() *proapp.App {
	if h == nil {
		return nil
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.proApp
}

// RegisterProFeatureRoutes delegates transport ownership to each static module.
func (h *Handler) RegisterProFeatureRoutes(group *gin.RouterGroup) {
	if h == nil || group == nil {
		return
	}
	application := h.proApplication()
	if application == nil {
		proxypool.RegisterManagementRoutes(group, nil)
		oauthpolicy.RegisterManagementRoutes(group, nil)
		return
	}
	proxypool.RegisterManagementRoutes(group, application.ProxyPool())
	oauthpolicy.RegisterManagementRoutes(group, application.OAuthPolicy())
}
