package management

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/misc"
	upstreamexecutor "github.com/router-for-me/CLIProxyAPI/v6/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
)

type accountInspectionHTTPResult struct {
	StatusCode int
	Body       string
	Header     http.Header
}

func (r accountInspectionHTTPResult) probeResponse() proinspection.ProbeResponse {
	return proinspection.ProbeResponse{StatusCode: r.StatusCode, Body: r.Body}
}

func intPtr(value int) *int {
	return &value
}

func (s *accountInspectionScheduler) prepareAntigravityInspectionAccount(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionAccount, bool, error) {
	if coreauth.InspectionAccessToken(account.Auth) != "" && !antigravityTokenNeedsRefresh(account.Auth.Metadata) {
		return account, false, nil
	}
	if ctx == nil {
		ctx = context.Background()
	}
	timeout := settings.Timeout
	if timeout <= 0 {
		timeout = accountInspectionDefaultTimeoutMS
	}
	// Reuse upstream OAuth parsing and proxy selection without its unconditional
	// manager.Update. Each attempt modifies only a private copy of the observation.
	resolver := &Handler{cfg: s.h.cfg}
	var updated *coreauth.Auth
	_, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout)*time.Millisecond)
		defer cancel()
		candidate := account.Auth.Clone()
		token, err := resolver.resolveTokenForAuth(attemptCtx, candidate, "")
		if err == nil {
			candidate.Metadata["access_token"] = strings.TrimSpace(token)
			updated = candidate
		}
		return accountInspectionHTTPResult{}, err
	})
	if err != nil {
		return account, true, err
	}
	updated.LastRefreshedAt = time.Now()
	updated.NextRefreshAfter = time.Time{}
	saved, err := s.h.authManager.CommitInspectionRefresh(ctx, account.Auth, updated)
	if err != nil {
		return account, true, err
	}
	return accountFromAuth(saved), true, nil
}

