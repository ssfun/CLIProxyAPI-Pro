package management

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const routingProtectionMetadataKey = prorouting.ProtectionMetadataKey

// This file owns the Management Handler adapter for durable Auth mutations.
// Inspection and routing both depend on this host capability, but neither
// feature owns virtual-source persistence or runtime Auth synchronization.

func syncAuthInspectionLastError(auth *coreauth.Auth, lastError *coreauth.Error) {
	if auth == nil {
		return
	}
	auth.LastError = lastError
	if lastError == nil {
		if auth.Metadata != nil {
			delete(auth.Metadata, "last_error")
		}
		return
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["last_error"] = map[string]any{
		"code":          lastError.Code,
		"message":       lastError.Message,
		"retryable":     lastError.Retryable,
		"http_status":   lastError.HTTPStatus,
		"source":        "account_inspection",
		"updated_at_ms": time.Now().UnixMilli(),
	}
}

func setProAuthDisabledState(auth *coreauth.Auth, disabled bool) {
	if auth == nil {
		return
	}
	auth.Disabled = disabled
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	if disabled {
		clearRoutingProtectionOwnership(auth)
	}
	auth.Metadata["disabled"] = disabled
	if disabled {
		auth.Status = coreauth.StatusDisabled
		auth.StatusMessage = "disabled by scheduled account inspection"
	} else {
		if authInspectionLastErrorCode(auth) != "" || auth.Unavailable {
			auth.Status = coreauth.StatusError
			if auth.LastError != nil {
				auth.StatusMessage = strings.TrimSpace(auth.LastError.Message)
			} else if raw, ok := auth.Metadata["last_error"].(map[string]any); ok {
				auth.StatusMessage = strings.TrimSpace(stringFromAny(raw["message"]))
			} else if auth.StatusMessage == "disabled by scheduled account inspection" {
				auth.StatusMessage = "account remains unavailable"
			}
		} else {
			auth.Status = coreauth.StatusActive
			auth.StatusMessage = ""
		}
	}
	auth.UpdatedAt = time.Now()
}

func clearRoutingProtectionOwnership(auth *coreauth.Auth) {
	if auth != nil {
		prorouting.InspectionOwnsStatus(auth.Metadata)
	}
}

func pluginVirtualSourcePath(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	sourcePath := strings.TrimSpace(authAttribute(auth, coreauth.AttributeVirtualSource))
	if sourcePath == "" {
		sourcePath = strings.TrimSpace(authAttribute(auth, "path"))
	}
	return sourcePath
}

func sameAuthSourcePath(left string, right string) bool {
	left = strings.TrimSpace(left)
	right = strings.TrimSpace(right)
	if left == "" || right == "" {
		return false
	}
	if strings.EqualFold(filepath.Clean(left), filepath.Clean(right)) {
		return true
	}
	leftAbs, leftErr := filepath.Abs(left)
	rightAbs, rightErr := filepath.Abs(right)
	return leftErr == nil && rightErr == nil && strings.EqualFold(filepath.Clean(leftAbs), filepath.Clean(rightAbs))
}

func isPluginVirtualRuntimeOnlyAuth(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	return strings.EqualFold(strings.TrimSpace(authAttribute(auth, "runtime_only")), "true")
}

func (h *Handler) preferredAuthForPluginVirtualWrite(auth *coreauth.Auth) *coreauth.Auth {
	if auth == nil || !coreauth.IsPluginVirtualAuth(auth) || h == nil || h.authManager == nil {
		return auth
	}
	sourcePath := pluginVirtualSourcePath(auth)
	if sourcePath == "" {
		return auth
	}
	var firstVirtual *coreauth.Auth
	for _, candidate := range h.authManager.List() {
		if candidate == nil || !sameAuthSourcePath(pluginVirtualSourcePath(candidate), sourcePath) {
			continue
		}
		if !coreauth.IsPluginVirtualAuth(candidate) {
			return candidate
		}
		if firstVirtual == nil {
			firstVirtual = candidate
		}
		if !isPluginVirtualRuntimeOnlyAuth(candidate) {
			return candidate
		}
	}
	if firstVirtual != nil {
		return firstVirtual
	}
	return auth
}

func savePluginVirtualAuthToSourceFile(auth *coreauth.Auth) error {
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	sourcePath := pluginVirtualSourcePath(auth)
	if sourcePath == "" {
		return fmt.Errorf("plugin virtual auth source path unavailable")
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["disabled"] = auth.Disabled
	if coreauth.IsPluginVirtualAuth(auth) {
		return savePluginVirtualManagedMetadataToSourceFile(sourcePath, auth)
	}
	type metadataSetter interface {
		SetMetadata(map[string]any)
	}
	if setter, ok := auth.Storage.(metadataSetter); ok {
		setter.SetMetadata(auth.Metadata)
	}
	if auth.Storage != nil {
		return auth.Storage.SaveTokenToFile(sourcePath)
	}
	raw, err := json.Marshal(auth.Metadata)
	if err != nil {
		return err
	}
	return os.WriteFile(sourcePath, append(raw, '\n'), 0o600)
}

func savePluginVirtualManagedMetadataToSourceFile(sourcePath string, auth *coreauth.Auth) error {
	source, err := readPluginVirtualSourceMetadata(sourcePath)
	if err != nil {
		return err
	}
	return writePluginVirtualManagedMetadataToSourceFile(sourcePath, auth, source)
}

func readPluginVirtualSourceMetadata(sourcePath string) (map[string]any, error) {
	rawSource, err := os.ReadFile(sourcePath)
	if err != nil && !os.IsNotExist(err) {
		return nil, err
	}
	source := make(map[string]any)
	if len(bytes.TrimSpace(rawSource)) > 0 {
		if err = json.Unmarshal(rawSource, &source); err != nil {
			return nil, fmt.Errorf("decode plugin virtual auth source: %w", err)
		}
		if source == nil {
			source = make(map[string]any)
		}
	}
	return source, nil
}

func writePluginVirtualManagedMetadataToSourceFile(sourcePath string, auth *coreauth.Auth, source map[string]any) error {
	if source == nil {
		source = make(map[string]any)
	}
	source["disabled"] = auth.Disabled
	if value, ok := auth.Metadata["last_error"]; ok {
		source["last_error"] = value
	} else {
		delete(source, "last_error")
	}
	delete(source, "quota_cache")
	if value, ok := auth.Metadata[prorouting.ProtectionMetadataKey]; ok {
		source[prorouting.ProtectionMetadataKey] = value
	} else {
		delete(source, prorouting.ProtectionMetadataKey)
	}
	raw, err := json.Marshal(source)
	if err != nil {
		return err
	}
	return writeExistingAuthFile(sourcePath, append(raw, '\n'))
}

func writeExistingAuthFile(path string, raw []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if err = file.Chmod(0o600); err != nil {
		_ = file.Close()
		return err
	}
	if _, err = file.Write(raw); err != nil {
		_ = file.Close()
		return err
	}
	return file.Close()
}

func (h *Handler) updatePluginVirtualRuntimeAuths(ctx context.Context, sourceAuth *coreauth.Auth, mutate func(*coreauth.Auth)) error {
	if h == nil || h.authManager == nil || sourceAuth == nil || mutate == nil {
		return fmt.Errorf("plugin virtual auth update is unavailable")
	}
	sourcePath := pluginVirtualSourcePath(sourceAuth)
	if sourcePath == "" {
		mutate(sourceAuth)
		updated, err := h.authManager.Update(ctx, sourceAuth)
		if err != nil {
			return err
		}
		if updated == nil {
			return fmt.Errorf("plugin virtual auth no longer exists")
		}
		return nil
	}
	updatedCount := 0
	for _, candidate := range h.authManager.List() {
		if candidate == nil || !sameAuthSourcePath(pluginVirtualSourcePath(candidate), sourcePath) {
			continue
		}
		mutate(candidate)
		updated, err := h.authManager.Update(ctx, candidate)
		if err != nil {
			return err
		}
		if updated == nil {
			return fmt.Errorf("plugin virtual auth %q no longer exists", candidate.ID)
		}
		updatedCount++
	}
	if updatedCount == 0 {
		return fmt.Errorf("plugin virtual auth source no longer has runtime identities")
	}
	return nil
}

func (h *Handler) updateProAuth(ctx context.Context, authIndex string, mutate func(*coreauth.Auth)) error {
	if h == nil || h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	h.proAuthMutationMu.Lock()
	defer h.proAuthMutationMu.Unlock()
	return h.updateProAuthLocked(ctx, authIndex, mutate)
}

func (h *Handler) updateProAuthLocked(ctx context.Context, authIndex string, mutate func(*coreauth.Auth)) error {
	auth := h.authByIndex(authIndex)
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	if mutate == nil {
		return nil
	}
	if coreauth.IsPluginVirtualAuth(auth) {
		sourceAuth := h.preferredAuthForPluginVirtualWrite(auth)
		var sourceMetadata map[string]any
		if coreauth.IsPluginVirtualAuth(sourceAuth) {
			var err error
			sourceMetadata, err = readPluginVirtualSourceMetadata(pluginVirtualSourcePath(sourceAuth))
			if err != nil {
				return err
			}
		}
		mutate(sourceAuth)
		if err := h.updatePluginVirtualRuntimeAuths(ctx, sourceAuth, mutate); err != nil {
			return err
		}
		if coreauth.IsPluginVirtualAuth(sourceAuth) {
			return writePluginVirtualManagedMetadataToSourceFile(pluginVirtualSourcePath(sourceAuth), sourceAuth, sourceMetadata)
		}
		return savePluginVirtualAuthToSourceFile(sourceAuth)
	}
	mutate(auth)
	updated, err := h.authManager.Update(ctx, auth)
	if err != nil {
		return err
	}
	if updated == nil {
		return fmt.Errorf("auth not found")
	}
	return nil
}

func (h *Handler) updateProErrorAuth(ctx context.Context, authIndex string, mutate func(*coreauth.Auth)) error {
	if h == nil || h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	h.proAuthMutationMu.Lock()
	defer h.proAuthMutationMu.Unlock()
	auth := h.authByIndex(authIndex)
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	if !coreauth.IsPluginVirtualAuth(auth) {
		return h.updateProAuthLocked(ctx, authIndex, mutate)
	}
	if mutate == nil {
		return nil
	}
	mutate(auth)
	updated, err := h.authManager.Update(ctx, auth)
	if err != nil {
		return err
	}
	if updated == nil {
		return fmt.Errorf("auth not found")
	}
	return nil
}

func authInspectionLastErrorCode(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.LastError != nil {
		return strings.TrimSpace(auth.LastError.Code)
	}
	raw, ok := auth.Metadata["last_error"].(map[string]any)
	if !ok {
		return ""
	}
	return stringFromAny(raw["code"])
}

func isInspectionAuthErrorCode(code string) bool {
	switch strings.TrimSpace(code) {
	case "inspection_http_error", "inspection_probe_error", "antigravity_deep_probe_error", "xai_deep_probe_error", "token_refresh_error":
		return true
	default:
		return false
	}
}

func stringFromAny(value any) string {
	switch v := value.(type) {
	case string:
		return strings.TrimSpace(v)
	case fmt.Stringer:
		return strings.TrimSpace(v.String())
	default:
		return ""
	}
}
