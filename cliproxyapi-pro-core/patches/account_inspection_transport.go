package management

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
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

const (
	antigravitySubscriptionTTL = 6 * time.Hour
	antigravityEndpointTTL     = 30 * time.Minute
	claudeProfileTTL           = 12 * time.Hour
)

type inspectionProbeCacheKey struct {
	authID      string
	epoch       uint64
	legacyProof string
}

func inspectionCacheKey(account accountInspectionAccount) (inspectionProbeCacheKey, bool) {
	if account.Auth == nil {
		return inspectionProbeCacheKey{}, false
	}
	id := strings.TrimSpace(account.AuthID)
	if id == "" {
		id = strings.TrimSpace(account.Auth.ID)
	}
	if id == "" {
		return inspectionProbeCacheKey{}, false
	}
	key := inspectionProbeCacheKey{authID: id, epoch: account.Auth.RegistrationEpoch}
	if key.epoch == 0 {
		key.legacyProof = accountInspectionCredentialFingerprint(account.Auth)
	}
	return key, true
}

type inspectionCachedSubscription struct {
	value   map[string]any
	expires time.Time
}

type inspectionCachedProfile struct {
	plan    string
	expires time.Time
}

type inspectionProbeCache struct {
	mu            sync.Mutex
	subscriptions map[inspectionProbeCacheKey]inspectionCachedSubscription
	profiles      map[inspectionProbeCacheKey]inspectionCachedProfile
	endpoint      string
	endpointUntil time.Time
}

func (c *inspectionProbeCache) subscription(key inspectionProbeCacheKey, now time.Time) (map[string]any, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.subscriptions[key]
	if !ok || !now.Before(entry.expires) {
		delete(c.subscriptions, key)
		return nil, false
	}
	return entry.value, true
}

func (c *inspectionProbeCache) rememberSubscription(key inspectionProbeCacheKey, value map[string]any, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subscriptions == nil {
		c.subscriptions = make(map[inspectionProbeCacheKey]inspectionCachedSubscription)
	}
	for staleKey, entry := range c.subscriptions {
		if !now.Before(entry.expires) {
			delete(c.subscriptions, staleKey)
		}
	}
	c.subscriptions[key] = inspectionCachedSubscription{value: value, expires: now.Add(antigravitySubscriptionTTL)}
}

func (c *inspectionProbeCache) profile(key inspectionProbeCacheKey, now time.Time) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.profiles[key]
	if !ok || !now.Before(entry.expires) {
		delete(c.profiles, key)
		return "", false
	}
	return entry.plan, true
}

func (c *inspectionProbeCache) rememberProfile(key inspectionProbeCacheKey, plan string, now time.Time) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.profiles == nil {
		c.profiles = make(map[inspectionProbeCacheKey]inspectionCachedProfile)
	}
	for staleKey, entry := range c.profiles {
		if !now.Before(entry.expires) {
			delete(c.profiles, staleKey)
		}
	}
	c.profiles[key] = inspectionCachedProfile{plan: plan, expires: now.Add(claudeProfileTTL)}
}

func (c *inspectionProbeCache) antigravityQuotaURLs(now time.Time) []string {
	urls := antigravityQuotaURLs()
	c.mu.Lock()
	preferred := c.endpoint
	valid := preferred != "" && now.Before(c.endpointUntil)
	c.mu.Unlock()
	if !valid || urls[0] == preferred {
		return urls
	}
	for _, url := range urls {
		if url == preferred {
			ordered := []string{preferred}
			for _, fallback := range urls {
				if fallback != preferred {
					ordered = append(ordered, fallback)
				}
			}
			return ordered
		}
	}
	return urls
}

