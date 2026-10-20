package management

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
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

// proAuthSourceSnapshot binds bytes to the existing file that supplied them.
// Resolving symlinks preserves a source alias when the target is replaced.
type proAuthSourceSnapshot struct {
	path          string
	referencePath string
	info          os.FileInfo
	raw           []byte
	metadata      map[string]any
}

func readProAuthSource(path string) (*proAuthSourceSnapshot, error) {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return nil, err
	}
	file, err := os.Open(resolved)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() {
		return nil, fmt.Errorf("auth source is not a regular file")
	}
	raw, err := io.ReadAll(file)
	if err != nil {
		return nil, err
	}
	metadata, err := decodeProAuthSourceMetadata(raw)
	if err != nil {
		return nil, err
	}
	return &proAuthSourceSnapshot{path: resolved, referencePath: path, info: info, raw: raw, metadata: metadata}, nil
}

func decodeProAuthSourceMetadata(raw []byte) (map[string]any, error) {
	metadata := make(map[string]any)
	if len(bytes.TrimSpace(raw)) == 0 {
		return metadata, nil
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := decoder.Decode(&metadata); err != nil {
		return nil, fmt.Errorf("decode plugin virtual auth source: %w", err)
	}
	if metadata == nil {
		return nil, fmt.Errorf("plugin virtual auth source must be an object")
	}
	var trailing any
	if err := decoder.Decode(&trailing); err != io.EOF {
		return nil, fmt.Errorf("plugin virtual auth source contains trailing JSON")
	}
	return metadata, nil
}

func sameProAuthManagedMetadata(expected, current map[string]any) bool {
	project := func(source map[string]any) []byte {
		managed := make(map[string]any)
		for _, key := range []string{"disabled", "last_error", "quota_cache", prorouting.ProtectionMetadataKey} {
			if value, ok := source[key]; ok {
				managed[key] = value
			}
		}
		raw, _ := json.Marshal(managed)
		return raw
	}
	return bytes.Equal(project(expected), project(current))
}

func sameProAuthSource(left, right *proAuthSourceSnapshot) bool {
	return left != nil && right != nil && sameAuthSourcePath(left.path, right.path) && os.SameFile(left.info, right.info)
}

type proAuthSourceTempFile interface {
	io.Writer
	Name() string
	Chmod(os.FileMode) error
	Sync() error
	Close() error
}

type proAuthSourceFileOps struct {
	createTemp func(string, string) (proAuthSourceTempFile, error)
	rename     func(string, string) error
}

// writeProAuthSource stages complete bytes before replacing an existing file.
// The final identity/content check detects observed conflicts; path-based rename
// is not an OS compare-and-swap against arbitrary external writers.
func writeProAuthSource(ctx context.Context, snapshot *proAuthSourceSnapshot, raw []byte, guard func() error, ops proAuthSourceFileOps) (*proAuthSourceSnapshot, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	metadata, err := decodeProAuthSourceMetadata(raw)
	if err != nil {
		return nil, err
	}
	if ops.createTemp == nil {
		ops.createTemp = func(dir, pattern string) (proAuthSourceTempFile, error) { return os.CreateTemp(dir, pattern) }
	}
	if ops.rename == nil {
		ops.rename = os.Rename
	}
	file, err := ops.createTemp(filepath.Dir(snapshot.path), ".pro-auth-*")
	if err != nil {
		return nil, err
	}
	name := file.Name()
	closed := false
	defer func() {
		if !closed {
			_ = file.Close()
		}
		_ = os.Remove(name)
	}()
	if err = file.Chmod(0o600); err != nil {
		return nil, err
	}
	n, err := file.Write(raw)
	if err != nil {
		return nil, err
	}
	if n != len(raw) {
		return nil, io.ErrShortWrite
	}
	if err = file.Sync(); err != nil {
		return nil, err
	}
	err = file.Close()
	closed = true
	if err != nil {
		return nil, err
	}
	info, err := os.Stat(name)
	if err != nil {
		return nil, err
	}
	if guard != nil {
		if err = guard(); err != nil {
			return nil, err
		}
	}
	current, err := readProAuthSource(snapshot.referencePath)
	if err != nil {
		return nil, err
	}
	if !sameProAuthSource(snapshot, current) || !bytes.Equal(snapshot.raw, current.raw) {
		return nil, fmt.Errorf("plugin virtual auth source changed before commit")
	}
	if err = ctx.Err(); err != nil {
		return nil, err
	}
	if err = ops.rename(name, snapshot.path); err != nil {
		return nil, err
	}
	return &proAuthSourceSnapshot{path: snapshot.path, referencePath: snapshot.referencePath, info: info, raw: raw, metadata: metadata}, nil
}

func pluginVirtualManagedSourceBytes(auth *coreauth.Auth, source map[string]any) ([]byte, error) {
	if source == nil {
		source = make(map[string]any)
	}
	source["disabled"] = auth.Disabled
	for _, key := range []string{"last_error", prorouting.ProtectionMetadataKey} {
		if value, ok := auth.Metadata[key]; ok {
			source[key] = value
		} else {
			delete(source, key)
		}
	}
	delete(source, "quota_cache")
	raw, err := json.Marshal(source)
	if err != nil {
		return nil, err
	}
	return append(raw, '\n'), nil
}

// Re-read current unmanaged fields instead of writing a caller's stale map.
func writePluginVirtualManagedMetadataToSourceFile(path string, auth *coreauth.Auth, _ map[string]any) error {
	snapshot, err := readProAuthSource(path)
	if err != nil {
		return err
	}
	raw, err := pluginVirtualManagedSourceBytes(auth, snapshot.metadata)
	if err != nil {
		return err
	}
	_, err = writeProAuthSource(context.Background(), snapshot, raw, nil, proAuthSourceFileOps{})
	return err
}

// Preserve the internal adapter entry point while enforcing the same durable
// contract as its callers. A disappeared identity must never be recreated.
func (h *Handler) updatePluginVirtualRuntimeAuths(ctx context.Context, sourceAuth *coreauth.Auth, mutate func(*coreauth.Auth)) error {
	if h == nil || h.authManager == nil || sourceAuth == nil {
		return fmt.Errorf("plugin virtual auth update is unavailable")
	}
	current, ok := h.authManager.GetByID(sourceAuth.ID)
	if !ok || current == nil {
		return fmt.Errorf("plugin virtual auth no longer exists")
	}
	return h.updateProAuthObserved(ctx, sourceAuth, nil, mutate)
}

func (h *Handler) commitPluginVirtualRuntimeAuths(ctx context.Context, candidates []*coreauth.Auth, saved *proAuthSourceSnapshot, triggerID string, check func(*coreauth.Auth) error, mutate func(*coreauth.Auth)) error {
	var failures []error
	committedCount := 0
	// Stable ordering also makes partial-result diagnostics repeatable.
	sort.Slice(candidates, func(i, j int) bool { return candidates[i].ID < candidates[j].ID })
	for _, candidate := range candidates {
		id, epoch := candidate.ID, candidate.RegistrationEpoch
		_, committed, err := h.authManager.Modify(coreauth.WithSkipPersist(ctx), id, epoch, func(current *coreauth.Auth) error {
			if current.ID != id || current.RegistrationEpoch != epoch || !sameAuthSourcePath(pluginVirtualSourcePath(current), saved.referencePath) {
				return fmt.Errorf("auth registration or source changed")
			}
			if id == triggerID {
				if err := check(current); err != nil {
					return err
				}
			}
			source, err := readProAuthSource(saved.referencePath)
			if err != nil {
				return err
			}
			if !sameProAuthSource(saved, source) {
				return fmt.Errorf("plugin virtual auth source replaced after commit")
			}
			if !sameProAuthManagedMetadata(saved.metadata, source.metadata) {
				return fmt.Errorf("plugin virtual auth managed source fields changed after commit")
			}
			mutate(current)
			if current.ID != id || current.RegistrationEpoch != epoch || !sameAuthSourcePath(pluginVirtualSourcePath(current), saved.referencePath) {
				return fmt.Errorf("auth mutation changed registration identity or source")
			}
			return nil
		})
		if err == nil && !committed {
			err = fmt.Errorf("auth mutation was not committed")
		}
		if err != nil {
			failures = append(failures, fmt.Errorf("%s: %w", id, err))
			continue
		}
		committedCount++
	}
	if len(failures) > 0 {
		return fmt.Errorf("plugin virtual auth source saved; runtime update incomplete (%d/%d committed); retry to reconcile: %w", committedCount, len(candidates), errors.Join(failures...))
	}
	if len(candidates) == 0 {
		return fmt.Errorf("plugin virtual auth source saved; no runtime identities remain; retry to reconcile")
	}
	return nil
}

func (h *Handler) updateProAuth(ctx context.Context, authIndex string, mutate func(*coreauth.Auth)) error {
	if h == nil || h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	return h.updateProAuthObserved(ctx, h.authByIndex(authIndex), nil, mutate)
}

// updateProAuthObserved keeps the registration identity captured by the caller.
// Ordinary edits run against the latest Auth under the native mutation gate;
// virtual identities retain their shared-source persistence contract.
func (h *Handler) updateProAuthObserved(ctx context.Context, observed *coreauth.Auth, guard func(*coreauth.Auth) error, mutate func(*coreauth.Auth)) error {
	return h.updateProAuthMutation(ctx, observed, guard, mutate, false)
}

func (h *Handler) updateProAuthMutation(ctx context.Context, observed *coreauth.Auth, guard func(*coreauth.Auth) error, mutate func(*coreauth.Auth), identityLocal bool) error {
	if h == nil || h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	if observed == nil {
		return fmt.Errorf("auth not found")
	}
	if mutate == nil {
		return nil
	}
	id, epoch := observed.ID, observed.RegistrationEpoch
	if id == "" || epoch == 0 {
		return fmt.Errorf("auth registration identity unavailable")
	}
	h.proAuthMutationMu.Lock()
	defer h.proAuthMutationMu.Unlock()
	check := func(current *coreauth.Auth) error {
		if current == nil || current.ID != id || current.RegistrationEpoch != epoch {
			return fmt.Errorf("auth registration changed")
		}
		if guard != nil {
			return guard(current)
		}
		return nil
	}
	if !coreauth.IsPluginVirtualAuth(observed) {
		_, committed, err := h.authManager.Modify(ctx, id, epoch, func(current *coreauth.Auth) error {
			if err := check(current); err != nil {
				return err
			}
			mutate(current)
			if current.ID != id || current.RegistrationEpoch != epoch {
				return fmt.Errorf("auth mutation changed registration identity")
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !committed {
			return fmt.Errorf("auth mutation was not committed")
		}
		return nil
	}
	// A virtual source may represent several runtime identities. Validate the
	// triggering observation once, then preserve propagation to every sibling.
	auth := h.authByIndex(observed.Index)
	if err := check(auth); err != nil {
		return err
	}
	if identityLocal {
		_, committed, err := h.authManager.Modify(ctx, id, epoch, func(current *coreauth.Auth) error {
			if err := check(current); err != nil {
				return err
			}
			mutate(current)
			if current.ID != id || current.RegistrationEpoch != epoch {
				return fmt.Errorf("auth mutation changed registration identity")
			}
			return nil
		})
		if err != nil {
			return err
		}
		if !committed {
			return fmt.Errorf("auth mutation was not committed")
		}
		return nil
	}
	sourceAuth := h.preferredAuthForPluginVirtualWrite(auth)
	path := pluginVirtualSourcePath(sourceAuth)
	source, err := readProAuthSource(path)
	if err != nil {
		return fmt.Errorf("plugin virtual auth source not saved: %w", err)
	}
	sourceID, sourceEpoch := sourceAuth.ID, sourceAuth.RegistrationEpoch
	candidates := make([]*coreauth.Auth, 0)
	for _, candidate := range h.authManager.List() {
		if candidate != nil && sameAuthSourcePath(pluginVirtualSourcePath(candidate), path) {
			candidates = append(candidates, candidate)
		}
	}
	if len(candidates) == 0 {
		return fmt.Errorf("plugin virtual auth source not saved: no runtime identities")
	}
	mutate(sourceAuth)
	if sourceAuth.ID != sourceID || sourceAuth.RegistrationEpoch != sourceEpoch || !sameAuthSourcePath(pluginVirtualSourcePath(sourceAuth), path) {
		return fmt.Errorf("plugin virtual auth source not saved: mutation changed registration identity or source")
	}
	// Native refresh can edit the same file while the managed mutation is prepared.
	// Preserve its latest unmanaged fields, but never adopt a replaced source.
	latest, err := readProAuthSource(path)
	if err != nil {
		return fmt.Errorf("plugin virtual auth source not saved: %w", err)
	}
	if !sameProAuthSource(source, latest) {
		return fmt.Errorf("plugin virtual auth source not saved: source replaced during preparation")
	}
	raw, err := pluginVirtualManagedSourceBytes(sourceAuth, latest.metadata)
	if err != nil {
		return fmt.Errorf("plugin virtual auth source not saved: %w", err)
	}
	guardSource := func() error {
		current, _ := h.authManager.GetByID(id)
		if err := check(current); err != nil {
			return err
		}
		if !sameAuthSourcePath(pluginVirtualSourcePath(current), path) {
			return fmt.Errorf("trigger auth source changed")
		}
		representative, _ := h.authManager.GetByID(sourceID)
		if representative == nil || representative.RegistrationEpoch != sourceEpoch || !sameAuthSourcePath(pluginVirtualSourcePath(representative), path) {
			return fmt.Errorf("source auth registration changed")
		}
		return nil
	}
	saved, err := writeProAuthSource(ctx, latest, raw, guardSource, proAuthSourceFileOps{})
	if err != nil {
		return fmt.Errorf("plugin virtual auth source not saved: %w", err)
	}
	return h.commitPluginVirtualRuntimeAuths(ctx, candidates, saved, id, check, mutate)
}

func (h *Handler) updateProErrorAuthObserved(ctx context.Context, observed *coreauth.Auth, guard func(*coreauth.Auth) error, mutate func(*coreauth.Auth)) error {
	return h.updateProAuthMutation(ctx, observed, guard, mutate, true)
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
