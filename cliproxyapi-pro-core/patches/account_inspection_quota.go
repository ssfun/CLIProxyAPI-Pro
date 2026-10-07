package management

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage"
	coreauth "github.com/router-for-me/CLIProxyAPI/v6/sdk/cliproxy/auth"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
)

func quotaSuccessState(values map[string]any) map[string]any {
	return proquota.SuccessCacheState(accountInspectionQuotaParserVersion, values)
}

func (s *accountInspectionScheduler) persistQuotaState(ctx context.Context, account accountInspectionAccount, state map[string]any) {
	if err := persistQuotaState(ctx, account, state); err != nil {
		s.appendLog("warning", fmt.Sprintf("%s 配额缓存写入失败：%s", account.identity(), err.Error()))
		return
	}
	s.markAccountPoliciesForRefresh()
	if err := s.cleanupLegacyQuotaCacheFromAuth(ctx, account); err != nil {
		s.appendLog("warning", fmt.Sprintf("%s 旧认证文件配额缓存清理失败：%s", account.identity(), err.Error()))
	}
}

func (s *accountInspectionScheduler) markAccountPoliciesForRefresh() {
	if s == nil {
		return
	}
	s.mu.Lock()
	s.policyRefreshPending = true
	s.mu.Unlock()
}

func (s *accountInspectionScheduler) refreshAccountPoliciesIfQuotaChanged() {
	if s == nil {
		return
	}
	s.mu.Lock()
	pending := s.policyRefreshPending
	s.policyRefreshPending = false
	handler := s.h
	s.mu.Unlock()
	if !pending || handler == nil {
		return
	}
	if application := handler.proApplication(); application != nil {
		application.RefreshAccountPolicies()
	}
}

func (s *accountInspectionScheduler) cleanupLegacyQuotaCacheFromAuth(ctx context.Context, account accountInspectionAccount) error {
	if s == nil || s.h == nil || s.inspectionAuthManager() == nil || account.AuthIndex == "" {
		return nil
	}
	auth := s.h.authByIndex(account.AuthIndex)
	if auth == nil || auth.Metadata == nil {
		return nil
	}
	if _, exists := auth.Metadata["quota_cache"]; !exists {
		return nil
	}
	return s.h.updateProAuth(ctx, account.AuthIndex, func(auth *coreauth.Auth) {
		if auth.Metadata == nil {
			return
		}
		delete(auth.Metadata, "quota_cache")
		auth.UpdatedAt = time.Now()
	})
}

func (s *accountInspectionScheduler) cleanupLegacyQuotaCaches(ctx context.Context) {
	if s == nil || s.h == nil || s.inspectionAuthManager() == nil {
		return
	}
	for _, auth := range s.inspectionAuthManager().List() {
		if auth == nil || auth.Metadata == nil {
			continue
		}
		if _, exists := auth.Metadata["quota_cache"]; !exists {
			continue
		}
		account := accountFromAuth(auth)
		if err := s.cleanupLegacyQuotaCacheFromAuth(ctx, account); err != nil {
			s.appendLog("warning", fmt.Sprintf("%s 启动清理旧认证文件配额缓存失败：%s", account.identity(), err.Error()))
		}
	}
}

func persistQuotaState(ctx context.Context, account accountInspectionAccount, state map[string]any) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	now := time.Now().UnixMilli()
	observedAt := now
	if cachedAt, ok := intFromAny(state["cachedAt"]); ok && cachedAt > 0 {
		observedAt = int64(cachedAt)
	}
	version := 1
	if schemaVersion, ok := intFromAny(state["schemaVersion"]); ok && schemaVersion > 0 {
		version = schemaVersion
	}
	fingerprintSource := strings.Join([]string{
		strings.ToLower(strings.TrimSpace(account.Provider)),
		strings.ToLower(strings.TrimSpace(account.FileName)),
		strings.ToLower(strings.TrimSpace(account.Email)),
		strings.ToLower(strings.TrimSpace(account.Name)),
	}, "|")
	fingerprint := sha256.Sum256([]byte(fingerprintSource))
	entry := embeddedusage.QuotaCacheEntry{
		ID:                  account.Provider + ":" + account.FileName,
		Provider:            account.Provider,
		FileName:            account.FileName,
		AuthIndex:           account.AuthIndex,
		IdentityFingerprint: hex.EncodeToString(fingerprint[:]),
		Data:                raw,
		CachedAt:            observedAt,
		ObservedAt:          observedAt,
		AccessedAt:          now,
		Version:             version,
	}
	if strings.EqualFold(strings.TrimSpace(account.Provider), "xai") {
		return embeddedusage.MergeXAIQuotaCache(ctx, entry)
	}
	return embeddedusage.SetQuotaCache(ctx, entry)
}