func (c *inspectionProbeCache) rememberAntigravityEndpoint(url string, now time.Time) {
	c.mu.Lock()
	c.endpoint = url
	c.endpointUntil = now.Add(antigravityEndpointTTL)
	c.mu.Unlock()
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
	saved, err := s.inspectionAuthManager().CommitInspectionRefresh(ctx, account.Auth, updated)
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
		if s == nil || s.h == nil || s.inspectionAuthManager() == nil {
			return accountInspectionHTTPResult{}, fmt.Errorf("core auth manager unavailable")
		}
		requestStartedAt := time.Now()
		defer func() { inspectionMetricsRecordRequest(ctx, url, time.Since(requestStartedAt)) }()
		resp, err := s.inspectionAuthManager().HttpRequest(reqCtx, auth, req)
		if err != nil {
			return accountInspectionHTTPResult{}, err
		}
		defer resp.Body.Close()
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 512*1024))
		return accountInspectionHTTPResult{StatusCode: resp.StatusCode, Body: string(raw), Header: resp.Header.Clone()}, nil
	}
	client := &http.Client{Timeout: time.Duration(timeoutMS) * time.Millisecond, Transport: s.h.apiCallTransport(auth)}
	requestStartedAt := time.Now()
	defer func() { inspectionMetricsRecordRequest(ctx, url, time.Since(requestStartedAt)) }()
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
		if i > 0 {
			inspectionMetricsRecordRetry(ctx)
		}
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
	urls := s.probeCache.antigravityQuotaURLs(time.Now())
	var priorityStatus *int
	var priorityDetail string
	var parseStatus *int
	var parseError error
	probeFailed := false
	for _, url := range urls {
		resp, err := s.withRetry(ctx, settings.Retries, func() (accountInspectionHTTPResult, error) {
			return s.apiCall(ctx, account.Auth, http.MethodPost, url, map[string]string{
				"Authorization": "Bearer $TOKEN$",
				"Content-Type":  "application/json",
				"User-Agent":    s.antigravityUserAgent(),
			}, body, settings.Timeout)
		})
		if err != nil {
			probeFailed = true
			continue
		}
		status := intPtr(resp.StatusCode)
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			if isQuotaHTTPStatus(resp.StatusCode) || proinspection.IsAntigravityQuotaFailure(resp.Body) {
				return quotaUnavailableDecision(account, "Antigravity 额度不可用，建议建立额度保护", resp.Body), status, nil
			}
			if resp.StatusCode == http.StatusTooManyRequests {
				return rateLimitedDecision(resp.Body), status, nil
			}
			if proinspection.IsAccountErrorStatus(resp.StatusCode) {
				priorityStatus = status
				priorityDetail = resp.Body
			} else {
				probeFailed = true
			}
			continue
		}
		groups, err := proinspection.BuildAntigravityGroups(resp.Body)
		if err != nil {
			parseStatus = status
			parseError = err
			continue
		}
		s.probeCache.rememberAntigravityEndpoint(url, time.Now())
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
		decision := proinspection.WithQuotaWindows(quotaDecision(account, used, used != nil, settings.UsedPercentThreshold), proinspection.AntigravityBlockingWindows(groups, settings.AntigravityQuotaMode), settings.UsedPercentThreshold)
		decision.QuotaModel = proinspection.AntigravityQuotaModel(groups, settings.AntigravityQuotaMode, settings.UsedPercentThreshold)
		if settings.AntigravityDeepProbeEnabled && shouldConfirmAntigravityInspection(ctx, decision) {
			stopConfirm := inspectionMetricsStartConfirmation(ctx)
			confirmed, confirmStatus, confirmErr := s.applyAntigravityDeepProbe(ctx, account, settings, decision, status)
			stopConfirm()
			return confirmed, confirmStatus, confirmErr
		}
		return decision, status, nil
	}
	if priorityStatus != nil {
		return proinspection.WithHTTPErrorDetail(authErrorDecision(account, *priorityStatus), priorityDetail), priorityStatus, nil
	}
	if parseError != nil && !probeFailed {
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "Antigravity 额度数据无法解析，保留账号", QuotaUnknown: true, Error: parseError.Error()}, parseStatus, nil
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
	key, cacheable := inspectionCacheKey(account)
	if cacheable {
		if cached, ok := s.probeCache.subscription(key, time.Now()); ok {
			return cached
		}
	}
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
	subscription := proinspection.BuildAntigravitySubscription(payload)
	if subscription != nil && cacheable {
		s.probeCache.rememberSubscription(key, subscription, time.Now())
	}
	return subscription
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
			probeDecision := accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: "Antigravity 深度检测返回额度不可用，建议建立额度保护", UsedPercent: decision.UsedPercent, IsQuota: true, ErrorDetail: probeDetail, DeepProbeStatus: accountInspectionDeepProbeQuota, DeepProbeError: probeMessage}
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
		if isQuotaHTTPStatus(usageResp.StatusCode) || proinspection.IsExplicitQuotaFailure(usageResp.Body) {
			return quotaUnavailableDecision(account, "Claude 额度不可用，建议建立额度保护", usageResp.Body), status, nil
		}
		if usageResp.StatusCode == http.StatusTooManyRequests {
			return rateLimitedDecision(usageResp.Body), status, nil
		}
		if proinspection.IsAccountErrorStatus(usageResp.StatusCode) {
			return proinspection.WithHTTPErrorDetail(authErrorDecision(account, usageResp.StatusCode), usageResp.Body), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", usageResp.StatusCode)
	}
	windows, extraUsage, err := proinspection.BuildClaudeWindows(usageResp.Body)
	if err != nil {
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "Claude 额度数据无法解析，保留账号", QuotaUnknown: true, Error: err.Error()}, status, nil
	}
	planType := ""
	key, cacheable := inspectionCacheKey(account)
	profileCached := false
	if cacheable {
		planType, profileCached = s.probeCache.profile(key, time.Now())
	}
	if !profileCached {
		profileResp, profileErr := s.apiCall(ctx, account.Auth, http.MethodGet, "https://api.anthropic.com/api/oauth/profile", s.claudeHeaders(), "", settings.Timeout)
		if profileErr == nil && profileResp.StatusCode >= 200 && profileResp.StatusCode < 300 {
			planType = proinspection.ResolveClaudePlan(profileResp.Body)
			if cacheable {
				s.probeCache.rememberProfile(key, planType, time.Now())
			}
		}
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"windows": windows, "extraUsage": extraUsage, "planType": emptyStringAsNil(planType), "rawShapeHash": proquota.JSONShapeHash(usageResp.Body)}))
	used := proinspection.MaxUsedPercentFromWindows(windows)
	decision := proinspection.WithQuotaWindows(quotaDecision(account, used, used != nil, settings.UsedPercentThreshold), windows, settings.UsedPercentThreshold)
	decision.QuotaModel = proinspection.ClaudeQuotaModel(windows, settings.UsedPercentThreshold)
	return decision, status, nil
}

