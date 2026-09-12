package management

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
)

// GetManagementModels lists the runtime catalog under management authentication.
// Business API key policies apply only to public model discovery.
func (h *Handler) GetManagementModels(c *gin.Context) {
	models := registry.GetGlobalRegistry().GetAvailableModels("openai")
	if models == nil {
		models = []map[string]any{}
	}
	c.Header("Cache-Control", "no-store")
	c.JSON(http.StatusOK, gin.H{"object": "list", "data": models})
}
