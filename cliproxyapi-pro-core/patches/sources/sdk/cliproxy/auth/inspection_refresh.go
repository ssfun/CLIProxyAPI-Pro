package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
)

var ErrInspectionAuthChanged = errors.New("account credentials changed during inspection refresh")

// InspectionAccessToken selects the same OAuth token as AccessTokenSHA256 so
// inspection requests and their guarded automatic actions share one identity.
func InspectionAccessToken(auth *Auth) string {
	return accessTokenForFingerprint(auth)
}

// CommitInspectionRefresh validates the observed credential under the same lock
// used to merge the refresh. User changes unrelated to credentials are retained.
func (m *Manager) CommitInspectionRefresh(ctx context.Context, base, updated *Auth) (*Auth, error) {
	if base == nil || updated == nil || base.ID != updated.ID {
		return nil, ErrInspectionAuthChanged
	}
	saved, err := m.updateInternal(ctx, base, updated, updateModeInspectionRefresh)
	if err == nil && saved == nil {
		err = ErrInspectionAuthChanged
	}
	return saved, err
}

// reconcileInspectionRefreshStatus runs under the manager lock after the
// three-way merge. Successful refresh clears the observed error, but must not
// erase a newer error (even one with the same message) or an active cooldown.
func reconcileInspectionRefreshStatus(base, current, merged *Auth) {
	if base == nil || current == nil || merged == nil {
		return
	}
	if !reflect.DeepEqual(base.LastError, current.LastError) {
		merged.LastError = current.LastError
		if current.LastError != nil {
			merged.Unavailable = current.Unavailable
			if !merged.Disabled {
				merged.Status = current.Status
				merged.StatusMessage = current.StatusMessage
			}
		}
		return
	}
	merged.LastError = nil
	if reflect.DeepEqual(base.Metadata["last_error"], current.Metadata["last_error"]) {
		delete(merged.Metadata, "last_error")
	}
	if merged.Disabled {
		return
	}
	now := time.Now()
	if (current.Quota.Exceeded && current.Quota.Reason == "credential_quota" && current.Quota.NextRecoverAt.After(now)) ||
		(current.Unavailable && current.NextRetryAfter.After(now)) {
		return
	}
	merged.Status = StatusActive
	merged.Unavailable = false
	merged.StatusMessage = ""
}

func inspectionRefreshIdentityMatches(base, current *Auth) bool {
	if base == nil || current == nil || base.ID != current.ID || base.Index != current.Index ||
		base.Provider != current.Provider || base.FileName != current.FileName ||
		base.RegistrationEpoch != current.RegistrationEpoch || AccessTokenSHA256(base) != AccessTokenSHA256(current) {
		return false
	}
	for _, key := range []string{"access_token", "accessToken", "token", "Token", "refresh_token", "refreshToken", "id_token", "idToken", "session_id"} {
		if !reflect.DeepEqual(base.Metadata[key], current.Metadata[key]) {
			return false
		}
	}
	return true
}

func (m *Manager) shouldRefreshForInspection(a *Auth, now time.Time) bool {
	if a == nil {
		return false
	}
	if hasUnauthorizedAuthFailure(a) {
		return false
	}
	if !a.NextRefreshAfter.IsZero() && now.Before(a.NextRefreshAfter) {
		return false
	}
	if evaluator, ok := a.Runtime.(RefreshEvaluator); ok && evaluator != nil {
		return evaluator.ShouldRefresh(now, a)
	}

	lastRefresh := a.LastRefreshedAt
	if lastRefresh.IsZero() {
		if ts, ok := authLastRefreshTimestamp(a); ok {
			lastRefresh = ts
		}
	}

	expiry, hasExpiry := a.ExpirationTime()

	if interval := authPreferredInterval(a); interval > 0 {
		if hasExpiry && !expiry.IsZero() {
			if !expiry.After(now) {
				return true
			}
			if expiry.Sub(now) <= interval {
				return true
			}
		}
		if lastRefresh.IsZero() {
			return true
		}
		return now.Sub(lastRefresh) >= interval
	}

	provider := strings.ToLower(a.Provider)
	lead := ProviderRefreshLead(provider, a.Runtime)
	if lead == nil {
		return false
	}
	if *lead <= 0 {
		if hasExpiry && !expiry.IsZero() {
			return now.After(expiry)
		}
		return false
	}
	if hasExpiry && !expiry.IsZero() {
		return time.Until(expiry) <= *lead
	}
	if !lastRefresh.IsZero() {
		return now.Sub(lastRefresh) >= *lead
	}
	return true
}

func (m *Manager) RefreshIfDueForInspection(ctx context.Context, id string) (*Auth, bool, error) {
	return m.refreshForInspection(ctx, id, false)
}

func (m *Manager) ForceRefreshForInspection(ctx context.Context, id string) (*Auth, bool, error) {
	return m.refreshForInspection(ctx, id, true)
}

func (m *Manager) refreshForInspection(ctx context.Context, id string, force bool) (*Auth, bool, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	now := time.Now()
	// Select the executor, snapshot credentials and reserve the refresh together.
	// No later lookup may switch this refresh to a newly registered credential.
	m.mu.Lock()
	auth := m.auths[id]
	if auth == nil {
		m.mu.Unlock()
		return nil, false, nil
	}
	accountType, _ := auth.AccountInfo()
	exec := m.executors[auth.Provider]
	if accountType == "api_key" || exec == nil || (!force && !m.shouldRefreshForInspection(auth, now)) {
		current := auth.Clone()
		m.mu.Unlock()
		return current, false, nil
	}
	auth.NextRefreshAfter = now.Add(refreshPendingBackoff)
	base := auth.Clone()
	m.mu.Unlock()
	m.queueRefreshReschedule(id)

	// Executors may mutate their input even on error; keep base immutable.
	cloned := base.Clone()
	updated, err := exec.Refresh(ctx, cloned)
	if err != nil && errors.Is(err, context.Canceled) {
		return cloned, false, err
	}
	now = time.Now()
	if err != nil {
		unauthorized := isUnauthorizedError(err)
		m.mu.Lock()
		if !inspectionRefreshIdentityMatches(base, m.auths[id]) {
			m.mu.Unlock()
			return base, false, ErrInspectionAuthChanged
		}
		if current := m.auths[id]; current != nil {
			current.LastError = refreshErrorFromError(err)
			if unauthorized {
				current.NextRefreshAfter = time.Time{}
				current.Unavailable = true
				current.Status = StatusError
				current.StatusMessage = "unauthorized"
			} else {
				current.NextRefreshAfter = now.Add(refreshFailureBackoff)
			}
			m.auths[id] = current
		}
		m.mu.Unlock()
		m.RefreshSchedulerEntry(id)
		m.queueRefreshReschedule(id)
		return cloned, false, err
	}
	if updated == nil {
		updated = cloned
	}
	if updated.Runtime == nil {
		updated.Runtime = base.Runtime
	}
	updated.Disabled = base.Disabled
	if base.Disabled {
		updated.Status = base.Status
		updated.StatusMessage = base.StatusMessage
	}
	updated.LastRefreshedAt = now
	updated.NextRefreshAfter = time.Time{}
	updated.LastError = nil
	if updated.Metadata != nil {
		delete(updated.Metadata, "last_error")
	}
	updated.UpdatedAt = now
	if m.shouldRefreshForInspection(updated, now) {
		updated.NextRefreshAfter = now.Add(refreshIneffectiveBackoff)
	}
	saved, err := m.CommitInspectionRefresh(ctx, base, updated)
	if err != nil {
		return updated, false, err
	}
	return saved, true, nil
}