func (s *accountInspectionScheduler) inspectCodex(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	accountID := codexAccountID(account.Auth)
	if accountID == "" {
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "缺少 ChatGPT account id，无法判断额度，保留账号", QuotaUnknown: true, Error: "missing ChatGPT account id"}, nil, nil
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
	isQuota := isQuotaHTTPStatus(resp.StatusCode) || proinspection.IsExplicitQuotaFailure(resp.Body)
	if resp.StatusCode == http.StatusTooManyRequests && !isQuota {
		return rateLimitedDecision(resp.Body), status, nil
	}
	if used != nil && *used >= settings.UsedPercentThreshold {
		isQuota = true
	}
	if payload != nil && len(windows) > 0 {
		s.persistQuotaState(ctx, account, quotaSuccessState(codexQuotaStateValues(account.Auth, payload, windows, resp.Body)))
	}
	decision := proinspection.WithQuotaWindows(codexDecision(account, resp.StatusCode, used, isQuota, settings.UsedPercentThreshold), windows, settings.UsedPercentThreshold)
	decision.QuotaModel = proinspection.QuotaModelScope("codex", windows, settings.UsedPercentThreshold)
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
	requestStartedAt := time.Now()
	result, err := s.quota.FetchQuota(attemptCtx, account.AuthIndex)
	inspectionMetricsRecordRequest(ctx, "quota-gateway", time.Since(requestStartedAt))
	cancel()
	for attempt := 0; err != nil && attempt < settings.Retries && ctx.Err() == nil; attempt++ {
		inspectionMetricsRecordRetry(ctx)
		attemptCtx, cancel = context.WithTimeout(ctx, time.Duration(settings.Timeout)*time.Millisecond)
		requestStartedAt = time.Now()
		result, err = s.quota.FetchQuota(attemptCtx, account.AuthIndex)
		inspectionMetricsRecordRequest(ctx, "quota-gateway", time.Since(requestStartedAt))
		cancel()
	}
	upstreamStatus := result.UpstreamStatus
	if err != nil {
		status := upstreamStatus
		if status == 0 {
			status = result.ServiceStatus
		}
		if isQuotaHTTPStatus(upstreamStatus) {
			return quotaUnavailableDecision(account, "Gemini CLI 额度不可用，建议建立额度保护", ""), intPtr(upstreamStatus), nil
		}
		if upstreamStatus == http.StatusTooManyRequests {
			return rateLimitedDecision(""), intPtr(upstreamStatus), nil
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
	return quotaDecision(account, used, hasQuota && used != nil, settings.UsedPercentThreshold), intPtr(http.StatusOK), nil
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
		if isQuotaHTTPStatus(resp.StatusCode) || proinspection.IsExplicitQuotaFailure(resp.Body) {
			return quotaUnavailableDecision(account, "Kimi 额度不可用，建议建立额度保护", resp.Body), status, nil
		}
		if resp.StatusCode == http.StatusTooManyRequests {
			return rateLimitedDecision(resp.Body), status, nil
		}
		if proinspection.IsAccountErrorStatus(resp.StatusCode) {
			return proinspection.WithHTTPErrorDetail(authErrorDecision(account, resp.StatusCode), resp.Body), status, nil
		}
		return accountInspectionDecision{}, status, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	rows, used, err := proinspection.BuildKimiRows(resp.Body)
	if err != nil {
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "Kimi 额度数据无法解析，保留账号", QuotaUnknown: true, Error: err.Error()}, status, nil
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{"rows": rows, "rawShapeHash": proquota.JSONShapeHash(resp.Body)}))
	decision := proinspection.WithQuotaWindows(quotaDecision(account, used, used != nil, settings.UsedPercentThreshold), rows, settings.UsedPercentThreshold)
	decision.QuotaModel = proinspection.QuotaModelScope("kimi", rows, settings.UsedPercentThreshold)
	return decision, status, nil
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
	probe := inspectionProbeContextFrom(ctx)
	confirm := settings.XAIDeepProbeEnabled && (probe.Trigger == inspectionTriggerManual || account.Disabled || probe.Previous != nil && probe.Previous.IsQuota)
	if confirm {
		outcome := s.runXAIResponsesProbe(ctx, account, settings, model, true)
		decision, status, err := s.applyXAIDeepProbeOutcome(ctx, account, healthyDecision(account), nil, outcome)
		if err == nil && outcome.status == accountInspectionDeepProbeSuccess {
			s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{
				"billing":      proquota.XAIPaidHealthSummary(),
				"rawShapeHash": proquota.JSONShapeHash(outcome.resp.Body),
			}))
		}
		return decision, status, err
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
		if resp.StatusCode == http.StatusTooManyRequests {
			return rateLimitedDecision(resp.Body), status, nil
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
	return decision, status, nil
}

