package management

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
)

func runAccountInspectionWorkers(total int, workers int, beforeNext func() bool, run func(index int) bool) {
	proinspection.RunWorkers(total, workers, beforeNext, run)
}

func runAccountInspectionProviderWorkers(total int, workers int, providerWorkers int, provider func(index int) string, beforeNext func() bool, run func(index int) bool) {
	proinspection.RunKeyedWorkers(total, workers, providerWorkers, provider, beforeNext, run)
}

func (s *accountInspectionScheduler) executeInspection(ctx context.Context, settings accountInspectionSettings) ([]accountInspectionResult, accountInspectionSummary, error) {
	auths, err := s.auths()
	if err != nil {
		return nil, accountInspectionSummary{}, err
	}
	liveAuths := make([]*coreauth.Auth, 0, len(auths))
	accounts := make([]accountInspectionAccount, 0, len(auths))
	existingPaths := make(map[string]bool)
	for _, auth := range auths {
		liveAuths = append(liveAuths, auth)
		account := accountFromAuth(auth)
		if shouldInspectAccount(account, settings.TargetType) {
			accounts = append(accounts, account)
		}
	}
	sort.Slice(accounts, func(i, j int) bool {
		if accounts[i].FileName == accounts[j].FileName {
			return accounts[i].AuthIndex < accounts[j].AuthIndex
		}
		return accounts[i].FileName < accounts[j].FileName
	})
	probeSetCount := len(accounts)
	accounts = sampleAccounts(accounts, settings.SampleSize)
	accounts = s.filterExistingAccounts(accounts, existingPaths)
	s.appendLog("info", fmt.Sprintf("巡检集合 %d 个账号，本次探测 %d 个账号", probeSetCount, len(accounts)))

	results := make([]accountInspectionResult, len(accounts))
	completed := 0
	inFlight := 0
	var progressMu sync.Mutex
	var runErr error
	var runErrOnce sync.Once
	setRunErr := func(err error) {
		if err == nil {
			return
		}
		runErrOnce.Do(func() { runErr = err })
	}
	s.updateProgress(len(accounts), 0, 0, true)
	s.appendLog("info", fmt.Sprintf("巡检并发：总任务 %d，单提供商 %d，操作执行 %d", settings.Workers, settings.ProviderWorkers, settings.DeleteWorkers))
	runAccountInspectionProviderWorkers(
		len(accounts),
		settings.Workers,
		settings.ProviderWorkers,
		func(index int) string { return accounts[index].Provider },
		func() bool {
			if err := s.waitIfPaused(ctx); err != nil {
				setRunErr(err)
				return false
			}
			return true
		},
		func(index int) bool {
			account := accounts[index]
			progressMu.Lock()
			inFlight++
			s.updateProgress(len(accounts), completed, inFlight, false)
			progressMu.Unlock()
			results[index] = s.inspectAccount(ctx, account, settings)
			progressMu.Lock()
			inFlight--
			completed++
			s.updateProgress(len(accounts), completed, inFlight, false)
			progressMu.Unlock()
			return true
		},
	)
	if runErr != nil {
		partial := proinspection.CompletedResults(results)
		return partial, summarizeAccountInspection(len(liveAuths), probeSetCount, accounts, partial), runErr
	}
	if err := ctx.Err(); err != nil {
		partial := proinspection.CompletedResults(results)
		return partial, summarizeAccountInspection(len(liveAuths), probeSetCount, accounts, partial), err
	}

	s.applyAutomaticActions(ctx, results, settings)
	return results, summarizeAccountInspection(len(liveAuths), probeSetCount, accounts, results), nil
}

func (s *accountInspectionScheduler) auths() ([]*coreauth.Auth, error) {
	if s.h == nil {
		return nil, fmt.Errorf("management handler unavailable")
	}
	s.h.mu.Lock()
	manager := s.h.authManager
	s.h.mu.Unlock()
	if manager == nil {
		return nil, fmt.Errorf("core auth manager unavailable")
	}
	return manager.List(), nil
}

