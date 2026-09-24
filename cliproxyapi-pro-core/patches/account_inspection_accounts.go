package management

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	prorouting "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/routing"
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
	queuedAt := time.Now()
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
			accountCtx, finishMetrics := inspectionMetricsStartAccount(ctx, account.Provider, queuedAt)
			results[index] = s.inspectAccount(accountCtx, account, settings)
			finishMetrics(results[index])
			s.mu.Lock()
			results[index].RunID = s.runID
			s.mu.Unlock()
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
	manager := s.inspectionAuthManager()
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
		Auth:                  auth,
		AuthID:                strings.TrimSpace(auth.ID),
		Key:                   proinspection.AccountKey(fileName, auth.Index),
		Provider:              provider,
		FileName:              fileName,
		DisplayName:           displayName,
		Email:                 email,
		Name:                  name,
		AuthIndex:             auth.Index,
		AccessTokenSHA256:     coreauth.AccessTokenSHA256(auth),
		CredentialFingerprint: accountInspectionCredentialFingerprint(auth),
		Disabled:              auth.Disabled,
	}
}

func accountInspectionCredentialFingerprint(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	if auth.RegistrationEpoch > 0 {
		return fmt.Sprintf("registration:%d", auth.RegistrationEpoch)
	}
	// Registered runtime auths always have a registration epoch. Keep a
	// credential-material fallback for isolated or legacy auth objects so the
	// confirmation boundary still fails closed instead of becoming unbound.
	digest := sha256.New()
	for _, key := range []string{"access_token", "accessToken", "token", "Token", "refresh_token", "refreshToken", "id_token", "idToken", "session_id"} {
		value, ok := auth.Metadata[key]
		if !ok {
			continue
		}
		raw, err := json.Marshal(value)
		if err != nil {
			raw = []byte(fmt.Sprint(value))
		}
		_, _ = fmt.Fprintf(digest, "%s:%d:", key, len(raw))
		_, _ = digest.Write(raw)
		_, _ = digest.Write([]byte{'\n'})
	}
	return "credential:" + hex.EncodeToString(digest.Sum(nil))
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
	ctx = s.withPreviousInspection(ctx, account)
	result := s.inspectAccountObserved(ctx, account, settings)
	result.ObservedAt = time.Now().UnixMilli()
	return result
}

func (s *accountInspectionScheduler) inspectAccountObserved(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) accountInspectionResult {
	observedCredentialFingerprint := account.CredentialFingerprint
	result := account.baseResult()
	result.ObservedSettings = settings
	if account.AuthIndex == "" {
		result.ActionReason = "缺少 auth_index，保留账号"
		result.Error = "missing auth_index"
		result.ErrorCode = "missing_auth_index"
		return result
	}
	stopRefresh := inspectionMetricsStartRefresh(ctx)
	refreshed, refreshTriggered, refreshErr := s.refreshAccountIfDue(ctx, account, settings)
	stopRefresh()
	if refreshErr != nil {
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
		s.appendLog("warning", fmt.Sprintf("%s 刷新令牌失败，保留账号：%s", account.identity(), refreshErr.Error()))
		return result
	} else if refreshTriggered {
		account = refreshed
		result = account.baseResult()
		result.ObservedSettings = settings
		result.TokenRefreshTriggered = true
		result.TokenRefreshStatus = "success"
	} else if refreshed.Auth != nil {
		account = refreshed
		result = account.baseResult()
		result.ObservedSettings = settings
	}
	result.CredentialObserved = observedCredentialFingerprint
	result.NextRefreshAt = account.nextRefreshAtMillis()
	s.fillQuotaProtectionResult(account.Auth, &result)
	var decision accountInspectionDecision
	var statusCode *int
	var err error
	stopPrimary := inspectionMetricsStartPrimary(ctx)
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
		stopPrimary()
		result.ActionReason = "暂不支持该 provider 巡检"
		result.Error = "unsupported provider"
		return result
	}
	stopPrimary()
	if err != nil {
		result.StatusCode = statusCode
		result.Error = err.Error()
		result.ErrorCode = proinspection.ErrorCode(statusCode, "inspection_probe_error")
		result.ActionReason = "探测异常，保留账号"
		s.appendLog("warning", fmt.Sprintf("%s 探测异常，保留账号：%s", account.identity(), err.Error()))
		return result
	}
	result.StatusCode = statusCode
	if statusCode != nil && (*statusCode == http.StatusBadRequest || *statusCode == http.StatusNotFound) && !decision.IsQuota {
		decision.Action = accountInspectionActionKeep
		decision.ActionReason = fmt.Sprintf("接口返回 %d，无法判断账号状态", *statusCode)
		if decision.Error == "" {
			decision.Error = fmt.Sprintf("HTTP %d", *statusCode)
		}
	}
	result.Action = decision.Action
	result.ActionReason = decision.ActionReason
	result.UsedPercent = decision.UsedPercent
	result.IsQuota = decision.IsQuota
	result.QuotaResetAt = decision.QuotaResetAt
	result.QuotaModel = decision.QuotaModel
	result.QuotaKnown = decision.QuotaKnown || decision.UsedPercent != nil
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
	if result.QuotaCooling && !result.Disabled && result.Error == "" && result.ErrorCode == "" && result.UsedPercent != nil && proinspection.QuotaRecovered(*result.UsedPercent, settings.UsedPercentThreshold) {
		result.Action = accountInspectionActionEnable
		result.ActionReason = "额度恢复，建议解除额度保护"
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
	quotaAction := result.IsQuota && result.Action == accountInspectionActionDisable || result.QuotaCooling && result.Action == accountInspectionActionEnable
	s.appendLog(level, fmt.Sprintf("%s -> %s (%s · 已用 %s)", account.identity(), accountInspectionActionLogLabel(result.Action, quotaAction), account.Provider, percent))
	return result
}

