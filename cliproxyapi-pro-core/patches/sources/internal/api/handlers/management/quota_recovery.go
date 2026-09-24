package management

import (
	"context"
	"encoding/json"
	"errors"
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

// A failed persistence attempt must not keep an account at the head of every
// recovery batch. This fallback is scoped to the observed protection revision.
type quotaRecoveryDeferral struct {
	revision int64
	retryAt  int64
}

type quotaRecoveryOutcome struct {
	authID   string
	err      error
	retryAt  int64
	canceled bool
}

// checkQuotaRecoveryNow shares the same directed recovery path as the timed
// worker. It never runs concurrently with a full inspection of the account.
func (s *accountInspectionScheduler) checkQuotaRecoveryNow(ctx context.Context, auth *coreauth.Auth) error {
	if s == nil || auth == nil || s.inspectionAuthManager() == nil {
		return errors.New("quota recovery unavailable")
	}
	release, err := s.beginLifecycle()
	if err != nil {
		return err
	}
	defer release()
	if !s.fullRunMu.TryRLock() {
		return errAccountInspectionAlreadyRunning
	}
	defer s.fullRunMu.RUnlock()
	s.mu.Lock()
	running := s.isRunningLocked()
	s.mu.Unlock()
	if running {
		return errAccountInspectionAlreadyRunning
	}
	current, ok := s.inspectionAuthManager().GetByID(auth.ID)
	if !ok || current == nil || current.EnsureIndex() != auth.EnsureIndex() || current.RegistrationEpoch != auth.RegistrationEpoch {
		return errAccountInspectionResultStale
	}
	hold, ok := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	if !ok {
		return errAccountInspectionResultStale
	}
	var settings accountInspectionSettings
	if err := json.Unmarshal(hold.Settings, &settings); err != nil {
		return fmt.Errorf("invalid quota recovery settings: %w", err)
	}
	if _, loaded := s.quotaRecoveryActive.LoadOrStore(auth.ID, struct{}{}); loaded {
		return errAccountInspectionAlreadyRunning
	}
	defer s.quotaRecoveryActive.Delete(auth.ID)
	recoveryErr := s.recoverQuotaAccountWithMode(ctx, current, true)
	s.refreshAccountPoliciesIfQuotaChanged()
	return recoveryErr
}

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
		if deferred, loaded := s.quotaRecoveryDeferred.Load(auth.ID); loaded {
			pause := deferred.(quotaRecoveryDeferral)
			if pause.revision == hold.Revision && pause.retryAt > now {
				continue
			}
			s.quotaRecoveryDeferred.Delete(auth.ID)
		}
		if _, active := s.quotaRecoveryActive.Load(auth.ID); active {
			continue
		}
		if len(due) == 4 {
			moreDue = true
			break
		}
		due = append(due, auth)
	}
	outcomes := make([]quotaRecoveryOutcome, len(due))
	runAccountInspectionWorkers(len(due), 4, nil, func(index int) bool {
		outcomes[index] = s.recoverQuotaAccount(ctx, due[index])
		return ctx.Err() == nil
	})
	for _, outcome := range outcomes {
		if outcome.err != nil && !outcome.canceled {
			if outcome.retryAt > 0 {
				s.appendLog("warning", fmt.Sprintf("账号 %s 额度恢复失败，下次尝试时间 %d：%v", outcome.authID, outcome.retryAt, outcome.err))
			} else {
				s.appendLog("warning", fmt.Sprintf("账号 %s 额度恢复失败，保护可能已变化：%v", outcome.authID, outcome.err))
			}
		}
	}
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

