package auth

import (
	"net/http"
	"reflect"
	"testing"
	"time"
)

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
