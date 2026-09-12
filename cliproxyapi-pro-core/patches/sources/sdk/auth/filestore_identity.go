package auth

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	claudeauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/claude"
	cliproxyauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const reusedExistingAuthIdentityAttribute = "reused_existing_auth_identity"

// TakeReusedExistingAuthIdentity reports and consumes the transient marker set
// when Save updates an existing strong provider identity in place.
func TakeReusedExistingAuthIdentity(auth *cliproxyauth.Auth) bool {
	if auth == nil || auth.Attributes == nil {
		return false
	}
	reused := strings.EqualFold(strings.TrimSpace(auth.Attributes[reusedExistingAuthIdentityAttribute]), "true")
	delete(auth.Attributes, reusedExistingAuthIdentityAttribute)
	return reused
}

func authMetadataString(metadata map[string]any, keys ...string) string {
	for _, key := range keys {
		if value, ok := metadata[key].(string); ok {
			if value = strings.TrimSpace(value); value != "" {
				return value
			}
		}
	}
	return ""
}

type providerFileIdentity struct {
	provider     string
	strongKey    string
	companion    string
	organization string
}

func resolveProviderFileIdentity(provider string, metadata map[string]any) (providerFileIdentity, bool) {
	provider = strings.ToLower(strings.TrimSpace(provider))
	if provider == "" || metadata == nil {
		return providerFileIdentity{}, false
	}
	email := strings.ToLower(authMetadataString(metadata, "email", "client_email"))
	identity := providerFileIdentity{provider: provider}
	switch provider {
	case "codex":
		identity.strongKey = authMetadataString(metadata, "account_id", "chatgpt_account_id", "chatgptAccountId")
		identity.companion = email
	case "claude":
		identity.strongKey = authMetadataString(metadata, "account_uuid", "account_id")
		identity.companion = email
		identity.organization = authMetadataString(metadata, "organization_uuid")
	case "xai":
		identity.strongKey = authMetadataString(metadata, "sub", "subject", "user_id")
		identity.companion = email
	case "antigravity":
		// Google userinfo supplies a verified account email and the current
		// upstream already treats it as the credential filename identity.
		identity.strongKey = email
	case "gemini-cli", "gemini", "vertex":
		identity.strongKey = authMetadataString(metadata, "project_id")
		identity.companion = email
	default:
		// Providers such as Kimi currently expose only device/token material.
		// Treating those values as account identity would merge unrelated users.
		return providerFileIdentity{}, false
	}
	if identity.strongKey == "" {
		return providerFileIdentity{}, false
	}
	return identity, true
}

func authProviderFileIdentity(auth *cliproxyauth.Auth) (providerFileIdentity, bool) {
	if auth == nil {
		return providerFileIdentity{}, false
	}
	provider := strings.TrimSpace(auth.Provider)
	if provider == "" && auth.Metadata != nil {
		provider = authMetadataString(auth.Metadata, "type")
	}
	return resolveProviderFileIdentity(provider, auth.Metadata)
}

func storedProviderFileIdentity(path string) (providerFileIdentity, bool) {
	raw, err := os.ReadFile(path)
	if err != nil || len(raw) == 0 {
		return providerFileIdentity{}, false
	}
	metadata := make(map[string]any)
	if err = json.Unmarshal(raw, &metadata); err != nil {
		return providerFileIdentity{}, false
	}
	return resolveProviderFileIdentity(authMetadataString(metadata, "type"), metadata)
}

// reuseExistingProviderIdentity follows an active-account upsert model: strong
// identity is only used to find the existing credential, while its file-backed
// ID and stable index remain the canonical history/runtime keys.
func (s *FileTokenStore) reuseExistingProviderIdentity(auth *cliproxyauth.Auth) error {
	if auth == nil {
		return nil
	}
	if auth.Attributes != nil {
		delete(auth.Attributes, reusedExistingAuthIdentityAttribute)
		if strings.TrimSpace(auth.Attributes[cliproxyauth.AttributePath]) != "" ||
			strings.TrimSpace(auth.Attributes[cliproxyauth.AttributeSource]) != "" {
			return nil
		}
	}
	identity, ok := authProviderFileIdentity(auth)
	if !ok {
		return nil
	}
	// Canonical Claude filenames and legacy migration are owned by upstream.
	// Reusing a legacy path here would let the caller delete the newly saved file.
	if identity.provider == "claude" {
		email := authMetadataString(auth.Metadata, "email")
		account := authMetadataString(auth.Metadata, "account_uuid")
		name := strings.TrimSpace(auth.FileName)
		if name == "" {
			name = strings.TrimSpace(auth.ID)
		}
		if email != "" && (identity.organization != "" || account != "") &&
			strings.EqualFold(filepath.Base(name), claudeauth.CredentialFileName(email, identity.organization, account)) {
			return nil
		}
	}
	baseDir := s.baseDirSnapshot()
	if baseDir == "" {
		return nil
	}
	entries, err := os.ReadDir(baseDir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("auth filestore: list provider identities: %w", err)
	}

	matches := make([]string, 0, 1)
	for _, entry := range entries {
		if entry == nil || entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".json") {
			continue
		}
		path := filepath.Join(baseDir, entry.Name())
		existing, exists := storedProviderFileIdentity(path)
		if !exists || existing.provider != identity.provider || existing.strongKey != identity.strongKey {
			continue
		}
		if identity.provider == "claude" && !strings.EqualFold(identity.organization, existing.organization) {
			continue
		}
		if identity.companion != "" && existing.companion != "" &&
			!strings.EqualFold(identity.companion, existing.companion) {
			continue
		}
		matches = append(matches, path)
	}
	if len(matches) == 0 {
		return nil
	}
	if len(matches) > 1 {
		return fmt.Errorf("auth filestore: multiple %s credentials match the same strong identity", identity.provider)
	}

	path := matches[0]
	id := s.idFor(path, baseDir)
	auth.ID = id
	auth.Index = ""
	auth.FileName = id
	if auth.Attributes == nil {
		auth.Attributes = make(map[string]string)
	}
	auth.Attributes[cliproxyauth.AttributePath] = path
	auth.Attributes[cliproxyauth.AttributeSource] = path
	auth.Attributes[cliproxyauth.AttributeSourceBackend] = cliproxyauth.AuthSourceFile
	auth.Attributes[reusedExistingAuthIdentityAttribute] = "true"
	return nil
}