func (s *accountInspectionScheduler) recoverQuotaAccount(ctx context.Context, auth *coreauth.Auth) quotaRecoveryOutcome {
	outcome := quotaRecoveryOutcome{}
	if auth == nil {
		return outcome
	}
	outcome.authID = auth.ID
	if _, loaded := s.quotaRecoveryActive.LoadOrStore(auth.ID, struct{}{}); loaded {
		return outcome
	}
	defer s.quotaRecoveryActive.Delete(auth.ID)
	outcome.err = s.recoverQuotaAccountWithMode(ctx, auth, false)
	if outcome.err == nil {
		s.quotaRecoveryDeferred.Delete(auth.ID)
		return outcome
	}
	if ctx.Err() != nil {
		outcome.canceled = true
		return outcome
	}
	outcome.retryAt = s.deferQuotaRecoveryFailure(ctx, auth, outcome.err)
	return outcome
}

// Persist retry state for every recoverable failure, including limiter/timeouts
// and failures writing the normal probe result. A revision change means another
// operation owns the protection, so it must never inherit this attempt's error.
func (s *accountInspectionScheduler) deferQuotaRecoveryFailure(ctx context.Context, auth *coreauth.Auth, recoveryErr error) int64 {
	manager := s.inspectionAuthManager()
	if manager == nil || auth == nil {
		return 0
	}
	current, ok := manager.GetByID(auth.ID)
	if !ok || current == nil || current.EnsureIndex() != auth.EnsureIndex() || current.RegistrationEpoch != auth.RegistrationEpoch {
		return 0
	}
	old := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	hold, ok := prorouting.QuotaProtections(current.Metadata)[inspectionQuotaSource]
	if !ok || hold.Revision != old.Revision {
		return 0
	}
	hold.Failures++
	hold.RetryAt = prorouting.NextRecheckAt(0, auth.ID, hold.Failures, time.Now())
	if err := manager.ChangeQuotaProtection(ctx, current, inspectionQuotaSource, hold.Revision, &hold); err == nil {
		s.quotaRecoveryDeferred.Delete(auth.ID)
		s.publishQuotaProtectionState(auth.ID, "额度恢复失败，等待下次复查")
		return hold.RetryAt
	} else {
		// Storage may be temporarily unavailable. Keep this attempt out of the
		// immediate queue even if the durable retry write also failed.
		s.quotaRecoveryDeferred.Store(auth.ID, quotaRecoveryDeferral{revision: hold.Revision, retryAt: hold.RetryAt})
		s.appendLog("warning", fmt.Sprintf("账号 %s 额度恢复错误 %v；重试时间写入失败：%v", auth.ID, recoveryErr, err))
		return hold.RetryAt
	}
}

func (s *accountInspectionScheduler) recoverQuotaAccountWithMode(ctx context.Context, auth *coreauth.Auth, manual bool) error {
	hold := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]
	var settings accountInspectionSettings
	if err := json.Unmarshal(hold.Settings, &settings); err != nil {
		return fmt.Errorf("invalid quota recovery settings: %w", err)
	}
	if !hold.Recheck && hold.RetryAt > 0 && auth.Provider == "xai" && xaiInspectionUsingAPI(auth) {
		return s.recoverQuotaWithProbeRequest(ctx, auth, hold, settings)
	}
	if !manual && !settings.AutoExecuteQuotaRecoveryEnable {
		return nil
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
		return err
	}
	defer release()
	result := s.inspectAccount(probeCtx, accountFromAuth(auth), settings)
	if probeCtx.Err() != nil {
		return probeCtx.Err()
	}
	// inspectAccount may have refreshed OAuth. Its bound result is the new identity,
	// while the source revision still guards against a concurrent new restriction.
	current, err := s.actionAuthForResult(result)
	if err != nil {
		return err
	}
	var next *prorouting.QuotaProtection
	recovered := result.Error == "" && result.ErrorCode == "" && result.QuotaKnown && result.UsedPercent != nil && proinspection.QuotaRecovered(*result.UsedPercent, threshold)
	if !recovered {
		if settings.AutoExecuteQuotaRecoveryEnable {
			hold.Failures++
			hold.RetryAt = prorouting.NextRecheckAt(result.QuotaResetAt, auth.ID, hold.Failures, time.Now())
		} else {
			hold.RetryAt = 0
		}
		next = &hold
	}
	if err := s.inspectionAuthManager().ChangeQuotaProtection(ctx, current, inspectionQuotaSource, hold.Revision, next); err != nil {
		return err
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
	return nil
}