func (s *accountInspectionScheduler) apiCall(ctx context.Context, auth *coreauth.Auth, method string, url string, headers map[string]string, data string, timeoutMS int) (accountInspectionHTTPResult, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	if timeoutMS <= 0 {
		timeoutMS = accountInspectionDefaultTimeoutMS
	}
	var body io.Reader
	if data != "" {
		body = bytes.NewBufferString(data)
	}
	reqCtx, cancel := context.WithTimeout(ctx, time.Duration(timeoutMS)*time.Millisecond)
	defer cancel()
	req, err := http.NewRequestWithContext(reqCtx, method, url, body)
	if err != nil {
		return accountInspectionHTTPResult{}, err
	}
	resolvedHeaders := make(map[string]string, len(headers))
	var token string
	var tokenResolved bool
	for key, value := range headers {
		if strings.Contains(value, "$TOKEN$") {
			if !tokenResolved {
				// Use the same token as the observation fingerprint for every OAuth
				// provider. Preparation owns refresh; probes must not rotate it.
				token = coreauth.InspectionAccessToken(auth)
				if token == "" {
					// Preserve API-key and legacy non-OAuth token formats.
					token = tokenValueForAuth(auth)
				}
				tokenResolved = true
			}
			value = strings.ReplaceAll(value, "$TOKEN$", token)
		}
		resolvedHeaders[key] = value
	}
	for key, value := range resolvedHeaders {
		req.Header.Set(key, value)
	}
	if accountInspectionShouldUseExecutorHTTPRequest(auth) {
		if s == nil || s.h == nil || s.h.authManager == nil {
			return accountInspectionHTTPResult{}, fmt.Errorf("core auth manager unavailable")
		}
		resp, err := s.h.authManager.HttpRequest(reqCtx, auth, req)
		if err != nil {
			return accountInspectionHTTPResult{}, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		return accountInspectionHTTPResult{StatusCode: resp.StatusCode, Body: string(raw), Header: resp.Header.Clone()}, nil
	}
	client := &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond, Transport: s.h.apiCallTransport(auth)}
	resp, err := client.Do(req)
	if err != nil {
		return accountInspectionHTTPResult{}, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
	return accountInspectionHTTPResult{StatusCode: resp.StatusCode, Body: string(raw), Header: resp.Header.Clone()}, nil
}

func accountInspectionShouldUseExecutorHTTPRequest(auth *coreauth.Auth) bool {
	if auth == nil {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(auth.Provider)) {
	case "gemini-cli", "xai":
		return true
	default:
		return false
	}
}

func (s *accountInspectionScheduler) withRetry(ctx context.Context, retries int, task func() (accountInspectionHTTPResult, error)) (accountInspectionHTTPResult, error) {
	var last accountInspectionHTTPResult
	var err error
	for i := 0; i <= retries; i++ {
		last, err = task()
		if err == nil {
			return last, nil
		}
		select {
		case <-ctx.Done():
			return last, ctx.Err()
		default:
		}
	}
	return last, err
}

func (s *accountInspectionScheduler) inspectAntigravity(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	projectID := antigravityProjectID(account.Auth)
	body := `{"project":"` + escapeJSONString(projectID) + `"}`
	urls := antigravityQuotaURLs()
	var priorityStatus *int
	var priorityDetail string
	for _, url := range urls {
		resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
			return s.apiCall(ctx, account.Auth, http.MethodPost, url, map[string]string{
				"Authorization": "Bearer $TOKEN$",
				"Content-Type":  "application/json",
				"User-Agent":    s.antigravityUserAgent(),
			}, body, settings.Timeout)
		})
		if err != nil {
			continue
		}
		status := intPtr(resp.StatusCode)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if isQuotaHTTPStatus(resp.StatusCode) || proinspection.IsAntigravityQuotaFailure(resp.Body) {
				return quotaUnavailableDecision(account, "Antigravity 额度不可用，建议禁用账号", resp.Body), status, nil
			}
			if proinspection.IsAccountErrorStatus(resp.StatusCode) {
				priorityStatus = status
				priorityDetail = resp.Body
			}
			continue
		}
		groups, err := proinspection.BuildAntigravityGroups(resp.Body)
		if err != nil {
			continue
		}
		quotaState := map[string]any{"groups": groups, "rawShapeHash": proquota.JSONShapeHash(resp.Body)}
		if subscription := s.fetchAntigravitySubscription(ctx, account, settings); subscription != nil {
			quotaState["subscription"] = subscription
			if plan := stringFromAny(subscription["plan"]); plan != "" {
				quotaState["plan"] = plan
				quotaState["planType"] = plan
			}
		}
		s.persistQuotaState(ctx, account, quotaSuccessState(quotaState))
		used := proinspection.AntigravityUsedPercent(groups, settings.AntigravityQuotaMode)
		decision := quotaDecision(account, used, used != nil, settings.UsedPercentThreshold)
		if settings.AntigravityDeepProbeEnabled && proinspection.ShouldAntigravityDeepProbe(decision) {
			return s.applyAntigravityDeepProbe(ctx, account, settings, decision, status)
		}
		return decision, status, nil
	}
	if priorityStatus != nil {
		return proinspection.WithHTTPErrorDetail(authErrorDecision(account, *priorityStatus), priorityDetail), priorityStatus, nil
	}
	return accountInspectionDecision{}, priorityStatus, fmt.Errorf("antigravity quota unavailable")
}

func antigravityQuotaURLs() []string {
	return []string{
		"https://daily-cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
		"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:retrieveUserQuotaSummary",
		"https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuotaSummary",
	}
}

func antigravityGenerateURLs() []string {
	return []string{
		"https://daily-cloudcode-pa.googleapis.com/v1internal:generateContent",
		"https://daily-cloudcode-pa.sandbox.googleapis.com/v1internal:generateContent",
		"https://cloudcode-pa.googleapis.com/v1internal:generateContent",
	}
}

func (s *accountInspectionScheduler) fetchAntigravitySubscription(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) map[string]any {
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodPost, antigravityCodeAssistURL, map[string]string{
			"Authorization": "Bearer $TOKEN$",
			"Content-Type":  "application/json",
			"User-Agent":    s.antigravityUserAgent(),
		}, `{"metadata":{"ideType":"ANTIGRAVITY"}}`, settings.Timeout)
	})
	if err != nil || resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil
	}
	payload, err := proinspection.ParseAntigravityQuotaPayload(resp.Body)
	if err != nil {
		return nil
	}
	return proinspection.BuildAntigravitySubscription(payload)
}

