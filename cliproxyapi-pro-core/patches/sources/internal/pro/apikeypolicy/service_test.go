package apikeypolicy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	probackup "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/backup"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	store, err := OpenStore(filepath.Join(t.TempDir(), "pro.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	return service
}

func TestQuotaSummariesExposeRollingRecoveryAndIsolatedBlockState(t *testing.T) {
	service := newTestService(t)
	requestLimit := int64(2)
	periodValue := int64(1)
	create := func(raw, name string) Policy {
		policy, err := service.Create(context.Background(), testIdentity(t, raw), name, ProfileInput{Name: "default"}, &QuotaInput{
			Enabled: true, Requests: &requestLimit, Period: QuotaPeriod{Type: QuotaPeriodPastDuration, Value: &periodValue, Unit: "hour"},
		})
		if err != nil {
			t.Fatal(err)
		}
		return policy
	}
	blocked := create("summary-blocked", "Blocked")
	healthy := create("summary-healthy", "Healthy")
	nowMS := time.Now().UnixMilli()
	firstAt := nowMS - int64(30*time.Minute/time.Millisecond)
	if _, err := service.store.db.Exec(`insert into api_key_quota_admissions(admission_id, policy_id, profile_id, epoch, admitted_at_ms) values(?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		"summary-admission-1", blocked.ID, blocked.ActiveProfileID, blocked.Quota.Epoch, firstAt,
		"summary-admission-2", blocked.ID, blocked.ActiveProfileID, blocked.Quota.Epoch, nowMS-int64(5*time.Minute/time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	service.pendingMu.Lock()
	service.pricingBlocked[quotaPricingBlockKey{policyID: blocked.ID, epoch: blocked.Quota.Epoch}] = 1
	service.pendingMu.Unlock()
	summaries, err := service.ListQuotaSummaries(context.Background(), nowMS)
	if err != nil {
		t.Fatal(err)
	}
	byID := make(map[string]QuotaSummary, len(summaries))
	for _, summary := range summaries {
		byID[summary.PolicyID] = summary
	}
	if got := byID[blocked.ID]; got.AdmissionState != QuotaAdmissionBlocked || got.BlockedReason != QuotaBlockPricingStore || got.NextRecoverAtMS != firstAt+int64(time.Hour/time.Millisecond) {
		t.Fatalf("blocked summary = %+v", got)
	}
	if got := byID[healthy.ID]; got.AdmissionState != QuotaAdmissionAvailable || got.BlockedReason != "" || got.NextRecoverAtMS != 0 {
		t.Fatalf("healthy summary = %+v", got)
	}
}

func TestQuotaSummariesUseCalendarBoundaryAndNeverExposeKeyHash(t *testing.T) {
	service := newTestService(t)
	requestLimit := int64(1)
	policy, err := service.Create(WithQuotaTimezoneAwareness(context.Background()), testIdentity(t, "calendar-summary-key"), "Calendar", ProfileInput{Name: "default"}, &QuotaInput{
		Enabled: true, Requests: &requestLimit, Period: QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "day", Timezone: "Asia/Shanghai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC)
	if _, err = service.store.db.Exec(`insert into api_key_quota_admissions(admission_id, policy_id, profile_id, epoch, admitted_at_ms) values(?, ?, ?, ?, ?)`, "calendar-admission", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, now.Add(-time.Hour).UnixMilli()); err != nil {
		t.Fatal(err)
	}
	summaries, err := service.ListQuotaSummaries(context.Background(), now.UnixMilli())
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries = %+v err=%v", summaries, err)
	}
	if summaries[0].AdmissionState != QuotaAdmissionExhausted || summaries[0].NextRecoverAtMS != time.Date(2026, time.August, 19, 16, 0, 0, 0, time.UTC).UnixMilli() || summaries[0].Quota == nil || summaries[0].Quota.Period.Timezone != "Asia/Shanghai" {
		t.Fatalf("calendar summary = %+v", summaries[0])
	}
	raw, err := json.Marshal(summaries[0])
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), policy.APIKeyHash) || strings.Contains(string(raw), "apiKeyHash") {
		t.Fatalf("summary exposed API key identity: %s", raw)
	}
}

func TestQuotaSummariesBatchRollingWindowsPreserveUsageAndRecovery(t *testing.T) {
	service := newTestService(t)
	requestLimit := int64(2)
	tokenLimit := int64(100)
	periodValue := int64(1)
	policy, err := service.Create(context.Background(), testIdentity(t, "batch-summary-key"), "Batch", ProfileInput{Name: "default"}, &QuotaInput{
		Enabled: true, Requests: &requestLimit, TotalTokens: &tokenLimit,
		Period: QuotaPeriod{Type: QuotaPeriodPastDuration, Value: &periodValue, Unit: "hour"},
	})
	if err != nil {
		t.Fatal(err)
	}
	nowMS := time.Now().UnixMilli()
	if _, err = service.store.db.Exec(`insert into api_key_quota_admissions(admission_id, policy_id, profile_id, epoch, admitted_at_ms) values(?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		"batch-admission-old", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, nowMS-int64(2*time.Hour/time.Millisecond),
		"batch-admission-current", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, nowMS-int64(10*time.Minute/time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, err = service.store.db.Exec(`insert into api_key_quota_token_events(event_id, admission_id, policy_id, profile_id, epoch, total_tokens, cost_micros, occurred_at_ms) values(?, ?, ?, ?, ?, ?, ?, ?)`,
		"batch-event-current", "batch-admission-current", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, 40, 0, nowMS-int64(10*time.Minute/time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	summaries, err := service.ListQuotaSummaries(context.Background(), nowMS)
	if err != nil || len(summaries) != 1 {
		t.Fatalf("summaries = %+v err=%v", summaries, err)
	}
	quota := summaries[0].Quota
	if quota == nil || quota.Usage.RequestsUsed != 1 || quota.Usage.TotalTokensUsed != 40 || quota.Usage.CostUsed != 0 {
		t.Fatalf("batched usage = %+v", quota)
	}
	if quota.Usage.Exhausted != nil && len(quota.Usage.Exhausted) != 0 {
		t.Fatalf("unexpected exhausted metrics = %+v", quota.Usage.Exhausted)
	}
	if summaries[0].NextRecoverAtMS != 0 {
		t.Fatalf("unexpected recovery = %d", summaries[0].NextRecoverAtMS)
	}
}

func TestQuotaNextRecoverAtIgnoresMalformedRollingPeriod(t *testing.T) {
	got, err := quotaNextRecoverAt(context.Background(), nil, "policy", Quota{
		Period: QuotaPeriod{Type: QuotaPeriodPastDuration, Unit: "hour"},
		Usage:  QuotaUsage{Exhausted: []string{"requests"}},
	})
	if err != nil || got != 0 {
		t.Fatalf("recovery = %d, err=%v", got, err)
	}
}

func TestTakeoverControlsNewRequestDecisionsAndPersists(t *testing.T) {
	path := filepath.Join(t.TempDir(), "takeover.sqlite")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity(t, "takeover-key")
	if _, err = service.Create(context.Background(), identity, "Takeover", ProfileInput{
		Name: "Restricted", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	}); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil || decision.Mode != ModePassthrough || decision.Snapshot != nil {
		t.Fatalf("disabled decision = %#v, %v", decision, err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	frozen, err := service.Decide(identity)
	if err != nil || frozen.Mode != ModeProfile || frozen.Snapshot == nil {
		t.Fatalf("enabled decision = %#v, %v", frozen, err)
	}
	if err = service.SetTakeover(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	decision, err = service.Decide(identity)
	if err != nil || decision.Mode != ModePassthrough || frozen.Mode != ModeProfile {
		t.Fatalf("disabled/frozen decisions = %#v / %#v, %v", decision, frozen, err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedStore, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewService(reopenedStore)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if !reopened.TakeoverEnabled() {
		t.Fatal("takeover setting did not persist")
	}
}

func TestUnhealthyPolicyServiceCanPersistEmergencyTakeoverStop(t *testing.T) {
	path := filepath.Join(t.TempDir(), "takeover-emergency-stop.sqlite")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity(t, "emergency-stop-key")
	if _, err = service.Create(context.Background(), identity, "Emergency", ProfileInput{
		Name: "Restricted", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	}); err != nil {
		t.Fatal(err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	service.MarkUnavailable()
	if service.Healthy() || !service.TakeoverEnabled() {
		t.Fatalf("unavailable state healthy=%v takeover=%v", service.Healthy(), service.TakeoverEnabled())
	}
	if err = service.SetTakeoverIfGeneration(context.Background(), true, service.PolicyGeneration(), service.ConfiguredGeneration()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unhealthy activation error=%v", err)
	}
	if err = service.SetTakeover(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	if service.Healthy() || service.TakeoverEnabled() {
		t.Fatalf("emergency stop state healthy=%v takeover=%v", service.Healthy(), service.TakeoverEnabled())
	}
	decision, err := service.Decide(identity)
	if err != nil || decision.Mode != ModePassthrough {
		t.Fatalf("emergency stop decision=%#v err=%v", decision, err)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}
	reopenedStore, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened, err := NewService(reopenedStore)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	if reopened.TakeoverEnabled() {
		t.Fatal("emergency stop did not persist")
	}
}

func TestTakeoverActivationRequiresConfirmedPolicyAndConfiguredGenerations(t *testing.T) {
	service := newTestService(t)
	if err := service.SetTakeover(context.Background(), false); err != nil {
		t.Fatal(err)
	}
	service.SetConfiguredAPIKeys([]string{"generation-key"})
	policyGeneration := service.PolicyGeneration()
	configuredGeneration := service.ConfiguredGeneration()
	identity := testIdentity(t, "generation-key")
	if _, err := service.Create(context.Background(), identity, "Generation", ProfileInput{
		Name: "Restricted", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetTakeoverIfGeneration(context.Background(), true, policyGeneration, configuredGeneration); !errors.Is(err, ErrTakeoverStateChanged) {
		t.Fatalf("stale policy generation error=%v", err)
	}
	if service.TakeoverEnabled() {
		t.Fatal("stale policy generation enabled takeover")
	}
	policyGeneration = service.PolicyGeneration()
	service.SetConfiguredAPIKeys([]string{"generation-key", "new-key"})
	if err := service.SetTakeoverIfGeneration(context.Background(), true, policyGeneration, configuredGeneration); !errors.Is(err, ErrTakeoverStateChanged) {
		t.Fatalf("stale configured generation error=%v", err)
	}
	if service.TakeoverEnabled() {
		t.Fatal("stale configured generation enabled takeover")
	}
	if err := service.SetTakeoverIfGeneration(context.Background(), true, service.PolicyGeneration(), service.ConfiguredGeneration()); err != nil {
		t.Fatal(err)
	}
}

func testIdentity(t *testing.T, raw string) AuthenticatedAPIKeyIdentity {
	t.Helper()
	identity, err := NewAuthenticatedAPIKeyIdentity(raw)
	if err != nil {
		t.Fatal(err)
	}
	return identity
}

func TestFingerprintGoldenAndContextCannotUseClientFields(t *testing.T) {
	identity := testIdentity(t, "raw-api-key")
	if got, want := identity.Hash(), "7394b42f2b2f119e4d91b02296fc193cd2bd0da8f13419af3499dd01a4eb6734"; got != want {
		t.Fatalf("hash = %q, want %q", got, want)
	}
	ctx := WithIdentity(context.Background(), identity)
	stored, ok := IdentityFromContext(ctx)
	if !ok || stored.Hash() != identity.Hash() {
		t.Fatalf("context identity = %#v, %v", stored, ok)
	}
	if _, ok := IdentityFromContext(context.Background()); ok {
		t.Fatal("empty context forged an API key identity")
	}
}

func TestCreatePolicyPublishesImmutableActiveProfile(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "key-a")
	policy, err := service.Create(context.Background(), identity, "Key A", ProfileInput{
		Name: "default", Providers: []string{"Codex", "claude"}, Models: []string{"gpt-5", "claude-opus"},
		Mappings: []ModelMapping{{Source: "smart", Target: "gpt-5"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.ActiveProfileID == "" || len(policy.Profiles) != 1 || policy.ActiveProfileID != policy.Profiles[0].ID {
		t.Fatalf("policy = %#v", policy)
	}
	decision, err := service.Decide(identity)
	if err != nil || decision.Mode != ModeProfile {
		t.Fatalf("decision = %#v, %v", decision, err)
	}
	effective, err := decision.ApplyModel("smart")
	if err != nil || effective != "gpt-5" {
		t.Fatalf("effective model = %q, %v", effective, err)
	}
	providers, err := decision.FilterProviders([]string{"gemini", "codex", "claude"})
	if err != nil || len(providers) != 2 || providers[0] != "codex" || providers[1] != "claude" {
		t.Fatalf("providers = %#v, %v", providers, err)
	}

	old := decision.Clone()
	created, err := service.CreateProfile(context.Background(), policy.ID, policy.Version, ProfileInput{
		Name: "restricted", Providers: []string{"gemini"}, Models: []string{"gemini-2.5-pro"},
	})
	if err != nil {
		t.Fatal(err)
	}
	var restrictedProfileID string
	for _, profile := range created.Profiles {
		if profile.ID != policy.ActiveProfileID {
			restrictedProfileID = profile.ID
			break
		}
	}
	if restrictedProfileID == "" {
		t.Fatalf("created policy has no inactive profile: %#v", created)
	}
	updated, err := service.ActivateProfile(context.Background(), policy.ID, restrictedProfileID, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	if updated.ActiveProfileID == policy.ActiveProfileID {
		t.Fatal("active profile did not switch")
	}
	if effective, err := old.ApplyModel("smart"); err != nil || effective != "gpt-5" {
		t.Fatalf("in-flight snapshot changed: %q, %v", effective, err)
	}
	newDecision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := newDecision.ApplyModel("smart"); err == nil {
		t.Fatal("new active profile allowed an old mapping")
	}
}

func TestQuotaOnlyPolicyAllowsAllAndRoundTripsEmptyProfileAttribution(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "quota-only-key")
	requestLimit := int64(1)
	policy, err := service.CreateOptionalProfile(context.Background(), identity, "Quota only", nil, &QuotaInput{
		Enabled: true, Requests: &requestLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if policy.ActiveProfileID != "" || len(policy.Profiles) != 0 || policy.Quota == nil {
		t.Fatalf("quota-only policy = %#v", policy)
	}
	decision, err := service.Decide(identity)
	if err != nil || decision.Mode != ModeProfile || decision.Snapshot == nil || decision.Snapshot.ProfileID != "" || decision.Snapshot.ProfileName != "" {
		t.Fatalf("quota-only decision = %#v, %v", decision, err)
	}
	if model, applyErr := decision.ApplyModel("custom-model"); applyErr != nil || model != "custom-model" {
		t.Fatalf("quota-only model = %q, %v", model, applyErr)
	}
	providers, filterErr := decision.FilterProviders([]string{"codex", "claude"})
	if filterErr != nil || len(providers) != 2 {
		t.Fatalf("quota-only providers = %#v, %v", providers, filterErr)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	attribution, ok := admitted.QuotaAttribution()
	if !ok || attribution.ProfileID != "" {
		t.Fatalf("quota-only attribution = %#v, %v", attribution, ok)
	}
	if _, err = service.AdmitDecision(context.Background(), decision); err == nil {
		t.Fatal("quota-only policy did not enforce its Key-wide request limit")
	}
	payload, err := service.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	target := newTestService(t)
	if err = target.ImportBackup(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	restored, err := target.Get(context.Background(), policy.ID)
	if err != nil || restored.ActiveProfileID != "" || len(restored.Profiles) != 0 || restored.Quota == nil || restored.Quota.Usage.RequestsUsed != 1 {
		t.Fatalf("restored quota-only policy = %#v, %v", restored, err)
	}
}

func TestQuotaOnlyPolicyFirstProfileAndLastProfileConfirmation(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "optional-profile-lifecycle-key")
	policy, err := service.CreateOptionalProfile(context.Background(), identity, "Optional profile", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertLastAudit := func(eventType, profileID string, expectedDetails map[string]any) int {
		t.Helper()
		audits, err := service.store.ListAudits(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if len(audits) == 0 {
			t.Fatalf("missing %s audit", eventType)
		}
		last := audits[len(audits)-1]
		if last.EventType != eventType || last.PolicyID != policy.ID {
			t.Fatalf("last audit = %#v, want %s", last, eventType)
		}
		var details map[string]any
		if err := json.Unmarshal(last.Details, &details); err != nil {
			t.Fatalf("decode %s audit details: %v", eventType, err)
		}
		if details["profileId"] != profileID {
			t.Fatalf("%s audit profile = %#v, want %q", eventType, details["profileId"], profileID)
		}
		for key, want := range expectedDetails {
			if details[key] != want {
				t.Fatalf("%s audit %s = %#v, want %#v", eventType, key, details[key], want)
			}
		}
		return len(audits)
	}
	created, err := service.CreateProfile(context.Background(), policy.ID, policy.Version, ProfileInput{Name: "Restricted", Providers: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(created.Profiles) != 1 || !created.ProfileEnabled || created.ActiveProfileID != created.Profiles[0].ID {
		t.Fatalf("first profile was not activated: %#v", created)
	}
	assertLastAudit("active_profile_changed", created.ActiveProfileID, map[string]any{"automatic": true})
	if _, err = service.DeleteProfile(context.Background(), created.ID, created.ActiveProfileID, created.Version); !errors.Is(err, ErrNoProfileConfirmation) {
		t.Fatalf("delete last active profile error = %v", err)
	}
	removed, err := service.DeleteProfile(context.Background(), created.ID, created.ActiveProfileID, created.Version, NoProfileConfirmation)
	if err != nil {
		t.Fatal(err)
	}
	if removed.ProfileEnabled || removed.ActiveProfileID != "" || len(removed.Profiles) != 0 {
		t.Fatalf("policy did not return to quota-only mode: %#v", removed)
	}
	assertLastAudit("active_profile_removed", created.ActiveProfileID, map[string]any{"confirmation": NoProfileConfirmation})
	decision, err := service.Decide(identity)
	if err != nil || decision.Snapshot == nil || !decision.Snapshot.allowsProvider("claude") {
		t.Fatalf("no-profile decision remained restricted: %#v, %v", decision, err)
	}
	workspaceProfile := ProfileInput{Name: "Workspace", Providers: []string{"claude"}}
	recreated, err := service.UpdateWorkspace(context.Background(), removed.ID, removed.Version, WorkspaceUpdate{
		DisplayName: removed.DisplayName, CreateProfile: true, Profile: &workspaceProfile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(recreated.Profiles) != 1 || !recreated.ProfileEnabled || recreated.ActiveProfileID != recreated.Profiles[0].ID {
		t.Fatalf("workspace-created first profile was not activated: %#v", recreated)
	}
	auditCount := assertLastAudit("active_profile_changed", recreated.ActiveProfileID, map[string]any{"automatic": true})
	withSecond, err := service.CreateProfile(context.Background(), recreated.ID, recreated.Version, ProfileInput{Name: "Second", Providers: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	audits, err := service.store.ListAudits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(withSecond.Profiles) != 2 || len(audits) != auditCount {
		t.Fatalf("inactive Profile creation wrote an automatic activation audit: profiles=%d audits=%#v", len(withSecond.Profiles), audits)
	}
}

func TestWorkspaceUpdatePausesAndResumesProfileEnforcementWithoutDeletingProfiles(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "atomic-profile-disable-key")
	requestLimit := int64(5)
	created, err := service.Create(context.Background(), identity, "Restricted", ProfileInput{
		Name: "Primary", Providers: []string{"codex"},
	}, &QuotaInput{Enabled: true, Requests: &requestLimit, Period: QuotaPeriod{Type: QuotaPeriodAllTime}})
	if err != nil {
		t.Fatal(err)
	}
	created, err = service.CreateProfile(context.Background(), created.ID, created.Version, ProfileInput{
		Name: "Secondary", Providers: []string{"claude"},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondaryProfileID := ""
	for _, profile := range created.Profiles {
		if profile.ID != created.ActiveProfileID {
			secondaryProfileID = profile.ID
		}
	}
	if secondaryProfileID == "" {
		t.Fatalf("secondary Profile missing: %#v", created.Profiles)
	}
	created, err = service.ActivateProfile(context.Background(), created.ID, secondaryProfileID, created.Version)
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	restrictedDecision := decision
	if _, err = service.AdmitDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	profilesBefore, err := json.Marshal(loaded.Profiles)
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	update := WorkspaceUpdate{
		DisplayName: "Unrestricted", ProfileEnabled: &disabled,
	}
	if _, err = service.UpdateWorkspace(context.Background(), loaded.ID, loaded.Version, update); err == nil || !strings.Contains(err.Error(), "profile_enforcement_toggle") {
		t.Fatalf("unnegotiated Profile disable error = %v", err)
	}
	invalidDisable := update
	invalidDisable.ActiveProfileID = loaded.ActiveProfileID
	if _, err = service.UpdateWorkspace(WithProfileEnforcementToggle(context.Background()), loaded.ID, loaded.Version, invalidDisable); err == nil || !strings.Contains(err.Error(), "must not include an active Profile ID") {
		t.Fatalf("invalid Profile disable error = %v", err)
	}
	updated, err := service.UpdateWorkspace(WithProfileEnforcementToggle(context.Background()), loaded.ID, loaded.Version, update)
	if err != nil {
		t.Fatal(err)
	}
	profilesAfter, err := json.Marshal(updated.Profiles)
	if err != nil {
		t.Fatal(err)
	}
	if updated.DisplayName != "Unrestricted" || updated.ProfileEnabled || updated.ActiveProfileID != loaded.ActiveProfileID || len(updated.Profiles) != 2 || string(profilesAfter) != string(profilesBefore) || updated.Version != loaded.Version+1 {
		t.Fatalf("disabled Profile workspace = %#v", updated)
	}
	if updated.Quota == nil || updated.Quota.Usage.RequestsUsed != 1 || updated.Quota.Epoch != loaded.Quota.Epoch {
		t.Fatalf("Profile disable changed Key quota = before %#v after %#v", loaded.Quota, updated.Quota)
	}
	if restrictedDecision.Snapshot == nil || restrictedDecision.Snapshot.allowsProvider("unrestricted-provider") {
		t.Fatalf("in-flight decision widened after Profile disable: %#v", restrictedDecision)
	}
	audits, err := service.store.ListAudits(context.Background())
	if err != nil || len(audits) == 0 {
		t.Fatalf("Profile disable audits = %#v error=%v", audits, err)
	}
	last := audits[len(audits)-1]
	var details map[string]any
	if err = json.Unmarshal(last.Details, &details); err != nil {
		t.Fatal(err)
	}
	if last.EventType != "profile_enforcement_disabled" || details["profileId"] != loaded.ActiveProfileID {
		t.Fatalf("Profile disable audit = %#v details=%#v", last, details)
	}
	decision, err = service.Decide(identity)
	if err != nil || decision.Snapshot == nil || !decision.Snapshot.allowsProvider("unrestricted-provider") {
		t.Fatalf("disabled Profile decision = %#v error=%v", decision, err)
	}
	enabled := true
	reenabled, err := service.UpdateWorkspace(WithProfileEnforcementToggle(context.Background()), updated.ID, updated.Version, WorkspaceUpdate{
		DisplayName: updated.DisplayName, ProfileEnabled: &enabled, ActiveProfileID: updated.ActiveProfileID,
	})
	if err != nil {
		t.Fatal(err)
	}
	reenabledProfiles, err := json.Marshal(reenabled.Profiles)
	if err != nil {
		t.Fatal(err)
	}
	if !reenabled.ProfileEnabled || reenabled.ActiveProfileID != secondaryProfileID || string(reenabledProfiles) != string(profilesBefore) || reenabled.Quota == nil || reenabled.Quota.Epoch != loaded.Quota.Epoch || reenabled.Quota.Usage.RequestsUsed != 1 {
		t.Fatalf("re-enabled Profile workspace = %#v", reenabled)
	}
	decision, err = service.Decide(identity)
	if err != nil || decision.Snapshot == nil || !decision.Snapshot.allowsProvider("claude") || decision.Snapshot.allowsProvider("codex") {
		t.Fatalf("re-enabled Profile decision = %#v error=%v", decision, err)
	}
}

func TestPausedProfileEnforcementSurvivesBothProfileCreationPaths(t *testing.T) {
	service := newTestService(t)
	created, err := service.Create(context.Background(), testIdentity(t, "paused-profile-create-key"), "Paused", ProfileInput{Name: "Primary"})
	if err != nil {
		t.Fatal(err)
	}
	selectedProfileID := created.ActiveProfileID
	disabled := false
	paused, err := service.UpdateWorkspace(WithProfileEnforcementToggle(context.Background()), created.ID, created.Version, WorkspaceUpdate{
		DisplayName: created.DisplayName, ProfileEnabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	createdByEndpoint, err := service.CreateProfile(context.Background(), paused.ID, paused.Version, ProfileInput{Name: "Endpoint"})
	if err != nil {
		t.Fatal(err)
	}
	if createdByEndpoint.ProfileEnabled || createdByEndpoint.ActiveProfileID != selectedProfileID || len(createdByEndpoint.Profiles) != 2 {
		t.Fatalf("Profile endpoint resumed paused enforcement: %#v", createdByEndpoint)
	}
	workspaceProfile := ProfileInput{Name: "Workspace"}
	createdByWorkspace, err := service.UpdateWorkspace(context.Background(), createdByEndpoint.ID, createdByEndpoint.Version, WorkspaceUpdate{
		DisplayName: createdByEndpoint.DisplayName, Profile: &workspaceProfile, CreateProfile: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if createdByWorkspace.ProfileEnabled || createdByWorkspace.ActiveProfileID != selectedProfileID || len(createdByWorkspace.Profiles) != 3 {
		t.Fatalf("workspace creation resumed paused enforcement: %#v", createdByWorkspace)
	}
}

func TestPolicyBackupRoundTripsPausedProfileEnforcement(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "paused-profile-backup-key")
	created, err := service.Create(context.Background(), identity, "Paused", ProfileInput{Name: "Saved", Providers: []string{"codex"}})
	if err != nil {
		t.Fatal(err)
	}
	disabled := false
	paused, err := service.UpdateWorkspace(WithProfileEnforcementToggle(context.Background()), created.ID, created.Version, WorkspaceUpdate{
		DisplayName: created.DisplayName, ProfileEnabled: &disabled,
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := service.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ImportBackup(context.Background(), payload); err != nil {
		t.Fatalf("restore paused Profile enforcement: %v", err)
	}
	restored, err := service.Get(context.Background(), paused.ID)
	if err != nil || restored.ProfileEnabled || restored.ActiveProfileID != created.ActiveProfileID || len(restored.Profiles) != 1 || restored.Profiles[0].Name != "Saved" {
		t.Fatalf("restored paused policy = %#v error=%v", restored, err)
	}
	decision, err := service.Decide(identity)
	if err != nil || decision.Snapshot == nil || !decision.Snapshot.allowsProvider("unrestricted-provider") {
		t.Fatalf("restored paused decision = %#v error=%v", decision, err)
	}
	var legacy backupDocument
	if err = json.Unmarshal(payload, &legacy); err != nil {
		t.Fatal(err)
	}
	legacy.SchemaVersion = 7
	legacy.Policies[0].ProfileEnabled = false
	legacy.Policies[0].ActiveProfileID = ""
	legacyPayload, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	if err = service.ImportBackup(context.Background(), legacyPayload); err != nil {
		t.Fatalf("restore legacy paused Profile enforcement: %v", err)
	}
	legacyRestored, err := service.Get(context.Background(), paused.ID)
	if err != nil || legacyRestored.ProfileEnabled || legacyRestored.ActiveProfileID != legacyRestored.Profiles[0].ID {
		t.Fatalf("legacy paused policy = %#v error=%v", legacyRestored, err)
	}
}

func TestPolicyJSONUsesEmptyCollectionsInsteadOfNull(t *testing.T) {
	service := newTestService(t)
	policy, err := service.Create(context.Background(), testIdentity(t, "empty-mappings-key"), "Empty mappings", ProfileInput{
		Name: "default", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := json.Marshal(policy)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), `"mappings":null`) || !strings.Contains(string(payload), `"mappings":[]`) {
		t.Fatalf("policy JSON must use an empty mappings array: %s", payload)
	}
}

func TestProfileCatalogReturnsOnlyCurrentNamesWithAtomicGeneration(t *testing.T) {
	service := newTestService(t)
	policy, err := service.Create(context.Background(), testIdentity(t, "profile-catalog-key"), "Catalog", ProfileInput{
		Name: "Original", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.ListProfileCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Items) != 1 || first.Items[0].ID != policy.ActiveProfileID || first.Items[0].Name != "Original" || first.PolicyGeneration != service.PolicyGeneration() {
		t.Fatalf("first catalog = %#v", first)
	}
	edited, err := service.ReplaceProfile(context.Background(), policy.ID, policy.ActiveProfileID, policy.Version, ProfileInput{
		Name: "Current", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := service.ListProfileCatalog(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(second.Items) != 1 || second.Items[0].Name != "Current" || second.Items[0].UpdatedAtMS != edited.Profiles[0].UpdatedAtMS || second.PolicyGeneration <= first.PolicyGeneration {
		t.Fatalf("second catalog = %#v; first generation = %d", second, first.PolicyGeneration)
	}
	service.MarkUnavailable()
	if _, err := service.ListProfileCatalog(context.Background()); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unhealthy profile catalog error = %v", err)
	}
}

func TestInFlightSnapshotSurvivesProfileEditActivationAndPolicyDelete(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "in-flight-key")
	policy, err := service.Create(context.Background(), identity, "In flight", ProfileInput{
		Name: "first", Providers: []string{"codex"}, Models: []string{"gpt-5"},
		Mappings: []ModelMapping{{Source: "smart", Target: "gpt-5"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	frozen, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	frozen = frozen.WithModels("smart", "gpt-5")
	assertFrozen := func(stage string) {
		t.Helper()
		if frozen.Snapshot == nil || frozen.Snapshot.PolicyID != policy.ID || frozen.Snapshot.ProfileID != policy.ActiveProfileID || frozen.Snapshot.ProfileName != "first" || frozen.Snapshot.Version != policy.Version {
			t.Fatalf("%s changed frozen identity: %#v", stage, frozen)
		}
		if got, applyErr := frozen.ApplyModel("smart"); applyErr != nil || got != "gpt-5" {
			t.Fatalf("%s changed frozen mapping: %q, %v", stage, got, applyErr)
		}
		if attribution := frozen.UsageAttribution(); attribution.APIKeyPolicyID != policy.ID || attribution.ProfileID != policy.ActiveProfileID || attribution.ProfileName != "first" || attribution.RequestedModel != "smart" || attribution.EffectiveModel != "gpt-5" {
			t.Fatalf("%s changed frozen usage attribution: %#v", stage, attribution)
		}
	}
	assertFrozen("initial")

	edited, err := service.ReplaceProfile(context.Background(), policy.ID, policy.ActiveProfileID, policy.Version, ProfileInput{
		Name: "first-edited", Providers: []string{"claude"}, Models: []string{"claude-sonnet-4-6"},
	})
	if err != nil {
		t.Fatal(err)
	}
	assertFrozen("profile edit")
	current, err := service.Decide(identity)
	if err != nil || current.Snapshot == nil || current.Snapshot.ProfileName != "first-edited" {
		t.Fatalf("new decision after edit = %#v, %v", current, err)
	}

	withSecond, err := service.CreateProfile(context.Background(), policy.ID, edited.Version, ProfileInput{
		Name: "second", Providers: []string{"gemini"}, Models: []string{"gemini-2.5-pro"},
	})
	if err != nil {
		t.Fatal(err)
	}
	secondID := ""
	for _, profile := range withSecond.Profiles {
		if profile.Name == "second" {
			secondID = profile.ID
		}
	}
	activated, err := service.ActivateProfile(context.Background(), policy.ID, secondID, withSecond.Version)
	if err != nil {
		t.Fatal(err)
	}
	assertFrozen("active switch")
	current, err = service.Decide(identity)
	if err != nil || current.Snapshot == nil || current.Snapshot.ProfileID != secondID {
		t.Fatalf("new decision after activation = %#v, %v", current, err)
	}

	if err := service.DeletePolicy(context.Background(), policy.ID, activated.Version, PassthroughConfirmation); err != nil {
		t.Fatal(err)
	}
	assertFrozen("policy delete")
	current, err = service.Decide(identity)
	if err != nil || current.Mode != ModePassthrough || current.Snapshot != nil {
		t.Fatalf("new decision after delete = %#v, %v", current, err)
	}
	audits, err := service.store.ListAudits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(audits) < 2 || audits[len(audits)-1].PolicyID != policy.ID || audits[len(audits)-1].EventType != "policy_deleted_passthrough" || !strings.Contains(string(audits[len(audits)-1].Details), PassthroughConfirmation) {
		t.Fatalf("delete audit was not retained with the permission-change confirmation: %#v", audits)
	}
}

func TestPassthroughUnavailableAndNoFallbackAreDistinct(t *testing.T) {
	service := newTestService(t)
	unconfigured := testIdentity(t, "unconfigured")
	decision, err := service.Decide(unconfigured)
	if err != nil || decision.Mode != ModePassthrough || decision.Snapshot != nil {
		t.Fatalf("passthrough = %#v, %v", decision, err)
	}
	service.MarkUnavailable()
	if _, err := service.Decide(unconfigured); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unavailable error = %v", err)
	}
}

func TestRequestDecisionNormalizesThinkingSuffixWithoutBroadeningModelAccess(t *testing.T) {
	decision := RequestPolicyDecision{Mode: ModeProfile, Snapshot: &RequestPolicySnapshot{
		ModelMappings: map[string]string{"smart": "gpt-5"},
		AllowedModels: map[string]struct{}{"gpt-5": {}, "custom(8192)": {}},
	}}
	tests := []struct {
		name      string
		requested string
		want      string
		wantErr   bool
	}{
		{name: "allowed base with suffix", requested: "gpt-5(high)", want: "gpt-5(high)"},
		{name: "mapped base with suffix", requested: "smart(8192)", want: "gpt-5(8192)"},
		{name: "exact custom parenthesized model", requested: "custom(8192)", want: "custom(8192)"},
		{name: "forbidden base cannot use suffix", requested: "claude-opus(high)", wantErr: true},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got, err := decision.ApplyModel(test.requested)
			if test.wantErr {
				if err == nil {
					t.Fatalf("ApplyModel(%q) unexpectedly succeeded with %q", test.requested, got)
				}
				return
			}
			if err != nil || got != test.want {
				t.Fatalf("ApplyModel(%q) = %q, %v; want %q", test.requested, got, err, test.want)
			}
		})
	}
}

func TestEmptyProviderAndModelSelectionsAllowAllCatalogAndFutureValues(t *testing.T) {
	service := newTestService(t)
	service.SetCatalogProvider(func() (ProfileCatalog, error) {
		return NewProfileCatalog([]string{"codex"}, []string{"gpt-5"}), nil
	})
	identity := testIdentity(t, "allow-all-key")
	policy, err := service.Create(context.Background(), identity, "Allow all", ProfileInput{
		Name: "Open", Mappings: []ModelMapping{{Source: "smart", Target: "gpt-5"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(policy.Profiles) != 1 || len(policy.Profiles[0].Providers) != 0 || len(policy.Profiles[0].Models) != 0 {
		t.Fatalf("persisted wildcard profile=%#v", policy.Profiles)
	}
	decision, err := service.Decide(identity)
	if err != nil || decision.Snapshot == nil {
		t.Fatalf("decision=%#v err=%v", decision, err)
	}
	for requested, want := range map[string]string{
		"future-model": "future-model",
		"smart":        "gpt-5",
		"smart(high)":  "gpt-5(high)",
	} {
		if got, applyErr := decision.ApplyModel(requested); applyErr != nil || got != want {
			t.Fatalf("ApplyModel(%q)=%q, %v; want %q", requested, got, applyErr, want)
		}
	}
	if err = decision.AllowsProvider("future-provider"); err != nil {
		t.Fatalf("future provider rejected: %v", err)
	}
	providers, err := decision.FilterProviders([]string{"Future-Provider", "codex", "future-provider"})
	if err != nil || len(providers) != 2 || providers[0] != "future-provider" || providers[1] != "codex" {
		t.Fatalf("filtered providers=%#v err=%v", providers, err)
	}
	visible, err := decision.FilterVisibleModels([]ModelCandidate{
		{ID: "future-model", Providers: []string{"future-provider"}},
		{ID: "gpt-5", Providers: []string{"codex"}},
	})
	if err != nil || len(visible) != 3 || visible[0].ID != "future-model" || visible[1].ID != "gpt-5" || visible[2] != (VisibleModel{ID: "smart", EffectiveID: "gpt-5"}) {
		t.Fatalf("visible models=%#v err=%v", visible, err)
	}
	invalid := ProfileInput{Name: "Open", Mappings: []ModelMapping{{Source: "smart", Target: "unknown"}}}
	if _, err = service.Create(context.Background(), testIdentity(t, "invalid-open-key"), "", invalid); err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("unknown wildcard mapping target error=%v", err)
	}
}

func TestRequestDecisionExactAutoAndPrefixedMappingsWinBeforeNormalization(t *testing.T) {
	decision := RequestPolicyDecision{Mode: ModeProfile, Snapshot: &RequestPolicySnapshot{
		ModelMappings: map[string]string{"auto": "gpt-5", "team/gpt": "gpt-5"},
		AllowedModels: map[string]struct{}{"gpt-5": {}},
	}}
	for source, want := range map[string]string{"auto": "gpt-5", "team/gpt": "gpt-5"} {
		if !decision.HasExactModelMapping(source) {
			t.Fatalf("HasExactModelMapping(%q) = false", source)
		}
		if got, err := decision.ApplyModel(source); err != nil || got != want {
			t.Fatalf("ApplyModel(%q) = %q, %v; want %q", source, got, err, want)
		}
	}
	if decision.HasExactModelMapping("auto(high)") {
		t.Fatal("suffix must not be confused with an exact mapping")
	}
	if got, err := decision.ApplyModel("auto(high)"); err != nil || got != "gpt-5(high)" {
		t.Fatalf("ApplyModel(auto(high)) = %q, %v; want gpt-5(high)", got, err)
	}
}

func TestRequestDecisionValidatesRouterTargetWithoutApplyingAliasAgain(t *testing.T) {
	decision := RequestPolicyDecision{Mode: ModeProfile, Snapshot: &RequestPolicySnapshot{
		ModelMappings: map[string]string{"smart": "gpt-5", "gpt-5": "claude-opus"},
		AllowedModels: map[string]struct{}{"gpt-5": {}, "claude-opus": {}},
	}}
	if got, err := decision.ValidateEffectiveModel("gpt-5(high)"); err != nil || got != "gpt-5(high)" {
		t.Fatalf("ValidateEffectiveModel() = %q, %v", got, err)
	}
	if _, err := decision.ValidateEffectiveModel("gemini-pro(high)"); err == nil {
		t.Fatal("forbidden router target unexpectedly passed validation")
	}
}

func TestListOrphanedPageUsesStableDatabaseCursor(t *testing.T) {
	service := newTestService(t)
	configured := testIdentity(t, "configured-key")
	if _, err := service.Create(context.Background(), configured, "Configured", ProfileInput{Name: "default", Providers: []string{"codex"}, Models: []string{"gpt-5"}}); err != nil {
		t.Fatal(err)
	}
	for _, key := range []string{"orphan-a", "orphan-b", "orphan-c"} {
		if _, err := service.Create(context.Background(), testIdentity(t, key), key, ProfileInput{Name: "default", Providers: []string{"codex"}, Models: []string{"gpt-5"}}); err != nil {
			t.Fatal(err)
		}
	}
	first, err := service.ListOrphanedPage(context.Background(), []string{configured.Hash()}, 0, "", 2)
	if err != nil || len(first) != 2 {
		t.Fatalf("first page = %#v, %v", first, err)
	}
	second, err := service.ListOrphanedPage(context.Background(), []string{configured.Hash()}, first[1].CreatedAtMS, first[1].ID, 2)
	if err != nil || len(second) != 1 || second[0].ID == first[0].ID || second[0].ID == first[1].ID {
		t.Fatalf("second page = %#v, %v", second, err)
	}
}

func TestProfileValidationAndOptimisticVersion(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "key-b")
	policy, err := service.Create(context.Background(), identity, "", ProfileInput{
		Name: "default", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.CreateProfile(context.Background(), policy.ID, policy.Version-1, ProfileInput{Name: "bad", Providers: []string{"codex"}, Models: []string{"gpt-5"}}); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("version error = %v", err)
	}
	if _, err := service.CreateProfile(context.Background(), policy.ID, policy.Version, ProfileInput{Name: "bad", Providers: []string{"codex"}, Models: []string{"gpt-5"}, Mappings: []ModelMapping{{Source: "a", Target: "b"}}}); err == nil {
		t.Fatal("accepted mapping to a non-allowed target")
	}
	if err := service.DeletePolicy(context.Background(), policy.ID, policy.Version, "ordinary-delete"); !errors.Is(err, ErrPassthroughConfirmation) {
		t.Fatalf("delete confirmation error = %v", err)
	}
	if err := service.DeletePolicy(context.Background(), policy.ID, policy.Version, PassthroughConfirmation); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil || decision.Mode != ModePassthrough {
		t.Fatalf("decision after confirmed delete = %#v, %v", decision, err)
	}
}

func TestConcurrentWritesWithSameVersionHaveOneWinner(t *testing.T) {
	service := newTestService(t)
	policy, err := service.Create(context.Background(), testIdentity(t, "concurrent-version-key"), "before", ProfileInput{
		Name: "default", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	})
	if err != nil {
		t.Fatal(err)
	}

	const writers = 12
	start := make(chan struct{})
	errorsByWriter := make(chan error, writers)
	var ready sync.WaitGroup
	ready.Add(writers)
	for writer := 0; writer < writers; writer++ {
		writer := writer
		go func() {
			ready.Done()
			<-start
			_, updateErr := service.UpdateDisplayName(context.Background(), policy.ID, "writer-"+string(rune('a'+writer)), policy.Version)
			errorsByWriter <- updateErr
		}()
	}
	ready.Wait()
	close(start)

	successes := 0
	conflicts := 0
	for writer := 0; writer < writers; writer++ {
		updateErr := <-errorsByWriter
		switch {
		case updateErr == nil:
			successes++
		case errors.Is(updateErr, ErrVersionConflict):
			conflicts++
		default:
			t.Fatalf("concurrent write returned unexpected error: %v", updateErr)
		}
	}
	if successes != 1 || conflicts != writers-1 {
		t.Fatalf("successes=%d conflicts=%d; want 1 and %d", successes, conflicts, writers-1)
	}
	stored, err := service.Get(context.Background(), policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != policy.Version+1 || !strings.HasPrefix(stored.DisplayName, "writer-") {
		t.Fatalf("stored policy after concurrent writes = %#v", stored)
	}
}

func TestWorkspaceUpdateIsAtomicAndIncrementsVersionOnce(t *testing.T) {
	service := newTestService(t)
	policy, err := service.Create(context.Background(), testIdentity(t, "workspace-key"), "Before", ProfileInput{
		Name: "Production", Providers: []string{"codex"}, Models: []string{"gpt-5"},
	})
	if err != nil {
		t.Fatal(err)
	}
	profile := ProfileInput{Name: "Production v2", Providers: []string{"claude"}, Models: []string{"claude-sonnet-4-6"}}
	updated, err := service.UpdateWorkspace(context.Background(), policy.ID, policy.Version, WorkspaceUpdate{
		DisplayName: "After", ProfileID: policy.ActiveProfileID, Profile: &profile,
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Version != policy.Version+1 || updated.DisplayName != "After" || len(updated.Profiles) != 1 || updated.Profiles[0].Name != "Production v2" {
		t.Fatalf("atomic update = %#v", updated)
	}

	failedProfile := ProfileInput{Name: "Should not persist", Providers: []string{"codex"}, Models: []string{"gpt-5"}}
	if _, err := service.UpdateWorkspace(context.Background(), policy.ID, updated.Version, WorkspaceUpdate{
		DisplayName: "Must roll back", ProfileID: "missing-profile", Profile: &failedProfile,
	}); !errors.Is(err, ErrProfileNotFound) {
		t.Fatalf("missing profile error = %v", err)
	}
	stored, err := service.Get(context.Background(), policy.ID)
	if err != nil {
		t.Fatal(err)
	}
	if stored.Version != updated.Version || stored.DisplayName != updated.DisplayName || stored.Profiles[0].Name != updated.Profiles[0].Name {
		t.Fatalf("failed workspace write partially committed: %#v", stored)
	}
}

func TestProfileCatalogRejectsUnknownProviderAndModel(t *testing.T) {
	service := newTestService(t)
	service.SetCatalogProvider(func() (ProfileCatalog, error) {
		return NewProfileCatalog(
			[]string{"codex", "home"},
			[]string{"gpt-5", "home-model"},
			map[string][]string{"gpt-5": {"codex"}, "home-model": {"home"}},
		), nil
	})
	identity := testIdentity(t, "catalog-key")
	if _, err := service.Create(context.Background(), identity, "", ProfileInput{Name: "bad-provider", Providers: []string{"claude"}, Models: []string{"gpt-5"}}); err == nil || !strings.Contains(err.Error(), "unknown provider") {
		t.Fatalf("unknown provider error = %v", err)
	}
	if _, err := service.Create(context.Background(), identity, "", ProfileInput{Name: "bad-model", Providers: []string{"codex"}, Models: []string{"gpt-unknown"}}); err == nil || !strings.Contains(err.Error(), "unknown model") {
		t.Fatalf("unknown model error = %v", err)
	}
	linkedContext := WithProviderModelLinkageValidation(context.Background())
	if _, err := service.Create(linkedContext, identity, "", ProfileInput{Name: "wrong-provider", Providers: []string{"home"}, Models: []string{"gpt-5"}}); err == nil || !strings.Contains(err.Error(), "not available from an allowed provider") {
		t.Fatalf("provider/model mismatch error = %v", err)
	}
	if _, err := service.Create(linkedContext, identity, "", ProfileInput{Name: "wrong-mapping-provider", Providers: []string{"home"}, Mappings: []ModelMapping{{Source: "alias", Target: "gpt-5"}}}); err == nil || !strings.Contains(err.Error(), "not available from an allowed provider") {
		t.Fatalf("provider/mapping mismatch error = %v", err)
	}
	legacyIdentity := testIdentity(t, "legacy-catalog-key")
	if _, err := service.Create(context.Background(), legacyIdentity, "", ProfileInput{Name: "legacy-independent-fields", Providers: []string{"home"}, Models: []string{"gpt-5"}}); err != nil {
		t.Fatalf("legacy client mismatch must remain accepted: %v", err)
	}
	if _, err := service.Create(context.Background(), identity, "", ProfileInput{Name: "valid", Providers: []string{"Codex"}, Models: []string{"gpt-5"}}); err != nil {
		t.Fatal(err)
	}
}

func TestUsageAttributionDoesNotInventPassthroughProfile(t *testing.T) {
	passthrough := PassthroughDecision().UsageAttribution()
	if passthrough.PolicyMode != ModePassthrough || passthrough.APIKeyPolicyID != "" || passthrough.ProfileID != "" {
		t.Fatalf("passthrough attribution = %#v", passthrough)
	}
	decision := RequestPolicyDecision{Mode: ModeProfile, Snapshot: &RequestPolicySnapshot{
		PolicyID: "policy-a", ProfileID: "profile-a", ProfileName: "Production",
		RequestedModel: "smart", EffectiveModel: "gpt-5",
	}}
	attribution := decision.UsageAttribution()
	if attribution.PolicyMode != ModeProfile || attribution.APIKeyPolicyID != "policy-a" || attribution.ProfileID != "profile-a" || attribution.ProfileName != "Production" || attribution.RequestedModel != "smart" || attribution.EffectiveModel != "gpt-5" {
		t.Fatalf("profile attribution = %#v", attribution)
	}
}

func TestQuotaIsKeyWideAcrossProfilesAndResetIsAudited(t *testing.T) {
	service := newTestService(t)
	ctx := context.Background()
	identity, _ := NewAuthenticatedAPIKeyIdentity("quota-key")
	requestLimit, tokenLimit := int64(2), int64(10)
	policy, err := service.Create(ctx, identity, "Quota", ProfileInput{Name: "first"}, &QuotaInput{Enabled: true, Requests: &requestLimit, TotalTokens: &tokenLimit})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetTakeover(ctx, true); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	first, err := service.AdmitDecision(ctx, decision)
	if err != nil {
		t.Fatal(err)
	}
	attribution, ok := first.QuotaAttribution()
	if !ok {
		t.Fatal("quota attribution missing")
	}
	attempt := int64(0)
	if err = service.RecordQuotaTokens(ctx, attribution, QuotaUsageEventID(attribution, &attempt), 6); err != nil {
		t.Fatal(err)
	}
	settledCtx, cancelSettlement := context.WithCancel(WithDecision(ctx, first))
	cancelSettlement()
	if err = SettleQuotaTokens(settledCtx, "attempt:1", 1); err != nil {
		t.Fatalf("settlement after request cancellation: %v", err)
	}
	if err = service.RecordQuotaTokens(ctx, attribution, QuotaUsageEventID(attribution, &attempt), 6); err != nil {
		t.Fatal(err)
	}
	policy, err = service.CreateProfile(ctx, policy.ID, policy.Version, ProfileInput{Name: "second"})
	if err != nil {
		t.Fatal(err)
	}
	secondID := policy.Profiles[len(policy.Profiles)-1].ID
	policy, err = service.ActivateProfile(ctx, policy.ID, secondID, policy.Version)
	if err != nil {
		t.Fatal(err)
	}
	decision, err = service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdmitDecision(ctx, decision); err != nil {
		t.Fatal(err)
	}
	decision, err = service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdmitDecision(ctx, decision); err == nil {
		t.Fatal("profile switch bypassed request quota")
	}
	loaded, err := service.Get(ctx, policy.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.RequestsUsed != 2 || loaded.Quota.Usage.TotalTokensUsed != 7 {
		t.Fatalf("quota usage = %#v error=%v", loaded.Quota, err)
	}
	if _, err = service.ResetQuota(ctx, policy.ID, loaded.Version, "wrong"); !errors.Is(err, ErrQuotaResetConfirmation) {
		t.Fatalf("reset confirmation error = %v", err)
	}
	reset, err := service.ResetQuota(ctx, policy.ID, loaded.Version, QuotaResetConfirmation)
	if err != nil || reset.Quota == nil || reset.Quota.Usage.RequestsUsed != 0 || reset.Quota.Usage.TotalTokensUsed != 0 || reset.Quota.Epoch <= loaded.Quota.Epoch {
		t.Fatalf("reset quota = %#v error=%v", reset.Quota, err)
	}
	if err = service.RecordQuotaTokens(ctx, attribution, "late-after-reset", 2); !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatalf("stale pre-reset settlement error = %v", err)
	}
	var admissions, events int
	if err = service.store.db.QueryRowContext(ctx, `select count(*) from api_key_quota_admissions where policy_id = ?`, policy.ID).Scan(&admissions); err != nil || admissions != 0 {
		t.Fatalf("reset admissions = %d error=%v", admissions, err)
	}
	if err = service.store.db.QueryRowContext(ctx, `select count(*) from api_key_quota_token_events where policy_id = ?`, policy.ID).Scan(&events); err != nil || events != 0 {
		t.Fatalf("reset token events = %d error=%v", events, err)
	}
	postResetDecision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	postResetAdmission, err := service.AdmitDecision(ctx, postResetDecision)
	if err != nil {
		t.Fatal(err)
	}
	postResetAttribution, ok := postResetAdmission.QuotaAttribution()
	if !ok {
		t.Fatal("post-reset quota attribution missing")
	}
	removed, err := service.UpdateWorkspace(ctx, policy.ID, reset.Version, WorkspaceUpdate{DisplayName: "Quota", Quota: QuotaUpdate{Present: true}})
	if err != nil || removed.Quota != nil {
		t.Fatalf("removed quota = %#v error=%v", removed.Quota, err)
	}
	recreated, err := service.UpdateWorkspace(ctx, policy.ID, removed.Version, WorkspaceUpdate{DisplayName: "Quota", Quota: QuotaUpdate{Present: true, Value: &QuotaInput{Enabled: true, Requests: &requestLimit, TotalTokens: &tokenLimit}}})
	if err != nil || recreated.Quota == nil || recreated.Quota.Epoch <= reset.Quota.Epoch {
		t.Fatalf("recreated quota = %#v error=%v", recreated.Quota, err)
	}
	if err = service.RecordQuotaTokens(ctx, postResetAttribution, "late-after-recreate", 2); !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatalf("stale pre-recreate settlement error = %v", err)
	}
	audits, err := service.store.ListAudits(ctx)
	if err != nil || audits[len(audits)-1].EventType != "api_key_quota_reset" {
		t.Fatalf("reset audit = %#v error=%v", audits, err)
	}
}

func TestAdmitQuotaTurnCreatesFreshPerTurnAdmission(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "websocket-turn-key")
	requestLimit := int64(1)
	if _, err := service.Create(context.Background(), identity, "WebSocket", ProfileInput{Name: "turns"}, &QuotaInput{Enabled: true, Requests: &requestLimit}); err != nil {
		t.Fatal(err)
	}
	if err := service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	base := WithQuotaAdmission(WithDecision(context.Background(), decision), service.AdmitDecision)
	first, err := AdmitQuotaTurn(base)
	if err != nil {
		t.Fatal(err)
	}
	firstDecision, ok := DecisionFromContext(first)
	if !ok || firstDecision.Snapshot == nil || firstDecision.Snapshot.QuotaAdmissionID == "" {
		t.Fatalf("first turn decision = %#v", firstDecision)
	}
	inherited := InheritContext(first, base)
	inheritedDecision, _ := DecisionFromContext(inherited)
	if inheritedDecision.Snapshot == nil || inheritedDecision.Snapshot.QuotaAdmissionID != firstDecision.Snapshot.QuotaAdmissionID {
		t.Fatalf("request context overwrote per-turn admission: %#v", inheritedDecision)
	}
	if _, err = AdmitQuotaTurn(base); err == nil {
		t.Fatal("second websocket turn bypassed request quota")
	}
}

func TestCostQuotaUsesServerPricingAndFailsClosedAfterExhaustion(t *testing.T) {
	service := newTestService(t)
	service.SetCostEstimator(func(_ context.Context, usage QuotaUsageDelta) (int64, error) {
		if usage.Provider != "codex" || usage.AuthType != "oauth" || usage.Model != "gpt-5" || usage.InputTokens != 8 || usage.OutputTokens != 2 {
			t.Fatalf("cost usage input = %#v", usage)
		}
		return 1_250_000, nil
	})
	identity := testIdentity(t, "cost-quota-key")
	costLimit := 1.0
	created, err := service.Create(context.Background(), identity, "Cost", ProfileInput{Name: "priced"}, &QuotaInput{
		Enabled: true, Cost: &costLimit, Period: QuotaPeriod{Type: QuotaPeriodAllTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	if err = SettleQuotaUsage(WithDecision(context.Background(), admitted), "priced-attempt", QuotaUsageDelta{
		Provider: "codex", AuthType: "oauth", Model: "gpt-5", InputTokens: 8, OutputTokens: 2, TotalTokens: 10,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.CostUsed != 1.25 || loaded.Quota.Usage.TotalTokensUsed != 10 || len(loaded.Quota.Usage.Exhausted) != 1 || loaded.Quota.Usage.Exhausted[0] != "cost" {
		t.Fatalf("settled cost quota = %#v error=%v", loaded.Quota, err)
	}
	decision, err = service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdmitDecision(context.Background(), decision); err == nil {
		t.Fatal("exhausted cost quota admitted another request")
	} else if exceeded := new(QuotaExceededError); !errors.As(err, &exceeded) || exceeded.Metric != "cost" {
		t.Fatalf("cost exhaustion error = %v", err)
	}
}

func TestCostQuotaSettlementIsIdempotentWithoutRepricing(t *testing.T) {
	service := newTestService(t)
	var pricingCalls atomic.Int64
	service.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) {
		pricingCalls.Add(1)
		return 500_000, nil
	})
	identity := testIdentity(t, "idempotent-cost-key")
	costLimit := 10.0
	created, err := service.Create(context.Background(), identity, "Idempotent", ProfileInput{Name: "priced"}, &QuotaInput{Enabled: true, Cost: &costLimit})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	settlementCtx := WithDecision(context.Background(), admitted)
	usage := QuotaUsageDelta{Provider: "codex", Model: "gpt-5", TotalTokens: 5}
	if err = SettleQuotaUsage(settlementCtx, "terminal", usage); err != nil {
		t.Fatal(err)
	}
	if err = SettleQuotaUsage(settlementCtx, "terminal", usage); err != nil {
		t.Fatalf("duplicate settlement = %v", err)
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.TotalTokensUsed != 5 || loaded.Quota.Usage.CostUsed != 0.5 || pricingCalls.Load() != 1 {
		t.Fatalf("idempotent cost usage = %#v pricingCalls=%d error=%v", loaded.Quota, pricingCalls.Load(), err)
	}
}

func TestMissingCostPriceSettlesTokensAtZeroCostWithoutBlocking(t *testing.T) {
	service := newTestService(t)
	service.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) {
		return 0, ErrQuotaPriceMissing
	})
	costLimit := 10.0
	identity := testIdentity(t, "missing-price-key")
	created, err := service.Create(context.Background(), identity, "Missing price", ProfileInput{Name: "missing"}, &QuotaInput{
		Enabled: true, Cost: &costLimit, Period: QuotaPeriod{Type: QuotaPeriodAllTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	if err = SettleQuotaUsage(WithDecision(context.Background(), admitted), "missing-price", QuotaUsageDelta{
		Provider: "codex", Model: "missing-price", InputTokens: 3, TotalTokens: 3,
	}); err != nil {
		t.Fatalf("missing price settlement failed: %v", err)
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.TotalTokensUsed != 3 || loaded.Quota.Usage.CostUsed != 0 {
		t.Fatalf("missing-price usage = %#v error=%v", loaded.Quota, err)
	}
	if _, err = service.AdmitDecision(context.Background(), decision); err != nil {
		t.Fatalf("missing price blocked the key: %v", err)
	}
}

func TestPricingStoreFailureBlocksOnlyAffectedPolicyAndRecovers(t *testing.T) {
	service := newTestService(t)
	var missingPrice atomic.Bool
	missingPrice.Store(true)
	service.SetCostEstimator(func(_ context.Context, usage QuotaUsageDelta) (int64, error) {
		if usage.Model == "storage-failure" && missingPrice.Load() {
			return 0, errors.New("price database is unavailable")
		}
		return 250_000, nil
	})
	costLimit := 10.0
	blockedIdentity := testIdentity(t, "blocked-pricing-key")
	blockedPolicy, err := service.Create(context.Background(), blockedIdentity, "Blocked", ProfileInput{Name: "blocked"}, &QuotaInput{
		Enabled: true, Cost: &costLimit, Period: QuotaPeriod{Type: QuotaPeriodAllTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	healthyIdentity := testIdentity(t, "healthy-pricing-key")
	_, err = service.Create(context.Background(), healthyIdentity, "Healthy", ProfileInput{Name: "healthy"}, &QuotaInput{
		Enabled: true, Cost: &costLimit, Period: QuotaPeriod{Type: QuotaPeriodAllTime},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	blockedDecision, err := service.Decide(blockedIdentity)
	if err != nil {
		t.Fatal(err)
	}
	blockedAdmission, err := service.AdmitDecision(context.Background(), blockedDecision)
	if err != nil {
		t.Fatal(err)
	}
	if err = SettleQuotaUsage(WithDecision(context.Background(), blockedAdmission), "storage-failure", QuotaUsageDelta{
		Provider: "codex", Model: "storage-failure", InputTokens: 1, TotalTokens: 1,
	}); !errors.Is(err, errQuotaPricingUnavailable) {
		t.Fatalf("pricing error = %v", err)
	}
	if !service.Healthy() || service.pendingCount.Load() != 0 {
		t.Fatalf("pricing miss poisoned global service: healthy=%v pending=%d", service.Healthy(), service.pendingCount.Load())
	}
	if _, err = service.AdmitDecision(context.Background(), blockedDecision); !errors.Is(err, ErrQuotaUnavailable) {
		t.Fatalf("affected policy admission error = %v", err)
	}
	healthyDecision, err := service.Decide(healthyIdentity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdmitDecision(context.Background(), healthyDecision); err != nil {
		t.Fatalf("unrelated policy was blocked: %v", err)
	}

	missingPrice.Store(false)
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		blockedDecision, err = service.Decide(blockedIdentity)
		if err == nil {
			if _, err = service.AdmitDecision(context.Background(), blockedDecision); err == nil {
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("affected policy did not recover: %v", err)
	}
	loaded, err := service.Get(context.Background(), blockedPolicy.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.CostUsed != 0.25 {
		t.Fatalf("recovered cost usage = %#v error=%v", loaded.Quota, err)
	}
}

func TestPricingStoreFailurePersistsBlockedSettlementAcrossRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pro.sqlite")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity(t, "restart-pricing-key")
	tokenLimit := int64(100)
	costLimit := 10.0
	policy, err := service.Create(context.Background(), identity, "Restart", ProfileInput{Name: "default"}, &QuotaInput{
		Enabled: true, TotalTokens: &tokenLimit, Cost: &costLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	service.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) {
		return 0, errors.New("pricing database is unavailable")
	})
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	if err = SettleQuotaUsage(WithDecision(context.Background(), admitted), "terminal", QuotaUsageDelta{
		Provider: "codex", Model: "gpt-5", TotalTokens: 20,
	}); !errors.Is(err, errQuotaPricingUnavailable) {
		t.Fatalf("pricing failure = %v", err)
	}
	if err = service.Close(); err != nil {
		t.Fatal(err)
	}

	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err = NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	summaries, err := service.ListQuotaSummaries(context.Background(), time.Now().UnixMilli())
	if err != nil || len(summaries) != 1 || summaries[0].AdmissionState != QuotaAdmissionBlocked || summaries[0].BlockedReason != QuotaBlockPricingStore {
		t.Fatalf("restored blocked settlement = %#v error=%v", summaries, err)
	}
	service.SetCostEstimator(func(_ context.Context, usage QuotaUsageDelta) (int64, error) {
		if usage.Model != "gpt-5" || usage.TotalTokens != 20 {
			t.Fatalf("restored usage = %#v", usage)
		}
		return 250_000, nil
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		summaries, err = service.ListQuotaSummaries(context.Background(), time.Now().UnixMilli())
		if err == nil && len(summaries) == 1 && summaries[0].AdmissionState == QuotaAdmissionAvailable && summaries[0].Quota != nil && summaries[0].Quota.Usage.TotalTokensUsed == 20 && summaries[0].Quota.Usage.CostUsed == 0.25 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || len(summaries) != 1 || summaries[0].PolicyID != policy.ID || summaries[0].AdmissionState != QuotaAdmissionAvailable || summaries[0].Quota.Usage.TotalTokensUsed != 20 || summaries[0].Quota.Usage.CostUsed != 0.25 {
		t.Fatalf("recovered settlement = %#v error=%v", summaries, err)
	}
	var pending int
	if err = service.store.db.QueryRow(`select count(*) from api_key_quota_pending_settlements`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending settlements = %d error=%v", pending, err)
	}
}

func TestPolicyBackupRestoresPendingQuotaSettlementAndBlockedState(t *testing.T) {
	sourceStore, err := OpenStore(filepath.Join(t.TempDir(), "source.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	source, err := NewService(sourceStore)
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity(t, "pending-backup-key")
	tokenLimit := int64(100)
	costLimit := 10.0
	policy, err := source.Create(context.Background(), identity, "Pending backup", ProfileInput{Name: "default"}, &QuotaInput{
		Enabled: true, TotalTokens: &tokenLimit, Cost: &costLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err = source.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	source.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) {
		return 0, errors.New("pricing database is unavailable")
	})
	decision, err := source.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := source.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	if err = SettleQuotaUsage(WithDecision(context.Background(), admitted), "terminal", QuotaUsageDelta{Provider: "codex", Model: "gpt-5", TotalTokens: 20}); !errors.Is(err, errQuotaPricingUnavailable) {
		t.Fatalf("pricing failure = %v", err)
	}
	payload, err := source.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	var document backupDocument
	if err = json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	if document.SchemaVersion != 10 || len(document.PendingQuotaSettlements) != 1 || document.PendingQuotaSettlements[0].Usage.TotalTokens != 20 {
		t.Fatalf("pending backup document = %#v", document)
	}
	invalid := document
	invalid.PendingQuotaSettlements = append([]backupPendingSettlement(nil), document.PendingQuotaSettlements...)
	invalid.PendingQuotaSettlements[0].BlockReason = "invalid"
	invalidPayload, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if err = source.Close(); err != nil {
		t.Fatal(err)
	}

	targetStore, err := OpenStore(filepath.Join(t.TempDir(), "target.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	target, err := NewService(targetStore)
	if err != nil {
		t.Fatal(err)
	}
	defer target.Close()
	target.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) {
		return 0, errors.New("pricing database is unavailable")
	})
	if err = target.ImportBackup(context.Background(), invalidPayload); err == nil || !strings.Contains(err.Error(), "block reason") {
		t.Fatalf("invalid pending settlement error = %v", err)
	}
	if err = target.ImportBackup(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	summaries, err := target.ListQuotaSummaries(context.Background(), time.Now().UnixMilli())
	if err != nil || len(summaries) != 1 || summaries[0].AdmissionState != QuotaAdmissionBlocked || summaries[0].BlockedReason != QuotaBlockPricingStore {
		t.Fatalf("restored pending summary = %#v error=%v", summaries, err)
	}
	target.SetCostEstimator(func(_ context.Context, usage QuotaUsageDelta) (int64, error) {
		if usage.Model != "gpt-5" || usage.TotalTokens != 20 {
			t.Fatalf("restored usage = %#v", usage)
		}
		return 250_000, nil
	})
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		summaries, err = target.ListQuotaSummaries(context.Background(), time.Now().UnixMilli())
		if err == nil && len(summaries) == 1 && summaries[0].AdmissionState == QuotaAdmissionAvailable && summaries[0].Quota != nil && summaries[0].Quota.Usage.TotalTokensUsed == 20 && summaries[0].Quota.Usage.CostUsed == 0.25 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err != nil || len(summaries) != 1 || summaries[0].PolicyID != policy.ID || summaries[0].Quota == nil || summaries[0].Quota.Usage.TotalTokensUsed != 20 || summaries[0].Quota.Usage.CostUsed != 0.25 {
		t.Fatalf("restored pending settlement = %#v error=%v", summaries, err)
	}
}

func TestPolicyRestoreFencesPreRestoreRetriesAndInFlightSettlement(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "restore-generation-key")
	tokenLimit := int64(100)
	costLimit := 10.0
	policy, err := service.Create(context.Background(), identity, "Restore generation", ProfileInput{Name: "default"}, &QuotaInput{
		Enabled: true, TotalTokens: &tokenLimit, Cost: &costLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := service.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	service.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) {
		return 0, errors.New("pricing database is unavailable")
	})
	if err = SettleQuotaUsage(WithDecision(context.Background(), admitted), "before-restore", QuotaUsageDelta{Provider: "codex", Model: "gpt-5", TotalTokens: 20}); !errors.Is(err, errQuotaPricingUnavailable) {
		t.Fatalf("pricing failure = %v", err)
	}
	if !service.quotaPricingIsBlocked(policy.ID, policy.Quota.Epoch) {
		t.Fatal("pre-restore retry did not block the policy")
	}
	if err = service.ImportBackup(context.Background(), payload); err != nil {
		t.Fatal(err)
	}
	service.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) { return 250_000, nil })
	if err = SettleQuotaUsage(WithDecision(context.Background(), admitted), "after-restore", QuotaUsageDelta{Provider: "codex", Model: "gpt-5", TotalTokens: 30}); err != nil {
		t.Fatalf("stale in-flight settlement should be discarded: %v", err)
	}
	time.Sleep(400 * time.Millisecond)
	summaries, err := service.ListQuotaSummaries(context.Background(), time.Now().UnixMilli())
	if err != nil || len(summaries) != 1 || summaries[0].Quota == nil || summaries[0].AdmissionState != QuotaAdmissionAvailable || summaries[0].Quota.Usage.TotalTokensUsed != 0 || summaries[0].Quota.Usage.CostUsed != 0 {
		t.Fatalf("pre-restore work mutated restored state = %#v error=%v", summaries, err)
	}
	var pending int
	if err = service.store.db.QueryRow(`select count(*) from api_key_quota_pending_settlements`).Scan(&pending); err != nil || pending != 0 {
		t.Fatalf("pending settlements after restore = %d error=%v", pending, err)
	}
}

func TestPolicyRestoreFailureResumesQuotaRuntimeWithoutAdvancingGeneration(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "failed-restore-key")
	requestLimit := int64(10)
	if _, err := service.Create(context.Background(), identity, "Failed restore", ProfileInput{Name: "default"}, &QuotaInput{
		Enabled: true, Requests: &requestLimit,
	}); err != nil {
		t.Fatal(err)
	}
	payload, err := service.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	generation := service.quotaRuntimeGeneration.Load()
	if _, err = service.store.db.Exec(`create trigger fail_api_key_policy_restore before insert on api_key_policies begin select raise(abort, 'restore failed'); end`); err != nil {
		t.Fatal(err)
	}
	if err = service.ImportBackup(context.Background(), payload); err == nil || !strings.Contains(err.Error(), "restore failed") {
		t.Fatalf("restore failure = %v", err)
	}
	if service.quotaRuntimeIsPaused() {
		t.Fatal("failed restore left quota runtime paused")
	}
	if got := service.quotaRuntimeGeneration.Load(); got != generation {
		t.Fatalf("quota runtime generation = %d, want %d", got, generation)
	}
	if _, err = service.store.db.Exec(`drop trigger fail_api_key_policy_restore`); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdmitDecision(context.Background(), decision); err != nil {
		t.Fatalf("quota admission after failed restore: %v", err)
	}
}

func TestQuotaRuntimePauseWaitsForActiveOperation(t *testing.T) {
	service := newTestService(t)
	generation := service.quotaRuntimeGeneration.Load()
	release, err := service.acquireQuotaRuntime(context.Background(), generation, true)
	if err != nil {
		t.Fatal(err)
	}
	pauseDone := make(chan error, 1)
	go func() {
		pauseDone <- service.PauseQuotaRuntime(context.Background())
	}()
	deadline := time.Now().Add(time.Second)
	for !service.quotaRuntimeIsPaused() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !service.quotaRuntimeIsPaused() {
		release()
		t.Fatal("quota runtime did not enter paused state")
	}
	select {
	case err = <-pauseDone:
		release()
		t.Fatalf("pause returned before active quota operation completed: %v", err)
	default:
	}
	release()
	if err = <-pauseDone; err != nil {
		t.Fatal(err)
	}
	if err = service.ResumeQuotaRuntime(context.Background()); err != nil {
		t.Fatal(err)
	}
	resumedRelease, err := service.acquireQuotaRuntime(context.Background(), generation, false)
	if err != nil {
		t.Fatalf("quota runtime did not resume: %v", err)
	}
	resumedRelease()
}

func TestQuotaHistoryPruningKeepsCurrentWindowAndPendingSettlements(t *testing.T) {
	service := newTestService(t)
	requestLimit := int64(100)
	tokenLimit := int64(1_000)
	periodValue := int64(1)
	policy, err := service.Create(context.Background(), testIdentity(t, "quota-pruning-key"), "Pruning", ProfileInput{Name: "default"}, &QuotaInput{
		Enabled: true, Requests: &requestLimit, TotalTokens: &tokenLimit,
		Period: QuotaPeriod{Type: QuotaPeriodPastDuration, Value: &periodValue, Unit: "hour"},
	})
	if err != nil {
		t.Fatal(err)
	}
	nowMS := time.Now().UnixMilli()
	expiredAt := nowMS - int64(8*24*time.Hour/time.Millisecond)
	currentAt := nowMS - int64(30*time.Minute/time.Millisecond)
	if _, err = service.store.db.Exec(`insert into api_key_quota_admissions(admission_id, policy_id, profile_id, epoch, admitted_at_ms) values
		(?, ?, ?, ?, ?), (?, ?, ?, ?, ?), (?, ?, ?, ?, ?)`,
		"expired", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, expiredAt,
		"current", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, currentAt,
		"pending", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, expiredAt); err != nil {
		t.Fatal(err)
	}
	if _, err = service.store.db.Exec(`insert into api_key_quota_token_events(event_id, admission_id, policy_id, profile_id, epoch, total_tokens, cost_micros, occurred_at_ms) values
		(?, ?, ?, ?, ?, ?, 0, ?), (?, ?, ?, ?, ?, ?, 0, ?), (?, ?, ?, ?, ?, ?, 0, ?)`,
		"expired:event", "expired", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, 1, expiredAt,
		"current:event", "current", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, 2, currentAt,
		"pending:event-old", "pending", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, 3, expiredAt); err != nil {
		t.Fatal(err)
	}
	usageJSON, err := json.Marshal(QuotaUsageDelta{TotalTokens: 4})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.store.db.Exec(`insert into api_key_quota_pending_settlements(event_id, admission_id, policy_id, profile_id, epoch, usage_json, require_cost, quoted, cost_micros, block_reason, created_at_ms, updated_at_ms) values(?, ?, ?, ?, ?, ?, 0, 0, 0, ?, ?, ?)`,
		"pending:event", "pending", policy.ID, policy.ActiveProfileID, policy.Quota.Epoch, string(usageJSON), QuotaBlockSettlementStore, expiredAt, expiredAt); err != nil {
		t.Fatal(err)
	}
	if err = service.pruneQuotaHistory(context.Background(), nowMS); err != nil {
		t.Fatal(err)
	}
	for id, want := range map[string]int{"expired": 0, "current": 1, "pending": 1} {
		var count int
		if err = service.store.db.QueryRow(`select count(*) from api_key_quota_admissions where admission_id = ?`, id).Scan(&count); err != nil || count != want {
			t.Fatalf("admission %s count = %d, want %d, error=%v", id, count, want, err)
		}
	}
	var events int
	if err = service.store.db.QueryRow(`select count(*) from api_key_quota_token_events`).Scan(&events); err != nil || events != 2 {
		t.Fatalf("retained events = %d error=%v", events, err)
	}
}

func TestRequestOnlyQuotaDoesNotInstallUsageSettlement(t *testing.T) {
	service := newTestService(t)
	requestLimit := int64(10)
	identity := testIdentity(t, "request-only-quota-key")
	if _, err := service.Create(context.Background(), identity, "Requests", ProfileInput{Name: "default"}, &QuotaInput{Enabled: true, Requests: &requestLimit}); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	if admitted.Snapshot == nil || admitted.Snapshot.QuotaAdmissionID == "" || admitted.Snapshot.QuotaUsageSettlement != nil {
		t.Fatalf("request-only admission = %#v", admitted)
	}
	if err = SettleQuotaUsage(WithDecision(context.Background(), admitted), "unused", QuotaUsageDelta{TotalTokens: 99}); err != nil {
		t.Fatal(err)
	}
	for table, want := range map[string]int{"api_key_quota_token_events": 0, "api_key_quota_pending_settlements": 0} {
		var count int
		if err = service.store.db.QueryRow(`select count(*) from ` + table).Scan(&count); err != nil || count != want {
			t.Fatalf("%s count = %d, want %d, error=%v", table, count, want, err)
		}
	}
}

func TestQuotaSettlementRetryKeepsFirstSuccessfulPriceQuote(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "stable-cost-quote-key")
	costLimit := 10.0
	created, err := service.Create(context.Background(), identity, "Stable quote", ProfileInput{Name: "priced"}, &QuotaInput{
		Enabled: true, Cost: &costLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	attribution, ok := admitted.QuotaAttribution()
	if !ok {
		t.Fatal("quota attribution missing")
	}
	var pricingCalls atomic.Int64
	service.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) {
		pricingCalls.Add(1)
		return 9_000_000, nil
	})
	service.retryQuotaSettlement(attribution, attribution.AdmissionID+":stable-price", QuotaUsageDelta{TotalTokens: 2}, true, 1_250_000, true)
	deadline := time.Now().Add(2 * time.Second)
	for service.pendingCount.Load() != 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if service.pendingCount.Load() != 0 {
		t.Fatal("quota settlement retry did not complete")
	}
	if pricingCalls.Load() != 0 {
		t.Fatalf("retry re-ran pricing %d times", pricingCalls.Load())
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.CostUsed != 1.25 || loaded.Quota.Usage.TotalTokensUsed != 2 {
		t.Fatalf("retried fixed quote = %#v error=%v", loaded.Quota, err)
	}
}

func TestCostLimitEditPreservesEpochUsageAndDoesNotAuditReset(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "cost-limit-edit-key")
	requestLimit, tokenLimit := int64(10), int64(100)
	costLimit := 10.0
	service.SetCostEstimator(func(context.Context, QuotaUsageDelta) (int64, error) {
		return 500_000, nil
	})
	created, err := service.Create(context.Background(), identity, "Cost edit", ProfileInput{Name: "default"}, &QuotaInput{
		Enabled: true, Requests: &requestLimit, TotalTokens: &tokenLimit, Cost: &costLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	if err = SettleQuotaUsage(WithDecision(context.Background(), admitted), "cost-limit-edit-usage", QuotaUsageDelta{
		Provider: "codex", Model: "gpt-5", TotalTokens: 5,
	}); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.RequestsUsed != 1 || loaded.Quota.Usage.TotalTokensUsed != 5 || loaded.Quota.Usage.CostUsed != 0.5 {
		t.Fatalf("initial usage = %#v error=%v", loaded.Quota, err)
	}
	previousEpoch := loaded.Quota.Epoch
	auditsBefore, err := service.store.ListAudits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	newCostLimit := 20.0
	updated, err := service.UpdateWorkspace(context.Background(), created.ID, loaded.Version, WorkspaceUpdate{
		DisplayName: loaded.DisplayName,
		Quota: QuotaUpdate{Present: true, Value: &QuotaInput{
			Enabled: true, Requests: &requestLimit, TotalTokens: &tokenLimit, Cost: &newCostLimit,
		}},
	})
	if err != nil || updated.Quota == nil || updated.Quota.Epoch != previousEpoch || updated.Quota.Usage.RequestsUsed != 1 || updated.Quota.Usage.TotalTokensUsed != 5 || updated.Quota.Usage.CostUsed != 0.5 {
		t.Fatalf("cost limit edit = %#v error=%v", updated.Quota, err)
	}
	auditsAfter, err := service.store.ListAudits(context.Background())
	if err != nil || len(auditsAfter) != len(auditsBefore) {
		t.Fatalf("cost limit edit audits before=%#v after=%#v error=%v", auditsBefore, auditsAfter, err)
	}
}

func TestRollingQuotaPeriodChangeResetsEpochAndWritesAudit(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "rolling-period-edit-key")
	requestLimit := int64(2)
	periodValue := int64(1)
	created, err := service.Create(context.Background(), identity, "Rolling", ProfileInput{Name: "window"}, &QuotaInput{
		Enabled: true, Requests: &requestLimit,
		Period: QuotaPeriod{Type: QuotaPeriodPastDuration, Value: &periodValue, Unit: "minute"},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdmitDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.RequestsUsed != 1 {
		t.Fatalf("rolling usage = %#v error=%v", loaded.Quota, err)
	}
	widerPeriod := int64(3)
	updated, err := service.UpdateWorkspace(context.Background(), created.ID, loaded.Version, WorkspaceUpdate{
		DisplayName: loaded.DisplayName,
		Quota: QuotaUpdate{Present: true, Value: &QuotaInput{
			Enabled: true, Requests: &requestLimit,
			Period: QuotaPeriod{Type: QuotaPeriodPastDuration, Value: &widerPeriod, Unit: "minute"},
		}},
	})
	if err != nil || updated.Quota == nil || updated.Quota.Epoch <= loaded.Quota.Epoch || updated.Quota.Usage.RequestsUsed != 0 {
		t.Fatalf("period reset = %#v error=%v", updated.Quota, err)
	}
	audits, err := service.store.ListAudits(context.Background())
	if err != nil || audits[len(audits)-1].EventType != "api_key_quota_period_reset" {
		t.Fatalf("period reset audit = %#v error=%v", audits, err)
	}
}

func TestLegacyQuotaWritePreservesExistingCalendarTimezone(t *testing.T) {
	service := newTestService(t)
	requestLimit := int64(2)
	created, err := service.Create(WithQuotaTimezoneAwareness(context.Background()), testIdentity(t, "legacy-calendar-edit-key"), "Calendar", ProfileInput{Name: "window"}, &QuotaInput{
		Enabled: true, Requests: &requestLimit,
		Period: QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "day", Timezone: "Asia/Shanghai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(testIdentity(t, "legacy-calendar-edit-key"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err = service.AdmitDecision(context.Background(), decision); err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil {
		t.Fatal(err)
	}
	updated, err := service.UpdateWorkspace(context.Background(), created.ID, loaded.Version, WorkspaceUpdate{
		DisplayName: loaded.DisplayName,
		Quota: QuotaUpdate{Present: true, Value: &QuotaInput{
			Enabled: true, Requests: &requestLimit,
			Period: QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "month"},
		}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.Quota == nil || updated.Quota.Period.Timezone != "Asia/Shanghai" || updated.Quota.Period.Unit != "month" {
		t.Fatalf("legacy calendar update = %#v", updated.Quota)
	}
	if updated.Quota.Epoch <= loaded.Quota.Epoch || updated.Quota.Usage.RequestsUsed != 0 {
		t.Fatalf("legacy calendar reset = %#v, previous = %#v", updated.Quota, loaded.Quota)
	}

	_, err = service.UpdateWorkspace(context.Background(), created.ID, updated.Version, WorkspaceUpdate{
		DisplayName: updated.DisplayName,
		Quota: QuotaUpdate{Present: true, Value: &QuotaInput{
			Enabled: true, Requests: &requestLimit,
			Period: QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "month", Timezone: "America/New_York"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "key_quota_calendar_timezone") {
		t.Fatalf("legacy timezone change error = %v", err)
	}

	updated, err = service.UpdateWorkspace(WithQuotaTimezoneAwareness(context.Background()), created.ID, updated.Version, WorkspaceUpdate{
		DisplayName: updated.DisplayName,
		Quota: QuotaUpdate{Present: true, Value: &QuotaInput{
			Enabled: true, Requests: &requestLimit,
			Period: QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "month"},
		}},
	})
	if err != nil || updated.Quota == nil || updated.Quota.Period.Timezone != "UTC" {
		t.Fatalf("timezone-aware UTC reset = %#v error=%v", updated.Quota, err)
	}
}

func TestQuotaCalendarPeriodBoundsUseConfiguredTimezoneAndDST(t *testing.T) {
	now := time.Date(2026, time.August, 19, 12, 34, 56, 0, time.FixedZone("local", 8*60*60)).UnixMilli()
	dayStart, dayEnd := quotaPeriodBounds(QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "day"}, 1, now)
	if got := time.UnixMilli(dayStart).UTC(); got.Hour() != 0 || got.Day() != 19 || dayEnd-dayStart != int64(24*time.Hour/time.Millisecond) {
		t.Fatalf("UTC day bounds = %s .. %s", time.UnixMilli(dayStart).UTC(), time.UnixMilli(dayEnd).UTC())
	}
	monthStart, monthEnd := quotaPeriodBounds(QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "month", Timezone: "Asia/Shanghai"}, 1, now)
	if got, want := time.UnixMilli(monthStart).UTC(), time.Date(2026, time.July, 31, 16, 0, 0, 0, time.UTC); !got.Equal(want) || time.UnixMilli(monthEnd).UTC() != time.Date(2026, time.August, 31, 16, 0, 0, 0, time.UTC) {
		t.Fatalf("Shanghai month bounds = %s .. %s", time.UnixMilli(monthStart).UTC(), time.UnixMilli(monthEnd).UTC())
	}
	springForward := time.Date(2026, time.March, 8, 12, 0, 0, 0, time.UTC).UnixMilli()
	dstStart, dstEnd := quotaPeriodBounds(QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "day", Timezone: "America/New_York"}, 1, springForward)
	if got, want := time.UnixMilli(dstStart).UTC(), time.Date(2026, time.March, 8, 5, 0, 0, 0, time.UTC); !got.Equal(want) || time.UnixMilli(dstEnd).UTC() != time.Date(2026, time.March, 9, 4, 0, 0, 0, time.UTC) || dstEnd-dstStart != int64(23*time.Hour/time.Millisecond) {
		t.Fatalf("New York DST day bounds = %s .. %s", time.UnixMilli(dstStart).UTC(), time.UnixMilli(dstEnd).UTC())
	}
	if got := normalizeQuotaPeriod(QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "day"}).Timezone; got != "UTC" {
		t.Fatalf("legacy calendar timezone = %q, want UTC", got)
	}
	if err := validateQuotaPeriod(QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "day", Timezone: "Mars/Olympus"}); err == nil {
		t.Fatal("invalid IANA timezone accepted")
	}
	if err := validateQuotaPeriod(QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "day", Timezone: "Local"}); err == nil {
		t.Fatal("process-local timezone accepted")
	}
	quotaLocationCache.Delete("Asia/Shanghai")
	firstLocation, err := loadQuotaLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	cached, ok := quotaLocationCache.Load("Asia/Shanghai")
	if !ok || cached != firstLocation {
		t.Fatalf("timezone cache = %#v, want %#v", cached, firstLocation)
	}
	secondLocation, err := loadQuotaLocation("Asia/Shanghai")
	if err != nil || secondLocation != firstLocation {
		t.Fatalf("cached timezone = %#v error=%v, want %#v", secondLocation, err, firstLocation)
	}
}

func TestQuotaTimezoneMigrationDefaultsExistingCalendarRowsToUTC(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`create table api_key_policies (
		id text primary key, api_key_hash text not null unique, display_name text not null default '', active_profile_id text,
		version integer not null default 1, created_at_ms integer not null, updated_at_ms integer not null
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`create table api_key_policy_quotas (
		policy_id text primary key, enabled integer not null default 0, request_limit integer, token_limit integer,
		cost_limit_micros integer, period_type text not null default 'all_time', period_value integer,
		period_unit text not null default '', epoch integer not null default 1, started_at_ms integer not null,
		requests_used integer not null default 0, total_tokens_used integer not null default 0,
		cost_used_micros integer not null default 0, updated_at_ms integer not null
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`insert into api_key_policies(id, api_key_hash, display_name, version, created_at_ms, updated_at_ms) values('legacy-policy', 'legacy-hash', 'Legacy', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`insert into api_key_policy_quotas(policy_id, enabled, request_limit, period_type, period_unit, epoch, started_at_ms, updated_at_ms) values('legacy-policy', 1, 10, 'calendar_duration', 'day', 1, 1, 1)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	quota, err := getPolicyQuotaAt(context.Background(), store.db, "legacy-policy", time.Date(2026, time.August, 19, 12, 0, 0, 0, time.UTC).UnixMilli())
	if err != nil || quota == nil || quota.Period.Timezone != "UTC" {
		t.Fatalf("migrated quota = %#v error=%v", quota, err)
	}
}

func TestProfileEnforcementMigrationSeparatesPausedStateFromSelectedProfile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "legacy-profile.sqlite")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`create table api_key_policies (
		id text primary key, api_key_hash text not null unique, display_name text not null default '', active_profile_id text,
		version integer not null default 1, created_at_ms integer not null, updated_at_ms integer not null
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`create table api_key_profiles (
		id text primary key, policy_id text not null, name text not null, created_at_ms integer not null, updated_at_ms integer not null,
		unique(policy_id, name)
	)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`insert into api_key_policies(id, api_key_hash, display_name, active_profile_id, version, created_at_ms, updated_at_ms) values
		('enabled-policy', 'enabled-hash', 'Enabled', 'enabled-profile', 1, 1, 1),
		('paused-policy', 'paused-hash', 'Paused', null, 1, 2, 2)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`insert into api_key_profiles(id, policy_id, name, created_at_ms, updated_at_ms) values
		('enabled-profile', 'enabled-policy', 'Enabled', 1, 1),
		('paused-profile', 'paused-policy', 'Paused', 2, 2)`); err != nil {
		t.Fatal(err)
	}
	if err = db.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = store.Close() })
	enabledPolicy, err := store.Get(context.Background(), "enabled-policy")
	if err != nil || !enabledPolicy.ProfileEnabled || enabledPolicy.ActiveProfileID != "enabled-profile" {
		t.Fatalf("migrated enabled policy = %#v error=%v", enabledPolicy, err)
	}
	pausedPolicy, err := store.Get(context.Background(), "paused-policy")
	if err != nil || pausedPolicy.ProfileEnabled || pausedPolicy.ActiveProfileID != "paused-profile" {
		t.Fatalf("migrated paused policy = %#v error=%v", pausedPolicy, err)
	}
}

func TestQuotaPeriodIndexesIncludeWindowTimestamps(t *testing.T) {
	service := newTestService(t)
	for name, columns := range map[string]string{
		"idx_api_key_quota_admissions_window": "(policy_id, epoch, admitted_at_ms)",
		"idx_api_key_quota_tokens_window":     "(policy_id, epoch, occurred_at_ms)",
	} {
		var definition string
		if err := service.store.db.QueryRow(`select sql from sqlite_master where type = 'index' and name = ?`, name).Scan(&definition); err != nil {
			t.Fatalf("load index %s: %v", name, err)
		}
		if !strings.Contains(definition, columns) {
			t.Fatalf("index %s definition = %q, want columns %s", name, definition, columns)
		}
	}
}

func TestQuotaSettlementWaitsForSerializationBeforeDatabaseTimeout(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "settlement-lock-key")
	tokenLimit := int64(100)
	created, err := service.Create(context.Background(), identity, "Settlement", ProfileInput{Name: "lock"}, &QuotaInput{Enabled: true, TotalTokens: &tokenLimit})
	if err != nil {
		t.Fatal(err)
	}
	if err = service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	service.quotaMu.Lock()
	settled := make(chan error, 1)
	go func() {
		settled <- SettleQuotaTokens(WithDecision(context.Background(), admitted), "serialized", 3)
	}()
	select {
	case err = <-settled:
		service.quotaMu.Unlock()
		t.Fatalf("settlement returned while quota serialization was held: %v", err)
	case <-time.After(50 * time.Millisecond):
	}
	service.quotaMu.Unlock()
	if err = <-settled; err != nil {
		t.Fatal(err)
	}
	loaded, err := service.Get(context.Background(), created.ID)
	if err != nil || loaded.Quota == nil || loaded.Quota.Usage.TotalTokensUsed != 3 {
		t.Fatalf("settled quota = %#v error=%v", loaded.Quota, err)
	}
}

func TestPolicyBackupRoundTripAndValidation(t *testing.T) {
	service := newTestService(t)
	service.SetCatalogProvider(func() (ProfileCatalog, error) {
		return NewProfileCatalog([]string{"codex"}, []string{"gpt-5"}), nil
	})
	identity := testIdentity(t, "backup-key")
	requestLimit := int64(25)
	created, err := service.Create(WithQuotaTimezoneAwareness(context.Background()), identity, "Backup key", ProfileInput{Name: "Production", Providers: []string{"codex"}, Models: []string{"gpt-5"}}, &QuotaInput{
		Enabled: true, Requests: &requestLimit,
		Period: QuotaPeriod{Type: QuotaPeriodCalendarDuration, Unit: "month", Timezone: "Asia/Shanghai"},
	})
	if err != nil {
		t.Fatal(err)
	}
	payload, err := service.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(payload), "backup-key") || !strings.Contains(string(payload), identity.Hash()) {
		t.Fatalf("backup secret/hash boundary = %s", payload)
	}
	if !strings.Contains(string(payload), `"schema_version":10`) || !strings.Contains(string(payload), `"profile_enabled":true`) || !strings.Contains(string(payload), `"takeover_enabled":true`) || !strings.Contains(string(payload), `"eventType":"policy_created"`) || !strings.Contains(string(payload), `"requests":25`) || !strings.Contains(string(payload), `"timezone":"Asia/Shanghai"`) {
		t.Fatalf("backup schema/audit record = %s", payload)
	}
	if err := service.DeletePolicy(context.Background(), created.ID, created.Version, PassthroughConfirmation); err != nil {
		t.Fatal(err)
	}
	service.SetCatalogProvider(func() (ProfileCatalog, error) { return ProfileCatalog{}, nil })
	if _, err := service.PreviewBackup(context.Background(), payload, []string{identity.Hash()}); err != nil {
		t.Fatalf("preview backup without a live catalog: %v", err)
	}
	if err := service.ImportBackup(context.Background(), payload); err != nil {
		t.Fatalf("restore backup without a live catalog: %v", err)
	}
	decision, err := service.Decide(identity)
	if err != nil || decision.Mode != ModeProfile || decision.Snapshot == nil || decision.Snapshot.ProfileName != "Production" || decision.Snapshot.Quota == nil || decision.Snapshot.Quota.Requests == nil || *decision.Snapshot.Quota.Requests != requestLimit || decision.Snapshot.Quota.Period.Timezone != "Asia/Shanghai" {
		t.Fatalf("restored decision = %#v, %v", decision, err)
	}
	audits, err := service.store.ListAudits(context.Background())
	if err != nil || len(audits) != 1 || audits[0].PolicyID != created.ID || audits[0].EventType != "policy_created" {
		t.Fatalf("restored audits = %#v, %v", audits, err)
	}
	var invalid backupDocument
	if err := json.Unmarshal(payload, &invalid); err != nil {
		t.Fatal(err)
	}
	invalid.Policies[0].Profiles[0].Name = ""
	invalidPayload, err := json.Marshal(invalid)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.ImportBackup(context.Background(), invalidPayload); err == nil || !strings.Contains(err.Error(), "profile name is required") {
		t.Fatalf("invalid structural import error = %v", err)
	}
	decision, err = service.Decide(identity)
	if err != nil || decision.Mode != ModeProfile || decision.Snapshot.ProfileName != "Production" {
		t.Fatalf("failed import changed live snapshot = %#v, %v", decision, err)
	}
}

func TestPolicyBackupRetainsUsageAttributedToDeletedProfile(t *testing.T) {
	service := newTestService(t)
	identity := testIdentity(t, "deleted-profile-history-key")
	requestLimit, tokenLimit := int64(10), int64(100)
	created, err := service.Create(context.Background(), identity, "History", ProfileInput{Name: "old"}, &QuotaInput{
		Enabled: true, Requests: &requestLimit, TotalTokens: &tokenLimit,
	})
	if err != nil {
		t.Fatal(err)
	}
	oldProfileID := created.ActiveProfileID
	decision, err := service.Decide(identity)
	if err != nil {
		t.Fatal(err)
	}
	admitted, err := service.AdmitDecision(context.Background(), decision)
	if err != nil {
		t.Fatal(err)
	}
	attribution, ok := admitted.QuotaAttribution()
	if !ok {
		t.Fatal("quota attribution missing")
	}
	if err = service.RecordQuotaTokens(context.Background(), attribution, "old-profile-usage", 7); err != nil {
		t.Fatal(err)
	}
	updated, err := service.CreateProfile(context.Background(), created.ID, created.Version, ProfileInput{Name: "current"})
	if err != nil {
		t.Fatal(err)
	}
	currentProfileID := updated.Profiles[len(updated.Profiles)-1].ID
	updated, err = service.ActivateProfile(context.Background(), updated.ID, currentProfileID, updated.Version)
	if err != nil {
		t.Fatal(err)
	}
	updated, err = service.DeleteProfile(context.Background(), updated.ID, oldProfileID, updated.Version)
	if err != nil {
		t.Fatal(err)
	}
	payload, err := service.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(payload), `"profile_id":"`+oldProfileID+`"`) {
		t.Fatalf("backup lost historical profile attribution: %s", payload)
	}
	if err = service.ImportBackup(context.Background(), payload); err != nil {
		t.Fatalf("restore rejected deleted-profile attribution: %v", err)
	}
	restored, err := service.Get(context.Background(), created.ID)
	if err != nil || restored.ActiveProfileID != currentProfileID || len(restored.Profiles) != 1 || restored.Quota == nil || restored.Quota.Usage.RequestsUsed != 1 || restored.Quota.Usage.TotalTokensUsed != 7 {
		t.Fatalf("restored historical usage = %#v error=%v", restored, err)
	}
}

func TestPolicyBackupPreviewUsesCommittedConfiguredKeysAndLegacyArrayStillImports(t *testing.T) {
	service := newTestService(t)
	service.SetCatalogProvider(func() (ProfileCatalog, error) {
		return NewProfileCatalog([]string{"codex"}, []string{"gpt-5"}), nil
	})
	first := testIdentity(t, "configured-key")
	second := testIdentity(t, "missing-key")
	for _, identity := range []AuthenticatedAPIKeyIdentity{first, second} {
		if _, err := service.Create(context.Background(), identity, "", ProfileInput{Name: identity.Hash()[:8], Providers: []string{"codex"}, Models: []string{"gpt-5"}}); err != nil {
			t.Fatal(err)
		}
	}
	payload, err := service.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	preview, err := service.PreviewBackup(context.Background(), payload, []string{first.Hash()})
	if err != nil || preview.TargetPolicies != 2 || preview.TargetProfiles != 2 || preview.ReplacePolicies != 2 || preview.AssociatedPolicies != 1 || preview.OrphanedPolicies != 1 || !preview.CurrentTakeoverEnabled || !preview.TargetTakeoverEnabled {
		t.Fatalf("preview = %+v, %v", preview, err)
	}
	var document backupDocument
	if err := json.Unmarshal(payload, &document); err != nil {
		t.Fatal(err)
	}
	legacy, err := json.Marshal(document.Policies)
	if err != nil {
		t.Fatal(err)
	}
	legacyPreview, err := service.PreviewBackup(context.Background(), legacy, []string{first.Hash()})
	if err != nil || !legacyPreview.CurrentTakeoverEnabled || legacyPreview.TargetTakeoverEnabled {
		t.Fatalf("legacy preview = %+v, %v", legacyPreview, err)
	}
	if err := service.ImportBackup(context.Background(), legacy); err != nil {
		t.Fatalf("legacy policy array import = %v", err)
	}
}

func TestPolicyBackupPreviewAndImportShareCanonicalStagedProfiles(t *testing.T) {
	service := newTestService(t)
	service.SetCatalogProvider(func() (ProfileCatalog, error) {
		return NewProfileCatalog([]string{"codex"}, []string{"gpt-5"}), nil
	})
	identity := testIdentity(t, "canonical-backup-key")
	if _, err := service.Create(context.Background(), identity, "", ProfileInput{Name: " Production ", Providers: []string{"codex"}, Models: []string{"gpt-5"}}); err != nil {
		t.Fatal(err)
	}
	payload, err := service.ExportBackup(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	mutated := strings.Replace(string(payload), `"providers":["codex"]`, `"providers":[" CODEX ","codex"]`, 1)
	if mutated == string(payload) {
		t.Fatalf("provider fixture was not mutated: %s", payload)
	}
	if _, err = service.PreviewBackup(context.Background(), []byte(mutated), []string{identity.Hash()}); err != nil {
		t.Fatalf("canonical preview failed: %v", err)
	}
	if err = service.ImportBackup(context.Background(), []byte(mutated)); err != nil {
		t.Fatalf("canonical import failed after preview: %v", err)
	}
	policies, err := service.List(context.Background())
	if err != nil || len(policies) != 1 || len(policies[0].Profiles) != 1 {
		t.Fatalf("imported policies=%#v, err=%v", policies, err)
	}
	profile := policies[0].Profiles[0]
	if profile.Name != "Production" || len(profile.Providers) != 1 || profile.Providers[0] != "codex" {
		t.Fatalf("persisted non-canonical profile: %#v", profile)
	}
}

func TestForeignKeysRejectOrphansAndCascadeProfiles(t *testing.T) {
	service := newTestService(t)
	if _, err := service.store.db.Exec(`insert into api_key_profiles(id, policy_id, name, created_at_ms, updated_at_ms) values('orphan', 'missing', 'bad', 1, 1)`); err == nil {
		t.Fatal("foreign keys accepted an orphan profile")
	}
	identity := testIdentity(t, "key-c")
	policy, err := service.Create(context.Background(), identity, "", ProfileInput{Name: "default", Providers: []string{"codex"}, Models: []string{"gpt-5"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := service.DeletePolicy(context.Background(), policy.ID, policy.Version, PassthroughConfirmation); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := service.store.db.QueryRow(`select count(*) from api_key_profiles where policy_id = ?`, policy.ID).Scan(&count); err != nil {
		t.Fatal(err)
	}
	if count != 0 {
		t.Fatalf("profile count = %d, want cascade delete", count)
	}
}

func TestVisibleModelsRequireAllowedProviderAndExposeMappingAliases(t *testing.T) {
	decision := RequestPolicyDecision{Mode: ModeProfile, Snapshot: &RequestPolicySnapshot{
		ModelMappings:    map[string]string{"smart": "gpt-5"},
		AllowedModels:    map[string]struct{}{"gpt-5": {}, "claude-opus": {}},
		AllowedProviders: map[string]struct{}{"codex": {}},
	}}
	visible, err := decision.FilterVisibleModels([]ModelCandidate{
		{ID: "gpt-5", Providers: []string{"codex", "openai-compatibility"}},
		{ID: "claude-opus", Providers: []string{"claude"}},
		{ID: "unlisted", Providers: []string{"codex"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(visible) != 2 || visible[0] != (VisibleModel{ID: "gpt-5", EffectiveID: "gpt-5"}) || visible[1] != (VisibleModel{ID: "smart", EffectiveID: "gpt-5"}) {
		t.Fatalf("visible models = %#v", visible)
	}
}

func TestKeyDisabledSurvivesTakeoverMutationsReloadAndBackup(t *testing.T) {
	ctx := context.Background()
	service := newTestService(t)
	identity := testIdentity(t, "disabled-key")
	other := testIdentity(t, "other-key")
	if err := service.SetKeyDisabled(ctx, identity, true, false); err != nil {
		t.Fatal(err)
	}
	assertBlocked := func() {
		t.Helper()
		_, err := service.Decide(identity)
		var policyErr *PolicyError
		if !errors.As(err, &policyErr) || policyErr.Code != "api_key_disabled" {
			t.Fatalf("expected disabled error, got %v", err)
		}
		if _, err := service.Decide(other); err != nil {
			t.Fatal(err)
		}
	}
	assertBlocked()
	if err := service.SetKeyDisabled(ctx, identity, false, false); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale state accepted: %v", err)
	}
	policy, err := service.CreateOptionalProfile(ctx, identity, "Preserved policy", nil)
	if err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	for _, enabled := range []bool{false, true, false, true} {
		if err := service.SetTakeover(ctx, enabled); err != nil {
			t.Fatal(err)
		}
		if !service.KeyDisabled(identity) {
			t.Fatal("takeover erased disabled setting")
		}
		if enabled {
			assertBlocked()
		} else {
			decision, err := service.Decide(identity)
			if err != nil || decision.Mode != ModePassthrough {
				t.Fatalf("stopped takeover did not pass through: %v", err)
			}
		}
	}
	if err := service.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	payload, err := service.ExportBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := service.SetKeyDisabled(ctx, identity, false, true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(identity); err != nil {
		t.Fatal(err)
	}
	if err := service.ImportBackup(ctx, payload); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	if err := service.DeletePolicy(ctx, policy.ID, policy.Version, PassthroughConfirmation); err != nil {
		t.Fatal(err)
	}
	assertBlocked()
	service.MarkUnavailable()
	if _, err := service.Decide(identity); !errors.Is(err, ErrUnavailable) {
		t.Fatalf("unhealthy takeover must fail closed: %v", err)
	}
	if err := service.SetTakeover(ctx, false); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(identity); err != nil {
		t.Fatalf("emergency stop did not pass through: %v", err)
	}
}

func TestDisabledKeyPersistsAcrossServiceRestart(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pro.sqlite")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	identity := testIdentity(t, "restart-key")
	if err := service.SetKeyDisabled(context.Background(), identity, true, false); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	service, err = NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	if !service.KeyDisabled(identity) {
		t.Fatal("disabled key re-enabled after restart")
	}
	if _, err := service.Decide(identity); err != nil {
		t.Fatalf("stopped takeover blocked after restart: %v", err)
	}
	if err := service.SetTakeover(context.Background(), true); err != nil {
		t.Fatal(err)
	}
	if _, err := service.Decide(identity); err == nil {
		t.Fatal("disabled key allowed after takeover resumed")
	}
}

func TestKeyStateRestorePreviewIncludesSettingsAndEffectiveChanges(t *testing.T) {
	for _, tc := range []struct {
		name                            string
		currentKeys, targetKeys         []string
		currentTakeover, targetTakeover bool
		configured                      bool
		legacySchema                    bool
		want                            [6]int
	}{
		{name: "restore re-enables key without policy", currentKeys: []string{"a"}, currentTakeover: true, targetTakeover: true, configured: true, want: [6]int{1, 0, 0, 1, 0, 1}},
		{name: "same counts different keys", currentKeys: []string{"a"}, targetKeys: []string{"b"}, currentTakeover: true, targetTakeover: true, configured: true, want: [6]int{1, 1, 1, 1, 1, 1}},
		{name: "paused settings", currentKeys: []string{"a"}, targetKeys: []string{"b"}, configured: true, want: [6]int{1, 1, 1, 1, 0, 0}},
		{name: "start takeover", currentKeys: []string{"a"}, targetKeys: []string{"a"}, targetTakeover: true, configured: true, want: [6]int{1, 1, 0, 0, 1, 0}},
		{name: "stop takeover", currentKeys: []string{"a"}, targetKeys: []string{"a"}, currentTakeover: true, configured: true, want: [6]int{1, 1, 0, 0, 0, 1}},
		{name: "orphaned keys", currentKeys: []string{"a"}, targetKeys: []string{"b"}, currentTakeover: true, targetTakeover: true, want: [6]int{1, 1, 1, 1, 0, 0}},
		{name: "schema 8 clears disabled settings", currentKeys: []string{"a"}, currentTakeover: true, targetTakeover: true, configured: true, legacySchema: true, want: [6]int{1, 0, 0, 1, 0, 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			service := newTestService(t)
			target := newTestService(t)
			for _, key := range tc.currentKeys {
				if err := service.SetKeyDisabled(ctx, testIdentity(t, key), true, false); err != nil {
					t.Fatal(err)
				}
			}
			for _, key := range tc.targetKeys {
				if err := target.SetKeyDisabled(ctx, testIdentity(t, key), true, false); err != nil {
					t.Fatal(err)
				}
			}
			if err := service.SetTakeover(ctx, tc.currentTakeover); err != nil {
				t.Fatal(err)
			}
			if err := target.SetTakeover(ctx, tc.targetTakeover); err != nil {
				t.Fatal(err)
			}
			payload, err := target.ExportBackup(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if tc.legacySchema {
				payload = []byte(strings.Replace(string(payload), `"schema_version":10`, `"schema_version":8`, 1))
			}
			var configured []string
			if tc.configured {
				configured = []string{testIdentity(t, "a").Hash(), testIdentity(t, "b").Hash()}
			}
			preview, err := service.PreviewBackup(ctx, payload, configured)
			if err != nil {
				t.Fatal(err)
			}
			got := [6]int{preview.CurrentDisabledKeys, preview.TargetDisabledKeys, preview.AddedDisabledKeys, preview.RemovedDisabledKeys, preview.NewlyBlockedKeys, preview.NewlyAllowedKeys}
			if got != tc.want {
				t.Fatalf("preview counts=%v want %v", got, tc.want)
			}
			if preview.TargetPolicies != 0 || preview.ReplacePolicies != 0 {
				t.Fatal("fixture must have no policies")
			}
			if err := service.ImportBackup(ctx, payload); err != nil {
				t.Fatal(err)
			}
			for _, key := range []string{"a", "b"} {
				_, gotErr := service.Decide(testIdentity(t, key))
				_, wantErr := target.Decide(testIdentity(t, key))
				if (gotErr == nil) != (wantErr == nil) {
					t.Fatalf("restored access for %s mismatch: %v vs %v", key, gotErr, wantErr)
				}
			}
		})
	}
}

func TestKeyConcurrencyAdmissionIsAtomicAndPerKey(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	key := testIdentity(t, "concurrency-a")
	other := testIdentity(t, "concurrency-b")
	if err := s.SetKeyConcurrencyLimit(ctx, key, 3, 0); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	var accepted atomic.Int32
	releases := make(chan func(), 64)
	for i := 0; i < 64; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			release, err := s.AcquireKeyRequest(key)
			if err == nil {
				accepted.Add(1)
				releases <- release
			} else if !errors.Is(err, ErrKeyConcurrencyExceeded) {
				t.Errorf("admit: %v", err)
			}
		}()
	}
	wg.Wait()
	if accepted.Load() != 3 {
		t.Fatalf("admitted %d, want 3", accepted.Load())
	}
	otherRelease, err := s.AcquireKeyRequest(other)
	if err != nil {
		t.Fatal(err)
	}
	otherRelease()
	close(releases)
	for release := range releases {
		release()
		release()
	} // release must be idempotent.
	release, err := s.AcquireKeyRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if len(s.activeRequests) != 0 {
		t.Fatalf("idle counters leaked: %v", s.activeRequests)
	}
}

func TestKeyConcurrencyChangesRetainInFlightRequests(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	key := testIdentity(t, "concurrency-live")
	// Traffic admitted without a limit must count when a limit is enabled.
	first, err := s.AcquireKeyRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	defer first()
	if err := s.SetKeyConcurrencyLimit(ctx, key, 1, 0); err != nil {
		t.Fatal(err)
	}
	blocked := func() {
		t.Helper()
		release, err := s.AcquireKeyRequest(key)
		if release != nil {
			release()
		}
		if !errors.Is(err, ErrKeyConcurrencyExceeded) {
			t.Fatalf("want limit error, got %v", err)
		}
	}
	blocked()
	if err := s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	blocked()
	backup, err := s.ExportBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ImportBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	blocked()
	if err := s.SetTakeover(ctx, false); err != nil {
		t.Fatal(err)
	}
	second, err := s.AcquireKeyRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	defer second()
	if s.KeyConcurrencyLimit(key) != 1 {
		t.Fatal("paused takeover discarded saved limit")
	}
	if err := s.SetTakeover(ctx, true); err != nil {
		t.Fatal(err)
	}
	blocked()
	if err := s.SetKeyConcurrencyLimit(ctx, key, 3, 1); err != nil {
		t.Fatal(err)
	}
	third, err := s.AcquireKeyRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	defer third()
	if err := s.SetKeyConcurrencyLimit(ctx, key, 1, 3); err != nil {
		t.Fatal(err)
	}
	blocked()
	first()
	second()
	blocked()
	third()
	next, err := s.AcquireKeyRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	next()
	if err := s.SetKeyConcurrencyLimit(ctx, key, 0, 1); err != nil {
		t.Fatal(err)
	}
	if s.KeyConcurrencyLimit(key) != 0 {
		t.Fatal("limit not cleared")
	}
}

func TestKeyConcurrencyPersistenceValidationAndRestorePreview(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "concurrency.sqlite")
	store, err := OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err := NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()
	key := testIdentity(t, "concurrency-backup")
	for _, limit := range []int{-1, MaxKeyConcurrency + 1} {
		if err := s.SetKeyConcurrencyLimit(ctx, key, limit, 0); !errors.Is(err, ErrInvalidKeyConcurrency) {
			t.Fatalf("invalid %d: %v", limit, err)
		}
	}
	if err := s.SetKeyConcurrencyLimit(ctx, key, 2, 1); !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("stale write: %v", err)
	}
	if err := s.SetKeyConcurrencyLimit(ctx, key, 2, 0); err != nil {
		t.Fatal(err)
	}
	backup, err := s.ExportBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	store, err = OpenStore(path)
	if err != nil {
		t.Fatal(err)
	}
	s, err = NewService(store)
	if err != nil {
		t.Fatal(err)
	}
	if s.KeyConcurrencyLimit(key) != 2 {
		t.Fatal("limit lost after restart")
	}
	if err := s.SetKeyConcurrencyLimit(ctx, key, 5, 2); err != nil {
		t.Fatal(err)
	}
	preview, err := s.PreviewBackup(ctx, backup, []string{key.Hash()})
	if err != nil {
		t.Fatal(err)
	}
	if preview.CurrentConcurrencyKeys != 1 || preview.TargetConcurrencyKeys != 1 || preview.ChangedConcurrencyKeys != 1 || preview.EffectiveConcurrencyChanges != 0 {
		t.Fatalf("paused preview: %+v", preview)
	}
	if err := s.SetTakeover(ctx, true); err != nil {
		t.Fatal(err)
	}
	preview, err = s.PreviewBackup(ctx, backup, []string{key.Hash()})
	if err != nil {
		t.Fatal(err)
	}
	if preview.EffectiveConcurrencyChanges != 1 {
		t.Fatalf("takeover preview: %+v", preview)
	}
	if err := s.ImportBackup(ctx, backup); err != nil {
		t.Fatal(err)
	}
	if s.KeyConcurrencyLimit(key) != 2 {
		t.Fatal("backup did not restore limit")
	}
	var document backupDocument
	if err := json.Unmarshal(backup, &document); err != nil {
		t.Fatal(err)
	}
	document.ConcurrencyLimits[key.Hash()] = -1
	invalid, _ := json.Marshal(document)
	if err := s.ImportBackup(ctx, invalid); err == nil {
		t.Fatal("invalid backup accepted")
	}
	if s.KeyConcurrencyLimit(key) != 2 {
		t.Fatal("failed import changed runtime")
	}
	document.SchemaVersion = 9 // Older backups have no concurrency settings.
	legacy, _ := json.Marshal(document)
	preview, err = s.PreviewBackup(ctx, legacy, []string{key.Hash()})
	if err != nil {
		t.Fatal(err)
	}
	if preview.TargetConcurrencyKeys != 0 || preview.ChangedConcurrencyKeys != 1 {
		t.Fatalf("legacy preview: %+v", preview)
	}
	if err := s.ImportBackup(ctx, legacy); err != nil {
		t.Fatal(err)
	}
	if s.KeyConcurrencyLimit(key) != 0 {
		t.Fatal("legacy backup did not clear limit")
	}
}

func TestKeyConcurrencyBackupRollbackAndSharedCommit(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	key := testIdentity(t, "concurrency-transaction")
	if err := s.SetKeyConcurrencyLimit(ctx, key, 1, 0); err != nil {
		t.Fatal(err)
	}
	payload, err := s.ExportBackup(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.SetKeyConcurrencyLimit(ctx, key, 3, 1); err != nil {
		t.Fatal(err)
	}
	active, err := s.AcquireKeyRequest(key)
	if err != nil {
		t.Fatal(err)
	}
	defer active()
	// Fail after the limit table has been replaced, proving durable rollback.
	if _, err := s.store.db.Exec(`create trigger fail_concurrency_restore before update on api_key_policy_settings begin select raise(abort, 'restore failure'); end`); err != nil {
		t.Fatal(err)
	}
	if err := s.ImportBackup(ctx, payload); err == nil {
		t.Fatal("expected failed import")
	}
	if _, err := s.store.db.Exec(`drop trigger fail_concurrency_restore`); err != nil {
		t.Fatal(err)
	}
	if err := s.Reload(ctx); err != nil {
		t.Fatal(err)
	}
	if s.KeyConcurrencyLimit(key) != 3 {
		t.Fatal("rollback lost current limit")
	}
	// The shared restore coordinator pauses runtime before opening its transaction.
	if err := s.PauseQuotaRuntime(ctx); err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.ResumeQuotaRuntime(ctx) }()
	for _, commit := range []bool{false, true} {
		tx, err := s.store.db.BeginTx(ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		transactionCtx := probackup.WithTransaction(ctx, tx)
		if err := s.ImportBackup(transactionCtx, payload); err != nil {
			_ = tx.Rollback()
			t.Fatal(err)
		}
		if s.KeyConcurrencyLimit(key) != 3 {
			_ = tx.Rollback()
			t.Fatal("published before commit")
		}
		if commit {
			if err := tx.Commit(); err != nil {
				t.Fatal(err)
			}
			probackup.RunAfterCommit(transactionCtx)
		} else {
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			if err := s.Reload(ctx); err != nil {
				t.Fatal(err)
			}
			if s.KeyConcurrencyLimit(key) != 3 {
				t.Fatal("shared rollback lost current limit")
			}
		}
	}
	if s.KeyConcurrencyLimit(key) != 1 {
		t.Fatal("shared commit not published")
	}
	if release, err := s.AcquireKeyRequest(key); !errors.Is(err, ErrKeyConcurrencyExceeded) {
		if release != nil {
			release()
		}
		t.Fatalf("shared restore forgot active request: %v", err)
	}
}

func TestWorkspaceConcurrencyAtomicWrites(t *testing.T) {
	s := newTestService(t)
	ctx := context.Background()
	key := testIdentity(t, "workspace-concurrency")
	requests := int64(20)
	quota := &QuotaInput{Enabled: true, Requests: &requests, Period: QuotaPeriod{Type: QuotaPeriodAllTime}}
	p, err := s.CreateWorkspace(ctx, key, "initial", nil, quota, &ConcurrencyUpdate{Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if s.KeyConcurrencyLimit(key) != 2 || p.Quota == nil || len(p.Profiles) != 0 {
		t.Fatalf("incomplete workspace: %+v", p)
	}
	_, err = s.UpdateWorkspace(ctx, p.ID, p.Version, WorkspaceUpdate{DisplayName: "conflicting", Concurrency: &ConcurrencyUpdate{Limit: 3, ExpectedLimit: 0}, Quota: QuotaUpdate{Present: true}})
	if !errors.Is(err, ErrVersionConflict) {
		t.Fatalf("expected conflict: %v", err)
	}
	got, _ := s.Get(ctx, p.ID)
	if got.DisplayName != "initial" || got.Quota == nil || got.Version != p.Version || s.KeyConcurrencyLimit(key) != 2 {
		t.Fatal("conflict partially saved")
	}
	// Fail after the concurrency write, proving SQL and runtime publication rollback together.
	if _, err := s.store.db.Exec(`create trigger fail_workspace before update on api_key_policies begin select raise(abort, 'workspace failure'); end`); err != nil {
		t.Fatal(err)
	}
	_, err = s.UpdateWorkspace(ctx, p.ID, p.Version, WorkspaceUpdate{DisplayName: "failed", Concurrency: &ConcurrencyUpdate{Limit: 3, ExpectedLimit: 2}, Quota: QuotaUpdate{Present: true}})
	if err == nil || s.KeyConcurrencyLimit(key) != 2 {
		t.Fatal("failed workspace published concurrency")
	}
	if _, err := s.store.db.Exec(`drop trigger fail_workspace`); err != nil {
		t.Fatal(err)
	}
	got, _ = s.Get(ctx, p.ID)
	if got.Quota == nil || got.Version != p.Version {
		t.Fatal("failed workspace saved quota")
	}
	p, err = s.UpdateWorkspace(ctx, p.ID, p.Version, WorkspaceUpdate{DisplayName: "legacy"})
	if err != nil || s.KeyConcurrencyLimit(key) != 2 {
		t.Fatalf("omitted concurrency changed: %v", err)
	}
	_, err = s.UpdateWorkspace(ctx, p.ID, p.Version, WorkspaceUpdate{DisplayName: "disabled", Concurrency: &ConcurrencyUpdate{Limit: 0, ExpectedLimit: 2}})
	if err != nil || s.KeyConcurrencyLimit(key) != 0 {
		t.Fatalf("disable failed: %v", err)
	}
}
