package auth

import (
	"context"
	"encoding/json"
	"net/http"
	"reflect"
	"strings"
	"testing"
	"time"

	cliproxyexecutor "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/executor"
)

func TestPinnedResultCannotClearReplacementAccountCooldown(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, nil, nil)
	old, err := manager.Register(ctx, &Auth{ID: "pinned-stale", Provider: "codex", FileName: "same.json", Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	result := Result{AuthID: old.ID, Model: "model-a", Success: true, Options: pinnedResultOptions(cliproxyexecutor.Options{}, old)}
	now := time.Now()
	newAuth, err := manager.Register(ctx, &Auth{ID: old.ID, Provider: "codex", FileName: "same.json", Status: StatusError,
		ModelStates: map[string]*ModelState{"model-a": {Unavailable: true, NextRetryAfter: now.Add(time.Hour), UpdatedAt: now}},
	})
	if err != nil {
		t.Fatal(err)
	}
	if old.RegistrationEpoch == newAuth.RegistrationEpoch {
		t.Fatal("replacement kept registration epoch")
	}
	manager.MarkResult(ctx, result)
	current, _ := manager.GetByID(old.ID)
	if current.Success != 0 || current.ModelStates["model-a"] == nil || !current.ModelStates["model-a"].Unavailable {
		t.Fatalf("stale pinned result changed replacement: %+v", current)
	}
}

func TestManualCooldownReleaseKeepsIndependentAuthFailure(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, nil, nil)
	auth, err := manager.Register(ctx, &Auth{
		ID: "auth-error-cooldown", Provider: "codex", FileName: "error.json",
		Unavailable: true, NextRetryAfter: time.Now().Add(time.Hour),
		LastError: &Error{HTTPStatus: 401, Message: "unauthorized"}, Status: StatusError,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := manager.ClearSchedulingBlock(ctx, auth, "", auth.UpdatedAt.UnixNano()); err != nil {
		t.Fatal(err)
	}
	current, _ := manager.GetByID(auth.ID)
	if current.LastError == nil || current.LastError.HTTPStatus != 401 || !current.NextRetryAfter.IsZero() {
		t.Fatalf("independent auth failure was cleared: %+v", current)
	}
	views := SchedulingBlockSnapshotForAuth(current, time.Now())
	if len(views) == 0 || views[0].Reason != "unauthorized" {
		t.Fatalf("auth failure disappeared from board: %+v", views)
	}
}

func TestSchedulingBlockSnapshotForAuthReportsSelectorRestrictions(t *testing.T) {
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name string
		auth *Auth
		want []string
	}{
		{name: "available", auth: &Auth{}, want: []string{}},
		{name: "untimed unavailable", auth: &Auth{Unavailable: true}, want: []string{"credential::unavailable"}},
		{name: "historical error is not a blocker", auth: &Auth{Status: StatusError, LastError: &Error{HTTPStatus: http.StatusBadGateway}}, want: []string{}},
		{name: "unauthorized", auth: &Auth{
			Status: StatusError, Unavailable: true,
			LastError: &Error{Code: "unauthorized", HTTPStatus: http.StatusUnauthorized},
		}, want: []string{"credential::unauthorized"}},
		{name: "expired access token", auth: &Auth{Metadata: map[string]any{
			"access_token": "opaque", "expired": now.Add(-time.Minute).Format(time.RFC3339),
		}}, want: []string{"credential::token_expired"}},
		{name: "model disabled and untimed", auth: &Auth{ModelStates: map[string]*ModelState{
			"model-b": {Status: StatusDisabled},
			"model-a": {Unavailable: true},
		}}, want: []string{"model:model-a:unavailable", "model:model-b:model_disabled"}},
		{name: "expired timer is not a blocker", auth: &Auth{ModelStates: map[string]*ModelState{
			"model-a": {Unavailable: true, NextRetryAfter: now.Add(-time.Second)},
		}}, want: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := SchedulingBlockSnapshotForAuth(tt.auth, now)
			keys := make([]string, 0, len(got))
			for _, view := range got {
				keys = append(keys, view.Scope+":"+view.ModelKey+":"+view.Reason)
			}
			if !reflect.DeepEqual(keys, tt.want) {
				t.Fatalf("views = %v, want %v", keys, tt.want)
			}
		})
	}
}

