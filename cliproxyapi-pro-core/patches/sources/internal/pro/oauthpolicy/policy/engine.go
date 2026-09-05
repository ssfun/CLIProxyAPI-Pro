package policy

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/misc"
	proinspection "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/inspection"
	modelconfig "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/oauthpolicy/config"
	proquota "github.com/router-for-me/CLIProxyAPI/v7/internal/pro/quota"
	upstreamexecutor "github.com/router-for-me/CLIProxyAPI/v7/internal/runtime/executor"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
)

const (
	xaiBillingURL            = "https://cli-chat-proxy.grok.com/v1/billing"
	claudeProfileURL         = "https://api.anthropic.com/api/oauth/profile"
	geminiCodeAssistURL      = "https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
	antigravityCodeAssistURL = "https://daily-cloudcode-pa.googleapis.com/v1internal:loadCodeAssist"
)

type ModelInfo struct {
	ID string
}

type HTTPRequest struct {
	Method  string
	URL     string
	Headers http.Header
	Body    []byte
}

type HTTPResponse struct {
	StatusCode int
	Headers    http.Header
	Body       []byte
}

type HTTPDo func(context.Context, HTTPRequest) (HTTPResponse, error)

type Input struct {
	AuthID             string
	AuthIndex          string
	FileName           string
	AuthProvider       string
	AuthKind           string
	StorageJSON        []byte
	Metadata           map[string]any
	Attributes         map[string]string
	AuthPrefix         string
	Models             []ModelInfo
	HTTPDo             HTTPDo
	QuotaSnapshotJSON  []byte
	QuotaObservedAtMS  int64
	QuotaSnapshotError string
}

type Result struct {
	Handled          bool
	ExcludedModelIDs []string
	Prefix           *string
	Priority         *int
	Weight           *int64
	Annotations      map[string]string
}

type EffectivePolicy struct {
	AuthID        string `json:"authId"`
	Provider      string `json:"provider"`
	PlanKey       string `json:"planKey"`
	PlanSource    string `json:"planSource"`
	MatchedRule   string `json:"matchedRule"`
	Prefix        string `json:"prefix"`
	Priority      int    `json:"priority"`
	Weight        int64  `json:"weight"`
	ExcludedCount int    `json:"excludedModelCount"`
	PlanError     string `json:"planError,omitempty"`
}

type cacheEntry struct {
	Plan       string
	ObservedAt time.Time
}

type Engine struct {
	mu         sync.RWMutex
	cfg        modelconfig.Config
	cache      map[string]cacheEntry
	authEpochs map[string]uint64
}

func New() *Engine {
	cfg, _ := modelconfig.Parse(nil)
	return &Engine{cfg: cfg, cache: make(map[string]cacheEntry), authEpochs: make(map[string]uint64)}
}

func (e *Engine) ApplyConfig(cfg modelconfig.Config) {
	e.mu.Lock()
	e.cfg = cfg
	e.cache = make(map[string]cacheEntry)
	e.authEpochs = make(map[string]uint64)
	e.mu.Unlock()
}

// ForgetAuth drops cached plan state and prevents an in-flight lookup that
// started before account removal from repopulating it.
func (e *Engine) ForgetAuth(authID string) {
	if e == nil || strings.TrimSpace(authID) == "" {
		return
	}
	e.mu.Lock()
	e.authEpochs[authID]++
	suffix := "\x00" + authID
	for key := range e.cache {
		if strings.HasSuffix(key, suffix) {
			delete(e.cache, key)
		}
	}
	e.mu.Unlock()
}

func (e *Engine) Filter(ctx context.Context, input Input) Result {
	provider := normalizeKey(input.AuthProvider)
	if normalizeKey(input.AuthKind) != "oauth" {
		return Result{}
	}
	e.mu.RLock()
	cfg := e.cfg
	providerCfg, configured := cfg.Providers[provider]
	e.mu.RUnlock()
	if !cfg.Enabled || !configured || len(providerCfg.Plans) == 0 {
		return Result{}
	}

	plan, source, resolveErr := e.resolvePlan(ctx, provider, cfg, input)
	rule, matchedPlan, matched := ruleForPlan(providerCfg, plan)
	if !matched {
		return Result{}
	}
	excluded := matchExcludedModels(input.Models, rule.ExcludedModels)
	annotations := map[string]string{
		"plan_key":     plan,
		"plan_source":  source,
		"matched_rule": matchedPlan,
	}
	if resolveErr != nil {
		annotations["plan_error"] = resolveErr.Error()
	}
	return Result{
		Handled: true, ExcludedModelIDs: excluded,
		Prefix: rule.Prefix, Priority: rule.Priority, Weight: rule.Weight,
		Annotations: annotations,
	}
}

