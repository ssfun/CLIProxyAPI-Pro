package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"path/filepath"
	"reflect"
	"strconv"
	"testing"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/embeddedusage"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

type accountPolicyRefreshExecutor struct{ err error }

func (accountPolicyRefreshExecutor) Identifier() string { return "codex" }

func (accountPolicyRefreshExecutor) Execute(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (accountPolicyRefreshExecutor) ExecuteStream(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (*cliproxyexecutor.StreamResult, error) {
	return nil, nil
}

func (e accountPolicyRefreshExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	if e.err != nil {
		return nil, e.err
	}
	if auth.Metadata == nil {
		auth.Metadata = make(map[string]any)
	}
	auth.Metadata["access_token"] = "refreshed-token"
	return auth, nil
}

func (accountPolicyRefreshExecutor) CountTokens(context.Context, *Auth, cliproxyexecutor.Request, cliproxyexecutor.Options) (cliproxyexecutor.Response, error) {
	return cliproxyexecutor.Response{}, nil
}

func (accountPolicyRefreshExecutor) HttpRequest(context.Context, *Auth, *http.Request) (*http.Response, error) {
	return nil, nil
}

func TestAccountPolicyResolverAffectsSchedulerWithoutMutatingBaseAuth(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	low := &Auth{ID: "low", Provider: "codex", Status: StatusActive, Attributes: map[string]string{"auth_kind": "oauth"}}
	high := &Auth{ID: "high", Provider: "codex", Status: StatusActive, Attributes: map[string]string{"auth_kind": "oauth"}}
	for _, auth := range []*Auth{low, high} {
		registry.GetGlobalRegistry().RegisterClient(auth.ID, "codex", []*registry.ModelInfo{{ID: "gpt-test"}})
		t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(auth.ID) })
		if _, err := manager.Register(context.Background(), auth); err != nil {
			t.Fatal(err)
		}
	}
	priority := 100
	manager.SetAccountPolicyResolver(func(auth *Auth) *Auth {
		clone := auth.Clone()
		RememberAccountPolicyBase(clone)
		clone.Attributes["priority"] = "10"
		clone.Attributes[AttributeWeight] = "2"
		clone.Prefix = "low-policy"
		if clone.ID == "high" {
			clone.Attributes["priority"] = strconv.Itoa(priority)
			clone.Attributes[AttributeWeight] = "7"
			clone.Prefix = "high-policy"
		}
		return clone
	})
	assertScheduledAccountPolicy(t, manager, "high", 100, 7, "high-policy")
	priority = 200
	manager.RefreshSchedulerEntry("high")
	assertScheduledAccountPolicy(t, manager, "high", 200, 7, "high-policy")
	manager.RefreshSchedulerAll()
	picked, err := manager.scheduler.pickSingle(context.Background(), "codex", "gpt-test", cliproxyexecutor.Options{}, nil)
	if err != nil || picked == nil || picked.ID != "high" {
		t.Fatalf("picked = %#v, %v", picked, err)
	}
	manager.MarkResult(context.Background(), Result{AuthID: "high", Provider: "codex", Model: "gpt-test", Success: true})
	assertScheduledAccountPolicy(t, manager, "high", 200, 7, "high-policy")
	updated, _ := manager.GetByID("high")
	updated.StatusMessage = "inspection state write"
	if _, err = manager.Update(context.Background(), updated); err != nil {
		t.Fatal(err)
	}
	assertScheduledAccountPolicy(t, manager, "high", 200, 7, "high-policy")
	manager.RegisterExecutor(accountPolicyRefreshExecutor{})
	if _, refreshed, errRefresh := manager.ForceRefreshForInspection(context.Background(), "high"); errRefresh != nil || !refreshed {
		t.Fatalf("ForceRefreshForInspection() refreshed=%v error=%v, want success", refreshed, errRefresh)
	}
	assertScheduledAccountPolicy(t, manager, "high", 200, 7, "high-policy")
	manager.RegisterExecutor(accountPolicyRefreshExecutor{err: errors.New("transient refresh failure")})
	if _, _, errRefresh := manager.ForceRefreshForInspection(context.Background(), "high"); errRefresh == nil {
		t.Fatal("ForceRefreshForInspection() accepted a failed refresh")
	}
	assertScheduledAccountPolicy(t, manager, "high", 200, 7, "high-policy")
	if _, errRefresh := manager.refreshAuthForRequest(context.Background(), "high", ""); errRefresh == nil {
		t.Fatal("refreshAuthForRequest() accepted a failed refresh")
	}
	assertScheduledAccountPolicy(t, manager, "high", 200, 7, "high-policy")
	picked, err = manager.scheduler.pickSingle(context.Background(), "codex", "gpt-test", cliproxyexecutor.Options{}, nil)
	if err != nil || picked == nil || picked.ID != "high" {
		t.Fatalf("picked after runtime updates = %#v, %v", picked, err)
	}
	stored, _ := manager.GetByID("high")
	if stored.Prefix != "" || stored.Attributes["priority"] != "" || stored.Attributes[AttributeWeight] != "" {
		t.Fatalf("base auth was mutated: %#v", stored.Attributes)
	}
}