func (s *accountInspectionScheduler) refreshAccountIfDue(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionAccount, bool, error) {
	if account.Auth == nil || account.Auth.ID == "" || s == nil || s.h == nil || s.inspectionAuthManager() == nil {
		return account, false, nil
	}
	if account.Provider == "antigravity" {
		current, ok := s.inspectionAuthManager().GetByID(account.Auth.ID)
		if !ok || current == nil {
			return account, false, coreauth.ErrInspectionAuthChanged
		}
		prepared, refreshed, err := s.prepareAntigravityInspectionAccount(ctx, accountFromAuth(current), settings)
		if err == nil && refreshed {
			s.appendLog("success", fmt.Sprintf("%s 刷新令牌成功", prepared.identity()))
		}
		return prepared, refreshed, err
	}
	updated, refreshed, err := s.inspectionAuthManager().RefreshIfDueForInspection(ctx, account.Auth.ID)
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
	var ref [16]byte
	// An empty ref fails closed at the action boundary if the random source fails.
	_, refErr := rand.Read(ref[:])
	resultRef := ""
	if refErr == nil {
		resultRef = hex.EncodeToString(ref[:])
	}
	epoch := ""
	if account.Auth != nil {
		epoch = strconv.FormatUint(account.Auth.RegistrationEpoch, 10)
	}
	return accountInspectionResult{
		RegistrationEpoch:  epoch,
		AuthID:             account.AuthID,
		Key:                account.Key,
		ResultRef:          resultRef,
		Provider:           account.Provider,
		FileName:           account.FileName,
		DisplayName:        account.DisplayName,
		Email:              account.Email,
		Name:               account.Name,
		AuthIndex:          account.AuthIndex,
		AccessTokenSHA256:  account.AccessTokenSHA256,
		CredentialObserved: account.CredentialFingerprint,
		CredentialFinal:    account.CredentialFingerprint,
		Disabled:           account.Disabled,
		Action:             accountInspectionActionKeep,
		ActionReason:       "无需处理",
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
	return status == http.StatusPaymentRequired
}

func isInspectionAuthRecoveryStatus(status int) bool {
	return (status >= 200 && status < 300) || status == http.StatusPaymentRequired
}

func (s *accountInspectionScheduler) clearInspectionAuthError(ctx context.Context, account accountInspectionAccount) {
	if s == nil || s.h == nil || s.inspectionAuthManager() == nil || account.AuthIndex == "" {
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

func rateLimitedDecision(body string) accountInspectionDecision {
	return accountInspectionDecision{
		Action:       accountInspectionActionKeep,
		ActionReason: "巡检接口暂时限流，保留账号",
		Error:        "HTTP 429",
		ErrorDetail:  proinspection.HTTPErrorDetail(body),
	}
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
		if item.ResultRef != "" && (result.ResultRef == "" || result.ResultRef != item.ResultRef) {
			return accountInspectionActionItem{}, errAccountInspectionResultStale
		}
		if item.Suggested && (result.Action != item.Action || result.Executed) {
			return accountInspectionActionItem{}, errAccountInspectionResultStale
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
		s.archiveInspectionResultLocked(current)
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
	s.updateInspectionResultLocked(result, true, func(current accountInspectionResult) (accountInspectionResult, bool) {
		return proinspection.MergeManualActionResult(current, result)
	})
}

func (s *accountInspectionScheduler) executeManualActions(ctx context.Context, items []accountInspectionActionItem) ([]accountInspectionActionOutcome, error) {
	return s.executeManualActionsWithExpected(ctx, items, nil)
}

func (s *accountInspectionScheduler) executeManualActionsWithExpected(ctx context.Context, items []accountInspectionActionItem, expected *accountInspectionResult) ([]accountInspectionActionOutcome, error) {
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
	s.manualActionMu.Lock()
	defer s.manualActionMu.Unlock()
	s.mu.Lock()
	restoredSnapshot = s.status.RestoredSnapshot
	running = s.isRunningLocked()
	workers := s.schedule.Settings.DeleteWorkers
	settings := s.lastRunSettings
	if strings.TrimSpace(settings.TargetType) == "" {
		settings = s.schedule.Settings
	}
	s.mu.Unlock()
	if restoredSnapshot {
		return nil, errAccountInspectionRestoredSnapshotReadOnly
	}
	if running {
		return nil, errAccountInspectionAlreadyRunning
	}
	// This check shares manualActionMu with every direct manual action. A newer
	// decision cannot slip between the batch's processing-state check and its
	// destructive mutation.
	if expected != nil {
		if err := s.inspectionBatchProcessingStateUnchanged(expected); err != nil {
			return nil, err
		}
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
		boundItems[len(boundItems)-1].Suggested = item.Suggested
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
		actionSettings := settings
		if item.Suggested && strings.TrimSpace(item.ObservedSettings.TargetType) != "" {
			actionSettings = item.ObservedSettings
		}
		result.OperationAction = action
		quotaAction := item.Suggested && (result.IsQuota && action == accountInspectionActionDisable || result.QuotaCooling && action == accountInspectionActionEnable)
		outcome := accountInspectionActionOutcome{Action: action, FileName: item.FileName, DisplayName: item.DisplayName, Email: item.Email, Name: item.Name, Provider: item.Provider, AuthIndex: item.AuthIndex}
		if err := s.executeRecordedInspectionAction(ctx, &result, actionSettings, action, item.Suggested, workers, "manual"); err != nil {
			outcome.Error = err.Error()
			result.ExecuteError = err.Error()
			s.appendLog("error", fmt.Sprintf("%s -> %s 执行失败：%s", proinspection.ResultIdentity(result), accountInspectionActionLogLabel(action, quotaAction), err.Error()))
		} else {
			outcome.Success = true
			result.Executed = true
			result.ExecutedAction = action
			result.ExecutedEffect = proinspection.EffectForAction(action, quotaAction)
			result.ExecutedAt = time.Now().UnixMilli()
			result.ExecutedSuggested = item.Suggested
			result.ExecuteError = ""
			if action == accountInspectionActionDisable && !quotaAction {
				result.Disabled = true
			}
			if action == accountInspectionActionEnable {
				result.Disabled = false
			}
			s.appendLog("success", fmt.Sprintf("%s %s 成功", proinspection.ResultIdentity(result), accountInspectionActionLogLabel(action, quotaAction)))
		}
		if current := s.h.authByIndex(result.AuthIndex); current != nil {
			s.fillQuotaProtectionResult(current, &result)
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
		quotaAction := results[index].IsQuota && action == accountInspectionActionDisable || results[index].QuotaCooling && action == accountInspectionActionEnable
		label := accountInspectionActionLogLabel(action, quotaAction)
		confirmed, count, required := s.confirmAutoAction(results[index], action, settings.AutoExecuteConfirmations)
		if !confirmed {
			if results[index].ActionReason != "" {
				results[index].ActionReason += fmt.Sprintf("；等待连续确认 %d/%d 后自动执行", count, required)
			}
			s.appendLog("info", fmt.Sprintf("%s -> %s 等待连续确认 %d/%d", proinspection.ResultIdentity(results[index]), label, count, required))
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
		results[index].OperationAction = action
		err := s.executeRecordedInspectionAction(ctx, &results[index], settings, action, true, workers, "automatic")
		mu.Lock()
		if err != nil {
			results[index].ExecuteError = err.Error()
			s.appendLog("error", fmt.Sprintf("%s -> %s 执行失败：%s", proinspection.ResultIdentity(results[index]), label, err.Error()))
		} else {
			results[index].Executed = true
			results[index].ExecutedAction = action
			results[index].ExecutedEffect = proinspection.EffectForAction(action, quotaAction)
			results[index].ExecutedAt = time.Now().UnixMilli()
			results[index].ExecutedSuggested = true
			s.clearAutoActionConfirmation(results[index])
			if action == accountInspectionActionDisable && !quotaAction {
				results[index].Disabled = true
			}
			if action == accountInspectionActionEnable {
				results[index].Disabled = false
			}
			s.appendLog("success", fmt.Sprintf("%s %s 成功", proinspection.ResultIdentity(results[index]), label))
		}
		mu.Unlock()
		return true
	})
}

func accountInspectionActionLogLabel(action accountInspectionAction, quotaAction bool) string {
	if quotaAction {
		if action == accountInspectionActionDisable {
			return "建立额度保护"
		}
		if action == accountInspectionActionEnable {
			return "检查并恢复"
		}
	}
	switch action {
	case accountInspectionActionDisable:
		return "禁用账号"
	case accountInspectionActionEnable:
		return "启用账号"
	case accountInspectionActionDelete:
		return "删除账号"
	case accountInspectionActionKeep:
		return "保留账号"
	default:
		return string(action)
	}
}

// Suggested actions share the automatic path, including versioned quota holds.
// Explicit administrative overrides retain the ordinary auth mutation path.
func (s *accountInspectionScheduler) executeResolvedAction(ctx context.Context, result *accountInspectionResult, settings accountInspectionSettings, action accountInspectionAction, suggested bool, workers int) error {
	if suggested && (result.IsQuota && action == accountInspectionActionDisable || result.QuotaCooling && action == accountInspectionActionEnable) {
		return s.executeQuotaProtection(ctx, result, settings, action)
	}
	return s.executeActionWithLimit(ctx, *result, action, workers)
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
	return confirmations.ConfirmWithFingerprint(
		key,
		result.CredentialObserved,
		result.CredentialFinal,
		required,
	)
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
	if s.h == nil || s.inspectionAuthManager() == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	auth, err := s.actionAuthForResult(result)
	if err != nil {
		return err
	}
	switch action {
	case accountInspectionActionDisable, accountInspectionActionEnable:
		if action == accountInspectionActionEnable && !auth.Disabled {
			if _, ok := prorouting.QuotaProtections(auth.Metadata)[inspectionQuotaSource]; ok {
				return fmt.Errorf("account has an inspection quota protection; use the versioned routing release action")
			}
		}
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
	if s == nil || s.h == nil || s.inspectionAuthManager() == nil || strings.TrimSpace(result.AuthIndex) == "" {
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
	if s == nil || s.h == nil || s.inspectionAuthManager() == nil || auth == nil || !coreauth.IsPluginVirtualAuth(auth) {
		return 0
	}
	sourcePath := pluginVirtualSourcePath(auth)
	if sourcePath == "" {
		return 0
	}
	count := 0
	for _, candidate := range s.inspectionAuthManager().List() {
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
