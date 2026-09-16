package management

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"sort"
	"strings"
	"time"

	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const inspectionQuotaSource = "inspection"

func (s *accountInspectionScheduler) fillQuotaProtectionResult(auth *coreauth.Auth, result *accountInspectionResult) {
	if auth == nil {
		return
	}
	hold, ok := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	result.QuotaCooling = ok
	result.QuotaRetryAt = hold.RetryAt
	result.QuotaRevision = hold.Revision
}

func (s *accountInspectionScheduler) executeQuotaProtection(ctx context.Context, result *accountInspectionResult, settings accountInspectionSettings, action accountInspectionAction) error {
	auth, err := s.actionAuthForResult(*result)
	if err != nil {
		return err
	}
	var hold *prorouting.QuotaProtection
	if action == accountInspectionActionDisable {
		raw, err := json.Marshal(settings)
		if err != nil {
			return err
		}
		hold = &prorouting.QuotaProtection{Recheck: true, Model: result.QuotaModel, Reason: "inspection quota threshold", Settings: raw}
		if auth.Provider == "xai" && xaiInspectionUsingAPI(auth) {
			hold.Recheck = false
		}
		if settings.AutoExecuteQuotaRecoveryEnable {
			hold.RetryAt = result.QuotaResetAt
			if hold.RetryAt <= time.Now().UnixMilli() {
				hold.RetryAt = time.Now().Add(prorouting.RecoveryBackoff(auth.ID, 0)).UnixMilli()
			} else {
				hold.RetryAt += int64(prorouting.RecoveryBackoff(auth.ID, 0)-time.Minute)/int64(time.Millisecond) + 1000
			}
		}
	}
	if err := s.inspectionAuthManager().ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, result.QuotaRevision, hold); err != nil {
		return err
	}
	current, _ := s.inspectionAuthManager().GetByID(auth.ID)
	s.fillQuotaProtectionResult(current, result)
	if hold != nil {
		result.ActionReason = "额度保护已暂停调度"
	} else {
		result.ActionReason = "额度已恢复，解除额度保护"
	}
	return nil
}

// Due accounts drain in batches of four, waking the next batch immediately.
// Queries share the inspection limiter
// and backup gate; no goroutine survives the host lifecycle. Recovery probes do
// not execute account deletion/disable actions or paid deep inference probes.
func (s *accountInspectionScheduler) recoverQuotaProtections(ctx context.Context) {
	if s == nil || s.h == nil || s.inspectionAuthManager() == nil {
		return
	}
	release, err := s.beginLifecycle()
	if err != nil {
		return
	}
	defer release()
	if !s.fullRunMu.TryRLock() {
		return
	}
	defer s.fullRunMu.RUnlock()
	s.mu.Lock()
	stopped := s.stopped
	running := s.isRunningLocked()
	autoRecover := s.schedule.Settings.AutoExecuteQuotaRecoveryEnable
	s.mu.Unlock()
	if stopped || running || !autoRecover {
		return
	}
	auths := s.inspectionAuthManager().List()
	sort.Slice(auths, func(i, j int) bool { return recoveryTime(auths[i]) < recoveryTime(auths[j]) })
	due := make([]*coreauth.Auth, 0, 4)
	moreDue := false
	now := time.Now().UnixMilli()
	for _, auth := range auths {
		if auth == nil || auth.Disabled || auth.Status == coreauth.StatusDisabled {
			continue
		}
		hold, ok := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
		if ok {
			var settings accountInspectionSettings
			if json.Unmarshal(hold.Settings, &settings) == nil && settings.AutoExecuteQuotaRecoveryEnable != autoRecover {
				settings.AutoExecuteQuotaRecoveryEnable = autoRecover
				hold.Settings, _ = json.Marshal(settings)
				hold.RetryAt = 0
				if autoRecover {
					hold.RetryAt = time.Now().Add(prorouting.RecoveryBackoff(auth.ID, 0)).UnixMilli()
				}
				if err := s.inspectionAuthManager().ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, hold.Revision, &hold); err != nil {
					s.appendLog("warning", "额度恢复开关同步失败："+err.Error())
				} else {
					s.publishQuotaProtectionState(auth.ID, "额度恢复设置已更新")
				}
				continue
			}
		}
		if !ok || hold.RetryAt <= 0 || hold.RetryAt > now {
			continue
		}
		if len(due) == 4 {
			moreDue = true
			break
		}
		due = append(due, auth)
	}
	runAccountInspectionWorkers(len(due), 4, nil, func(index int) bool { s.recoverQuotaAccount(ctx, due[index]); return ctx.Err() == nil })
	s.refreshAccountPoliciesIfQuotaChanged()
	if moreDue && ctx.Err() == nil {
		select {
		case s.trigger <- struct{}{}:
		default:
		}
	}
}