func (s *accountInspectionScheduler) applyAntigravityDeepProbe(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, decision accountInspectionDecision, quotaStatus *int) (accountInspectionDecision, *int, error) {
	model := proinspection.SelectAntigravityDeepProbeModel(settings.AntigravityDeepProbeModel)
	projectID := antigravityProjectID(account.Auth)
	if model == "" || projectID == "" {
		decision.DeepProbeStatus = accountInspectionDeepProbeSkipped
		if model == "" {
			decision.DeepProbeError = "no available Claude/GPT model for deep probe"
		} else {
			decision.DeepProbeError = "missing Antigravity project id"
		}
		s.appendLog("warning", fmt.Sprintf("%s Antigravity 深度检测跳过：%s", account.identity(), decision.DeepProbeError))
		return decision, quotaStatus, nil
	}

	s.appendLog("info", fmt.Sprintf("%s Antigravity 深度检测开始：%s", account.identity(), model))
	body := proinspection.BuildAntigravityDeepProbeBody(projectID, model)
	var lastStatus *int
	var lastMessage string
	var lastDetail string
endpointLoop:
	for _, url := range antigravityGenerateURLs() {
		resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
			return s.apiCall(ctx, account.Auth, http.MethodPost, url, map[string]string{
				"Authorization": "Bearer $TOKEN$",
				"Content-Type":  "application/json",
				"User-Agent":    s.antigravityUserAgent(),
			}, body, settings.Timeout)
		})
		if err != nil {
			lastMessage = err.Error()
			continue
		}
		lastStatus = intPtr(resp.StatusCode)
		probeStatus, probeMessage := classifyAntigravityDeepProbeResponse(resp)
		probeDetail := proinspection.HTTPErrorDetail(resp.Body)
		switch probeStatus {
		case accountInspectionDeepProbeSuccess:
			s.clearInspectionAuthError(ctx, account)
			decision.DeepProbeStatus = accountInspectionDeepProbeSuccess
			decision.DeepProbeError = ""
			s.appendLog("success", fmt.Sprintf("%s Antigravity 深度检测通过", account.identity()))
			return decision, lastStatus, nil
		case accountInspectionDeepProbeAuthError:
			s.syncInspectionAuthStatus(ctx, account, resp.StatusCode)
			probeDecision := authErrorDecision(account, resp.StatusCode)
			probeDecision.UsedPercent = decision.UsedPercent
			probeDecision.DeepProbeStatus = accountInspectionDeepProbeAuthError
			probeDecision.DeepProbeError = probeMessage
			probeDecision.ErrorDetail = probeDetail
			s.appendLog("warning", fmt.Sprintf("%s Antigravity 深度检测授权异常：%s", account.identity(), probeMessage))
			return probeDecision, lastStatus, nil
		case accountInspectionDeepProbeQuota:
			s.clearInspectionAuthError(ctx, account)
			probeDecision := accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: "Antigravity 深度检测返回额度不可用，建议禁用账号", UsedPercent: decision.UsedPercent, IsQuota: true, ErrorDetail: probeDetail, DeepProbeStatus: accountInspectionDeepProbeQuota, DeepProbeError: probeMessage}
			if account.Disabled {
				probeDecision.Action = accountInspectionActionKeep
				probeDecision.ActionReason = "Antigravity 深度检测返回额度不可用，但账号已禁用"
			}
			s.appendLog("warning", fmt.Sprintf("%s Antigravity 深度检测额度不可用：%s", account.identity(), probeMessage))
			return probeDecision, lastStatus, nil
		default:
			lastMessage = probeMessage
			lastDetail = probeDetail
			if shouldStopAntigravityDeepProbeFailover(resp.StatusCode) {
				break endpointLoop
			}
		}
	}
	if lastMessage == "" {
		lastMessage = "antigravity deep probe unavailable"
	}
	s.syncInspectionAuthError(ctx, account, "antigravity_deep_probe_error", lastMessage, proinspection.StatusValue(lastStatus))
	decision.Action = accountInspectionActionKeep
	decision.ActionReason = "Antigravity 深度检测临时异常，保留账号"
	decision.Error = lastMessage
	decision.ErrorDetail = lastDetail
	decision.DeepProbeStatus = accountInspectionDeepProbeTransientError
	decision.DeepProbeError = lastMessage
	s.appendLog("warning", fmt.Sprintf("%s Antigravity 深度检测临时异常：%s", account.identity(), lastMessage))
	return decision, proinspection.FirstStatus(lastStatus, quotaStatus), nil
}

func shouldStopAntigravityDeepProbeFailover(status int) bool {
	return status != http.StatusTooManyRequests && status < http.StatusInternalServerError
}

func classifyAntigravityDeepProbeResponse(resp accountInspectionHTTPResult) (accountInspectionDeepProbeStatus, string) {
	return proinspection.ClassifyAntigravityDeepProbeResponse(resp.probeResponse())
}

