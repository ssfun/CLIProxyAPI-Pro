package auth

import (
	"context"
	"errors"
	"fmt"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	"reflect"
	"testing"
	"time"
)

type inspectionRefreshDue struct{}

func (inspectionRefreshDue) ShouldRefresh(time.Time, *Auth) bool { return true }

type inspectionRefreshExecutor struct {
	ProviderExecutor
	provider string
	refresh  func(*Auth) (*Auth, error)
}

func (e inspectionRefreshExecutor) Identifier() string { return e.provider }
func (e inspectionRefreshExecutor) Refresh(_ context.Context, auth *Auth) (*Auth, error) {
	return e.refresh(auth)
}

func TestInspectionRefreshFencesConcurrentCredentials(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "kimi", "gemini-cli", "xai"} {
		for _, mutation := range []string{"none", "credentials", "refresh-token", "camel-refresh-token", "recreate", "remove", "note", "disable"} {
			for _, status := range []int{0, 401, 503} {
				t.Run(fmt.Sprintf("%s/%s/%d", provider, mutation, status), func(t *testing.T) {
					ctx := context.Background()
					manager := NewManager(nil, nil, nil)
					registered, err := manager.Register(ctx, &Auth{
						ID: "inspection-refresh", Provider: provider, FileName: "inspection.json", Status: StatusActive,
						Runtime: inspectionRefreshDue{}, Metadata: map[string]any{
							"access_token": "old-token", "refresh_token": "old-refresh", "refreshToken": "old-alias",
						},
					})
					if err != nil {
						t.Fatal(err)
					}
					changed := mutation != "none" && mutation != "note" && mutation != "disable"
					var calls int
					manager.RegisterExecutor(inspectionRefreshExecutor{provider: provider, refresh: func(candidate *Auth) (*Auth, error) {
						calls++
						current, _ := manager.GetByID(registered.ID)
						switch mutation {
						case "credentials":
							current.Metadata["access_token"] = "new-login-token"
							current.Metadata["refresh_token"] = "new-login-refresh"
						case "refresh-token":
							current.Metadata["refresh_token"] = "new-login-refresh"
						case "camel-refresh-token":
							current.Metadata["refreshToken"] = "new-login-alias"
						case "note":
							current.Metadata["note"] = "user note"
						case "disable":
							current.Disabled = true
							current.Status = StatusDisabled
						}
						switch mutation {
						case "remove":
							manager.Remove(ctx, current.ID)
						case "recreate":
							_, err = manager.Register(ctx, current)
						case "none":
						default:
							_, err = manager.Update(ctx, current)
						}
						if err != nil {
							t.Fatal(err)
						}
						// A provider is allowed to mutate its input before returning an error.
						candidate.Metadata["access_token"] = "refreshed-token"
						if status != 0 {
							return nil, &Error{HTTPStatus: status, Message: "refresh failed"}
						}
						return candidate, nil
					}})
					_, refreshed, refreshErr := manager.RefreshIfDueForInspection(ctx, registered.ID)
					if calls != 1 {
						t.Fatalf("refresh calls = %d", calls)
					}
					current, exists := manager.GetByID(registered.ID)
					if changed {
						if refreshed || !errors.Is(refreshErr, ErrInspectionAuthChanged) {
							t.Fatalf("stale refresh = %v, %v", refreshed, refreshErr)
						}
						if mutation == "remove" {
							if exists {
								t.Fatal("removed account was restored")
							}
							return
						}
						if !exists || current.Unavailable || current.LastError != nil || current.Status != StatusActive {
							t.Fatalf("new identity status changed: %+v", current)
						}
						wantToken := "old-token"
						if mutation == "credentials" {
							wantToken = "new-login-token"
						}
						if current.Metadata["access_token"] != wantToken {
							t.Fatal("new credentials overwritten")
						}
						if mutation == "refresh-token" && current.Metadata["refresh_token"] != "new-login-refresh" {
							t.Fatal("new refresh token overwritten")
						}
						if mutation == "camel-refresh-token" && current.Metadata["refreshToken"] != "new-login-alias" {
							t.Fatal("new refresh alias overwritten")
						}
						return
					}
					if mutation == "note" && current.Metadata["note"] != "user note" {
						t.Fatal("user note overwritten")
					}
					if mutation == "disable" && !current.Disabled {
						t.Fatal("user disable overwritten")
					}
					if status == 0 {
						if refreshErr != nil || !refreshed || current.Metadata["access_token"] != "refreshed-token" {
							t.Fatalf("refresh = %v, %v", refreshed, refreshErr)
						}
					} else {
						if refreshed || refreshErr == nil || errors.Is(refreshErr, ErrInspectionAuthChanged) || current.LastError == nil {
							t.Fatalf("refresh error lost: %v", refreshErr)
						}
						if current.Metadata["access_token"] != "old-token" {
							t.Fatal("failed refresh leaked mutated token")
						}
						if status == 401 && !current.Unavailable {
							t.Fatal("current unauthorized credential was not marked unavailable")
						}
						if status == 503 && (current.Unavailable || !current.NextRefreshAfter.After(time.Now())) {
							t.Fatal("transient error must retain retry backoff")
						}
					}
				})
			}
		}
	}
}