func (e *Engine) resolvePlan(ctx context.Context, provider string, cfg modelconfig.Config, input Input) (string, string, error) {
	if provider == "gemini-cli" || provider == "antigravity" {
		if plan := strongGoogleLocalPlan(provider, input); plan != "" && plan != "unknown" {
			return plan, "auth", nil
		}
	}
	quotaPlan, quotaSource, quotaErr := "", "quota-cache", error(nil)
	if provider != "xai" {
		quotaPlan, quotaSource, quotaErr = planFromQuotaSnapshot(provider, input)
	}
	quotaFresh := quotaErr == nil && quotaPlan != "" && quotaPlan != "unknown" && snapshotIsFresh(snapshotObservedAtMS(input), cfg.CacheTTL)
	// A flattened Gemini snapshot cannot tell whether standard-tier came from
	// currentTier or from a paidTier whose display name was dropped. The official
	// client gives paidTier precedence, so re-resolve this ambiguous value before
	// accepting it as the account's effective plan.
	if provider == "gemini-cli" && quotaPlan == "standard" {
		quotaFresh = false
	}
	if (provider == "gemini-cli" || provider == "antigravity") && quotaFresh {
		return quotaPlan, quotaSource, nil
	}
	localPlanValue := localPlan(provider, input)
	deferredLocalPlan := ""
	if localPlanValue != "" && localPlanValue != "unknown" {
		// currentTier=standard-tier does not prove that a Gemini account has no
		// paidTier. Give the provider response a chance to supply the authoritative
		// paid plan, while retaining the local tier as a fallback when lookup fails.
		deferGeminiStandard := provider == "gemini-cli" &&
			localPlanValue == "standard" &&
			input.HTTPDo != nil && accessToken(input) != "" && projectID(input) != ""
		if deferGeminiStandard {
			deferredLocalPlan = localPlanValue
		} else {
			return localPlanValue, "auth", nil
		}
	}
	if provider != "xai" && quotaFresh {
		return quotaPlan, quotaSource, nil
	}
	useCache := provider != "xai"
	now := time.Now()
	cacheKey := provider + "\x00" + input.AuthID
	e.mu.RLock()
	cached, hasCache := e.cache[cacheKey]
	authEpoch := e.authEpochs[input.AuthID]
	e.mu.RUnlock()
	if useCache && hasCache && now.Sub(cached.ObservedAt) <= cfg.CacheTTL {
		return cached.Plan, "cache", nil
	}
	plan, errResolve := resolveProviderPlan(ctx, provider, cfg.ResolveTimeout, input)
	if errResolve == nil && plan != "" {
		if useCache {
			e.mu.Lock()
			if e.authEpochs[input.AuthID] == authEpoch {
				e.cache[cacheKey] = cacheEntry{Plan: plan, ObservedAt: now}
			}
			e.mu.Unlock()
		}
		source := "provider-api"
		if provider == "xai" {
			source = "billing"
		}
		return plan, source, nil
	}
	if deferredLocalPlan != "" {
		return deferredLocalPlan, "auth", errResolve
	}
	if provider != "xai" && quotaPlan != "" && quotaPlan != "unknown" {
		return quotaPlan, "stale-" + quotaSource, combinePlanErrors(errResolve, quotaErr)
	}
	if useCache && hasCache && cached.Plan != "" {
		return cached.Plan, "stale-cache", errResolve
	}
	if errResolve == nil {
		errResolve = fmt.Errorf("%s plan is unavailable", provider)
	}
	return "unknown", "unknown", combinePlanErrors(errResolve, quotaErr)
}