func (s *accountInspectionScheduler) inspectClaude(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	usageResp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodGet, "https://api.anthropic.com/api/oauth/usage", s.claudeHeaders(), "", settings.Timeout)
	})
	status := intPtr(usageResp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	if usageResp.StatusCode < 200 || usageResp.StatusCode >= 300 {
		if isQuotaHTTPStatus(usageResp.StatusCode) {
			return quotaUnavailableDecision(account, "Claude 额度不可用，建议禁用账号", usageResp.Body), status, nil
		}
		if proinspection.IsAccountErrorStatus(usageResp.StatusCode) {
			return proinspection.WithHTTPErrorDetail(authErrorDecision(account, usageResp.StatusCode), usageResp.Body), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", usageResp.StatusCode)
	}
	windows, extraUsage, err := proinspection.BuildClaudeWindows(usageResp.Body)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	planType := ""
	profileResp, profileErr := s.apiCall(ctx, account.Auth, http.MethodGet, "https://api.anthropic.com/api/oauth/profile", s.claudeHeaders(), "", settings.Timeout)
	if profileErr == nil && profileResp.StatusCode >= 200 && profileResp.StatusCode < 300 {
		planType = proinspection.ResolveClaudePlan(profileResp.Body)
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"windows": windows, "extraUsage": extraUsage, "planType": emptyStringAsNil(planType), "rawShapeHash": proquota.JSONShapeHash(usageResp.Body)}))
	used := proinspection.MaxUsedPercentFromWindows(windows)
	return quotaDecision(account, used, len(windows) > 0, settings.UsedPercentThreshold), status, nil
}

func (s *accountInspectionScheduler) inspectCodex(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	accountID := codexAccountID(account.Auth)
	if accountID == "" {
		return accountInspectionDecision{}, nil, fmt.Errorf("missing ChatGPT account id")
	}
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodGet, "https://chatgpt.com/backend-api/wham/usage", map[string]string{
			"Authorization":      "Bearer $TOKEN$",
			"Content-Type":       "application/json",
			"User-Agent":         s.codexUserAgent(),
			"Chatgpt-Account-Id": accountID,
		}, "", settings.Timeout)
	})
	status := intPtr(resp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	payload, windows, used := proinspection.BuildCodexWindows(resp.Body)
	isQuota := isQuotaHTTPStatus(resp.StatusCode) || strings.Contains(strings.ToLower(resp.Body), "quota exhausted") || strings.Contains(strings.ToLower(resp.Body), "limit reached") || strings.Contains(strings.ToLower(resp.Body), "payment_required")
	if used != nil && *used >= settings.UsedPercentThreshold {
		isQuota = true
	}
	if payload != nil && len(windows) > 0 {
		s.persistQuotaState(ctx, account, quotaSuccessState(codexQuotaStateValues(account.Auth, payload, windows, resp.Body)))
	}
	decision := codexDecision(account, resp.StatusCode, used, isQuota, settings.UsedPercentThreshold)
	if proinspection.IsAccountErrorStatus(resp.StatusCode) {
		decision = proinspection.WithHTTPErrorDetail(decision, resp.Body)
	}
	return decision, status, nil
}

func (s *accountInspectionScheduler) inspectGeminiCLI(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	if s == nil || s.quota == nil {
		return accountInspectionDecision{}, intPtr(http.StatusServiceUnavailable), fmt.Errorf("quota gateway unavailable")
	}
	attemptCtx, cancel := context.WithTimeout(ctx, time.Duration(settings.Timeout)*time.Millisecond)
	result, err := s.quota.FetchQuota(attemptCtx, account.AuthIndex)
	cancel()
	for attempt := 0; err != nil && attempt < settings.Retries && ctx.Err() == nil; attempt++ {
		attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(settings.Timeout)*time.Millisecond)
		result, err = s.quota.FetchQuota(attemptCtx, account.AuthIndex)
		cancel()
	}
	upstreamStatus := result.UpstreamStatus
	if err != nil {
		status := upstreamStatus
		if status == 0 {
			status = result.ServiceStatus
		}
		if isQuotaHTTPStatus(upstreamStatus) {
			return quotaUnavailableDecision(account, "Gemini CLI 额度不可用，建议禁用账号", ""), intPtr(upstreamStatus), nil
		}
		if proinspection.IsAccountErrorStatus(upstreamStatus) {
			return authErrorDecision(account, upstreamStatus), intPtr(upstreamStatus), nil
		}
		return accountInspectionDecision{}, intPtr(status), err
	}
	if errCleanup := s.cleanupLegacyQuotaCacheFromAuth(ctx, account); errCleanup != nil {
		s.appendLog("warning", fmt.Sprintf("%s 旧认证文件配额缓存清理失败：%s", account.identity(), errCleanup.Error()))
	}
	used, hasQuota := proquota.SnapshotMaxUsedPercent(result.Snapshot)
	return quotaDecision(account, used, hasQuota, settings.UsedPercentThreshold), intPtr(http.StatusOK), nil
}

