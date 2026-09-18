package auth

import (
	"net/http"
	"sort"
	"strings"
	"time"
)

// SchedulingBlockView describes a selector restriction without exposing raw
// credential data or historical errors that no longer block selection.
type SchedulingBlockView struct {
	Scope      string    `json:"scope"`
	ModelKey   string    `json:"model_key,omitempty"`
	Reason     string    `json:"reason"`
	RetryAt    time.Time `json:"retry_at,omitempty"`
	HTTPStatus int       `json:"http_status,omitempty"`
}

// SchedulingBlockSnapshotForAuth projects the live restrictions used by the
// selector. Unlike CooldownSnapshotForAuth, it also reports untimed
// unavailability, authentication failures, expired access tokens, and
// model-level disablement.
func SchedulingBlockSnapshotForAuth(auth *Auth, now time.Time) []SchedulingBlockView {
	views := make([]SchedulingBlockView, 0)
	if auth == nil {
		return views
	}

	credentialIdentityBlocked := false
	if auth.Disabled || auth.Status == StatusDisabled {
		views = append(views, SchedulingBlockView{Scope: "credential", Reason: "credential_disabled"})
		credentialIdentityBlocked = true
	}
	if hasUnauthorizedAuthFailure(auth) {
		views = append(views, SchedulingBlockView{
			Scope: "credential", Reason: "unauthorized", HTTPStatus: http.StatusUnauthorized,
		})
		credentialIdentityBlocked = true
	}
	if exp, ok := auth.AccessTokenExpirationTime(); ok && !exp.IsZero() && !exp.After(now) {
		views = append(views, SchedulingBlockView{Scope: "credential", Reason: "token_expired"})
		credentialIdentityBlocked = true
	}

	if auth.Quota.Exceeded && auth.Quota.Reason == "credential_quota" && auth.Quota.NextRecoverAt.After(now) {
		views = append(views, schedulingBlockView(
			"credential", "", auth.Quota.NextRecoverAt, now,
			auth.Quota, auth.StatusMessage, auth.LastError,
		))
	} else if len(auth.ModelStates) == 0 {
		if blocked, _, next := availabilityBlock(auth.Unavailable, auth.Quota.Exceeded, auth.NextRetryAfter, auth.Quota.NextRecoverAt, now); blocked {
			if !credentialIdentityBlocked || !next.IsZero() {
				views = append(views, schedulingBlockView(
					"credential", "", next, now,
					auth.Quota, auth.StatusMessage, auth.LastError,
				))
			}
		}
	}

	keys := make([]string, 0, len(auth.ModelStates))
	for key := range auth.ModelStates {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	type modelBlock struct {
		view   SchedulingBlockView
		reason blockReason
	}
	byModel := make(map[string]modelBlock)
	for _, key := range keys {
		state := auth.ModelStates[key]
		model := canonicalModelKey(key)
		if state == nil || model == "" {
			continue
		}
		if state.Status == StatusDisabled {
			byModel[model] = modelBlock{view: SchedulingBlockView{
				Scope: "model", ModelKey: model, Reason: "model_disabled",
			}, reason: blockReasonDisabled}
			continue
		}
		blocked, reason, next := availabilityBlock(state.Unavailable, state.Quota.Exceeded, state.NextRetryAfter, state.Quota.NextRecoverAt, now)
		if !blocked {
			continue
		}
		candidate := modelBlock{
			view: schedulingBlockView(
				"model", model, next, now,
				state.Quota, state.StatusMessage, state.LastError,
			),
			reason: reason,
		}
		if previous, ok := byModel[model]; ok {
			if previous.reason == blockReasonDisabled || previous.view.RetryAt.IsZero() {
				continue
			}
			if candidate.view.RetryAt.IsZero() {
				byModel[model] = candidate
				continue
			}
			preferQuotaTie := candidate.view.RetryAt.Equal(previous.view.RetryAt) && reason == blockReasonCooldown && previous.reason != blockReasonCooldown
			if !candidate.view.RetryAt.After(previous.view.RetryAt) && !preferQuotaTie {
				continue
			}
		}
		byModel[model] = candidate
	}
	models := make([]string, 0, len(byModel))
	for model := range byModel {
		models = append(models, model)
	}
	sort.Strings(models)
	for _, model := range models {
		views = append(views, byModel[model].view)
	}
	return views
}

func schedulingBlockView(scope, model string, retryAt, now time.Time, quota QuotaState, statusMessage string, lastErr *Error) SchedulingBlockView {
	diagnostic := newCooldownView(scope, model, retryAt, now, quota, statusMessage, lastErr)
	reason := strings.TrimSpace(diagnostic.Reason)
	if reason == "" || reason == "unknown" {
		reason = "unavailable"
	}
	view := SchedulingBlockView{
		Scope: scope, ModelKey: model, Reason: reason, HTTPStatus: diagnostic.HTTPStatus,
	}
	if !retryAt.IsZero() {
		view.RetryAt = retryAt.UTC()
	}
	return view
}