func strongGoogleLocalPlan(provider string, input Input) string {
	sources := []map[string]any{input.Metadata, stringMapToAny(input.Attributes)}
	storage := map[string]any{}
	if len(input.StorageJSON) > 0 && json.Unmarshal(input.StorageJSON, &storage) == nil {
		sources = append(sources, storage)
	}
	for _, source := range sources {
		for _, key := range []string{"paidTier", "paid_tier"} {
			if tier, ok := source[key].(map[string]any); ok {
				if plan := googleTierPlan(provider, tier, true); plan != "" && plan != "unknown" {
					return plan
				}
			}
		}
	}
	return ""
}

func snapshotObservedAtMS(input Input) int64 {
	if input.QuotaObservedAtMS > 0 {
		return input.QuotaObservedAtMS
	}
	attributes, err := proquota.PlanEvidenceAttributes(input.AuthProvider, input.QuotaSnapshotJSON)
	if err != nil {
		return 0
	}
	payload := map[string]any{}
	if json.Unmarshal(attributes, &payload) == nil {
		if value, known := numberValue(firstValue(payload, "observed_at_ms", "observedAtMS", "cachedAt")); known {
			return int64(value)
		}
	}
	return 0
}

func snapshotIsFresh(observedAtMS int64, ttl time.Duration) bool {
	if observedAtMS <= 0 {
		return false
	}
	age := time.Since(time.UnixMilli(observedAtMS))
	return age >= -5*time.Minute && age <= ttl
}

func combinePlanErrors(primary, secondary error) error {
	if primary == nil {
		return secondary
	}
	if secondary == nil {
		return primary
	}
	return fmt.Errorf("%v; quota snapshot: %v", primary, secondary)
}

func planFromQuotaSnapshot(provider string, input Input) (string, string, error) {
	if len(input.QuotaSnapshotJSON) == 0 {
		if strings.TrimSpace(input.QuotaSnapshotError) != "" {
			return "", "quota-cache", fmt.Errorf("%s", input.QuotaSnapshotError)
		}
		return "", "quota-cache", nil
	}
	attributes, err := proquota.PlanEvidenceAttributes(provider, input.QuotaSnapshotJSON)
	if err != nil {
		return "", "quota-cache", err
	}
	payload := map[string]any{}
	if err := json.Unmarshal(attributes, &payload); err != nil {
		return "", "quota-cache", fmt.Errorf("decode snapshot: %w", err)
	}
	if status := normalizeKey(stringValue(payload["status"])); status != "" && status != "success" {
		return "", "quota-cache", fmt.Errorf("snapshot status is %s", status)
	}
	if rawPlan, ok := payload["plan"].(map[string]any); ok {
		if stale, _ := boolValue(rawPlan["stale"]); stale {
			return planFromQuotaPlan(provider, rawPlan), "quota-provider", fmt.Errorf("snapshot plan is stale")
		}
		if message := stringValue(rawPlan["error"]); message != "" {
			return planFromQuotaPlan(provider, rawPlan), "quota-provider", fmt.Errorf("%s", proinspection.HTTPErrorDetail(message))
		}
		if plan := planFromQuotaPlan(provider, rawPlan); plan != "" && plan != "unknown" {
			return plan, "quota-provider", nil
		}
	}
	if plan := planFromMap(provider, payload); plan != "" && plan != "unknown" {
		return plan, "quota-inspection", nil
	}
	return "", "quota-cache", fmt.Errorf("snapshot contains no supported plan")
}

func planFromQuotaPlan(provider string, plan map[string]any) string {
	if plan == nil {
		return ""
	}
	if provider == "antigravity" {
		if normalized := normalizeProviderPlan(provider, stringValue(plan["kind"])); isKnownAntigravityPlan(normalized) {
			return normalized
		}
		if normalized := normalizeAntigravityTierID(stringValue(plan["id"])); normalized != "unknown" {
			return normalized
		}
		return ""
	}
	keys := []string{"kind", "id", "label"}
	if provider == "gemini-cli" {
		keys = []string{"label", "kind", "id"}
	}
	for _, key := range keys {
		if normalized := normalizeProviderPlan(provider, stringValue(plan[key])); normalized != "" && normalized != "unknown" {
			return normalized
		}
	}
	return ""
}