func assertScheduledAccountPolicy(t *testing.T, manager *Manager, authID string, wantPriority int, wantWeight int64, wantPrefix string) {
	t.Helper()
	manager.scheduler.mu.Lock()
	defer manager.scheduler.mu.Unlock()
	provider := manager.scheduler.providers["codex"]
	if provider == nil || provider.auths[authID] == nil {
		t.Fatalf("scheduled auth %q is missing", authID)
	}
	meta := provider.auths[authID]
	if meta.priority != wantPriority || meta.weight != wantWeight || meta.auth.Prefix != wantPrefix {
		t.Fatalf("scheduled auth policy = priority:%d weight:%d prefix:%q, want priority:%d weight:%d prefix:%q", meta.priority, meta.weight, meta.auth.Prefix, wantPriority, wantWeight, wantPrefix)
	}
}

func TestUpdateStripsRuntimeAccountPolicyMarkers(t *testing.T) {
	manager := NewManager(nil, nil, nil)
	base := &Auth{ID: "auth-1", Provider: "codex", Prefix: "base", Status: StatusActive, Attributes: map[string]string{"auth_kind": "oauth", "priority": "2", AttributeWeight: "3"}}
	if _, err := manager.Register(context.Background(), base); err != nil {
		t.Fatal(err)
	}
	overlay := base.Clone()
	RememberAccountPolicyBase(overlay)
	overlay.Prefix = "policy"
	overlay.Attributes["priority"] = "100"
	overlay.Attributes[AttributeWeight] = "9"
	if _, err := manager.Update(context.Background(), overlay); err != nil {
		t.Fatal(err)
	}
	stored, _ := manager.GetByID(base.ID)
	if stored.Prefix != "base" || stored.Attributes["priority"] != "2" || stored.Attributes[AttributeWeight] != "3" {
		t.Fatalf("stored auth retained runtime policy: %#v", stored)
	}
	for _, marker := range []string{accountPolicyBasePrefix, accountPolicyBasePriority, accountPolicyBaseWeight} {
		if _, found := stored.Attributes[marker]; found {
			t.Fatalf("marker %q was persisted", marker)
		}
	}
}