func TestSchedulingBlockSnapshotForAuthKeepsTimerDiagnostics(t *testing.T) {
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	retryAt := now.Add(time.Minute)
	auth := &Auth{ModelStates: map[string]*ModelState{
		"model-a": {
			Unavailable: true, NextRetryAfter: retryAt,
			Quota:     QuotaState{Exceeded: true, Reason: "quota", NextRecoverAt: retryAt},
			LastError: &Error{HTTPStatus: http.StatusTooManyRequests},
		},
	}}
	got := SchedulingBlockSnapshotForAuth(auth, now)
	if len(got) != 1 || got[0].Reason != "quota" || got[0].HTTPStatus != http.StatusTooManyRequests || !got[0].RetryAt.Equal(retryAt) {
		t.Fatalf("views = %+v", got)
	}
}

func TestSchedulingBlockSnapshotForAuthMatchesSelectorBlocking(t *testing.T) {
	now := time.Date(2026, 9, 18, 10, 0, 0, 0, time.UTC)
	tests := []struct {
		name  string
		auth  *Auth
		model string
		want  bool
	}{
		{name: "available", auth: &Auth{}},
		{name: "untimed unavailable", auth: &Auth{Unavailable: true}, want: true},
		{name: "expired token", auth: &Auth{Metadata: map[string]any{
			"access_token": "opaque", "expired": now.Add(-time.Minute).Format(time.RFC3339),
		}}, want: true},
		{name: "model disabled", auth: &Auth{ModelStates: map[string]*ModelState{
			"model-a": {Status: StatusDisabled},
		}}, model: "model-a", want: true},
		{name: "historical error", auth: &Auth{Status: StatusError, LastError: &Error{HTTPStatus: http.StatusBadGateway}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			blocked, _, _ := isAuthBlockedForModel(tt.auth, tt.model, now)
			got := len(SchedulingBlockSnapshotForAuth(tt.auth, now)) > 0
			if blocked != tt.want || got != tt.want {
				t.Fatalf("selector/snapshot = %v/%v, want %v", blocked, got, tt.want)
			}
		})
	}
}

func TestPinnedSuccessRecoversCredentialAndModelCooldown(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, nil, nil)
	now := time.Now()
	observed := now.Add(-time.Minute)
	quota := QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: now.Add(time.Hour), ObservedAt: observed, Signals: map[string]string{"remaining": "0"}}
	auth, err := manager.Register(ctx, &Auth{ID: "pinned-quota", Provider: "codex", Status: StatusError, Unavailable: true, NextRetryAfter: quota.NextRecoverAt, Quota: quota,
		ModelStates: map[string]*ModelState{"model-a": {Status: StatusError, Unavailable: true, NextRetryAfter: quota.NextRecoverAt, Quota: quota, UpdatedAt: now}}})
	if err != nil {
		t.Fatal(err)
	}
	manager.MarkResult(ctx, Result{AuthID: auth.ID, Model: "model-a", Success: true, SkipQuotaObservation: true, Options: pinnedResultOptions(cliproxyexecutor.Options{}, auth)})
	current, _ := manager.GetByID(auth.ID)
	if current.Quota.Exceeded || !current.NextRetryAfter.IsZero() || current.ModelStates["model-a"].Unavailable || !current.ModelStates["model-a"].NextRetryAfter.IsZero() {
		t.Fatalf("cooldown remains: %+v model=%+v", current, current.ModelStates["model-a"])
	}
	if !current.Quota.ObservedAt.Equal(observed) || current.Quota.Signals["remaining"] != "0" {
		t.Fatal("quota observations lost")
	}
}

func TestPinnedSuccessCannotClearConcurrentFailureWithSameDeadline(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, nil, nil)
	auth, err := manager.Register(ctx, &Auth{ID: "pinned-concurrent", Provider: "codex", Status: StatusActive})
	if err != nil {
		t.Fatal(err)
	}
	result := Result{AuthID: auth.ID, Model: "model-a", Success: true, SkipQuotaObservation: true, Options: pinnedResultOptions(cliproxyexecutor.Options{}, auth)}
	manager.mu.Lock()
	current := manager.auths[auth.ID]
	// A new untimed failure can retain both deadlines; the old endpoint compared
	// only these deadlines and then borrowed the new state revision.
	current.LastError = &Error{HTTPStatus: 401, Message: "new unauthorized failure"}
	current.Unavailable = true
	current.Status = StatusError
	manager.mu.Unlock()
	manager.MarkResult(ctx, result)
	current, _ = manager.GetByID(auth.ID)
	if current.LastError == nil || current.LastError.HTTPStatus != 401 || !current.Unavailable || current.Success != 0 {
		t.Fatalf("new failure cleared: %+v", current)
	}
}