func localPlan(provider string, input Input) string {
	if provider == "xai" {
		if plan, known := proquota.XAIPlanTypeFromAccessToken(accessToken(input)); known {
			return plan
		}
		return ""
	}
	sources := []map[string]any{input.Metadata, stringMapToAny(input.Attributes)}
	storage := map[string]any{}
	if len(input.StorageJSON) > 0 && json.Unmarshal(input.StorageJSON, &storage) == nil {
		sources = append(sources, storage)
	}
	for _, source := range sources {
		if plan := planFromMap(provider, source); plan != "" && plan != "unknown" {
			return plan
		}
	}
	return ""
}

func planFromMap(provider string, source map[string]any) string {
	if source == nil {
		return ""
	}
	for _, key := range []string{
		"plan_type", "planType", "plan", "package", "tier_id", "tierId",
		"tier", "tier_label", "tierLabel", "subscription_type", "subscriptionType",
		"chatgpt_plan_type",
	} {
		plan := normalizeProviderPlan(provider, stringValue(source[key]))
		if provider == "antigravity" {
			if isAntigravityTierEvidenceKey(key) {
				plan = normalizeAntigravityTierID(stringValue(source[key]))
			}
			if !isKnownAntigravityPlan(plan) {
				continue
			}
		}
		if plan != "" && plan != "unknown" {
			return plan
		}
	}
	if provider == "codex" {
		if plan := codexPlanFromIDToken(source["id_token"]); plan != "" {
			return plan
		}
		if plan := codexPlanFromIDToken(source["idToken"]); plan != "" {
			return plan
		}
	}
	if provider == "claude" {
		if plan := claudePlanFromMap(source); plan != "" {
			return plan
		}
	}
	if provider == "gemini-cli" || provider == "antigravity" {
		if plan := googlePlanFromMap(provider, source); plan != "" {
			return plan
		}
	}
	for _, key := range []string{"billing", "subscription", "paidTier", "paid_tier", "currentTier", "current_tier"} {
		if nested, ok := source[key].(map[string]any); ok {
			if plan := planFromMap(provider, nested); plan != "" {
				return plan
			}
		}
	}
	if billing, ok := source["billing"].(map[string]any); ok && provider == "xai" {
		if plan := planFromMap(provider, billing); plan != "" {
			return plan
		}
		if limit, known := numberValue(firstValue(billing, "monthlyLimitCents", "monthly_limit_cents", "monthlyLimit", "monthly_limit")); known {
			return xaiPlanFromLimit(limit)
		}
	}
	return ""
}

func resolveProviderPlan(ctx context.Context, provider string, timeout time.Duration, input Input) (string, error) {
	switch provider {
	case "xai":
		return resolveXAIPlan(ctx, timeout, input)
	case "claude":
		return resolveClaudePlan(ctx, timeout, input)
	case "gemini-cli", "antigravity":
		return resolveGooglePlan(ctx, provider, timeout, input)
	default:
		return "", fmt.Errorf("%s plan is unavailable in auth metadata", provider)
	}
}

func codexPlanFromIDToken(raw any) string {
	claims := tokenClaims(raw)
	if claims == nil {
		return ""
	}
	if authInfo, ok := claims["https://api.openai.com/auth"].(map[string]any); ok {
		if plan := normalizeProviderPlan("codex", stringValue(firstValue(authInfo, "chatgpt_plan_type", "plan_type", "planType"))); plan != "" {
			return plan
		}
	}
	return normalizeProviderPlan("codex", stringValue(firstValue(claims, "chatgpt_plan_type", "plan_type", "planType")))
}

func tokenClaims(raw any) map[string]any {
	if mapped, ok := raw.(map[string]any); ok {
		return mapped
	}
	token := stringValue(raw)
	if token == "" {
		return nil
	}
	claims := map[string]any{}
	if json.Unmarshal([]byte(token), &claims) == nil {
		return claims
	}
	parts := strings.Split(token, ".")
	if len(parts) < 2 {
		return nil
	}
	payload, errDecode := base64.RawURLEncoding.DecodeString(parts[1])
	if errDecode != nil || json.Unmarshal(payload, &claims) != nil {
		return nil
	}
	return claims
}