func mergeCachedXAIFreeQuota(ctx context.Context, account accountInspectionAccount, billing map[string]any) map[string]any {
	state, ok, err := embeddedusage.GetXAIQuotaState(ctx, account.FileName)
	if err != nil || !ok {
		return billing
	}
	cachedBilling := firstMap(state, "billing")
	freeQuota := firstMap(cachedBilling, "freeQuota", "free_quota")
	if freeQuota == nil {
		return billing
	}
	if billing == nil {
		billing = proquota.EmptyXAIBillingSummary()
	}
	billing["freeQuota"] = freeQuota
	return billing
}

const xaiFreeQuotaRefreshInterval = 15 * time.Minute

func shouldRefreshXAIFreeQuota(billing map[string]any, now time.Time, trigger inspectionProbeTrigger) bool {
	if trigger == inspectionTriggerManual || trigger == inspectionTriggerRecovery {
		return true
	}
	freeQuota := firstMap(billing, "freeQuota", "free_quota")
	if freeQuota == nil {
		return true
	}
	observedAt, ok := intFromAny(freeQuota["observedAt"])
	if !ok || observedAt <= 0 {
		return true
	}
	if now.IsZero() {
		now = time.Now()
	}
	age := now.Sub(time.UnixMilli(int64(observedAt)))
	return age < 0 || age >= xaiFreeQuotaRefreshInterval
}

func observeAccountXAIQuota(ctx context.Context, account accountInspectionAccount, model string, result accountInspectionHTTPResult) map[string]any {
	observedAt := time.Now()
	_ = embeddedusage.ObserveXAIQuotaResponse(ctx, embeddedusage.XAIQuotaObservation{
		FileName:   account.FileName,
		AuthIndex:  account.AuthIndex,
		Email:      account.Email,
		Label:      firstNonEmptyStringValue(account.Name, account.DisplayName),
		Model:      model,
		Status:     result.StatusCode,
		Header:     result.Header,
		Body:       []byte(result.Body),
		ObservedAt: observedAt,
	})
	var freeQuota map[string]any
	if proquota.XAIHeadersStatus(result.StatusCode) {
		freeQuota = proquota.XAIRateLimitSnapshot(result.Header, model, observedAt)
	}
	if proquota.XAIFreeQuotaExhausted([]byte(result.Body)) {
		freeQuota = proquota.XAIExhaustedQuotaSnapshot([]byte(result.Body), model, observedAt)
	}
	return freeQuota
}

func xaiPlanTypeFromAccessToken(auth *coreauth.Auth) (string, bool) {
	if auth == nil {
		return "", false
	}
	return proquota.XAIPlanTypeFromAccessToken(firstNonEmptyAuthValue(auth, "access_token", "accessToken"))
}

func intFromAny(value any) (int, bool) {
	parsed, ok := floatFromAny(value)
	if !ok {
		return 0, false
	}
	return int(parsed), true
}

func antigravityProjectID(auth *coreauth.Auth) string {
	for _, source := range []map[string]any{auth.Metadata, nestedMap(auth.Metadata, "installed"), nestedMap(auth.Metadata, "web")} {
		if source == nil {
			continue
		}
		if value := firstNonEmptyStringValue(stringFromAny(source["project_id"]), stringFromAny(source["projectId"])); value != "" {
			return value
		}
	}
	return "bamboo-precept-lgxtn"
}

func codexAccountID(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, source := range []map[string]any{auth.Metadata, stringMapToAnyMap(auth.Attributes)} {
		if value := codexAccountIDFromMap(source); value != "" {
			return value
		}
	}
	return ""
}

