package management

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
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
	values["rateLimitResetCreditsAvailableCount"] = codexRateLimitResetCreditsAvailableCount(payload)
	return values
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

func codexRateLimitResetCreditsAvailableCount(payload map[string]any) any {
	if payload == nil {
		return nil
	}
	resetCredits, _ := firstAny(payload, "rate_limit_reset_credits", "rateLimitResetCredits").(map[string]any)
	if resetCredits == nil {
		return nil
	}
	if value, ok := floatFromAny(firstAny(resetCredits, "available_count", "availableCount")); ok {
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