func recoveryTime(auth *coreauth.Auth) int64 {
	if auth == nil {
		return math.MaxInt64
	}
	hold := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	if hold.RetryAt <= 0 {
		return math.MaxInt64
	}
	return hold.RetryAt
}

func (s *accountInspectionScheduler) recoverQuotaAccount(ctx context.Context, auth *coreauth.Auth) {
	hold := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	var settings accountInspectionSettings
	if json.Unmarshal(hold.Settings, &settings) != nil || !settings.AutoExecuteQuotaRecoveryEnable {
		return
	}
	if !hold.Recheck {
		if err := s.inspectionAuthManager().ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, hold.Revision, nil); err == nil {
			s.appendLog("info", "额度冷却到期，允许真实请求重新验证")
			s.publishQuotaProtectionState(auth.ID, "冷却到期，等待真实请求验证")
		}
		return
	}
	threshold := settings.UsedPercentThreshold
	// A small hysteresis prevents repeated protection near a configurable threshold.
	settings.UsedPercentThreshold = math.Max(0, threshold-2)
	settings.AntigravityDeepProbeEnabled = false
	settings.XAIDeepProbeEnabled = false
	settings.Retries = 0
	probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	release, err := s.probeLimiter.Acquire(probeCtx, min(4, settings.Workers), settings.ProviderWorkers, auth.Provider)
	if err != nil {
		return
	}
	defer release()
	result := s.inspectAccount(probeCtx, accountFromAuth(auth), settings)
	if ctx.Err() != nil {
		return
	}
	// inspectAccount may have refreshed OAuth. Its bound result is the new identity,
	// while the source revision still guards against a concurrent new restriction.
	current, err := s.actionAuthForResult(result)
	if err != nil {
		return
	}
	var next *prorouting.QuotaProtection
	recovered := result.Error == "" && result.ErrorCode == "" && result.QuotaKnown && result.UsedPercent != nil && proinspection.QuotaRecovered(*result.UsedPercent, threshold)
	if !recovered {
		hold.Failures++
		hold.RetryAt = time.Now().Add(prorouting.RecoveryBackoff(auth.ID, hold.Failures)).UnixMilli()
		if result.IsQuota && result.QuotaResetAt > hold.RetryAt {
			hold.RetryAt = result.QuotaResetAt + 1000
		}
		next = &hold
	}
	if err := s.inspectionAuthManager().ChangeQuotaProtection(ctx, current, inspectionQuotaSource, hold.Revision, next); err != nil {
		return
	}
	updated, _ := s.inspectionAuthManager().GetByID(auth.ID)
	s.fillQuotaProtectionResult(updated, &result)
	result.Action = accountInspectionActionKeep
	result.Executed = true
	if recovered {
		result.ActionReason = "额度已恢复，自动解除额度保护"
	} else {
		result.ActionReason = "额度保护中，等待下次定向复查"
	}
	s.mu.Lock()
	s.updateInspectionResultLocked(result, true, func(accountInspectionResult) (accountInspectionResult, bool) { return result, true })
	saveErr := s.saveResultSnapshotLocked()
	broadcast := s.statusBroadcastLocked()
	s.mu.Unlock()
	broadcast.send()
	if saveErr != nil {
		s.appendLog("warning", fmt.Sprintf("额度恢复结果保存失败：%v", saveErr))
	}
	if recovered {
		s.appendLog("success", fmt.Sprintf("%s 额度恢复，已解除调度保护", proinspection.ResultIdentity(result)))
	}
}