func (s *accountInspectionScheduler) recoverQuotaWithProbeRequest(ctx context.Context, auth *coreauth.Auth, hold prorouting.QuotaProtection, settings accountInspectionSettings) error {
	settings.Retries = 0
	settings.AntigravityDeepProbeEnabled = false
	settings.XAIDeepProbeEnabled = false
	probeCtx, cancel := context.WithTimeout(ctx, 45*time.Second)
	defer cancel()
	release, err := s.probeLimiter.Acquire(probeCtx, 1, 1, auth.Provider)
	if err != nil {
		return err
	}
	defer release()
	model := strings.TrimSpace(settings.XAIDeepProbeModel)
	if model == "" {
		model = "grok-4.5"
	}
	probeResult := coreauth.BindPinnedResult(auth, coreauth.Result{Model: model})
	result := s.inspectAccount(probeCtx, accountFromAuth(auth), settings)
	if probeCtx.Err() != nil {
		return probeCtx.Err()
	}
	_, err = s.actionAuthForResult(result)
	if err != nil {
		return err
	}
	statusCode := 0
	if result.StatusCode != nil {
		statusCode = *result.StatusCode
	}
	succeeded := statusCode >= http.StatusOK && statusCode < http.StatusMultipleChoices && result.Error == "" && result.ErrorCode == "" && !result.IsQuota
	failedWithResponse := statusCode >= http.StatusMultipleChoices
	var next *prorouting.QuotaProtection
	if succeeded || failedWithResponse {
		probeResult.Success = succeeded
		if !succeeded {
			message := firstNonEmptyStringValue(result.ErrorCode, result.Error, result.ActionReason, http.StatusText(statusCode))
			probeResult.Error = &coreauth.Error{HTTPStatus: statusCode, Message: message}
		}
		if !s.inspectionAuthManager().MarkPinnedResult(ctx, probeResult) {
			return coreauth.ErrSchedulingBlockChanged
		}
	}
	// A failed request is not evidence that account-wide quota recovered.
	// Keep the original scope even when native accounting adds a model cooldown.
	if !succeeded {
		hold.Failures++
		hold.RetryAt = prorouting.NextRecheckAt(0, auth.ID, hold.Failures, time.Now())
		next = &hold
	}
	if err := s.inspectionAuthManager().ChangeQuotaProtection(ctx, auth, inspectionQuotaSource, hold.Revision, next); err != nil {
		return err
	}
	updated, _ := s.inspectionAuthManager().GetByID(auth.ID)
	s.fillQuotaProtectionResult(updated, &result)
	result.Action = accountInspectionActionKeep
	result.Executed = true
	switch {
	case succeeded:
		result.ActionReason = "真实请求验证成功，解除额度保护"
		s.appendLog("success", fmt.Sprintf("%s 真实请求验证成功，已解除调度保护", proinspection.ResultIdentity(result)))
	case failedWithResponse:
		result.ActionReason = "真实请求验证失败，保留额度保护并等待下次重试"
		s.appendLog("warning", fmt.Sprintf("%s 真实请求验证失败，保留额度保护", proinspection.ResultIdentity(result)))
	default:
		result.ActionReason = "真实请求验证未完成，等待下次重试"
	}
	s.saveQuotaRecoveryResult(result)
	return nil
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
		// This notification only reflects the live scheduling restriction. The
		// last probe's action, reason and quota conclusion remain observations.
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
	if changed && reason != "" {
		s.appendLog("info", fmt.Sprintf("账号 %s：%s", authID, reason))
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
