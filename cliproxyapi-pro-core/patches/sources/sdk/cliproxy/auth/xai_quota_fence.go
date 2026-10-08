package auth

import (
	"path/filepath"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
)

// BindXAIQuotaCache attaches this runtime manager to the current usage Store.
func (m *Manager) BindXAIQuotaCache() {
	embeddedusage.SetXAIQuotaCacheGuard(m.WithCurrentXAIQuotaIdentity)
}

// WithCurrentXAIQuotaIdentity serializes identity validation and the SQLite
// commit with all manager mutations, including removal, reload and replacement.
// Same-subject credential refreshes retain the registration and remain valid.
func (m *Manager) WithCurrentXAIQuotaIdentity(entry embeddedusage.QuotaCacheEntry, write func() error) error {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, auth := range m.auths {
		if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "xai") {
			continue
		}
		fileName := filepath.Base(strings.TrimSpace(auth.FileName))
		if fileName == "." || fileName == "" {
			fileName = filepath.Base(strings.TrimSpace(auth.ID))
		}
		if fileName != entry.FileName {
			continue
		}
		subject, email, credential := proquota.XAIQuotaAuthIdentity(auth.Metadata, auth.Attributes)
		if entry.IdentityFingerprint == "" || entry.IdentityFingerprint != proquota.XAIQuotaIdentityFingerprint(fileName, subject, email, credential) ||
			(entry.XAIRegistrationEpoch != 0 && entry.XAIRegistrationEpoch != auth.RegistrationEpoch) {
			return embeddedusage.ErrXAIQuotaIdentityChanged
		}
		return write()
	}
	return embeddedusage.ErrXAIQuotaIdentityChanged
}