// Request quota failures use the same restriction store. Native cooldowns keep
// ownership of their model/credential scope and retry time; their expiry is
// reevaluated by the upstream scheduler without an inspection cycle.
func (c *routingPolicyController) protectRequestQuota(ctx context.Context, auth *coreauth.Auth, model string, policy routingProtectionProviderPolicy, event *routingProtectionEvent) error {
	now := time.Now()
	retryAt := event.ReleaseAt
	scope := model
	if auth.Quota.Reason == "credential_quota" && auth.Quota.NextRecoverAt.After(now) {
		scope = ""
		retryAt = auth.Quota.NextRecoverAt.UnixMilli()
	} else if state := auth.ModelStates[model]; state != nil && state.NextRetryAfter.After(now) {
		retryAt = state.NextRetryAfter.UnixMilli()
	}
	if policy.AutoEnable {
		if retryAt <= now.UnixMilli() {
			retryAt = now.Add(prorouting.RecoveryBackoff(auth.ID, 0)).UnixMilli()
		}
	} else {
		retryAt = 0
	}
	source := "routing:" + scope
	protections := prorouting.QuotaProtections(auth.Metadata)
	old := protections[source]
	if old.RetryAt > retryAt && retryAt != 0 {
		retryAt = old.RetryAt
	}
	hold := prorouting.QuotaProtection{RetryAt: retryAt, Model: scope, Reason: event.Reason}
	if err := c.h.authManager.ChangeQuotaProtection(ctx, auth, source, old.Revision, &hold); err != nil {
		return err
	}
	event.Action = "cooldown"
	event.ReleaseAt = retryAt
	return nil
}

func (c *routingPolicyController) releaseQuotaProtections(ctx context.Context, auth *coreauth.Auth, dueOnly bool) (bool, error) {
	changed := false
	for source, hold := range prorouting.QuotaProtections(auth.Metadata) {
		if !strings.HasPrefix(source, "routing:") {
			continue
		}
		if dueOnly && (hold.RetryAt == 0 || hold.RetryAt > time.Now().UnixMilli()) {
			continue
		}
		if err := c.h.authManager.ChangeQuotaProtection(ctx, auth, source, hold.Revision, nil); err != nil {
			return changed, err
		}
		changed = true
	}
	return changed, nil
}

func hasRoutingQuotaProtection(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	for source := range prorouting.QuotaProtections(auth.Metadata) {
		if strings.HasPrefix(source, "routing:") {
			return true
		}
	}
	return false
}

func (s *accountInspectionScheduler) publishQuotaProtectionState(authID, reason string) {
	auth, ok := s.inspectionAuthManager().GetByID(authID)
	if !ok || auth == nil {
		return
	}
	observed := accountFromAuth(auth).baseResult()
	s.fillQuotaProtectionResult(auth, &observed)
	s.mu.Lock()
	changed := s.updateInspectionResultLocked(observed, false, func(current accountInspectionResult) (accountInspectionResult, bool) {
		current.QuotaCooling = observed.QuotaCooling
		current.QuotaRetryAt = observed.QuotaRetryAt
		current.QuotaRevision = observed.QuotaRevision
		current.ActionReason = reason
		if !observed.QuotaCooling {
			current.Action = accountInspectionActionKeep
			current.IsQuota = false
		}
		return current, true
	})
	var err error
	if changed {
		err = s.saveResultSnapshotLocked()
	}
	broadcast := s.statusBroadcastLocked()
	s.mu.Unlock()
	broadcast.send()
	if err != nil {
		s.appendLog("warning", "额度保护状态保存失败："+err.Error())
	}
}
