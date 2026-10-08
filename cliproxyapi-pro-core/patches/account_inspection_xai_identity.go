package management

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage"
)

// BindQuotaCacheIdentity keeps client quota observations attached to the auth
// snapshot that produced them. In particular, a delayed manual refresh must
// never be relabeled as a newly uploaded account with the same filename.
func (h *Handler) BindQuotaCacheIdentity(c *gin.Context) {
	if !strings.HasSuffix(c.Request.URL.Path, "/usage/quota-cache") {
		c.Next()
		return
	}
	if c.Request.Method == http.MethodGet {
		if stats := c.Query("stats"); stats == "1" || stats == "true" {
			c.Next()
			return
		}
		entries, err := embeddedusage.GetQuotaCache(c.Request.Context(), strings.TrimSpace(c.Query("provider")), strings.TrimSpace(c.Query("fileName")))
		if err != nil {
			c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
			return
		}
		visible := make([]embeddedusage.QuotaCacheEntry, 0, len(entries))
		for _, entry := range entries {
			if strings.EqualFold(entry.Provider, "xai") {
				auth, found := h.lookupAuthFile(entry.FileName, "")
				if !found || auth == nil || !strings.EqualFold(auth.Provider, "xai") || entry.IdentityFingerprint == "" || xaiAccountQuotaIdentityFingerprint(accountFromAuth(auth)) != entry.IdentityFingerprint {
					continue
				}
			}
			visible = append(visible, entry)
		}
		c.AbortWithStatusJSON(http.StatusOK, gin.H{"items": visible})
		return
	}
	if c.Request.Method != http.MethodPut {
		c.Next()
		return
	}
	raw, err := io.ReadAll(c.Request.Body)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	var entry embeddedusage.QuotaCacheEntry
	if json.Unmarshal(raw, &entry) != nil || !strings.EqualFold(strings.TrimSpace(entry.Provider), "xai") {
		c.Next()
		return
	}
	entry.FileName = strings.TrimSpace(entry.FileName)
	auth, found := h.lookupAuthFile(entry.FileName, "")
	if !found || auth == nil || !strings.EqualFold(auth.Provider, "xai") || entry.IdentityFingerprint == "" || xaiAccountQuotaIdentityFingerprint(accountFromAuth(auth)) != entry.IdentityFingerprint {
		c.AbortWithStatusJSON(http.StatusConflict, gin.H{"error": "xAI quota observation identity is missing or no longer current"})
		return
	}
	// Preserve the submitted fingerprint: a replacement racing the store write
	// can make this record obsolete, but can never give it the replacement's identity.
	entry.Provider = "xai"
	entry.ID = "xai:" + entry.FileName
	entry.AuthIndex = auth.Index
	entry.XAIRegistrationEpoch = auth.RegistrationEpoch
	raw, err = json.Marshal(entry)
	if err != nil {
		c.AbortWithStatusJSON(http.StatusBadRequest, gin.H{"error": err.Error()})
		return
	}
	c.Request.Body = io.NopCloser(bytes.NewReader(raw))
	c.Next()
}
