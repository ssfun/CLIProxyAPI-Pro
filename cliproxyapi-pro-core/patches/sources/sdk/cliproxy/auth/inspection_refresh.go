package auth

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"time"
)

var ErrInspectionAuthChanged = errors.New("account credentials changed during inspection refresh")

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

func inspectionRefreshIdentityMatches(base, current *Auth) bool {
	if base == nil || current == nil || base.ID != current.ID || base.Index != current.Index ||
		base.Provider != current.Provider || base.FileName != current.FileName ||
		base.RegistrationEpoch != current.RegistrationEpoch || AccessTokenSHA256(base) != AccessTokenSHA256(current) {
		return false
	}
	for _, key := range []string{"refresh_token", "id_token", "session_id"} {
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

func (m *Manager) markRefreshPendingForInspection(id string, now time.Time, force bool) bool {
	m.mu.Lock()
	auth, ok := m.auths[id]
	if !ok || auth == nil {
		m.mu.Unlock()
		return false
	}
	if !force && !auth.NextRefreshAfter.IsZero() && now.Before(auth.NextRefreshAfter) {
		m.mu.Unlock()
		return false
	}
	auth.NextRefreshAfter = now.Add(refreshPendingBackoff)
	m.auths[id] = auth
	m.mu.Unlock()

	m.queueRefreshReschedule(id)
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
	m.mu.RLock()
	auth := m.auths[id]
	if auth == nil {
		m.mu.RUnlock()
		return nil, false, nil
	}
	current := auth.Clone()
	accountType, _ := auth.AccountInfo()
	if accountType == "api_key" || (!force && !m.shouldRefreshForInspection(auth, now)) {
		m.mu.RUnlock()
		return current, false, nil
	}
	exec := m.executors[auth.Provider]
	m.mu.RUnlock()
	if exec == nil {
		return current, false, nil
	}
	if !m.markRefreshPendingForInspection(id, now, force) {
		m.mu.RLock()
		defer m.mu.RUnlock()
		if latest := m.auths[id]; latest != nil {
			return latest.Clone(), false, nil
		}
		return nil, false, nil
	}

	m.mu.RLock()
	auth = m.auths[id]
	if auth == nil {
		m.mu.RUnlock()
		return nil, false, nil
	}
	exec = m.executors[auth.Provider]
	cloned := auth.Clone()
	preservedDisabled := auth.Disabled
	preservedStatus := auth.Status
	preservedStatusMessage := auth.StatusMessage
	m.mu.RUnlock()
	if exec == nil {
		return cloned, false, nil
	}

	updated, err := exec.Refresh(ctx, cloned)
	if err != nil && errors.Is(err, context.Canceled) {
		return cloned, false, err
	}
	now = time.Now()
	if err != nil {
		unauthorized := isUnauthorizedError(err)
		m.mu.Lock()
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
		updated.Runtime = auth.Runtime
	}
	updated.Disabled = preservedDisabled
	if preservedDisabled {
		updated.Status = preservedStatus
		updated.StatusMessage = preservedStatusMessage
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
	saved, err := m.Update(ctx, updated)
	if err != nil {
		return updated, false, err
	}
	return saved, true, nil
}
