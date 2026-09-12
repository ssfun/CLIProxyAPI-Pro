package api

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/observability"
	sdkaccess "github.com/router-for-me/CLIProxyAPI/v7/sdk/access"
)

func selfQueryAuth(manager *sdkaccess.Manager) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Header("Cache-Control", "no-store")
		c.Header("Referrer-Policy", "no-referrer")
		c.Header("X-Content-Type-Options", "nosniff")
		parts := strings.Fields(c.GetHeader("Authorization"))
		if manager == nil || len(parts) != 2 || !strings.EqualFold(parts[0], "Bearer") {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			return
		}
		// Authenticate a clean request so query-string and provider-specific
		// credentials cannot override the explicit Bearer credential.
		request := c.Request.Clone(c.Request.Context())
		request.Header = make(http.Header)
		request.Header.Set("Authorization", "Bearer "+parts[1])
		request.URL.RawQuery = ""
		validate := func() bool {
			result, err := manager.Authenticate(c.Request.Context(), request)
			return err == nil && result != nil && result.Provider == sdkaccess.DefaultAccessProviderName && result.Principal == parts[1]
		}
		if !validate() {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Invalid API key"})
			return
		}
		identity, err := apikeypolicy.NewAuthenticatedAPIKeyIdentity(parts[1])
		if err != nil {
			c.AbortWithStatus(http.StatusUnauthorized)
			return
		}
		c.Set("selfQueryValidate", validate)
		c.Request = c.Request.WithContext(apikeypolicy.WithIdentity(c.Request.Context(), identity))
		c.Next()
	}
}

func (s *Server) registerSelfQueryRoutes() {
	queries := make(chan struct{}, 16)
	streams := make(chan struct{}, 64)
	group := s.engine.Group("/v0/self")
	group.Use(selfQueryAuth(s.accessManager))
	group.GET("/:section", func(c *gin.Context) {
		semaphore := queries
		if c.Param("section") == "stream" {
			semaphore = streams
		}
		select {
		case semaphore <- struct{}{}:
			defer func() { <-semaphore }()
		default:
			c.Header("Retry-After", "3")
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Please retry shortly"})
			return
		}
		if c.Param("section") != "quota" {
			observability.HandleSelfQuery(c)
			return
		}
		identity, _ := apikeypolicy.IdentityFromContext(c.Request.Context())
		quota, err := s.apiKeyPolicy.QuerySelfQuota(c.Request.Context(), identity)
		if err != nil {
			c.JSON(http.StatusServiceUnavailable, gin.H{"error": "Quota temporarily unavailable"})
			return
		}
		c.JSON(http.StatusOK, quota)
	})
}