func (s *accountInspectionScheduler) filterExistingAccounts(accounts []accountInspectionAccount, existingPaths map[string]bool) []accountInspectionAccount {
	out := accounts[:0]
	for _, account := range accounts {
		if s.authFileExists(account.Auth, existingPaths) {
			out = append(out, account)
		}
	}
	return out
}

func (s *accountInspectionScheduler) authFileExists(auth *coreauth.Auth, existingPaths map[string]bool) bool {
	if auth == nil {
		return false
	}
	if isRuntimeOnlyAuth(auth) {
		return true
	}
	path := strings.TrimSpace(authAttribute(auth, "path"))
	if path == "" && s.h != nil && s.h.cfg != nil {
		fileName := strings.TrimSpace(auth.FileName)
		if fileName != "" {
			path = filepath.Join(s.h.cfg.AuthDir, filepath.Base(fileName))
		}
	}
	if path == "" {
		return true
	}
	if exists, ok := existingPaths[path]; ok {
		return exists
	}
	_, err := os.Stat(path)
	exists := err == nil || !os.IsNotExist(err)
	existingPaths[path] = exists
	return exists
}

func accountFromAuth(auth *coreauth.Auth) accountInspectionAccount {
	if auth == nil {
		return accountInspectionAccount{}
	}
	auth.EnsureIndex()
	provider := accountInspectionProvider(auth)
	fileName := strings.TrimSpace(auth.FileName)
	if fileName == "" {
		fileName = strings.TrimSpace(auth.ID)
	}
	name := firstNonEmptyAuthValue(auth, "name")
	email := accountInspectionAuthEmail(auth)
	displayName := firstNonEmptyStringValue(email, fileName)
	return accountInspectionAccount{
		Auth:              auth,
		AuthID:            strings.TrimSpace(auth.ID),
		Key:               proinspection.AccountKey(fileName, auth.Index),
		Provider:          provider,
		FileName:          fileName,
		DisplayName:       displayName,
		Email:             email,
		Name:              name,
		AuthIndex:         auth.Index,
		AccessTokenSHA256: coreauth.AccessTokenSHA256(auth),
		Disabled:          auth.Disabled,
	}
}

func accountInspectionProvider(auth *coreauth.Auth) string {
	return strings.ToLower(strings.TrimSpace(auth.Provider))
}

func accountInspectionAuthEmail(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if value := firstNonEmptyAuthValue(auth, "email"); value != "" {
		return value
	}
	return idTokenStringClaim(auth.Metadata["id_token"], "email")
}

func firstNonEmptyAuthValue(auth *coreauth.Auth, keys ...string) string {
	if auth == nil {
		return ""
	}
	for _, key := range keys {
		if value := stringFromAny(auth.Metadata[key]); value != "" {
			return value
		}
		if value := strings.TrimSpace(auth.Attributes[key]); value != "" {
			return value
		}
	}
	return ""
}