func claudePlanFromMap(source map[string]any) string {
	if source == nil {
		return ""
	}
	account, _ := source["account"].(map[string]any)
	if account == nil {
		account = source
	}
	if value, known := boolValue(account["has_claude_max"]); known && value {
		return "max"
	}
	if value, known := boolValue(account["has_claude_pro"]); known && value {
		return "pro"
	}
	organization, _ := source["organization"].(map[string]any)
	if strings.EqualFold(stringValue(organization["organization_type"]), "claude_team") &&
		strings.EqualFold(stringValue(organization["subscription_status"]), "active") {
		return "team"
	}
	max, maxKnown := boolValue(account["has_claude_max"])
	pro, proKnown := boolValue(account["has_claude_pro"])
	if maxKnown && proKnown && !max && !pro {
		return "free"
	}
	return ""
}

func resolveClaudePlan(ctx context.Context, timeout time.Duration, input Input) (string, error) {
	token := accessToken(input)
	if token == "" {
		return "", fmt.Errorf("claude access token is unavailable")
	}
	resp, errDo := doProviderRequest(ctx, timeout, input, HTTPRequest{
		Method: http.MethodGet,
		URL:    claudeProfileURL,
		Headers: http.Header{
			"Authorization":  []string{"Bearer " + token},
			"Content-Type":   []string{"application/json"},
			"anthropic-beta": []string{"oauth-2025-04-20"},
		},
	})
	if errDo != nil {
		return "", fmt.Errorf("fetch claude profile: %w", errDo)
	}
	payload := map[string]any{}
	if errUnmarshal := json.Unmarshal(resp.Body, &payload); errUnmarshal != nil {
		return "", fmt.Errorf("decode claude profile: %w", errUnmarshal)
	}
	if plan := claudePlanFromMap(payload); plan != "" {
		return plan, nil
	}
	return "", fmt.Errorf("claude profile contains no supported plan")
}

func resolveGooglePlan(ctx context.Context, provider string, timeout time.Duration, input Input) (string, error) {
	token := accessToken(input)
	if token == "" {
		return "", fmt.Errorf("%s access token is unavailable", provider)
	}
	url := antigravityCodeAssistURL
	body := map[string]any{"metadata": map[string]any{"ideType": "ANTIGRAVITY"}}
	if provider == "gemini-cli" {
		projectID := projectID(input)
		if projectID == "" {
			return "", fmt.Errorf("gemini-cli project_id is unavailable")
		}
		url = geminiCodeAssistURL
		body = map[string]any{
			"cloudaicompanionProject": projectID,
			"metadata": map[string]any{
				"ideType": "IDE_UNSPECIFIED", "platform": "PLATFORM_UNSPECIFIED",
				"pluginType": "GEMINI", "duetProject": projectID,
			},
		}
	}
	rawBody, errMarshal := json.Marshal(body)
	if errMarshal != nil {
		return "", fmt.Errorf("encode %s plan request: %w", provider, errMarshal)
	}
	headers := http.Header{
		"Authorization": []string{"Bearer " + token},
		"Accept":        []string{"application/json"},
		"Content-Type":  []string{"application/json"},
	}
	if provider == "antigravity" {
		headers.Set("Accept", "*/*")
		headers.Set("User-Agent", misc.AntigravityUserAgent())
	}
	resp, errDo := doProviderRequest(ctx, timeout, input, HTTPRequest{
		Method:  http.MethodPost,
		URL:     url,
		Headers: headers,
		Body:    rawBody,
	})
	if errDo != nil {
		return "", fmt.Errorf("fetch %s plan: %w", provider, errDo)
	}
	payload := map[string]any{}
	if errUnmarshal := json.Unmarshal(resp.Body, &payload); errUnmarshal != nil {
		return "", fmt.Errorf("decode %s plan: %w", provider, errUnmarshal)
	}
	if plan := googlePlanFromMap(provider, payload); plan != "" {
		return plan, nil
	}
	return "", fmt.Errorf("%s response contains no supported tier", provider)
}

