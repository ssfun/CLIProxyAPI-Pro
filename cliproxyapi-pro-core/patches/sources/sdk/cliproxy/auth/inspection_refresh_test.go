package auth

import (
	"context"
	"errors"
	"fmt"
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