func (s *accountInspectionScheduler) inspectKimi(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodGet, "https://api.kimi.com/coding/v1/usages", map[string]string{"Authorization": "Bearer $TOKEN$"}, "", settings.Timeout)
	})
	status := intPtr(resp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isQuotaHTTPStatus(resp.StatusCode) {
			return quotaUnavailableDecision(account, "Kimi 额度不可用，建议禁用账号", resp.Body), status, nil
		}
		if proinspection.IsAccountErrorStatus(resp.StatusCode) {
			return proinspection.WithHTTPErrorDetail(authErrorDecision(account, resp.StatusCode), resp.Body), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	rows, used, err := proinspection.BuildKimiRows(resp.Body)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"rows": rows, "rawShapeHash": proquota.JSONShapeHash(resp.Body)}))
	return quotaDecision(account, used, len(rows) > 0, settings.UsedPercentThreshold), status, nil
}

func (s *accountInspectionScheduler) inspectXAI(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	if xaiInspectionUsingAPI(account.Auth) {
		return s.inspectXAIOfficialAPI(ctx, account, settings)
	}
	return s.inspectXAICLI(ctx, account, settings)
}

func (s *accountInspectionScheduler) inspectXAIOfficialAPI(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	model := strings.TrimSpace(settings.XAIDeepProbeModel)
	if model == "" {
		model = "grok-4.5"
	}
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodPost, xaiOfficialChatURL(account.Auth), xaiOfficialAPIHeaders(account.Auth), proinspection.BuildXAIOfficialHealthBody(model), settings.Timeout)
	})
	status := intPtr(resp.StatusCode)
	if err != nil {
		return accountInspectionDecision{}, status, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if isQuotaHTTPStatus(resp.StatusCode) || proinspection.IsXAIQuotaFailure(resp.Body) {
			return xaiOfficialAPIQuotaDecision(account, resp.Body), status, nil
		}
		if proinspection.IsAccountErrorStatus(resp.StatusCode) {
			return proinspection.WithHTTPErrorDetail(authErrorDecision(account, resp.StatusCode), resp.Body), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", resp.StatusCode)
	}

	billing := proquota.XAIPaidHealthSummary()
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{
		"billing":      billing,
		"rawShapeHash": proquota.JSONShapeHash(resp.Body),
	}))
	decision := healthyDecision(account)
	if settings.XAIDeepProbeEnabled {
		return s.applyXAIDeepProbe(ctx, account, settings, decision, status)
	}
	return decision, status, nil
}