func doProviderRequest(ctx context.Context, timeout time.Duration, input Input, request HTTPRequest) (HTTPResponse, error) {
	if input.HTTPDo == nil {
		return HTTPResponse{}, fmt.Errorf("host http client is unavailable")
	}
	requestCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	resp, errDo := input.HTTPDo(requestCtx, request)
	if errDo != nil {
		return HTTPResponse{}, errDo
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return HTTPResponse{}, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return resp, nil
}

func googlePlanFromMap(provider string, source map[string]any) string {
	if source == nil {
		return ""
	}
	if provider == "antigravity" {
		for _, key := range []string{"paidTier", "paid_tier"} {
			if tier, ok := source[key].(map[string]any); ok && stringValue(tier["id"]) != "" {
				return normalizeAntigravityTierID(stringValue(tier["id"]))
			}
		}
		for _, key := range []string{"currentTier", "current_tier"} {
			if tier, ok := source[key].(map[string]any); ok {
				return normalizeAntigravityTierID(stringValue(tier["id"]))
			}
		}
	}
	for _, key := range []string{"paidTier", "paid_tier", "currentTier", "current_tier"} {
		if tier, ok := source[key].(map[string]any); ok {
			if plan := googleTierPlan(provider, tier, key == "paidTier" || key == "paid_tier"); plan != "" && plan != "unknown" {
				return plan
			}
		}
	}
	for _, key := range []string{"allowedTiers", "allowed_tiers"} {
		for _, raw := range anySlice(source[key]) {
			tier, _ := raw.(map[string]any)
			isDefault, _ := boolValue(firstValue(tier, "isDefault", "is_default"))
			if !isDefault {
				continue
			}
			if plan := googleTierPlan(provider, tier, false); plan != "" && plan != "unknown" {
				return plan
			}
		}
	}
	if plan, ok := source["plan"].(map[string]any); ok {
		if normalized := planFromQuotaPlan(provider, plan); normalized != "" {
			return normalized
		}
	}
	for _, key := range []string{"body", "bodyText", "data", "response", "result"} {
		switch nested := source[key].(type) {
		case map[string]any:
			if plan := googlePlanFromMap(provider, nested); plan != "" {
				return plan
			}
		case string:
			decoded := map[string]any{}
			if json.Unmarshal([]byte(strings.TrimSpace(nested)), &decoded) == nil {
				if plan := googlePlanFromMap(provider, decoded); plan != "" {
					return plan
				}
			}
		}
	}
	return ""
}

func googleTierPlan(provider string, tier map[string]any, paid bool) string {
	id := stringValue(tier["id"])
	name := stringValue(tier["name"])
	if provider == "antigravity" {
		return normalizeAntigravityTierID(id)
	}
	if provider == "gemini-cli" && paid {
		if plan := normalizeProviderPlan(provider, name); plan != "" && plan != "unknown" && plan != "standard" {
			return plan
		}
	}
	if plan := normalizeProviderPlan(provider, id); plan != "" && plan != "unknown" {
		return plan
	}
	return normalizeProviderPlan(provider, name)
}

// normalizeAntigravityTierID mirrors Management's Antigravity subscription
// parser: tier names are display-only and only upstream tier IDs determine plan.
func normalizeAntigravityTierID(value string) string {
	switch normalizeKey(value) {
	case "free-tier":
		return "free"
	case "g1-pro-tier":
		return "pro"
	case "g1-ultra-tier":
		return "ultra"
	case "g1-ultra-lite-tier":
		return "ultra-lite"
	default:
		return "unknown"
	}
}

func isKnownAntigravityPlan(plan string) bool {
	switch plan {
	case "free", "pro", "ultra", "ultra-lite":
		return true
	default:
		return false
	}
}

func isAntigravityTierEvidenceKey(key string) bool {
	switch key {
	case "tier_id", "tierId", "tier", "tier_label", "tierLabel":
		return true
	default:
		return false
	}
}

func resolveXAIPlan(ctx context.Context, timeout time.Duration, input Input) (string, error) {
	if input.HTTPDo == nil {
		return "", fmt.Errorf("host http client is unavailable")
	}
	storage := map[string]any{}
	if len(input.StorageJSON) > 0 {
		if errUnmarshal := json.Unmarshal(input.StorageJSON, &storage); errUnmarshal != nil {
			return "", fmt.Errorf("decode xai auth storage: %w", errUnmarshal)
		}
	}
	sources := []map[string]any{storage, input.Metadata, stringMapToAny(input.Attributes)}
	auth := xaiPolicyAuth(input)
	if upstreamexecutor.XAIUsingAPI(auth) {
		return "paid-unknown", nil
	}
	token := accessToken(input)
	if token == "" {
		return "", fmt.Errorf("xai access token is unavailable")
	}
	userID := firstString(sources, "x_user_id", "xUserId", "user_id", "userId", "subject", "sub", "id")
	requestCtx := ctx
	var cancel context.CancelFunc
	if timeout > 0 {
		requestCtx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	headers := upstreamexecutor.XAIChatRequestHeaders(auth, token, false)
	if userID != "" {
		headers["x-userid"] = []string{userID}
	}
	resp, errDo := input.HTTPDo(requestCtx, HTTPRequest{Method: http.MethodGet, URL: xaiBillingURL, Headers: headers})
	if errDo != nil {
		return "", fmt.Errorf("fetch xai billing: %w", errDo)
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return "", fmt.Errorf("fetch xai billing returned HTTP %d", resp.StatusCode)
	}
	plan, known := proquota.XAIPlanTypeFromBillingBody(resp.StatusCode, string(resp.Body))
	if !known {
		return "", fmt.Errorf("xai billing contains no supported plan evidence")
	}
	return plan, nil
}

func xaiPlanFromLimit(limit float64) string {
	plan, _ := proquota.XAIPlanTypeFromMonthlyLimit(limit, true)
	return plan
}

func xaiPolicyAuth(input Input) *coreauth.Auth {
	attributes := make(map[string]string, len(input.Attributes)+1)
	for key, value := range input.Attributes {
		attributes[key] = value
	}
	if strings.TrimSpace(attributes["auth_kind"]) == "" {
		attributes["auth_kind"] = input.AuthKind
	}
	return &coreauth.Auth{
		ID:         input.AuthID,
		Provider:   input.AuthProvider,
		Attributes: attributes,
		Metadata:   input.Metadata,
	}
}

func ruleForPlan(provider modelconfig.Provider, plan string) (modelconfig.Plan, string, bool) {
	plan = normalizeKey(plan)
	keys := []string{plan}
	if plan == "" || plan == "unknown" {
		keys = append(keys, "_unknown")
	} else {
		keys = append(keys, "_default")
	}
	for _, key := range keys {
		key = normalizeKey(key)
		if rule, ok := provider.Plans[key]; ok {
			return rule, key, true
		}
	}
	return modelconfig.Plan{}, "", false
}

func matchExcludedModels(models []ModelInfo, patterns []string) []string {
	if len(models) == 0 || len(patterns) == 0 {
		return nil
	}
	out := make([]string, 0)
	for _, model := range models {
		modelID := strings.ToLower(strings.TrimSpace(model.ID))
		if modelID == "" {
			continue
		}
		for _, pattern := range patterns {
			matched, errMatch := path.Match(pattern, modelID)
			if errMatch == nil && matched {
				out = append(out, model.ID)
				break
			}
		}
	}
	return out
}

func normalizeProviderPlan(provider, value string) string {
	value = normalizeKey(value)
	if strings.HasPrefix(value, "plan-") {
		value = strings.TrimPrefix(value, "plan-")
	}
	switch provider {
	case "xai":
		switch value {
		case "super-grok":
			return "supergrok"
		case "super-grok-heavy":
			return "supergrok-heavy"
		}
	case "codex":
		if value == "prolite" {
			return "pro-lite"
		}
	case "gemini-cli":
		switch {
		case strings.Contains(value, "google-ai-ultra") || strings.Contains(value, "gemini-ultra"):
			return "ultra"
		case strings.Contains(value, "google-ai-pro") || strings.Contains(value, "gemini-ai-pro"):
			return "pro"
		case strings.Contains(value, "gemini-code-assist"):
			return "standard"
		}
		switch value {
		case "free-tier":
			return "free"
		case "legacy-tier":
			return "legacy"
		case "standard-tier":
			return "standard"
		case "g1-pro-tier", "pro-tier":
			return "pro"
		case "g1-ultra-tier", "ultra-tier":
			return "ultra"
		}
	case "antigravity":
		switch {
		case strings.Contains(value, "google-ai-ultra") || strings.Contains(value, "gemini-ultra"):
			return "ultra"
		case strings.Contains(value, "google-ai-pro") || strings.Contains(value, "gemini-ai-pro"):
			return "pro"
		}
		switch value {
		case "":
			return ""
		case "free", "free-tier":
			return "free"
		case "pro", "g1-pro-tier":
			return "pro"
		case "ultra", "g1-ultra-tier":
			return "ultra"
		case "ultra-lite", "g1-ultra-lite-tier":
			return "ultra-lite"
		case "standard", "standard-tier":
			return "unknown"
		default:
			// Provider plans are extensible and Management supports custom keys.
			// Preserve future or organization-specific tiers instead of silently
			// forcing their configured rules through _unknown.
			return value
		}
	}
	return value
}

func normalizeKey(value string) string {
	value = strings.ToLower(strings.TrimSpace(value))
	value = strings.Join(strings.Fields(value), "-")
	if strings.HasPrefix(value, "_") {
		return "_" + strings.ReplaceAll(strings.TrimPrefix(value, "_"), "_", "-")
	}
	return strings.ReplaceAll(value, "_", "-")
}

func stringValue(value any) string {
	switch typed := value.(type) {
	case string:
		return strings.TrimSpace(typed)
	case json.Number:
		return strings.TrimSpace(typed.String())
	default:
		return ""
	}
}

func numberValue(value any) (float64, bool) {
	switch typed := value.(type) {
	case map[string]any:
		return numberValue(typed["val"])
	case float64:
		return typed, true
	case float32:
		return float64(typed), true
	case int:
		return float64(typed), true
	case int64:
		return float64(typed), true
	case json.Number:
		parsed, errParse := typed.Float64()
		return parsed, errParse == nil
	case string:
		parsed, errParse := strconv.ParseFloat(strings.TrimSpace(typed), 64)
		return parsed, errParse == nil
	default:
		return 0, false
	}
}

func firstValue(source map[string]any, keys ...string) any {
	for _, key := range keys {
		if value, ok := source[key]; ok {
			return value
		}
	}
	return nil
}

func firstString(sources []map[string]any, keys ...string) string {
	for _, source := range sources {
		for _, key := range keys {
			if value := stringValue(source[key]); value != "" {
				return value
			}
		}
	}
	return ""
}

func accessToken(input Input) string {
	storage := map[string]any{}
	if len(input.StorageJSON) > 0 {
		_ = json.Unmarshal(input.StorageJSON, &storage)
	}
	sources := []map[string]any{storage, input.Metadata, stringMapToAny(input.Attributes)}
	for _, source := range sources {
		if token := firstString([]map[string]any{source}, "access_token", "accessToken"); token != "" {
			return token
		}
		switch raw := source["token"].(type) {
		case map[string]any:
			if token := firstString([]map[string]any{raw}, "access_token", "accessToken"); token != "" {
				return token
			}
		case string:
			decoded := map[string]any{}
			if json.Unmarshal([]byte(strings.TrimSpace(raw)), &decoded) == nil {
				if token := firstString([]map[string]any{decoded}, "access_token", "accessToken"); token != "" {
					return token
				}
			}
		}
	}
	return ""
}

func projectID(input Input) string {
	storage := map[string]any{}
	if len(input.StorageJSON) > 0 {
		_ = json.Unmarshal(input.StorageJSON, &storage)
	}
	value := firstString(
		[]map[string]any{stringMapToAny(input.Attributes), input.Metadata, storage},
		"project_id", "projectId", "gemini_virtual_project",
	)
	if comma := strings.IndexByte(value, ','); comma >= 0 {
		value = value[:comma]
	}
	return strings.TrimSpace(value)
}

func boolValue(value any) (bool, bool) {
	switch typed := value.(type) {
	case bool:
		return typed, true
	case float64:
		return typed != 0, true
	case int:
		return typed != 0, true
	case string:
		switch strings.ToLower(strings.TrimSpace(typed)) {
		case "true", "1", "yes", "y", "on":
			return true, true
		case "false", "0", "no", "n", "off":
			return false, true
		}
	}
	return false, false
}

func anySlice(value any) []any {
	if values, ok := value.([]any); ok {
		return values
	}
	return nil
}

func stringMapToAny(source map[string]string) map[string]any {
	out := make(map[string]any, len(source))
	for key, value := range source {
		out[key] = value
	}
	return out
}