func codexAccountIDFromMap(source map[string]any) string {
	if source == nil {
		return ""
	}
	for _, key := range []string{"chatgpt_account_id", "chatgptAccountId", "account_id", "accountId"} {
		if value := stringFromAny(source[key]); value != "" {
			return value
		}
	}
	return idTokenClaim(source["id_token"], "chatgpt_account_id", "chatgptAccountId", "account_id", "accountId")
}

func xaiUserID(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, source := range []map[string]any{auth.Metadata, stringMapToAnyMap(auth.Attributes)} {
		if value := xaiUserIDFromMap(source); value != "" {
			return value
		}
	}
	return ""
}

func xaiUserIDFromMap(source map[string]any) string {
	if source == nil {
		return ""
	}
	for _, key := range []string{"x_user_id", "xUserId", "user_id", "userId", "subject", "sub", "id"} {
		if value := stringFromAny(source[key]); value != "" {
			return value
		}
	}
	return idTokenStringClaim(source["id_token"], "sub", "id", "user_id", "userId")
}

func stringMapToAnyMap(values map[string]string) map[string]any {
	if len(values) == 0 {
		return nil
	}
	result := make(map[string]any, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

func codexPlanType(auth *coreauth.Auth, payload map[string]any) any {
	if value := firstNonEmptyStringValue(stringFromAny(payload["plan_type"]), stringFromAny(payload["planType"])); value != "" {
		return value
	}
	for _, raw := range []any{auth.Metadata["plan_type"], auth.Metadata["planType"], auth.Attributes["plan_type"], auth.Attributes["planType"]} {
		if value := stringFromAny(raw); value != "" {
			return value
		}
	}
	return nil
}

func codexQuotaStateValues(auth *coreauth.Auth, payload map[string]any, windows []map[string]any, rawBody string) map[string]any {
	values := map[string]any{
		"windows":      windows,
		"planType":     codexPlanType(auth, payload),
		"rawShapeHash": proquota.JSONShapeHash(rawBody),
	}
	values["subscriptionActiveUntil"] = codexSubscriptionActiveUntil(auth)
	credits := firstMap(payload, "credits")
	values["creditBalance"] = codexCreditBalance(credits["balance"])
	values["creditsUnlimited"] = credits["unlimited"] == true
	summary, _ := codexResetCreditsSummary(firstMap(payload, "rate_limit_reset_credits", "rateLimitResetCredits"), time.Now())
	values["rateLimitResetCreditsAvailableCount"] = summary["availableCount"]
	values["rateLimitResetCreditsApplicableAvailableCount"] = summary["applicableAvailableCount"]
	values["rateLimitResetCredits"] = summary["credits"]
	values["rateLimitResetCreditsError"] = ""
	return values
}

const codexInspectionDetailsTTL = 15 * time.Minute

func codexCreditBalance(value any) any {
	text := codexQuotaString(value)
	if text == "" {
		return nil
	}
	dots := 0
	for _, char := range text {
		if char == '.' {
			dots++
			continue
		}
		if char < '0' || char > '9' {
			return nil
		}
	}
	if dots > 1 || strings.HasPrefix(text, ".") || strings.HasSuffix(text, ".") {
		return nil
	}
	parsed, err := strconv.ParseFloat(text, 64)
	if err != nil || math.IsNaN(parsed) || math.IsInf(parsed, 0) {
		return nil
	}
	return text
}

func codexQuotaString(value any) string {
	switch typed := value.(type) {
	case float64:
		if math.IsNaN(typed) || math.IsInf(typed, 0) {
			return ""
		}
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case int64:
		return strconv.FormatInt(typed, 10)
	default:
		return stringFromAny(value)
	}
}

func codexResetCreditsSummary(payload map[string]any, now time.Time) (map[string]any, bool) {
	credits := make([]map[string]any, 0)
	summary := map[string]any{"availableCount": nil, "applicableAvailableCount": nil, "credits": credits}
	valid := false
	if raw, present := payload["credits"]; present {
		if _, ok := raw.([]any); !ok {
			return summary, false
		}
		valid = true
	}
	for key, aliases := range map[string][]string{"availableCount": {"available_count", "availableCount"}, "applicableAvailableCount": {"applicable_available_count", "applicableAvailableCount"}} {
		raw := firstAny(payload, aliases...)
		if raw == nil {
			continue
		}
		if count, ok := floatFromAny(raw); ok && count >= 0 && !math.IsInf(count, 0) && !math.IsNaN(count) {
			summary[key] = count
			valid = true
		} else {
			return summary, false
		}
	}
	items, _ := payload["credits"].([]any)
	for _, item := range items {
		credit, ok := item.(map[string]any)
		if !ok || stringFromAny(firstAny(credit, "reset_type", "resetType")) != "codex_rate_limits" || stringFromAny(credit["status"]) != "available" {
			continue
		}
		expires := codexQuotaString(firstAny(credit, "expires_at", "expiresAt"))
		if !codexResetCreditUnexpired(expires, now) {
			continue
		}
		credits = append(credits, map[string]any{"id": codexQuotaString(credit["id"]), "status": "available", "grantedAt": codexQuotaString(firstAny(credit, "granted_at", "grantedAt")), "expiresAt": expires})
	}
	summary["credits"] = credits
	return summary, valid
}

func codexResetCreditUnexpired(value string, now time.Time) bool {
	if timestamp, err := time.Parse(time.RFC3339Nano, value); err == nil {
		return timestamp.After(now)
	}
	if timestamp, err := strconv.ParseFloat(value, 64); err == nil && timestamp > 0 && !math.IsInf(timestamp, 0) && !math.IsNaN(timestamp) {
		if timestamp < 1e12 {
			timestamp *= 1000
		}
		return timestamp > float64(now.UnixMilli())
	}
	return false
}

// A failed optional request may retain details, but never renew their age. The
// binding prevents same-file replacement credentials inheriting reset credits.
// Manual UI cache writes omit the binding and auth index: accept them only
// when observed after the current auth update, rejecting any conflicting index.
func cachedCodexInspectionDetails(ctx context.Context, account accountInspectionAccount) map[string]any {
	entries, err := embeddedusage.GetQuotaCache(ctx, "codex", account.FileName)
	if err != nil || len(entries) != 1 || account.Auth == nil || (entries[0].AuthIndex != "" && entries[0].AuthIndex != account.AuthIndex) {
		return nil
	}
	var state map[string]any
	if json.Unmarshal(entries[0].Data, &state) != nil || state["status"] != "success" {
		return nil
	}
	if binding := stringFromAny(state["codexCredentialFingerprint"]); binding != "" {
		if binding != account.CredentialFingerprint || stringFromAny(state["codexAccessTokenSHA256"]) != account.AccessTokenSHA256 {
			return nil
		}
	} else {
		if entries[0].ObservedAt < account.Auth.UpdatedAt.UnixMilli() {
			return nil
		}
		// UI writes are direct observations. Bound Core caches from older
		// versions may have renewed cachedAt while retaining an old date,
		// so only an unbound UI entry can supply the missing timestamp.
		if _, ok := state["subscriptionObservedAt"]; !ok {
			state["subscriptionObservedAt"] = entries[0].ObservedAt
		}
	}
	if _, ok := state["rateLimitResetCreditsObservedAt"]; !ok {
		state["rateLimitResetCreditsObservedAt"] = entries[0].ObservedAt
	}
	return state
}

func (s *accountInspectionScheduler) enrichCodexInspectionQuota(ctx context.Context, account accountInspectionAccount, settings accountInspectionSettings, values map[string]any) error {
	if ctx == nil {
		ctx = context.Background()
	}
	previous := cachedCodexInspectionDetails(ctx, account)
	headers := map[string]string{"Authorization": "Bearer $TOKEN$", "Content-Type": "application/json", "User-Agent": s.codexUserAgent(), "Chatgpt-Account-Id": codexAccountID(account.Auth)}
	resetHeaders := make(map[string]string, len(headers)+3)
	for key, value := range headers {
		resetHeaders[key] = value
	}
	resetHeaders["Accept"], resetHeaders["OpenAI-Beta"], resetHeaders["Originator"] = "application/json", "codex-1", "Codex Desktop"
	timeout := settings.Timeout
	if timeout <= 0 || timeout > 8000 {
		timeout = 8000
	}
	var subscription, resets accountInspectionHTTPResult
	var subscriptionErr, resetsErr error
	var probes sync.WaitGroup
	probes.Add(2)
	go func() {
		defer probes.Done()
		subscription, subscriptionErr = s.apiCall(ctx, account.Auth, http.MethodGet, "https://chatgpt.com/backend-api/subscriptions?account_id="+url.QueryEscape(codexAccountID(account.Auth)), headers, "", timeout)
	}()
	go func() {
		defer probes.Done()
		resets, resetsErr = s.apiCall(ctx, account.Auth, http.MethodGet, "https://chatgpt.com/backend-api/wham/rate-limit-reset-credits", resetHeaders, "", timeout)
	}()
	probes.Wait()
	if err := ctx.Err(); err != nil {
		return err
	}
	if s.inspectionAuthManager() != nil && !accountInspectionResultMatchesAuth(account.baseResult(), s.h.authByIndex(account.AuthIndex)) {
		return coreauth.ErrInspectionAuthChanged
	}
	var subscriptionPayload map[string]any
	var liveActiveUntil any
	if subscriptionErr == nil && subscription.StatusCode >= 200 && subscription.StatusCode < 300 && json.Unmarshal([]byte(subscription.Body), &subscriptionPayload) == nil {
		if activeUntil := dateLikeValue(firstAny(subscriptionPayload, "active_until", "activeUntil")); activeUntil != nil {
			liveActiveUntil = activeUntil
		}
	}
	if liveActiveUntil != nil {
		values["subscriptionActiveUntil"] = liveActiveUntil
		values["subscriptionObservedAt"] = time.Now().UnixMilli()
	} else if previous != nil && previous["subscriptionActiveUntil"] != nil {
		observedAt, _ := intFromAny(previous["subscriptionObservedAt"])
		age := time.Since(time.UnixMilli(int64(observedAt)))
		if observedAt > 0 && age >= 0 && age < codexInspectionDetailsTTL && int64(observedAt) >= account.Auth.UpdatedAt.UnixMilli() {
			values["subscriptionActiveUntil"] = previous["subscriptionActiveUntil"]
			values["subscriptionObservedAt"] = observedAt
		}
	}
	var resetPayload map[string]any
	var summary map[string]any
	valid := false
	if resetsErr == nil && resets.StatusCode >= 200 && resets.StatusCode < 300 && json.Unmarshal([]byte(resets.Body), &resetPayload) == nil {
		summary, valid = codexResetCreditsSummary(resetPayload, time.Now())
	}
	if valid {
		credits := summary["credits"].([]map[string]any)
		values["rateLimitResetCredits"] = credits
		values["rateLimitResetCreditsObservedAt"] = time.Now().UnixMilli()
		if count := summary["availableCount"]; count != nil {
			values["rateLimitResetCreditsAvailableCount"] = count
		} else if len(credits) > 0 {
			values["rateLimitResetCreditsAvailableCount"] = len(credits)
		}
		if values["rateLimitResetCreditsApplicableAvailableCount"] == nil {
			values["rateLimitResetCreditsApplicableAvailableCount"] = summary["applicableAvailableCount"]
		}
	} else {
		detailError := "invalid reset credits payload"
		if resetsErr != nil {
			detailError = resetsErr.Error()
		} else if resets.StatusCode < 200 || resets.StatusCode >= 300 {
			detailError = fmt.Sprintf("HTTP %d", resets.StatusCode)
		}
		values["rateLimitResetCreditsError"] = detailError
		observedAt, _ := intFromAny(previous["rateLimitResetCreditsObservedAt"])
		age := time.Since(time.UnixMilli(int64(observedAt)))
		count, countKnown := floatFromAny(values["rateLimitResetCreditsAvailableCount"])
		if previous != nil && observedAt > 0 && age >= 0 && age < codexInspectionDetailsTTL && (!countKnown || count > 0) {
			credits := make([]map[string]any, 0)
			items, _ := previous["rateLimitResetCredits"].([]any)
			for _, raw := range items {
				if credit, ok := raw.(map[string]any); ok && stringFromAny(credit["status"]) == "available" && codexResetCreditUnexpired(stringFromAny(credit["expiresAt"]), time.Now()) {
					credits = append(credits, credit)
				}
			}
			values["rateLimitResetCredits"] = credits
			values["rateLimitResetCreditsObservedAt"] = observedAt
			if values["rateLimitResetCreditsAvailableCount"] == nil && len(credits) > 0 {
				values["rateLimitResetCreditsAvailableCount"] = len(credits)
			}
		} else {
			values["rateLimitResetCredits"] = []map[string]any{}
		}
	}
	if values["rateLimitResetCreditsApplicableAvailableCount"] == nil {
		values["rateLimitResetCreditsApplicableAvailableCount"] = values["rateLimitResetCreditsAvailableCount"]
	}
	values["codexCredentialFingerprint"], values["codexAccessTokenSHA256"] = account.CredentialFingerprint, account.AccessTokenSHA256
	return nil
}

func codexSubscriptionActiveUntil(auth *coreauth.Auth) any {
	if auth == nil {
		return nil
	}
	for _, source := range []map[string]any{auth.Metadata, stringMapToAnyMap(auth.Attributes)} {
		if value := codexSubscriptionActiveUntilFromMap(source); value != nil {
			return value
		}
	}
	return nil
}

func codexSubscriptionActiveUntilFromMap(source map[string]any) any {
	if source == nil {
		return nil
	}
	for _, key := range []string{"chatgpt_subscription_active_until", "chatgptSubscriptionActiveUntil", "subscription_active_until", "subscriptionActiveUntil"} {
		if value := dateLikeValue(source[key]); value != nil {
			return value
		}
	}
	for _, rawSubscription := range []any{source["subscription"], source["Subscription"]} {
		if subscription, ok := rawSubscription.(map[string]any); ok {
			for _, key := range []string{"active_until", "activeUntil"} {
				if value := dateLikeValue(subscription[key]); value != nil {
					return value
				}
			}
		}
	}
	if value := idTokenClaimAny(source["id_token"], "chatgpt_subscription_active_until", "chatgptSubscriptionActiveUntil", "subscription_active_until", "subscriptionActiveUntil"); value != nil {
		return value
	}
	return nil
}

func dateLikeValue(value any) any {
	if number, ok := floatFromAny(value); ok {
		if number == 0 {
			return nil
		}
		return value
	}
	if text := stringFromAny(value); text != "" && text != "0" {
		return text
	}
	return nil
}

func idTokenClaim(raw any, keys ...string) string {
	value := idTokenClaimAny(raw, keys...)
	if text := stringFromAny(value); text != "" {
		return text
	}
	return ""
}

func idTokenClaimAny(raw any, keys ...string) any {
	switch value := raw.(type) {
	case map[string]any:
		for _, key := range keys {
			if claim := dateLikeValue(value[key]); claim != nil {
				return claim
			}
		}
		return nil
	}
	token := stringFromAny(raw)
	if token == "" {
		return nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(token), &parsed); err == nil {
		for _, key := range keys {
			if value := dateLikeValue(parsed[key]); value != nil {
				return value
			}
		}
		return nil
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil
	}
	var data map[string]any
	if err := json.Unmarshal(payload, &data); err != nil {
		return nil
	}
	for _, key := range keys {
		if value := dateLikeValue(data[key]); value != nil {
			return value
		}
	}
	return nil
}

func firstAny(data map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := data[key]; ok {
			return value
		}
	}
	return nil
}

func firstMap(data map[string]any, keys ...string) map[string]any {
	for _, key := range keys {
		if value, ok := data[key].(map[string]any); ok {
			return value
		}
	}
	return nil
}

func nestedMap(data map[string]any, key string) map[string]any {
	if data == nil {
		return nil
	}
	value, _ := data[key].(map[string]any)
	return value
}

func floatFromAny(value any) (float64, bool) {
	switch v := value.(type) {
	case float64:
		return v, true
	case float32:
		return float64(v), true
	case int:
		return float64(v), true
	case int64:
		return float64(v), true
	case json.Number:
		parsed, err := v.Float64()
		return parsed, err == nil
	case string:
		parsed, err := strconv.ParseFloat(strings.TrimSpace(v), 64)
		return parsed, err == nil
	default:
		return 0, false
	}
}

func emptyStringAsNil(value string) any {
	if strings.TrimSpace(value) == "" {
		return nil
	}
	return value
}

func escapeJSONString(value string) string {
	raw, _ := json.Marshal(value)
	return strings.Trim(string(raw), "\"")
}