func (s *accountInspectionScheduler) inspectXAICLI(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	headers := xaiRequestHeaders(account.Auth)
	weeklyBilling, weeklyResp, weeklyErr := s.fetchXAIBillingSummary(ctx, account, settings, xaiBillingWeeklyURL(), headers)
	monthlyBilling, monthlyResp, monthlyErr := s.fetchXAIBillingSummary(ctx, account, settings, xaiBillingURL(), headers)
	status := proinspection.FirstNonZeroStatus(monthlyResp.StatusCode, weeklyResp.StatusCode)
	billing := proquota.MergeXAIBillingSummaries(weeklyBilling, monthlyBilling)
	if billing == nil {
		if isQuotaHTTPStatus(weeklyResp.StatusCode) || proinspection.IsXAIQuotaFailure(weeklyResp.Body) {
			return quotaUnavailableDecision(account, "xAI 额度不可用，建议禁用账号", weeklyResp.Body), status, nil
		}
		if isQuotaHTTPStatus(monthlyResp.StatusCode) || proinspection.IsXAIQuotaFailure(monthlyResp.Body) {
			return quotaUnavailableDecision(account, "xAI 额度不可用，建议禁用账号", monthlyResp.Body), status, nil
		}
		if proinspection.IsAccountErrorStatus(weeklyResp.StatusCode) {
			return proinspection.WithHTTPErrorDetail(authErrorDecision(account, weeklyResp.StatusCode), weeklyResp.Body), status, nil
		}
		if proinspection.IsAccountErrorStatus(monthlyResp.StatusCode) {
			return proinspection.WithHTTPErrorDetail(authErrorDecision(account, monthlyResp.StatusCode), monthlyResp.Body), status, nil
		}
		if weeklyErr != nil {
			return accountInspectionDecision{}, status, weeklyErr
		}
		if monthlyErr != nil {
			return accountInspectionDecision{}, status, monthlyErr
		}
		return accountInspectionDecision{}, status, fmt.Errorf("empty xai billing config")
	}
	if planType, known := xaiPlanTypeFromAccessToken(account.Auth); known {
		billing["planType"] = planType
	} else if planType, known := proquota.XAIPlanTypeFromBillingBody(monthlyResp.StatusCode, monthlyResp.Body); known {
		billing["planType"] = planType
	}
	billing = mergeCachedXAIFreeQuota(ctx, account, billing)

	// Free 套餐的 token 额度不在 billing 响应中，只能从一次真实 Responses
	// 请求的 x-ratelimit-* 响应头或 free-usage-exhausted 错误体获得。该采样是
	// 巡检的固定额度步骤，不依赖可选的“深度检测”开关；开启深度检测时复用同一
	// 次请求做健康分类，避免重复消耗额度。
	var freeProbe *xaiResponsesProbeOutcome
	if strings.EqualFold(strings.TrimSpace(stringFromAny(billing["planType"])), "free") {
		model := strings.TrimSpace(settings.XAIDeepProbeModel)
		if model == "" {
			model = "grok-4.5"
		}
		s.appendLog("info", fmt.Sprintf("%s xAI 免费额度探测开始：%s", account.identity(), model))
		outcome := s.runXAIResponsesProbe(ctx, account, settings, model, settings.XAIDeepProbeEnabled)
		freeProbe = &outcome
		if outcome.freeQuota != nil {
			billing["freeQuota"] = outcome.freeQuota
			if outcome.resp.StatusCode != 0 {
				status = intPtr(outcome.resp.StatusCode)
			}
		} else if outcome.err != nil {
			s.appendLog("warning", fmt.Sprintf("%s xAI 免费额度探测失败，保留 billing 与历史快照：%s", account.identity(), outcome.err.Error()))
		} else if firstMap(billing, "freeQuota", "free_quota") == nil {
			s.appendLog("warning", fmt.Sprintf("%s xAI 免费额度探测未返回限额明细", account.identity()))
		}
	}
	used := proquota.XAISummaryUsedPercent(billing)
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{
		"billing":             billing,
		"rawShapeHash":        proquota.JSONShapeHashForBodies(map[string]string{"weekly": weeklyResp.Body, "monthly": monthlyResp.Body}),
		"weeklyRawShapeHash":  proquota.JSONShapeHash(weeklyResp.Body),
		"monthlyRawShapeHash": proquota.JSONShapeHash(monthlyResp.Body),
	}))
	decision := quotaDecision(account, used, billing != nil, settings.UsedPercentThreshold)
	if settings.XAIDeepProbeEnabled && proinspection.ShouldDeepProbe(decision) {
		if freeProbe != nil {
			return s.applyXAIDeepProbeOutcome(ctx, account, decision, status, *freeProbe)
		}
		return s.applyXAIDeepProbe(ctx, account, settings, decision, status)
	}
	return decision, status, nil
}

type xaiResponsesProbeOutcome struct {
	resp      accountInspectionHTTPResult
	status    accountInspectionDeepProbeStatus
	message   string
	err       error
	freeQuota map[string]any
}

func (s *accountInspectionScheduler) runXAIResponsesProbe(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, model string, classifyDeep bool) xaiResponsesProbeOutcome {
	var freeQuota map[string]any
	task := func() (accountInspectionHTTPResult, error) {
		result, requestErr := s.apiCall(ctx, account.Auth, http.MethodPost, xaiResponsesURL(account.Auth), xaiDeepProbeHeaders(account.Auth), proinspection.BuildXAIDeepProbeBody(model), settings.Timeout)
		if !xaiInspectionUsingAPI(account.Auth) {
			if observed := observeAccountXAIQuota(ctx, account, model, result); observed != nil {
				freeQuota = observed
			}
		}
		return result, requestErr
	}
	if !classifyDeep {
		resp, requestErr := s.withRetry(ctx, settings.Retries, task)
		status, message := classifyXAIDeepProbeResponse(resp)
		return xaiResponsesProbeOutcome{resp: resp, status: status, message: message, err: requestErr, freeQuota: freeQuota}
	}
	resp, status, message, err := runXAIDeepProbeWithRetry(ctx, settings.Retries, accountInspectionXAIRetryDelay, task)
	return xaiResponsesProbeOutcome{resp: resp, status: status, message: message, err: err, freeQuota: freeQuota}
}