func TestInspectionRefreshRecoversObservedError(t *testing.T) {
	for _, provider := range []string{"claude", "codex", "kimi", "gemini-cli", "xai"} {
		for _, state := range []string{"error", "disabled", "cooldown", "quota", "new-error", "new-error-same-message"} {
			t.Run(provider+"/"+state, func(t *testing.T) {
				ctx := context.Background()
				manager := NewManager(nil, nil, nil)
				deadline := time.Now().Add(time.Hour)
				auth := &Auth{ID: "recover-error", Provider: provider, Status: StatusError, Unavailable: true,
					LastError: &Error{HTTPStatus: 401, Code: "unauthorized", Message: "token rejected"},
					Metadata:  map[string]any{"access_token": "old-token", "refresh_token": "test-refresh", "last_error": "old metadata error"},
				}
				switch state {
				case "disabled":
					auth.Disabled = true
					auth.Status = StatusDisabled
				case "cooldown":
					auth.NextRetryAfter = deadline
				case "quota":
					auth.Quota = QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: deadline}
				}
				registered, err := manager.Register(ctx, auth)
				if err != nil {
					t.Fatal(err)
				}
				manager.RegisterExecutor(inspectionRefreshExecutor{provider: provider, refresh: func(candidate *Auth) (*Auth, error) {
					if state == "new-error" || state == "new-error-same-message" {
						current, _ := manager.GetByID(candidate.ID)
						message := "new upstream failure"
						if state == "new-error-same-message" {
							message = "token rejected"
						}
						current.LastError = &Error{HTTPStatus: 503, Message: message}
						current.StatusMessage = "concurrent failure"
						current.Metadata["last_error"] = "new metadata error"
						if _, err := manager.Update(ctx, current); err != nil {
							t.Fatal(err)
						}
					}
					candidate.Metadata["access_token"] = "refreshed-token"
					candidate.Metadata["expired"] = deadline.Format(time.RFC3339)
					return candidate, nil
				}})
				_, refreshed, refreshErr := manager.ForceRefreshForInspection(ctx, registered.ID)
				current, _ := manager.GetByID(registered.ID)
				if refreshErr != nil || !refreshed || current.Metadata["access_token"] != "refreshed-token" {
					t.Fatalf("refresh=%v err=%v", refreshed, refreshErr)
				}
				if state == "new-error" || state == "new-error-same-message" {
					if current.LastError == nil || current.LastError.HTTPStatus != 503 || current.StatusMessage != "concurrent failure" || !current.Unavailable || current.Metadata["last_error"] != "new metadata error" {
						t.Fatal("newer failure was cleared")
					}
					return
				}
				if current.LastError != nil || current.Metadata["last_error"] != nil || hasUnauthorizedAuthFailure(current) {
					t.Fatal("successful refresh retained obsolete authentication error")
				}
				switch state {
				case "disabled":
					if !current.Disabled || current.Status != StatusDisabled {
						t.Fatal("refresh enabled disabled account")
					}
				case "cooldown":
					if !current.Unavailable || !current.NextRetryAfter.Equal(deadline) {
						t.Fatal("refresh cleared active cooldown")
					}
				case "quota":
					if !current.Unavailable || !current.Quota.Exceeded || !current.Quota.NextRecoverAt.Equal(deadline) {
						t.Fatal("refresh cleared active quota restriction")
					}
				default:
					if current.Unavailable || current.Status != StatusActive || current.StatusMessage != "" {
						t.Fatal("successful refresh did not recover authentication status")
					}
				}
			})
		}
	}
}

