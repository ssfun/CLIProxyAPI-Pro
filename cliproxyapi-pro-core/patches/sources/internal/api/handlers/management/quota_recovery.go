package management

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/registry"
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
		if coveredByUpstreamQuota(auth, result) {
			result.ActionReason = "上游冷却已覆盖该额度窗口，未叠加巡检保护"
			return nil
		}
		hold = newInspectionQuotaHold(auth, result, settings)
		if hold == nil {
			return fmt.Errorf("marshal inspection quota protection settings")
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
	if stopped || running {
		return
	}
	if !s.inspectionAuthManager().HasStoredQuotaProtections(ctx) {
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
				syncInspectionHoldResume(&hold, auth, autoRecover)
				if err := s.inspectionAuthManager().ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, hold.Revision, &hold); err != nil {
					s.appendLog("warning", "额度恢复开关同步失败："+err.Error())
				} else {
					s.publishQuotaProtectionState(auth.ID, "额度恢复设置已更新")
				}
				continue
			}
		}
		if !ok || hold.RetryAt <= 0 || hold.RetryAt > now || hold.Recheck && !autoRecover {
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

func newInspectionQuotaHold(auth *coreauth.Auth, result *accountInspectionResult, settings accountInspectionSettings) *prorouting.QuotaProtection {
	raw, err := json.Marshal(settings)
	if err != nil {
		return nil
	}
	hold := &prorouting.QuotaProtection{
		Recheck:  settings.AutoExecuteQuotaRecoveryEnable,
		Model:    result.QuotaModel,
		Reason:   "inspection quota threshold",
		Settings: raw,
	}
	if auth != nil && auth.Provider == "xai" && xaiInspectionUsingAPI(auth) {
		hold.Recheck = false
		hold.RetryAt = prorouting.ScheduleRecheckAt(result.QuotaResetAt, authIDForHold(auth), time.Now())
		return hold
	}
	if !settings.AutoExecuteQuotaRecoveryEnable {
		return hold
	}
	hold.RetryAt = prorouting.ScheduleRecheckAt(result.QuotaResetAt, authIDForHold(auth), time.Now())
	return hold
}

func syncInspectionHoldResume(hold *prorouting.QuotaProtection, auth *coreauth.Auth, autoRecover bool) {
	if hold == nil {
		return
	}
	probeRequest := auth != nil && auth.Provider == "xai" && xaiInspectionUsingAPI(auth)
	if !autoRecover {
		hold.Recheck = false
		if probeRequest {
			if hold.RetryAt <= 0 {
				hold.RetryAt = time.Now().Add(prorouting.RecoveryBackoff(authIDForHold(auth), 0)).UnixMilli()
			}
			return
		}
		hold.RetryAt = 0
		return
	}
	hold.Recheck = !probeRequest
	hold.RetryAt = time.Now().Add(prorouting.RecoveryBackoff(authIDForHold(auth), 0)).UnixMilli()
}

func authIDForHold(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	return auth.ID
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
	if json.Unmarshal(hold.Settings, &settings) != nil {
		return
	}
	if !hold.Recheck {
		s.recoverQuotaWithProbeRequest(ctx, auth, hold, settings)
		return
	}
	if !settings.AutoExecuteQuotaRecoveryEnable {
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
		hold.RetryAt = prorouting.NextRecheckAt(result.QuotaResetAt, auth.ID, hold.Failures, time.Now())
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
	s.saveQuotaRecoveryResult(result)
	if recovered {
		s.appendLog("success", fmt.Sprintf("%s 额度恢复，已解除调度保护", proinspection.ResultIdentity(result)))
	}
}

func (s *accountInspectionScheduler) recoverQuotaWithProbeRequest(ctx context.Context, auth *coreauth.Auth, hold prorouting.QuotaProtection, settings accountInspectionSettings) {
	settings.Retries = 0
	settings.AntigravityDeepProbeEnabled = false
	settings.XAIDeepProbeEnabled = false
	probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	release, err := s.probeLimiter.Acquire(probeCtx, 1, 1, auth.Provider)
	if err != nil {
		return
	}
	defer release()
	result := s.inspectAccount(probeCtx, accountFromAuth(auth), settings)
	if ctx.Err() != nil {
		return
	}
	current, err := s.actionAuthForResult(result)
	if err != nil {
		return
	}
	statusCode := 0
	if result.StatusCode != nil {
		statusCode = *result.StatusCode
	}
	succeeded := statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices && result.Error == "" && result.ErrorCode == "" && !result.IsQuota
	failedWithResponse := statusCode >= http.StatusMultipleChoices
	completed := succeeded || failedWithResponse
	var next *prorouting.QuotaProtection
	if completed {
		model := strings.TrimSpace(settings.XAIDeepProbeModel)
		if model == "" {
			model = "grok-4.5"
		}
		probeResult := coreauth.Result{AuthID: current.ID, Provider: current.Provider, Model: model, Success: succeeded}
		if !succeeded {
			message := firstNonEmptyStringValue(result.ErrorCode, result.Error, result.ActionReason, http.StatusText(statusCode))
			probeResult.Error = &coreauth.Error{HTTPStatus: statusCode, Message: message}
		}
		s.inspectionAuthManager().MarkResult(ctx, probeResult)
		current, _ = s.inspectionAuthManager().GetByID(auth.ID)
	} else {
		hold.Failures++
		hold.RetryAt = prorouting.NextRecheckAt(0, auth.ID, hold.Failures, time.Now())
		next = &hold
	}
	if err := s.inspectionAuthManager().ChangeQuotaProtection(ctx, current, inspectionQuotaSource, hold.Revision, next); err != nil {
		return
	}
	updated, _ := s.inspectionAuthManager().GetByID(auth.ID)
	s.fillQuotaProtectionResult(updated, &result)
	result.Action = accountInspectionActionKeep
	result.Executed = true
	switch {
	case succeeded:
		result.ActionReason = "真实请求验证成功，解除额度保护"
		s.appendLog("success", fmt.Sprintf("%s 真实请求验证成功，已解除调度保护", proinspection.ResultIdentity(result)))
	case completed:
		result.ActionReason = "真实请求验证失败，已交由上游冷却"
		s.appendLog("warning", fmt.Sprintf("%s 真实请求验证失败，已转入上游冷却", proinspection.ResultIdentity(result)))
	default:
		result.ActionReason = "真实请求验证未完成，等待下次重试"
	}
	s.saveQuotaRecoveryResult(result)
}

func (s *accountInspectionScheduler) saveQuotaRecoveryResult(result accountInspectionResult) {
	s.mu.Lock()
	s.updateInspectionResultLocked(result, true, func(accountInspectionResult) (accountInspectionResult, bool) { return result, true })
	saveErr := s.saveResultSnapshotLocked()
	broadcast := s.statusBroadcastLocked()
	s.mu.Unlock()
	broadcast.send()
	if saveErr != nil {
		s.appendLog("warning", fmt.Sprintf("额度恢复结果保存失败：%v", saveErr))
	}
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

func coveredByUpstreamQuota(auth *coreauth.Auth, result *accountInspectionResult) bool {
	if auth == nil || result == nil {
		return false
	}
	now := time.Now()
	if auth.Quota.Exceeded && auth.Quota.Reason == "credential_quota" && auth.Quota.NextRecoverAt.After(now) {
		if result.QuotaResetAt <= 0 || auth.Quota.NextRecoverAt.UnixMilli() >= result.QuotaResetAt {
			return true
		}
	}
	if strings.TrimSpace(result.QuotaModel) == "" || len(auth.ModelStates) == 0 {
		return false
	}
	models := registry.GetGlobalRegistry().GetModelsForClient(auth.ID)
	if len(models) == 0 {
		return false
	}
	coveredUntil := time.Time{}
	for _, pattern := range strings.Split(result.QuotaModel, ",") {
		pattern = strings.TrimSpace(pattern)
		if pattern == "" {
			continue
		}
		patternCovered := false
		for _, model := range models {
			if model == nil || !prorouting.ModelMatchesProtection(pattern, model.ID) {
				continue
			}
			patternCovered = true
			state := auth.ModelStates[strings.TrimSpace(model.ID)]
			if state == nil || !state.Quota.Exceeded || state.Quota.Reason != "quota" {
				return false
			}
			next := state.NextRetryAfter
			if state.Quota.NextRecoverAt.After(next) {
				next = state.Quota.NextRecoverAt
			}
			if !next.After(now) {
				return false
			}
			if coveredUntil.IsZero() || next.Before(coveredUntil) {
				coveredUntil = next
			}
		}
		if !patternCovered {
			return false
		}
	}
	return !coveredUntil.IsZero() && (result.QuotaResetAt <= 0 || coveredUntil.UnixMilli() >= result.QuotaResetAt)
}