func (s *accountInspectionScheduler) inspectXAICLI(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings) (accountInspectionDecision, *int, error) {
	headers := xaiRequestHeaders(account.Auth)
	type billingResult struct {
		weekly  bool
		billing map[string]any
		resp    accountInspectionHTTPResult
		err     error
	}
	results := make(chan billingResult, 2)
	for _, endpoint := range []struct {
		weekly bool
		url    string
	}{
		{weekly: true, url: xaiBillingWeeklyURL()},
		{weekly: false, url: xaiBillingURL()},
	} {
		go func() {
			billing, resp, err := s.fetchXAIBillingSummary(ctx, account, settings, endpoint.url, headers)
			results <- billingResult{weekly: endpoint.weekly, billing: billing, resp: resp, err: err}
		}()
	}
	var weeklyBilling, monthlyBilling map[string]any
	var weeklyResp, monthlyResp accountInspectionHTTPResult
	var weeklyErr, monthlyErr error
	for range 2 {
		result := <-results
		if result.weekly {
			weeklyBilling, weeklyResp, weeklyErr = result.billing, result.resp, result.err
		} else {
			monthlyBilling, monthlyResp, monthlyErr = result.billing, result.resp, result.err
		}
	}
	status := proinspection.FirstNonZeroStatus(monthlyResp.StatusCode, weeklyResp.StatusCode)
	billing := proquota.MergeXAIBillingSummaries(weeklyBilling, monthlyBilling)
	if weeklyResp.StatusCode >= 400 && (isQuotaHTTPStatus(weeklyResp.StatusCode) || proinspection.IsXAIQuotaFailure(weeklyResp.Body)) {
		return quotaUnavailableDecision(account, "xAI 额度不可用，建议建立额度保护", weeklyResp.Body), intPtr(weeklyResp.StatusCode), nil
	}
	if monthlyResp.StatusCode >= 400 && (isQuotaHTTPStatus(monthlyResp.StatusCode) || proinspection.IsXAIQuotaFailure(monthlyResp.Body)) {
		return quotaUnavailableDecision(account, "xAI 额度不可用，建议建立额度保护", monthlyResp.Body), intPtr(monthlyResp.StatusCode), nil
	}
	if weeklyResp.StatusCode == http.StatusTooManyRequests && !proinspection.IsXAIQuotaFailure(weeklyResp.Body) {
		return rateLimitedDecision(weeklyResp.Body), intPtr(weeklyResp.StatusCode), nil
	}
	if monthlyResp.StatusCode == http.StatusTooManyRequests && !proinspection.IsXAIQuotaFailure(monthlyResp.Body) {
		return rateLimitedDecision(monthlyResp.Body), intPtr(monthlyResp.StatusCode), nil
	}
	if proinspection.IsAccountErrorStatus(weeklyResp.StatusCode) {
		return proinspection.WithHTTPErrorDetail(authErrorDecision(account, weeklyResp.StatusCode), weeklyResp.Body), intPtr(weeklyResp.StatusCode), nil
	}
	if proinspection.IsAccountErrorStatus(monthlyResp.StatusCode) {
		return proinspection.WithHTTPErrorDetail(authErrorDecision(account, monthlyResp.StatusCode), monthlyResp.Body), intPtr(monthlyResp.StatusCode), nil
	}
	if weeklyErr != nil || monthlyErr != nil {
		// One parsed window can prove exhaustion, but cannot prove that an
		// unreadable or unavailable companion window is healthy.
		used := proquota.XAISummaryUsedPercent(billing)
		quota := quotaDecision(account, used, used != nil, settings.UsedPercentThreshold)
		if quota.IsQuota {
			return quota, status, nil
		}
		if weeklyErr != nil && (weeklyResp.StatusCode < 200 || weeklyResp.StatusCode >= 300) {
			return accountInspectionDecision{}, intPtr(weeklyResp.StatusCode), weeklyErr
		}
		if monthlyErr != nil && (monthlyResp.StatusCode < 200 || monthlyResp.StatusCode >= 300) {
			return accountInspectionDecision{}, intPtr(monthlyResp.StatusCode), monthlyErr
		}
		parseError := weeklyErr
		if parseError == nil {
			parseError = monthlyErr
		}
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "xAI billing 数据无法解析，保留账号", QuotaUnknown: true, Error: parseError.Error()}, status, nil
	}
	if billing == nil {
		return accountInspectionDecision{Action: accountInspectionActionKeep, ActionReason: "xAI billing 数据不足，保留账号", QuotaUnknown: true, Error: "empty xai billing config"}, status, nil
	}
	if planType, known := xaiPlanTypeFromAccessToken(account.Auth); known {
		billing["planType"] = planType
	} else if planType, known := proquota.XAIPlanTypeFromBillingBody(monthlyResp.StatusCode, monthlyResp.Body); known {
		billing["planType"] = planType
	}
	billing = mergeCachedXAIFreeQuota(ctx, account, billing)

	// Free token quota comes from a Responses observation. Reuse a fresh
	// observation on scheduled runs; manual and recovery checks sample again.
	var freeProbe *xaiResponsesProbeOutcome
	freeProbeConfirm := false
	probeContext := inspectionProbeContextFrom(ctx)
	if probeContext.Now.IsZero() {
		probeContext.Now = time.Now()
	}
	freeQuota := firstMap(billing, "freeQuota", "free_quota")
	model := strings.TrimSpace(settings.XAIDeepProbeModel)
	if model == "" {
		model = "grok-4.5"
	}
	cachedModel := strings.TrimSpace(stringFromAny(freeQuota["model"]))
	refreshFreeQuota := probeContext.Previous == nil || (cachedModel != "" && !strings.EqualFold(cachedModel, model)) || shouldRefreshXAIFreeQuota(billing, probeContext.Now, probeContext.Trigger)
	freePlan := strings.EqualFold(strings.TrimSpace(stringFromAny(billing["planType"])), "free")
	// Keep the cached observation in billing for display, but require a new
	// observation before it can affect this run when a refresh is due.
	freeQuotaCurrent := !freePlan || !refreshFreeQuota
	if freePlan && refreshFreeQuota {
		s.appendLog("info", fmt.Sprintf("%s xAI 免费额度探测开始：%s", account.identity(), model))
		currentUsed := proquota.XAISummaryUsedPercent(billing)
		currentDecision := quotaDecision(account, currentUsed, currentUsed != nil, settings.UsedPercentThreshold)
		freeProbeConfirm = settings.XAIDeepProbeEnabled && shouldConfirmInspection(ctx, currentDecision)
		outcome := s.runXAIResponsesProbe(ctx, account, settings, model, freeProbeConfirm)
		freeProbe = &outcome
		if outcome.resp.StatusCode == http.StatusTooManyRequests && outcome.freeQuota == nil {
			if proinspection.IsXAIQuotaFailure(outcome.resp.Body) {
				return quotaUnavailableDecision(account, "xAI 免费额度不可用，建议建立额度保护", outcome.resp.Body), intPtr(outcome.resp.StatusCode), nil
			}
			return rateLimitedDecision(outcome.resp.Body), intPtr(outcome.resp.StatusCode), nil
		}
		if outcome.freeQuota != nil {
			billing["freeQuota"] = outcome.freeQuota
			freeQuotaCurrent = true
			if outcome.resp.StatusCode != 0 {
				status = intPtr(outcome.resp.StatusCode)
			}
		} else if outcome.err != nil {
			s.appendLog("warning", fmt.Sprintf("%s xAI 免费额度探测失败，保留 billing 与历史快照：%s", account.identity(), outcome.err.Error()))
		} else {
			s.appendLog("warning", fmt.Sprintf("%s xAI 免费额度探测未返回限额明细", account.identity()))
		}
	}
	var used *float64
	if freeQuotaCurrent {
		used = proquota.XAISummaryUsedPercent(billing)
	}
	s.persistQuotaState(ctx, account, quotaSuccessState(map[string]any{
		"billing":             billing,
		"rawShapeHash":        proquota.JSONShapeHashForBodies(map[string]string{"weekly": weeklyResp.Body, "monthly": monthlyResp.Body}),
		"weeklyRawShapeHash":  proquota.JSONShapeHash(weeklyResp.Body),
		"monthlyRawShapeHash": proquota.JSONShapeHash(monthlyResp.Body),
	}))
	decision := quotaDecision(account, used, used != nil, settings.UsedPercentThreshold)
	if !freeQuotaCurrent {
		// Current Responses auth, quota and health evidence still matters even
		// when it has no quota headers. A transport failure provides no new
		// evidence, so retain the incomplete decision without using old quota.
		if freeProbe != nil && freeProbe.err == nil {
			return s.applyXAIDeepProbeOutcome(ctx, account, decision, status, *freeProbe)
		}
		return decision, status, nil
	}
	if settings.XAIDeepProbeEnabled && (freeProbeConfirm || shouldConfirmInspection(ctx, decision)) {
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
	defer inspectionMetricsStartConfirmation(ctx)()
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
		probeDecision := accountInspectionDecision{Action: accountInspectionActionDisable, ActionReason: "xAI 深度检测返回额度不可用，建议建立额度保护", UsedPercent: decision.UsedPercent, IsQuota: true, ErrorDetail: errorDetail, DeepProbeStatus: accountInspectionDeepProbeQuota, DeepProbeError: message}
		if account.Disabled {
			probeDecision.Action = accountInspectionActionKeep
			probeDecision.ActionReason = "xAI 深度检测返回额度不可用，但账号已禁用"
		}
		s.appendLog("warning", fmt.Sprintf("%s xAI 深度检测额度不可用：%s", account.identity(), message))
		return probeDecision, probeStatus, nil
	default:
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