func TestQuotaProtectionSchedulingPersistenceAndCAS(t *testing.T) {
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "quota.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "false")
	ctx, cancel := context.WithCancel(context.Background())
	service, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(service)
	t.Cleanup(func() { embeddedusage.SetDefaultService(nil); cancel() })
	m := NewManager(nil, nil, nil)
	a, err := m.Register(ctx, &Auth{ID: "quota-protection-test", Provider: "codex", FileName: "quota.json", Metadata: map[string]any{"access_token": "original"}})
	if err != nil {
		t.Fatal(err)
	}
	registry.GetGlobalRegistry().RegisterClient(a.ID, "codex", []*registry.ModelInfo{{ID: "gpt-quota-test"}, {ID: "gpt-other-test"}})
	t.Cleanup(func() { registry.GetGlobalRegistry().UnregisterClient(a.ID) })
	hold := prorouting.QuotaProtection{Model: "gpt-quota-test", Recheck: true, RetryAt: time.Now().Add(-time.Minute).UnixMilli()}
	if err = m.ChangeQuotaProtection(ctx, a, "inspection", 0, &hold); err != nil {
		t.Fatal(err)
	}
	current, _ := m.GetByID(a.ID)
	rev := prorouting.QuotaProtections(current.Metadata)["inspection"].Revision
	if current.Disabled {
		t.Fatal("quota protection disabled account")
	}
	if picked, err := m.scheduler.pickSingle(ctx, "codex", "gpt-quota-test", cliproxyexecutor.Options{}, nil); err == nil || picked != nil {
		t.Fatal("due recheck restriction allowed traffic")
	}
	if picked, err := m.scheduler.pickSingle(ctx, "codex", "gpt-other-test", cliproxyexecutor.Options{}, nil); err != nil || picked == nil {
		t.Fatalf("unrelated model blocked: %v", err)
	}
	if err = m.ChangeQuotaProtection(ctx, a, "inspection", 0, nil); !errors.Is(err, ErrQuotaProtectionChanged) {
		t.Fatalf("stale release: %v", err)
	}
	// Generic stale metadata updates and successful requests cannot erase the hold.
	if _, err = m.Update(ctx, a.Clone()); err != nil {
		t.Fatal(err)
	}
	m.MarkResult(ctx, Result{AuthID: a.ID, Provider: a.Provider, Model: "gpt-other-test", Success: true})
	current, _ = m.GetByID(a.ID)
	if prorouting.QuotaProtections(current.Metadata)["inspection"].Revision != rev {
		t.Fatal("ordinary update lost protection")
	}
	restarted := NewManager(nil, nil, nil)
	restored, err := restarted.Register(ctx, a.Clone())
	if err != nil {
		t.Fatal(err)
	}
	if prorouting.QuotaProtections(restored.Metadata)["inspection"].Revision != rev {
		t.Fatal("restart lost SQLite restriction")
	}
	// Release the inspection source without erasing a concurrent native cooldown.
	delay := time.Hour
	m.MarkResult(ctx, Result{AuthID: a.ID, Provider: a.Provider, Model: "gpt-quota-test", RetryAfter: &delay, Error: &Error{HTTPStatus: 429}})
	current, _ = m.GetByID(a.ID)
	if err = m.ChangeQuotaProtection(ctx, current, "inspection", rev, nil); err != nil {
		t.Fatal(err)
	}
	current, _ = m.GetByID(a.ID)
	if blocked, _, _ := isAuthBlockedForModel(current, "gpt-quota-test", time.Now()); !blocked {
		t.Fatal("release cleared native cooldown")
	}
	// Imported absence is authoritative; a restored hold must not undo manual disable.
	restored.Disabled = true
	restored.Status = StatusDisabled
	if _, err = restarted.Update(ctx, restored); err != nil {
		t.Fatal(err)
	}
	if err = restarted.ApplyImportedQuotaProtection(ctx, embeddedusage.ProSetting{}); err != nil {
		t.Fatal(err)
	}
	restored, _ = restarted.GetByID(a.ID)
	if !restored.Disabled || len(prorouting.QuotaProtections(restored.Metadata)) != 0 {
		t.Fatal("backup application changed manual disable or retained removed hold")
	}
}