func firstNonEmptyStringValue(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

func idTokenStringClaim(raw any, keys ...string) string {
	if mapped, ok := raw.(map[string]any); ok {
		for _, key := range keys {
			if value := stringFromAny(mapped[key]); value != "" {
				return value
			}
		}
		return ""
	}
	token := stringFromAny(raw)
	if token == "" {
		return ""
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(token), &parsed); err == nil {
		for _, key := range keys {
			if value := stringFromAny(parsed[key]); value != "" {
				return value
			}
		}
		return ""
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return ""
	}
	for _, key := range keys {
		if value := stringFromAny(data[key]); value != "" {
			return value
		}
	}
	return ""
}

func isAccountInspectionAPIKeyAuth(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	label := strings.ToLower(strings.TrimSpace(auth.Label))
	if strings.Contains(label, "apikey") || strings.Contains(label, "api-key") {
		return true
	}
	source := strings.ToLower(strings.TrimSpace(authAttribute(auth, "source")))
	if strings.HasPrefix(source, "config:") && strings.TrimSpace(authAttribute(auth, "api_key")) != "" {
		return true
	}
	return strings.TrimSpace(authAttribute(auth, "api_key")) != "" && strings.TrimSpace(authAttribute(auth, "path")) == ""
}

func shouldInspectAccount(account accountInspectionAccount, targetType string) bool {
	return proinspection.ShouldInspectCandidate(account.Auth != nil, isAccountInspectionAPIKeyAuth(account.Auth), account.Provider, targetType)
}

func sampleAccounts(accounts []accountInspectionAccount, sampleSize int) []accountInspectionAccount {
	return proinspection.Sample(accounts, sampleSize, time.Now().UnixNano())
}

func (s *accountInspectionScheduler) inspectAccount(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) accountInspectionResult {
	result := account.baseResult()
	if account.AuthIndex == "" {
		result.ActionReason = "缺少 auth_index，保留账号"
		result.Error = "missing auth_index"
		result.ErrorCode = "missing_auth_index"
		return result
	}
	if refreshed, refreshTriggered, refreshErr := s.refreshAccountIfDue(ctx, account, settings); refreshErr != nil {
		result.TokenRefreshTriggered = refreshTriggered
		result.NextRefreshAt = account.nextRefreshAtMillis()
		if errors.Is(refreshErr, coreauth.ErrInspectionAuthChanged) {
			result.TokenRefreshStatus = "failed"
			result.TokenRefreshError = refreshErr.Error()
			result.Error = refreshErr.Error()
			result.ErrorCode = "inspection_identity_changed"
			result.ActionReason = "账号凭据已变化，跳过本次巡检动作"
			s.appendLog("warning", fmt.Sprintf("%s 凭据已变化，跳过本次巡检动作", account.identity()))
			return result
		}
		if errors.Is(refreshErr, context.Canceled) || errors.Is(refreshErr, context.DeadlineExceeded) {
			result.Error = refreshErr.Error()
			result.ActionReason = "巡检已取消，保留账号"
			return result
		}
		result.TokenRefreshStatus = "failed"
		result.TokenRefreshError = refreshErr.Error()
		result.Error = refreshErr.Error()
		result.ErrorCode = "token_refresh_error"
		result.ActionReason = "刷新令牌失败，保留账号"
		s.syncInspectionAuthError(ctx, account, "token_refresh_error", refreshErr.Error(), 0)
		s.appendLog("warning", fmt.Sprintf("%s 刷新令牌失败，保留账号：%s", account.identity(), refreshErr.Error()))
		return result
	} else if refreshTriggered {
		account = refreshed
		result = account.baseResult()
		result.TokenRefreshTriggered = true
		result.TokenRefreshStatus = "success"
	} else if refreshed.Auth != nil {
		account = refreshed
		result = account.baseResult()
	}
	result.NextRefreshAt = account.nextRefreshAtMillis()
	var decision accountInspectionDecision
	var statusCode *int
	var err error
	switch account.Provider {
	case "antigravity":
		decision, statusCode, err = s.inspectAntigravity(ctx, account, settings)
	case "claude":
		decision, statusCode, err = s.inspectClaude(ctx, account, settings)
	case "codex":
		decision, statusCode, err = s.inspectCodex(ctx, account, settings)
	case "gemini-cli":
		decision, statusCode, err = s.inspectGeminiCLI(ctx, account, settings)
	case "kimi":
		decision, statusCode, err = s.inspectKimi(ctx, account, settings)
	case "xai":
		decision, statusCode, err = s.inspectXAI(ctx, account, settings)
	default:
		result.ActionReason = "暂不支持该 provider 巡检"
		result.Error = "unsupported provider"
		return result
	}
	if err != nil {
		result.StatusCode = statusCode
		result.Error = err.Error()
		result.ErrorCode = proinspection.ErrorCode(statusCode, "inspection_probe_error")
		result.ActionReason = "探测异常，保留账号"
		if statusCode != nil && proinspection.IsAccountErrorStatus(*statusCode) {
			s.syncInspectionAuthStatus(ctx, account, *statusCode)
		} else {
			s.syncInspectionAuthError(ctx, account, "inspection_probe_error", err.Error(), 0)
		}
		s.appendLog("warning", fmt.Sprintf("%s 探测异常，保留账号：%s", account.identity(), err.Error()))
		return result
	}
	result.StatusCode = statusCode
	result.Action = decision.Action
	result.ActionReason = decision.ActionReason
	result.UsedPercent = decision.UsedPercent
	result.IsQuota = decision.IsQuota
	result.Error = decision.Error
	result.ErrorDetail = decision.ErrorDetail
	result.ErrorCode = proinspection.DecisionErrorCode(account.Provider, decision, statusCode)
	if decision.DeepProbeStatus != "" {
		result.DeepProbeTriggered = true
		result.DeepProbeStatus = string(decision.DeepProbeStatus)
		result.DeepProbeError = decision.DeepProbeError
	}
	if decision.IsQuota {
		s.clearInspectionAuthError(ctx, account)
	} else if statusCode != nil && decision.DeepProbeStatus != accountInspectionDeepProbeTransientError {
		s.syncInspectionAuthStatus(ctx, account, *statusCode)
	}
	level := "info"
	if result.Action == accountInspectionActionDisable {
		level = "warning"
	} else if result.Action == accountInspectionActionEnable {
		level = "success"
	} else if result.Action == accountInspectionActionDelete {
		level = "error"
	}
	percent := "--"
	if result.UsedPercent != nil {
		percent = fmt.Sprintf("%.1f%%", *result.UsedPercent)
	}
	s.appendLog(level, fmt.Sprintf("%s -> %s (%s · 已用 %s)", account.identity(), result.Action, account.Provider, percent))
	return result
}

func (s *accountInspectionScheduler) refreshAccountIfDue(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionAccount, bool, error) {
	if account.Auth == nil || account.Auth.ID == "" || s == nil || s.h == nil || s.h.authManager == nil {
		return account, false, nil
	}
	if account.Provider == "antigravity" {
		current, ok := s.h.authManager.GetByID(account.Auth.ID)
		if !ok || current == nil {
			return account, false, coreauth.ErrInspectionAuthChanged
		}
		prepared, refreshed, err := s.prepareAntigravityInspectionAccount(ctx, accountFromAuth(current), settings)
		if err == nil && refreshed {
			s.appendLog("success", fmt.Sprintf("%s 刷新令牌成功", prepared.identity()))
		}
		return prepared, refreshed, err
	}
	updated, refreshed, err := s.h.authManager.RefreshIfDueForInspection(ctx, account.Auth.ID)
	if err != nil {
		return account, true, err
	}
	if updated == nil {
		return account, false, nil
	}
	refreshedAccount := accountFromAuth(updated)
	if refreshed {
		s.appendLog("success", fmt.Sprintf("%s 刷新令牌成功", refreshedAccount.identity()))
	}
	return refreshedAccount, refreshed, nil
}

func (account accountInspectionAccount) nextRefreshAtMillis() int64 {
	if account.Auth == nil || account.Auth.NextRefreshAfter.IsZero() {
		return 0
	}
	return account.Auth.NextRefreshAfter.UnixMilli()
}

func (account accountInspectionAccount) baseResult() accountInspectionResult {
	return accountInspectionResult{
		AuthID:            account.AuthID,
		Key:               account.Key,
		Provider:          account.Provider,
		FileName:          account.FileName,
		DisplayName:       account.DisplayName,
		Email:             account.Email,
		Name:              account.Name,
		AuthIndex:         account.AuthIndex,
		AccessTokenSHA256: account.AccessTokenSHA256,
		Disabled:          account.Disabled,
		Action:            accountInspectionActionKeep,
		ActionReason:      "无需处理",
	}
}

func accountInspectionObservationMatchesAuth(authID, authIndex, provider, fileName, accessTokenSHA256 string, auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	current := accountFromAuth(auth)
	if authID != "" && strings.TrimSpace(authID) != current.AuthID {
		return false
	}
	if authIndex != "" && strings.TrimSpace(authIndex) != current.AuthIndex {
		return false
	}
	if provider != "" && !strings.EqualFold(strings.TrimSpace(provider), current.Provider) {
		return false
	}
	if fileName != "" && strings.TrimSpace(fileName) != current.FileName {
		return false
	}
	if accessTokenSHA256 != "" && strings.TrimSpace(accessTokenSHA256) != current.AccessTokenSHA256 {
		return false
	}
	return true
}

func accountInspectionAccountMatchesAuth(account accountInspectionAccount, auth *coreauth.Auth) bool {
	return accountInspectionObservationMatchesAuth(
		account.AuthID,
		account.AuthIndex,
		account.Provider,
		account.FileName,
		account.AccessTokenSHA256,
		auth,
	)
}

func accountInspectionResultMatchesAuth(result accountInspectionResult, auth *coreauth.Auth) bool {
	return accountInspectionObservationMatchesAuth(
		result.AuthID,
		result.AuthIndex,
		result.Provider,
		result.FileName,
		result.AccessTokenSHA256,
		auth,
	)
}

func formatAccountInspectionIdentity(fileName string, email string, name string, displayName string) string {
	label := firstNonEmptyStringValue(email, name, displayName)
	if label != "" && label != "-" {
		if fileName != "" {
			return fmt.Sprintf("%s[%s]", label, fileName)
		}
		return label
	}
	return fileName
}

func (account accountInspectionAccount) identity() string {
	return formatAccountInspectionIdentity(account.FileName, account.Email, account.Name, account.DisplayName)
}

func isQuotaHTTPStatus(status int) bool {
	return status == http.StatusPaymentRequired || status == http.StatusTooManyRequests
}

func isInspectionAuthRecoveryStatus(status int) bool {
	return (status >= 200 && status < 300) || status == 402 || status == 429
}

func (s *accountInspectionScheduler) syncInspectionAuthError(ctx context.Context, account accountInspectionAccount, code string, message string, status int) {
	if s == nil || s.h == nil || s.h.authManager == nil || account.AuthIndex == "" {
		return
	}
	if !accountInspectionAccountMatchesAuth(account, s.h.authByIndex(account.AuthIndex)) {
		return
	}
	err := s.h.updateProErrorAuth(ctx, account.AuthIndex, func(auth *coreauth.Auth) {
		auth.Status = coreauth.StatusError
		auth.StatusMessage = message
		auth.Unavailable = true
		syncAuthInspectionLastError(auth, &coreauth.Error{Code: code, Message: message, HTTPStatus: status})
		auth.UpdatedAt = time.Now()
	})
	if err != nil {
		s.appendLog("warning", fmt.Sprintf("%s 认证状态回写失败：%s", account.identity(), err.Error()))
	}
}

func (s *accountInspectionScheduler) clearInspectionAuthError(ctx context.Context, account accountInspectionAccount) {
	if s == nil || s.h == nil || s.h.authManager == nil || account.AuthIndex == "" {
		return
	}
	auth := s.h.authByIndex(account.AuthIndex)
	if !accountInspectionAccountMatchesAuth(account, auth) {
		return
	}
	if !isInspectionAuthErrorCode(authInspectionLastErrorCode(auth)) {
		return
	}
	err := s.h.updateProErrorAuth(ctx, account.AuthIndex, func(auth *coreauth.Auth) {
		if auth.Disabled {
			auth.Status = coreauth.StatusDisabled
		} else {
			auth.Status = coreauth.StatusActive
		}
		auth.StatusMessage = ""
		auth.Unavailable = false
		syncAuthInspectionLastError(auth, nil)
		auth.UpdatedAt = time.Now()
	})
	if err != nil {
		s.appendLog("warning", fmt.Sprintf("%s 认证状态清理失败：%s", account.identity(), err.Error()))
	}
}

func (s *accountInspectionScheduler) syncInspectionAuthStatus(ctx context.Context, account accountInspectionAccount, status int) {
	if proinspection.IsAccountErrorStatus(status) {
		message := fmt.Sprintf("HTTP %d", status)
		s.syncInspectionAuthError(ctx, account, "inspection_http_error", message, status)
		return
	}
	if isInspectionAuthRecoveryStatus(status) {
		s.clearInspectionAuthError(ctx, account)
	}
}

func authErrorDecision(account accountInspectionAccount, status int) accountInspectionDecision {
	return proinspection.AuthErrorDecision(account.Disabled, status)
}

func healthyDecision(account accountInspectionAccount) accountInspectionDecision {
	return proinspection.HealthyDecision(account.Disabled)
}

func quotaDecision(account accountInspectionAccount, used *float64, hasQuotaData bool, threshold float64) accountInspectionDecision {
	return proinspection.QuotaDecision(account.Disabled, used, hasQuotaData, threshold)
}

func quotaUnavailableDecision(account accountInspectionAccount, reason string, body string) accountInspectionDecision {
	return proinspection.QuotaUnavailableDecision(account.Disabled, reason, proinspection.HTTPErrorDetail(body))
}

func codexDecision(account accountInspectionAccount, status int, used *float64, isQuota bool, threshold float64) accountInspectionDecision {
	return proinspection.CodexDecision(account.Disabled, status, used, isQuota, threshold)
}

func (s *accountInspectionScheduler) bindActionItemToSnapshot(item accountInspectionActionItem) (accountInspectionActionItem, error) {
	if s == nil {
		return accountInspectionActionItem{}, fmt.Errorf("account inspection scheduler unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, result := range s.status.Results {
		if item.Key != "" {
			if result.Key != item.Key {
				continue
			}
		} else if result.FileName != item.FileName || result.AuthIndex != item.AuthIndex {
			continue
		}
		return proinspection.ActionItemFromResult(result, item.Action), nil
	}
	return accountInspectionActionItem{}, errAccountInspectionResultStale
}

func (s *accountInspectionScheduler) removeInspectionResultLocked(result accountInspectionResult) bool {
	for index, current := range s.status.Results {
		if !proinspection.SameResult(current, result) {
			continue
		}
		s.status.Summary = proinspection.AdjustSummaryForResult(s.status.Summary, current, -1)
		s.healthCounts = proinspection.AdjustHealthCountsForResult(s.healthCounts, current, -1)
		s.status.Results = append(s.status.Results[:index], s.status.Results[index+1:]...)
		return true
	}
	return false
}

func (s *accountInspectionScheduler) applyManualActionResultLocked(result accountInspectionResult) {
	if result.Key == "" {
		result.Key = proinspection.AccountKey(result.FileName, result.AuthIndex)
	}
	if result.Executed && result.Action == accountInspectionActionDelete {
		s.removeInspectionResultLocked(result)
		return
	}
	s.updateInspectionResultLocked(result, true, func(current accountInspectionResult) (accountInspectionResult, bool) {
		return proinspection.MergeManualActionResult(current, result)
	})
}

func (s *accountInspectionScheduler) executeManualActions(ctx context.Context, items []accountInspectionActionItem) ([]accountInspectionActionOutcome, error) {
	release, err := s.beginLifecycle()
	if err != nil {
		return nil, err
	}
	defer release()
	s.mu.Lock()
	restoredSnapshot := s.status.RestoredSnapshot
	running := s.isRunningLocked()
	s.mu.Unlock()
	if restoredSnapshot {
		return nil, errAccountInspectionRestoredSnapshotReadOnly
	}
	if running {
		return nil, errAccountInspectionAlreadyRunning
	}
	s.fullRunMu.RLock()
	defer s.fullRunMu.RUnlock()
	s.mu.Lock()
	restoredSnapshot = s.status.RestoredSnapshot
	running = s.isRunningLocked()
	workers := s.schedule.Settings.DeleteWorkers
	s.mu.Unlock()
	if restoredSnapshot {
		return nil, errAccountInspectionRestoredSnapshotReadOnly
	}
	if running {
		return nil, errAccountInspectionAlreadyRunning
	}
	boundItems := make([]accountInspectionActionItem, 0, len(items))
	for _, item := range items {
		if item.Action == accountInspectionActionNone || item.Action == accountInspectionActionKeep || item.Action == "" {
			continue
		}
		boundItem, err := s.bindActionItemToSnapshot(item)
		if err != nil {
			return nil, err
		}
		boundItems = append(boundItems, boundItem)
	}
	executableItems := proinspection.DedupeActionItems(boundItems)
	outcomes := make([]accountInspectionActionOutcome, len(executableItems))
	executedResults := make([]accountInspectionResult, len(executableItems))
	if workers <= 0 {
		workers = proinspection.DefaultSettings().DeleteWorkers
	}
	runAccountInspectionWorkers(len(executableItems), workers, nil, func(index int) bool {
		item := executableItems[index]
		result := item.ToResult()
		action := item.Action
		outcome := accountInspectionActionOutcome{Action: action, FileName: item.FileName, DisplayName: item.DisplayName, Email: item.Email, Name: item.Name, Provider: item.Provider, AuthIndex: item.AuthIndex}
		if err := s.executeActionWithLimit(ctx, result, action, workers); err != nil {
			outcome.Error = err.Error()
			result.ExecuteError = err.Error()
			s.appendLog("error", fmt.Sprintf("%s -> %s 执行失败：%s", proinspection.ResultIdentity(result), action, err.Error()))
		} else {
			outcome.Success = true
			result.Executed = true
			result.ExecuteError = ""
			if action == accountInspectionActionDisable {
				result.Disabled = true
			}
			if action == accountInspectionActionEnable {
				result.Disabled = false
			}
			s.appendLog("success", fmt.Sprintf("%s %s 成功", proinspection.ResultIdentity(result), action))
		}
		outcomes[index] = outcome
		executedResults[index] = result
		return true
	})

	s.mu.Lock()
	for _, result := range executedResults {
		if result.FileName == "" {
			continue
		}
		s.applyManualActionResultLocked(result)
	}
	s.status.Results = proinspection.SortResults(s.status.Results)
	saveErr := s.saveResultSnapshotLocked()
	broadcast := s.statusBroadcastLocked()
	s.mu.Unlock()
	broadcast.send()
	if saveErr != nil {
		return outcomes, fmt.Errorf("failed to save account inspection snapshot: %w", saveErr)
	}
	return outcomes, nil
}

func (s *accountInspectionScheduler) applyAutomaticActions(ctx context.Context, results []accountInspectionResult, settings accountInspectionSettings) {
	workers := settings.DeleteWorkers
	if workers <= 0 {
		workers = settings.Workers
	}
	deletedFiles := make(map[string]struct{})
	var mu sync.Mutex
	runAccountInspectionWorkers(len(results), workers, nil, func(index int) bool {
		action := proinspection.AutoActionForResult(results[index], settings)
		if action == accountInspectionActionNone || action == "" {
			s.clearAutoActionConfirmation(results[index])
			return true
		}
		confirmed, count, required := s.confirmAutoAction(results[index], action, settings.AutoExecuteConfirmations)
		if !confirmed {
			if results[index].ActionReason != "" {
				results[index].ActionReason += fmt.Sprintf("；等待连续确认 %d/%d 后自动执行", count, required)
			}
			s.appendLog("info", fmt.Sprintf("%s -> %s 等待连续确认 %d/%d", proinspection.ResultIdentity(results[index]), action, count, required))
			return true
		}
		if action == accountInspectionActionDelete {
			mu.Lock()
			if _, ok := deletedFiles[results[index].FileName]; ok {
				results[index].ExecuteError = "auth file already deleted in this inspection run"
				mu.Unlock()
				return true
			}
			deletedFiles[results[index].FileName] = struct{}{}
			mu.Unlock()
		}
		err := s.executeActionWithLimit(ctx, results[index], action, workers)
		mu.Lock()
		if err != nil {
			results[index].ExecuteError = err.Error()
			s.appendLog("error", fmt.Sprintf("%s -> %s 执行失败：%s", proinspection.ResultIdentity(results[index]), action, err.Error()))
		} else {
			results[index].Executed = true
			results[index].Action = action
			s.clearAutoActionConfirmation(results[index])
			if action == accountInspectionActionDisable {
				results[index].Disabled = true
			}
			if action == accountInspectionActionEnable {
				results[index].Disabled = false
			}
			s.appendLog("success", fmt.Sprintf("%s %s 成功", proinspection.ResultIdentity(results[index]), action))
		}
		mu.Unlock()
		return true
	})
}

func (s *accountInspectionScheduler) confirmAutoAction(result accountInspectionResult, action accountInspectionAction, required int) (bool, int, int) {
	key := proinspection.AutoActionConfirmationKey(result, action)
	if s == nil {
		return true, 1, required
	}
	s.mu.Lock()
	if s.autoActionConfirmations == nil {
		s.autoActionConfirmations = proinspection.NewConfirmationCounter()
	}
	confirmations := s.autoActionConfirmations
	s.mu.Unlock()
	return confirmations.Confirm(key, required)
}

func (s *accountInspectionScheduler) clearAutoActionConfirmation(result accountInspectionResult) {
	keyPrefix := result.Key
	if keyPrefix == "" {
		keyPrefix = result.FileName + ":" + result.AuthIndex
	}
	if keyPrefix == "" {
		return
	}
	if s != nil {
		s.mu.Lock()
		confirmations := s.autoActionConfirmations
		s.mu.Unlock()
		if confirmations != nil {
			confirmations.ClearPrefix(keyPrefix + "|")
		}
	}
}

func (s *accountInspectionScheduler) executeAction(ctx context.Context, result accountInspectionResult, action accountInspectionAction) error {
	if s.h == nil || s.h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	auth, err := s.actionAuthForResult(result)
	if err != nil {
		return err
	}
	switch action {
	case accountInspectionActionDisable, accountInspectionActionEnable:
		return s.h.updateProAuth(ctx, result.AuthIndex, func(auth *coreauth.Auth) {
			setProAuthDisabledState(auth, action == accountInspectionActionDisable)
		})
	case accountInspectionActionDelete:
		if s.pluginVirtualSourceAuthCount(auth) > 1 {
			return errAccountInspectionSharedSourceDelete
		}
		_, _, err := s.h.deleteAuthFileByName(ctx, accountFromAuth(auth).FileName)
		return err
	default:
		return fmt.Errorf("unsupported action %s", action)
	}
}

func (s *accountInspectionScheduler) executeActionWithLimit(ctx context.Context, result accountInspectionResult, action accountInspectionAction, workers int) error {
	auth, err := s.actionAuthForResult(result)
	if err != nil {
		return err
	}
	key := strings.TrimSpace(auth.ID)
	if sourcePath := pluginVirtualSourcePath(auth); sourcePath != "" {
		key = "source:" + sourcePath
	}
	release, err := s.actionLimiter.Acquire(ctx, workers, 1, key)
	if err != nil {
		return err
	}
	defer release()
	return s.executeAction(ctx, result, action)
}

func (s *accountInspectionScheduler) actionAuthForResult(result accountInspectionResult) (*coreauth.Auth, error) {
	if s == nil || s.h == nil || s.h.authManager == nil || strings.TrimSpace(result.AuthIndex) == "" {
		return nil, errAccountInspectionResultStale
	}
	auth := s.h.authByIndex(result.AuthIndex)
	if !accountInspectionResultMatchesAuth(result, auth) {
		return nil, errAccountInspectionResultStale
	}
	account := accountFromAuth(auth)
	if result.Key != "" && result.Key != account.Key {
		return nil, errAccountInspectionResultStale
	}
	return auth, nil
}

func (s *accountInspectionScheduler) pluginVirtualSourceAuthCount(auth *coreauth.Auth) int {
	if s == nil || s.h == nil || s.h.authManager == nil || auth == nil || !coreauth.IsPluginVirtualAuth(auth) {
		return 0
	}
	sourcePath := pluginVirtualSourcePath(auth)
	if sourcePath == "" {
		return 0
	}
	count := 0
	for _, candidate := range s.h.authManager.List() {
		if candidate != nil && coreauth.IsPluginVirtualAuth(candidate) && sameAuthSourcePath(pluginVirtualSourcePath(candidate), sourcePath) {
			count++
		}
	}
	return count
}

func summarizeAccountInspection(totalFiles int, probeSetCount int, accounts []accountInspectionAccount, results []accountInspectionResult) accountInspectionSummary {
	disabledCount := 0
	for _, account := range accounts {
		if account.Disabled {
			disabledCount++
		}
	}
	return proinspection.SummarizeResults(totalFiles, probeSetCount, disabledCount, len(accounts)-disabledCount, results)
}