func (s *accountInspectionScheduler) applyXAIDeepProbe(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, decision accountInspectionDecision, quotaStatus *int) (accountInspectionDecision, *int, error) {
	model := strings.TrimSpace(settings.XAIDeepProbeModel)
	if model == "" {
		decision.DeepProbeStatus = accountInspectionDeepProbeSkipped
		decision.DeepProbeError = "missing xAI deep probe model"
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测跳过：%s", account.identity(), decision.DeepProbeError))
		return decision, quotaStatus, nil
	}

	s.appendLog("info", fmt.Sprintf("%s xAI 深度检测开始：%s", account.identity(), model))
	outcome := s.runXAIResponsesProbe(ctx, account, settings, model, true)
	return s.applyXAIDeepProbeOutcome(ctx, account, decision, quotaStatus, outcome)
}

func (s *accountInspectionScheduler) applyXAIDeepProbeOutcome(ctx context.Context, account accountInspectionAccount, decision accountInspectionDecision, quotaStatus *int, outcome xaiResponsesProbeOutcome) (accountInspectionDecision, *int, error) {
	resp := outcome.resp
	status := outcome.status
	message := outcome.message
	err := outcome.err
	var probeStatus *int
	if resp.StatusCode != 0 {
		probeStatus = intPtr(resp.StatusCode)
	}
	if err != nil {
		message := err.Error()
		s.syncInspectionAuthError(ctx, account, "xai_deep_probe_error", message, proinspection.StatusValue(probeStatus))
		decision.Action = accountInspectionActionKeep
		decision.ActionReason = "xAI 深度检测临时异常，保留账号"
		decision.Error = message
		decision.DeepProbeStatus = accountInspectionDeepProbeTransientError
		decision.DeepProbeError = message
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测临时异常：%s", account.identity(), message))
		return decision, proinspection.FirstStatus(probeStatus, quotaStatus), nil
	}

	errorDetail := proinspection.HTTPErrorDetail(resp.Body)
	switch status {
	case accountInspectionDeepProbeSuccess:
		s.clearInspectionAuthError(ctx, account)
		decision.DeepProbeStatus = accountInspectionDeepProbeSuccess
		decision.DeepProbeError = ""
		s.appendLog("success", fmt.Sprintf("%s xAI 深度检测通过", account.identity()))
		return decision, probeStatus, nil
	case accountInspectionDeepProbeAuthError:
		s.syncInspectionAuthStatus(ctx, account, resp.StatusCode)
		probeDecision := authErrorDecision(account, resp.StatusCode)
		probeDecision.UsedPercent = decision.UsedPercent
		probeDecision.DeepProbeStatus = accountInspectionDeepProbeAuthError
		probeDecision.DeepProbeError = message
		probeDecision.ErrorDetail = errorDetail
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测授权异常：%s", account.identity(), message))
		return probeDecision, probeStatus, nil
	case accountInspectionDeepProbeQuota:
		s.clearInspectionAuthError(ctx, account)
		probeDecision := accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: "xAI 深度检测返回额度不可用，建议禁用账号", UsedPercent: decision.UsedPercent, IsQuota: true, ErrorDetail: errorDetail, DeepProbeStatus: accountInspectionDeepProbeQuota, DeepProbeError: message}
		if account.Disabled {
			probeDecision.Action = accountInspectionActionKeep
			probeDecision.ActionReason = "xAI 深度检测返回额度不可用，但账号已禁用"
		}
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测额度不可用：%s", account.identity(), message))
		return probeDecision, probeStatus, nil
	default:
		s.syncInspectionAuthError(ctx, account, "xai_deep_probe_error", message, resp.StatusCode)
		decision.Action = accountInspectionActionKeep
		decision.ActionReason = "xAI 深度检测临时异常，保留账号"
		decision.Error = message
		decision.ErrorDetail = errorDetail
		decision.DeepProbeStatus = accountInspectionDeepProbeTransientError
		decision.DeepProbeError = message
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测临时异常：%s", account.identity(), message))
		return decision, probeStatus, nil
	}
}