func TestTimedProbeQuotaProtectionRequiresExplicitProbeCompletion(t *testing.T) {
	now := time.Now()
	a := &Auth{ID: "timed-quota-test", Provider: "codex", Metadata: map[string]any{}}
	setQuotaProtections(a, map[string]prorouting.QuotaProtection{"inspection": {Source: "inspection", Model: "gpt-test", RetryAt: now.Add(time.Minute).UnixMilli()}})
	if blocked, _, _ := isAuthBlockedForModel(a, "gpt-test", now); !blocked {
		t.Fatal("active cooldown allowed traffic")
	}
	if blocked, _, _ := isAuthBlockedForModel(a, "gpt-test", now.Add(2*time.Minute)); !blocked {
		t.Fatal("due probe restriction allowed traffic without its recovery probe")
	}
	setQuotaProtections(a, nil)
	if blocked, _, _ := isAuthBlockedForModel(a, "gpt-test", now.Add(2*time.Minute)); blocked {
		t.Fatal("released probe restriction still blocked traffic")
	}
	a.Disabled = true
	if blocked, reason, _ := isAuthBlockedForModel(a, "gpt-test", now.Add(2*time.Minute)); !blocked || reason != blockReasonDisabled {
		t.Fatal("expiry overrode manual disable")
	}
}

func TestImportedLegacyRoutingQuotaProtectionIsIgnored(t *testing.T) {
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "quota.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "false")
	ctx, cancel := context.WithCancel(context.Background())
	service, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(service)
	t.Cleanup(func() { embeddedusage.SetDefaultService(nil); cancel() })
	now := time.Now()
	m := NewManager(nil, nil, nil)
	a, err := m.Register(ctx, &Auth{ID: "legacy-routing-import", Provider: "codex", FileName: "legacy.json"})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]quotaProtectionRecord{
		a.ID: {
			Identity: authRuntimeIdentityFingerprint(a),
			Protections: map[string]prorouting.QuotaProtection{
				"inspection":       {Source: "inspection", Recheck: true, RetryAt: now.Add(time.Hour).UnixMilli()},
				"routing:gpt-test": {Source: "routing:gpt-test", Model: "gpt-test", RetryAt: now.Add(time.Hour).UnixMilli()},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = m.ApplyImportedQuotaProtection(ctx, embeddedusage.ProSetting{Namespace: prorouting.QuotaProtectionNamespace, SchemaVersion: 1, Settings: raw}); err != nil {
		t.Fatal(err)
	}
	current, _ := m.GetByID(a.ID)
	got := prorouting.QuotaProtections(current.Metadata)
	if len(got) != 1 || got["inspection"].Source != "inspection" {
		t.Fatalf("imported protections = %#v", got)
	}
	if _, ok := got["routing:gpt-test"]; ok {
		t.Fatal("legacy routing hold survived backup restore")
	}
	item, _, err := embeddedusage.GetProSetting(ctx, prorouting.QuotaProtectionNamespace)
	if err != nil {
		t.Fatal(err)
	}
	stored, err := decodeQuotaProtectionRecords(item)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := stored[a.ID].Protections["routing:gpt-test"]; ok {
		t.Fatal("legacy routing hold remained in SQLite")
	}
	if err = m.SweepLegacyRoutingQuotaProtections(ctx); err != nil {
		t.Fatal(err)
	}
}

func TestSweepLegacyRoutingQuotaProtectionsLeavesUnprotectedAuthsUnchanged(t *testing.T) {
	t.Setenv("USAGE_DB_PATH", filepath.Join(t.TempDir(), "quota.sqlite"))
	t.Setenv("USAGE_SERVICE_ENABLED", "false")
	ctx, cancel := context.WithCancel(context.Background())
	service, err := embeddedusage.Start(ctx)
	if err != nil {
		t.Fatal(err)
	}
	embeddedusage.SetDefaultService(service)
	t.Cleanup(func() { embeddedusage.SetDefaultService(nil); cancel() })
	m := NewManager(nil, nil, nil)
	a, err := m.Register(ctx, &Auth{ID: "clean-auth", Provider: "codex", FileName: "clean.json"})
	if err != nil {
		t.Fatal(err)
	}
	before, _ := m.GetByID(a.ID)
	if err = m.SweepLegacyRoutingQuotaProtections(ctx); err != nil {
		t.Fatal(err)
	}
	after, _ := m.GetByID(a.ID)
	if !reflect.DeepEqual(before, after) {
		t.Fatal("legacy sweep mutated an unprotected auth")
	}
	if m.HasStoredQuotaProtections(ctx) {
		t.Fatal("empty quota protection store reported holdings")
	}
}