func TestInspectionRefreshNativePolicy(t *testing.T) {
	for _, scenario := range []string{"siwc-due", "outer-due", "siwc-fresh", "devin-due", "devin-wait", "disabled", "invalid-grant", "forced-invalid-grant", "backoff", "evaluator-deny", "evaluator-allow", "ineffective"} {
		t.Run(scenario, func(t *testing.T) {
			now := time.Now()
			a := &Auth{ID: scenario, Provider: "codex", Status: StatusActive, Metadata: map[string]any{"access_token": "outer", "refresh_token": "refresh", "expired": now.Add(168 * time.Hour).Format(time.RFC3339)}}
			want := true
			switch scenario {
			case "siwc-due", "outer-due", "siwc-fresh":
				expiry := now.Add(168 * time.Hour)
				if scenario == "siwc-due" {
					expiry = now.Add(-time.Hour)
				}
				a.Metadata["siwc"] = map[string]any{"access_token": "inner", "refresh_token": "inner-refresh", "client_id": "client", "expired": expiry.Format(time.RFC3339)}
				a.Metadata["use_siwc"] = true
				if scenario == "outer-due" {
					a.Metadata["expired"] = now.Add(-time.Hour).Format(time.RFC3339)
				}
				want = scenario != "siwc-fresh"
			case "devin-due", "devin-wait":
				a.Provider = "devin"
				a.LastRefreshedAt = now.Add(-2 * time.Hour)
				if scenario == "devin-wait" {
					a.LastRefreshedAt = now
					want = false
				}
				a.Quota = QuotaState{Exceeded: true, Reason: "credential_quota", NextRecoverAt: now.Add(24 * time.Hour), ObservedAt: a.LastRefreshedAt}
			case "disabled", "invalid-grant", "forced-invalid-grant":
				a.Disabled = true
				a.Status = StatusDisabled
				a.Runtime = inspectionRefreshDue{}
				if scenario != "disabled" {
					a.LastError = &Error{HTTPStatus: 400, Message: "invalid_grant"}
				}
				want = scenario != "invalid-grant"
			case "backoff":
				a.Runtime = inspectionRefreshDue{}
				a.NextRefreshAfter = now.Add(time.Hour)
				want = false
			case "evaluator-deny":
				a.Runtime = inspectionRefreshNever{}
				a.Metadata["expired"] = now.Add(-time.Hour).Format(time.RFC3339)
				want = false
			case "evaluator-allow", "ineffective":
				a.Runtime = inspectionRefreshDue{}
			}
			m := NewManager(nil, nil, nil)
			registered, err := m.Register(context.Background(), a)
			if err != nil {
				t.Fatal(err)
			}
			calls := 0
			m.RegisterExecutor(inspectionRefreshExecutor{provider: a.Provider, refresh: func(candidate *Auth) (*Auth, error) {
				calls++
				candidate.Metadata["access_token"] = "renewed"
				return candidate, nil
			}})
			var refreshed bool
			if scenario == "forced-invalid-grant" {
				_, refreshed, err = m.ForceRefreshForInspection(context.Background(), registered.ID)
			} else {
				_, refreshed, err = m.RefreshIfDueForInspection(context.Background(), registered.ID)
			}
			if err != nil || refreshed != want || (calls == 1) != want {
				t.Fatalf("refresh=%v calls=%d err=%v want=%v", refreshed, calls, err, want)
			}
			current, _ := m.GetByID(registered.ID)
			if want && current.Metadata["access_token"] != "renewed" {
				t.Fatal("executor result not committed")
			}
			if !want && current.Metadata["access_token"] != "outer" {
				t.Fatal("skipped credential mutated")
			}
			if a.Disabled && (!current.Disabled || current.Status != StatusDisabled) {
				t.Fatal("disabled state lost")
			}
			if scenario == "ineffective" && !current.NextRefreshAfter.After(now) {
				t.Fatal("ineffective refresh missing backoff")
			}
		})
	}
}

type inspectionRefreshNever struct{}

func (inspectionRefreshNever) ShouldRefresh(time.Time, *Auth) bool { return false }

func TestInspectionRefreshUsesDecisionInstant(t *testing.T) {
	now := time.Now().Add(24 * time.Hour)
	m := NewManager(nil, nil, nil)
	RegisterRefreshLeadProvider("inspection-clock", func() *time.Duration { lead := time.Minute; return &lead })
	a := &Auth{Provider: "inspection-clock", Metadata: map[string]any{"expired": now.Add(-time.Hour).Format(time.RFC3339)}}
	if !m.shouldRefreshForInspection(a, now) {
		t.Fatal("decision used wall clock instead of supplied now")
	}
}

type inspectionRefreshContextExecutor struct {
	ProviderExecutor
	refresh func(context.Context, *Auth) (*Auth, error)
}