func runXAIDeepProbeWithRetry(
	ctx context.Context,
	retries int,
	retryDelay time.Duration,
	task func() (accountInspectionHTTPResult, error),
) (accountInspectionHTTPResult, accountInspectionDeepProbeStatus, string, error) {
	var last accountInspectionHTTPResult
	resp, status, message, err := proinspection.RunXAIDeepProbeWithRetry(ctx, retries, retryDelay, func() (proinspection.ProbeResponse, error) {
		var taskErr error
		last, taskErr = task()
		return last.probeResponse(), taskErr
	})
	last.StatusCode = resp.StatusCode
	last.Body = resp.Body
	return last, status, message, err
}

func xaiInspectionBaseURL(auth *coreauth.Auth) string {
	return strings.TrimRight(upstreamexecutor.XAIChatBaseURL(auth), "/")
}

func xaiResponsesURL(auth *coreauth.Auth) string {
	return xaiInspectionBaseURL(auth) + "/responses"
}

func xaiOfficialChatURL(auth *coreauth.Auth) string {
	return xaiInspectionBaseURL(auth) + "/chat/completions"
}

func xaiInspectionUsingAPI(auth *coreauth.Auth) bool {
	return upstreamexecutor.XAIUsingAPI(auth)
}

func xaiDeepProbeHeaders(auth *coreauth.Auth) map[string]string {
	return xaiHeaderMap(upstreamexecutor.XAIChatRequestHeaders(auth, "$TOKEN$", true))
}

func xaiOfficialAPIHeaders(auth *coreauth.Auth) map[string]string {
	return xaiHeaderMap(upstreamexecutor.XAIChatRequestHeaders(auth, "$TOKEN$", false))
}

func xaiOfficialAPIQuotaDecision(account accountInspectionAccount, body string) accountInspectionDecision {
	return proinspection.XAIOfficialAPIQuotaDecision(account.Disabled, body)
}

func classifyXAIDeepProbeResponse(resp accountInspectionHTTPResult) (accountInspectionDeepProbeStatus, string) {
	return proinspection.ClassifyXAIDeepProbeResponse(resp.probeResponse())
}

func (s *accountInspectionScheduler) fetchXAIBillingSummary(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, url string, headers map[string]string) (map[string]any, accountInspectionHTTPResult, error) {
	resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
		return s.apiCall(ctx, account.Auth, http.MethodGet, url, headers, "", settings.Timeout)
	})
	if err != nil {
		return nil, resp, err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, resp, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	billing, _, err := proquota.BuildXAIBillingSummary(resp.Body)
	if err != nil {
		return nil, resp, err
	}
	return billing, resp, nil
}

func xaiBillingURL() string {
	return "https://cli-chat-proxy.grok.com/v1/billing"
}

func xaiBillingWeeklyURL() string {
	return "https://cli-chat-proxy.grok.com/v1/billing?format=credits"
}

func xaiRequestHeaders(auth *coreauth.Auth) map[string]string {
	headers := xaiHeaderMap(upstreamexecutor.XAIChatRequestHeaders(auth, "$TOKEN$", false))
	if userID := xaiUserID(auth); userID != "" {
		headers["x-userid"] = userID
	}
	return headers
}

func xaiHeaderMap(headers http.Header) map[string]string {
	values := make(map[string]string, len(headers))
	for key, entries := range headers {
		if len(entries) > 0 {
			values[key] = entries[0]
		}
	}
	return values
}

func (s *accountInspectionScheduler) antigravityUserAgent() string {
	return misc.AntigravityUserAgent()
}

func (s *accountInspectionScheduler) codexUserAgent() string {
	if s != nil && s.h != nil && s.h.cfg != nil {
		if value := strings.TrimSpace(s.h.cfg.CodexHeaderDefaults.UserAgent); value != "" {
			return value
		}
	}
	return "codex_cli_rs/0.118.0 (Mac OS 26.3.1; arm64) iTerm.app/3.6.9"
}

func (s *accountInspectionScheduler) claudeUserAgent() string {
	if s != nil && s.h != nil && s.h.cfg != nil {
		return strings.TrimSpace(s.h.cfg.ClaudeHeaderDefaults.UserAgent)
	}
	return ""
}

func (s *accountInspectionScheduler) claudeHeaders() map[string]string {
	headers := map[string]string{
		"Authorization":  "Bearer $TOKEN$",
		"Content-Type":   "application/json",
		"anthropic-beta": "oauth-2025-04-20",
	}
	if userAgent := s.claudeUserAgent(); userAgent != "" {
		headers["User-Agent"] = userAgent
	}
	return headers
}