func TestManualCooldownReleasePreservesQuotaObservations(t *testing.T) {
	for _, model := range []string{"", "model-a"} {
		t.Run("scope_"+model, func(t *testing.T) {
			ctx := context.Background()
			manager := NewManager(nil, nil, nil)
			now := time.Now()
			quota := QuotaState{Exceeded: true, Reason: "rate_limit", NextRecoverAt: now.Add(time.Hour), ObservedAt: now.Add(-time.Minute), Signals: map[string]string{"remaining": "0"}}
			auth, err := manager.Register(ctx, &Auth{ID: "manual-observations", Provider: "codex", Quota: quota, NextRetryAfter: quota.NextRecoverAt, ModelStates: map[string]*ModelState{"model-a": {Quota: quota, NextRetryAfter: quota.NextRecoverAt, UpdatedAt: now}}})
			if err != nil {
				t.Fatal(err)
			}
			if model == "" {
				manager.mu.Lock()
				manager.auths[auth.ID].ModelStates = nil
				manager.mu.Unlock()
				auth, _ = manager.GetByID(auth.ID)
			}
			revision := auth.UpdatedAt.UnixNano()
			if model != "" {
				revision = auth.ModelStates[model].UpdatedAt.UnixNano()
			}
			if err := manager.ClearSchedulingBlock(ctx, auth, model, revision); err != nil {
				t.Fatal(err)
			}
			current, _ := manager.GetByID(auth.ID)
			got := current.Quota
			if model != "" {
				got = current.ModelStates[model].Quota
			}
			if got.Exceeded || !got.NextRecoverAt.IsZero() || !got.ObservedAt.Equal(quota.ObservedAt) || !reflect.DeepEqual(got.Signals, quota.Signals) {
				t.Fatalf("unexpected quota after release: %+v", got)
			}
		})
	}
}

func TestPinnedResultRejectsRotatedAPIKey(t *testing.T) {
	ctx := context.Background()
	manager := NewManager(nil, nil, nil)
	auth, err := manager.Register(ctx, &Auth{ID: "rotated-key", Provider: "codex", Attributes: map[string]string{"api_key": "old"}})
	if err != nil {
		t.Fatal(err)
	}
	result := Result{AuthID: auth.ID, Model: "model-a", Success: true, Options: pinnedResultOptions(cliproxyexecutor.Options{}, auth)}
	manager.mu.Lock()
	manager.auths[auth.ID].Attributes["api_key"] = "new"
	manager.mu.Unlock()
	manager.MarkResult(ctx, result)
	current, _ := manager.GetByID(auth.ID)
	if current.Success != 0 {
		t.Fatal("result attributed to rotated credential")
	}
}

func TestPinnedResultMetadataContainsOnlyFingerprints(t *testing.T) {
	auth := &Auth{ID: "private-result", Provider: "codex", Metadata: map[string]any{"access_token": "secret-access-token", "refresh_token": "secret-refresh-token"}, Attributes: map[string]string{"api_key": "secret-api-key"}, ProxyURL: "https://secret-proxy-password@example.com"}
	opts := pinnedResultOptions(cliproxyexecutor.Options{}, auth)
	for _, key := range []string{pinnedResultIdentityKey, pinnedResultIdentityKey + ".snapshot"} {
		value, ok := opts.Metadata[key].(string)
		if !ok || len(value) != 64 {
			t.Fatalf("metadata %s is not a SHA-256 fingerprint", key)
		}
	}
	data, err := json.Marshal(opts.Metadata)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(data), "secret-") {
		t.Fatal("result metadata contains raw credentials")
	}
	if !pinnedResultIdentityMatches(Result{Success: true, Options: opts}, auth) {
		t.Fatal("fingerprints do not match unchanged auth")
	}
}