func (e inspectionRefreshContextExecutor) Identifier() string { return "codex" }
func (e inspectionRefreshContextExecutor) Refresh(ctx context.Context, a *Auth) (*Auth, error) {
	return e.refresh(ctx, a)
}

func TestInspectionRefreshForceContext(t *testing.T) {
	type contextKey struct{}
	for _, force := range []bool{false, true} {
		t.Run(fmt.Sprint(force), func(t *testing.T) {
			m := NewManager(nil, nil, nil)
			ctx := context.WithValue(context.Background(), contextKey{}, "caller")
			a, err := m.Register(ctx, &Auth{ID: "force-context", Provider: "codex", Status: StatusActive, Runtime: inspectionRefreshDue{}, Metadata: map[string]any{"access_token": "old", "refresh_token": "refresh"}})
			if err != nil {
				t.Fatal(err)
			}
			m.RegisterExecutor(inspectionRefreshContextExecutor{refresh: func(got context.Context, candidate *Auth) (*Auth, error) {
				if IsForceRefreshContext(got) != force {
					t.Errorf("native force marker = %v, want %v", IsForceRefreshContext(got), force)
				}
				if got.Value(contextKey{}) != "caller" {
					t.Error("caller context lost")
				}
				return candidate, nil
			}})
			if force {
				_, _, err = m.ForceRefreshForInspection(ctx, a.ID)
			} else {
				_, _, err = m.RefreshIfDueForInspection(ctx, a.ID)
			}
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestInspectionRefreshDevinRecoveryCompatibility(t *testing.T) {
	withQuotaCooldownEnabled(t)
	for _, scenario := range []string{"restored", "exhausted", "partial", "stale", "future", "missing-time", "disabled", "disable-during", "new-error", "new-unauthorized", "new-model-unauthorized", "new-quota", "new-model", "replacement", "note", "independent-model", "pro-protection"} {
		t.Run(scenario, func(t *testing.T) {
			for _, inspection := range []bool{false, true} {
				t.Run(fmt.Sprintf("inspection=%v", inspection), func(t *testing.T) {
					m, a, model, sibling := devinRefreshAuthFixture(t, "inspection-devin-"+scenario+fmt.Sprint(inspection))
					deadline := time.Now().Add(72 * time.Hour)
					base := devinExhaustedAuth(t, m, a.ID, model, sibling, deadline)
					if scenario == "independent-model" {
						base.ModelStates["independent"] = &ModelState{Status: StatusError, Unavailable: true, NextRetryAfter: deadline, LastError: &Error{HTTPStatus: 503, Message: "independent"}}
						if _, err := m.Update(context.Background(), base); err != nil {
							t.Fatal(err)
						}
						base, _ = m.GetByID(a.ID)
					}
					if scenario == "disabled" {
						base.Disabled = true
						base.Status = StatusDisabled
						if _, err := m.Update(context.Background(), base); err != nil {
							t.Fatal(err)
						}
						base, _ = m.GetByID(a.ID)
					}
					current := base.Clone()
					switch scenario {
					case "disable-during":
						current.Disabled = true
						current.Status = StatusDisabled
					case "new-error":
						current.LastError = &Error{HTTPStatus: 503, Message: "new failure"}
						current.StatusMessage = "new failure"
					case "new-unauthorized":
						current.LastError = &Error{HTTPStatus: 401, Message: "new unauthorized"}
						current.StatusMessage = "new unauthorized"
					case "new-model-unauthorized":
						current.ModelStates[sibling].LastError = &Error{HTTPStatus: 401, Message: "new model unauthorized"}
						current.ModelStates[sibling].StatusMessage = "new model unauthorized"
					case "new-quota":
						current.Quota.NextRecoverAt = deadline.Add(time.Hour)
						current.Quota.ObservedAt = time.Now()
						current.NextRetryAfter = current.Quota.NextRecoverAt
					case "new-model":
						current.ModelStates[sibling].Quota.NextRecoverAt = deadline.Add(time.Hour)
						current.ModelStates[sibling].NextRetryAfter = deadline.Add(time.Hour)
					case "replacement":
						current.Metadata = map[string]any{"access_token": "replacement"}
					case "note":
						if current.Metadata == nil {
							current.Metadata = map[string]any{}
						}
						current.Metadata["note"] = "keep note"
					case "pro-protection":
						m.mu.Lock()
						setQuotaProtections(m.auths[a.ID], map[string]prorouting.QuotaProtection{"inspection": {Source: "inspection", RetryAt: deadline.Unix()}})
						m.mu.Unlock()
					}
					if scenario == "disable-during" || scenario == "new-error" || scenario == "new-unauthorized" || scenario == "new-model-unauthorized" || scenario == "new-quota" || scenario == "new-model" || scenario == "replacement" || scenario == "note" || scenario == "pro-protection" {
						if _, err := m.Update(context.Background(), current); err != nil {
							t.Fatal(err)
						}
					}
					updated := devinRefreshedObservation(base, "80%", "55%", time.Time{}, time.Time{})
					if scenario == "new-unauthorized" || scenario == "new-model-unauthorized" {
						if updated.Metadata == nil {
							updated.Metadata = map[string]any{}
						}
						updated.Metadata["access_token"] = "rotated"
					}
					switch scenario {
					case "exhausted":
						updated.Quota.Signals["daily_quota_remaining_percent"] = "0%"
						updated.Quota.Signals["weekly_quota_remaining_percent"] = "0%"
					case "partial":
						updated.Quota.Signals["weekly_quota_remaining_percent"] = "0%"
					case "stale":
						updated.Quota.ObservedAt = base.Quota.ObservedAt.Add(-time.Hour)
					case "future":
						updated.Quota.ObservedAt = time.Now().Add(time.Hour)
					case "missing-time":
						updated.Quota.ObservedAt = time.Time{}
					}
					before, _ := m.GetByID(a.ID)
					var err error
					if inspection {
						_, err = m.CommitInspectionRefresh(context.Background(), base, updated)
					} else {
						_, err = m.UpdateRefreshedAuth(context.Background(), base, updated)
					}
					stored, _ := m.GetByID(a.ID)
					if scenario == "replacement" {
						if inspection && !errors.Is(err, ErrInspectionAuthChanged) {
							t.Fatalf("replacement error = %v", err)
						}
						if !reflect.DeepEqual(before, stored) {
							t.Fatal("replacement changed")
						}
						return
					}
					if err != nil {
						t.Fatal(err)
					}
					if scenario == "disabled" || scenario == "disable-during" {
						if !stored.Disabled || stored.Status != StatusDisabled {
							t.Fatal("manual disable cleared")
						}
						return
					}
					if scenario == "new-error" || scenario == "new-unauthorized" || scenario == "new-model-unauthorized" || scenario == "new-quota" || scenario == "new-model" {
						// Native refresh is the recovery baseline, not the inspection concurrency policy.
						if !inspection {
							return
						}
						if !stored.Quota.Exceeded {
							t.Fatal("new restriction cleared")
						}
						if (scenario == "new-error" || scenario == "new-unauthorized") && !reflect.DeepEqual(stored.LastError, before.LastError) {
							t.Fatal("new error cleared")
						}
						if scenario == "new-quota" && !stored.Quota.NextRecoverAt.Equal(before.Quota.NextRecoverAt) {
							t.Fatal("new quota deadline cleared")
						}
						if (scenario == "new-model" || scenario == "new-model-unauthorized") && !reflect.DeepEqual(stored.ModelStates[sibling], before.ModelStates[sibling]) {
							t.Fatal("new model restriction cleared")
						}
						return
					}
					wantRecovery := scenario == "restored" || scenario == "note" || scenario == "independent-model" || scenario == "pro-protection"
					if stored.Quota.Exceeded == wantRecovery {
						t.Fatalf("quota exceeded=%v, want recovery=%v", stored.Quota.Exceeded, wantRecovery)
					}
					for _, name := range []string{model, sibling} {
						state := stored.ModelStates[name]
						if state == nil || state.Quota.Exceeded == wantRecovery || state.Unavailable == wantRecovery {
							t.Fatalf("model recovery mismatch: %+v", state)
						}
						blocked, _, _ := isAuthBlockedForModel(stored, name, time.Now())
						wantBlocked := !wantRecovery || scenario == "pro-protection"
						if blocked != wantBlocked {
							t.Fatalf("scheduler blocked=%v want=%v", blocked, wantBlocked)
						}
					}
					if scenario == "restored" && (stored.Status != StatusActive || stored.Unavailable || !stored.NextRetryAfter.IsZero()) {
						t.Fatal("account recovery incomplete")
					}
					if scenario == "note" && stored.Metadata["note"] != "keep note" {
						t.Fatal("note lost")
					}
					if scenario == "independent-model" && !stored.ModelStates["independent"].NextRetryAfter.Equal(deadline) {
						t.Fatal("independent cooldown lost")
					}
					if scenario == "pro-protection" && !reflect.DeepEqual(proroutingQuotaProtections(stored), proroutingQuotaProtections(before)) {
						t.Fatal("Pro protection lost")
					}
				})
			}
		})
	}
}
