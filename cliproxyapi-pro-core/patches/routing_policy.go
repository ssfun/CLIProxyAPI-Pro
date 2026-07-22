package management

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/config"
	coreauth "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/auth"
	coreusage "github.com/router-for-me/CLIProxyAPI/v7/sdk/cliproxy/usage"
	log "github.com/sirupsen/logrus"
)

const (
	routingPolicyUsagePluginName = "pro-routing-request-protection"
	routingProtectionOwner       = "request-protection"
	routingProtectionMetadataKey = "request_protection"
	routingProtectionModeObserve = "observe"
	routingProtectionModeEnforce = "enforce"
	routingProtectionMaxEvents   = 100
)

var routingPolicyControllers sync.Map

var routingProtectionProviders = []string{
	"antigravity",
	"xai",
	"codex",
	"gemini-cli",
	"gemini",
	"gemini-interactions",
	"vertex",
	"aistudio",
	"claude",
	"kimi",
}

type routingPolicyController struct {
	h             *Handler
	mu            sync.Mutex
	confirmations map[string]routingProtectionConfirmation
	events        []routingProtectionEvent
	lifecycleMu   sync.Mutex
	usageWG       sync.WaitGroup
	stopped       bool
}

type routingProtectionConfirmation struct {
	Count   int
	FirstAt time.Time
	LastAt  time.Time
}

type routingPolicyGlobalSettings struct {
	Strategy                      string `json:"strategy"`
	SessionAffinity               bool   `json:"sessionAffinity"`
	SessionAffinityTTL            string `json:"sessionAffinityTTL"`
	RequestRetry                  int    `json:"requestRetry"`
	MaxRetryCredentials           int    `json:"maxRetryCredentials"`
	MaxRetryInterval              int    `json:"maxRetryInterval"`
	CoolingEnabled                bool   `json:"coolingEnabled"`
	SaveCooldownStatus            bool   `json:"saveCooldownStatus"`
	TransientErrorCooldownSeconds int    `json:"transientErrorCooldownSeconds"`
	QuotaSwitchProject            bool   `json:"quotaSwitchProject"`
	QuotaSwitchPreviewModel       bool   `json:"quotaSwitchPreviewModel"`
	QuotaAntigravityCredits       bool   `json:"quotaAntigravityCredits"`
	CodexIdentityConfuse          bool   `json:"codexIdentityConfuse"`
}

type routingPolicyResponse struct {
	Global             routingPolicyGlobalSettings      `json:"global"`
	RequestProtection  config.RequestProtectionConfig   `json:"requestProtection"`
	AvailableProviders []string                         `json:"availableProviders"`
	Active             []routingProtectionActiveAccount `json:"active"`
	RecentEvents       []routingProtectionEvent         `json:"recentEvents"`
}

type routingProtectionActiveAccount struct {
	Provider    string `json:"provider"`
	AuthID      string `json:"authId"`
	AuthIndex   string `json:"authIndex"`
	FileName    string `json:"fileName"`
	StatusCode  int    `json:"statusCode"`
	Reason      string `json:"reason"`
	TriggeredAt int64  `json:"triggeredAt"`
	ReleaseAt   int64  `json:"releaseAt"`
}

type routingProtectionEvent struct {
	ID          string `json:"id"`
	Provider    string `json:"provider"`
	AuthID      string `json:"authId"`
	AuthIndex   string `json:"authIndex"`
	FileName    string `json:"fileName"`
	StatusCode  int    `json:"statusCode"`
	Mode        string `json:"mode"`
	Action      string `json:"action"`
	Reason      string `json:"reason"`
	Count       int    `json:"count"`
	Required    int    `json:"required"`
	TriggeredAt int64  `json:"triggeredAt"`
	ReleaseAt   int64  `json:"releaseAt"`
}

type routingPolicyReleaseRequest struct {
	AuthIndex string `json:"authIndex"`
}

func startRoutingPolicyController(h *Handler) {
	if h == nil {
		return
	}
	controller := &routingPolicyController{
		h:             h,
		confirmations: make(map[string]routingProtectionConfirmation),
	}
	actual, loaded := routingPolicyControllers.LoadOrStore(h, controller)
	if loaded {
		controller, _ = actual.(*routingPolicyController)
	}
	if controller == nil {
		return
	}
	coreusage.RegisterNamedPlugin(routingPolicyUsagePluginName, controller)
	if !loaded {
		if h.lifecycleContext == nil {
			return
		}
		h.lifecycleWG.Add(1)
		go func() {
			defer h.lifecycleWG.Done()
			controller.reconcileLoop(h.lifecycleContext)
		}()
	}
}

func stopRoutingPolicyController(h *Handler) {
	if h == nil {
		return
	}
	value, loaded := routingPolicyControllers.LoadAndDelete(h)
	if !loaded {
		return
	}
	controller, _ := value.(*routingPolicyController)
	if controller != nil {
		controller.stop()
	}
}

func (c *routingPolicyController) beginUsage() bool {
	if c == nil {
		return false
	}
	c.lifecycleMu.Lock()
	defer c.lifecycleMu.Unlock()
	if c.stopped {
		return false
	}
	c.usageWG.Add(1)
	return true
}

func (c *routingPolicyController) stop() {
	if c == nil {
		return
	}
	c.lifecycleMu.Lock()
	c.stopped = true
	c.lifecycleMu.Unlock()
	coreusage.UnregisterNamedPlugin(routingPolicyUsagePluginName, c)
	c.usageWG.Wait()
}

func routingPolicyControllerForHandler(h *Handler) *routingPolicyController {
	value, ok := routingPolicyControllers.Load(h)
	if !ok {
		return nil
	}
	controller, _ := value.(*routingPolicyController)
	return controller
}

func (c *routingPolicyController) HandleUsage(ctx context.Context, record coreusage.Record) {
	if !c.beginUsage() {
		return
	}
	defer c.usageWG.Done()
	if c.h == nil {
		return
	}
	provider := strings.ToLower(strings.TrimSpace(record.Provider))
	policyConfig := c.requestProtectionConfig()
	policy, ok := policyConfig.Providers[provider]
	if !policyConfig.Enabled || !ok || !policy.Enabled {
		return
	}
	auth := c.authForRecord(record)
	if auth == nil {
		return
	}
	if !record.Failed {
		c.clearConfirmations(auth.ID, provider)
		return
	}
	statusCode := record.Fail.StatusCode
	if statusCode <= 0 || !routingProtectionStatusMatches(policy.StatusCodes, statusCode) {
		return
	}
	if statusCode == http.StatusTooManyRequests && policy.RequireQuotaEvidence && !routingProtectionHasQuotaEvidence(record) {
		return
	}
	if auth.Disabled && !routingProtectionOwned(auth) {
		return
	}

	now := time.Now()
	confirmed, count, required := c.confirm(auth.ID, provider, statusCode, policy, now)
	releaseAt := routingProtectionReleaseAt(record, policy, now)
	mode := normalizeRoutingProtectionMode(policyConfig.Mode)
	event := routingProtectionEvent{
		ID:          fmt.Sprintf("%d-%s-%s", now.UnixNano(), provider, auth.Index),
		Provider:    provider,
		AuthID:      auth.ID,
		AuthIndex:   auth.Index,
		FileName:    routingProtectionAuthFileName(auth),
		StatusCode:  statusCode,
		Mode:        mode,
		Action:      "observe",
		Reason:      routingProtectionReason(record),
		Count:       count,
		Required:    required,
		TriggeredAt: now.UnixMilli(),
	}
	if !releaseAt.IsZero() {
		event.ReleaseAt = releaseAt.UnixMilli()
	}
	if !confirmed {
		event.Action = "pending"
		c.appendEvent(event)
		return
	}
	if mode != routingProtectionModeEnforce {
		c.appendEvent(event)
		return
	}
	if err := c.disableAuth(ctx, auth, event); err != nil {
		event.Action = "error"
		event.Reason = err.Error()
		c.appendEvent(event)
		log.WithError(err).WithFields(log.Fields{"provider": provider, "auth_index": auth.Index, "status": statusCode}).Warn("routing request protection failed to disable auth")
		return
	}
	event.Action = "disabled"
	c.clearConfirmations(auth.ID, provider)
	c.appendEvent(event)
	log.WithFields(log.Fields{"provider": provider, "auth_index": auth.Index, "status": statusCode, "release_at": event.ReleaseAt}).Info("routing request protection disabled auth")
}

func (c *routingPolicyController) requestProtectionConfig() config.RequestProtectionConfig {
	if c == nil || c.h == nil {
		return defaultRoutingRequestProtectionConfig()
	}
	c.h.mu.Lock()
	defer c.h.mu.Unlock()
	if c.h.cfg == nil {
		return defaultRoutingRequestProtectionConfig()
	}
	return normalizeRoutingRequestProtectionConfig(c.h.cfg.Routing.RequestProtection)
}

func (c *routingPolicyController) authForRecord(record coreusage.Record) *coreauth.Auth {
	if c == nil || c.h == nil || c.h.authManager == nil {
		return nil
	}
	if authID := strings.TrimSpace(record.AuthID); authID != "" {
		if auth, ok := c.h.authManager.GetByID(authID); ok {
			auth.EnsureIndex()
			return auth
		}
	}
	if authIndex := strings.TrimSpace(record.AuthIndex); authIndex != "" {
		if auth := c.h.authByIndex(authIndex); auth != nil {
			auth.EnsureIndex()
			return auth
		}
	}
	return nil
}

func (c *routingPolicyController) confirm(authID, provider string, statusCode int, policy config.RequestProtectionProviderPolicy, now time.Time) (bool, int, int) {
	required := policy.Confirmations
	if required <= 1 {
		return true, 1, 1
	}
	window := time.Duration(policy.ConfirmationWindowSeconds) * time.Second
	if window <= 0 {
		window = 10 * time.Minute
	}
	key := strings.Join([]string{authID, provider, strconv.Itoa(statusCode)}, "|")
	c.mu.Lock()
	defer c.mu.Unlock()
	state := c.confirmations[key]
	if state.FirstAt.IsZero() || now.Sub(state.FirstAt) > window {
		state = routingProtectionConfirmation{FirstAt: now}
	}
	state.Count++
	state.LastAt = now
	c.confirmations[key] = state
	return state.Count >= required, state.Count, required
}

func (c *routingPolicyController) clearConfirmations(authID, provider string) {
	prefix := authID + "|" + provider + "|"
	c.mu.Lock()
	for key := range c.confirmations {
		if strings.HasPrefix(key, prefix) {
			delete(c.confirmations, key)
		}
	}
	c.mu.Unlock()
}

func (c *routingPolicyController) disableAuth(ctx context.Context, auth *coreauth.Auth, event routingProtectionEvent) error {
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	return c.updateAuth(ctx, auth.Index, func(updated *coreauth.Auth) {
		if updated == nil || (updated.Disabled && !routingProtectionOwned(updated)) {
			return
		}
		setAuthInspectionDisabledState(updated, true)
		updated.StatusMessage = fmt.Sprintf("disabled by routing policy after HTTP %d", event.StatusCode)
		if updated.Metadata == nil {
			updated.Metadata = make(map[string]any)
		}
		updated.Metadata[routingProtectionMetadataKey] = map[string]any{
			"owner":        routingProtectionOwner,
			"provider":     event.Provider,
			"status_code":  event.StatusCode,
			"reason":       event.Reason,
			"triggered_at": event.TriggeredAt,
			"release_at":   event.ReleaseAt,
		}
	})
}

func (c *routingPolicyController) releaseAuth(ctx context.Context, auth *coreauth.Auth) error {
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	return c.updateAuth(ctx, auth.Index, func(updated *coreauth.Auth) {
		if updated == nil || !routingProtectionOwned(updated) {
			return
		}
		setAuthInspectionDisabledState(updated, false)
		clearRoutingProtectionOwnership(updated)
	})
}

func (c *routingPolicyController) updateAuth(ctx context.Context, authIndex string, mutate func(*coreauth.Auth)) error {
	if c == nil || c.h == nil || c.h.authManager == nil {
		return fmt.Errorf("core auth manager unavailable")
	}
	if scheduler := schedulerForHandler(c.h); scheduler != nil {
		return scheduler.updateInspectionAuth(ctx, authIndex, mutate)
	}
	auth := c.h.authByIndex(authIndex)
	if auth == nil {
		return fmt.Errorf("auth not found")
	}
	mutate(auth)
	_, err := c.h.authManager.Update(ctx, auth)
	return err
}

func (c *routingPolicyController) reconcileLoop(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticker.C:
			c.reconcile(now)
		}
	}
}

func (c *routingPolicyController) reconcile(now time.Time) {
	if c == nil || c.h == nil || c.h.authManager == nil {
		return
	}
	for _, auth := range c.h.authManager.List() {
		if auth == nil || !routingProtectionOwned(auth) {
			continue
		}
		auth.EnsureIndex()
		metadata := routingProtectionMetadata(auth)
		releaseAt := routingProtectionMetadataInt64(metadata, "release_at")
		if !auth.Disabled {
			_ = c.updateAuth(context.Background(), auth.Index, func(updated *coreauth.Auth) {
				if updated != nil && updated.Metadata != nil {
					delete(updated.Metadata, routingProtectionMetadataKey)
				}
			})
			continue
		}
		if releaseAt <= 0 || now.UnixMilli() < releaseAt {
			continue
		}
		if err := c.releaseAuth(context.Background(), auth); err != nil {
			log.WithError(err).WithField("auth_index", auth.Index).Warn("routing request protection failed to auto-enable auth")
			continue
		}
		c.appendEvent(routingProtectionEvent{
			ID:          fmt.Sprintf("%d-release-%s", now.UnixNano(), auth.Index),
			Provider:    strings.ToLower(strings.TrimSpace(auth.Provider)),
			AuthID:      auth.ID,
			AuthIndex:   auth.Index,
			FileName:    routingProtectionAuthFileName(auth),
			Mode:        routingProtectionModeEnforce,
			Action:      "released",
			Reason:      "automatic release time reached",
			TriggeredAt: now.UnixMilli(),
		})
	}
}

func (c *routingPolicyController) appendEvent(event routingProtectionEvent) {
	c.mu.Lock()
	c.events = append([]routingProtectionEvent{event}, c.events...)
	if len(c.events) > routingProtectionMaxEvents {
		c.events = c.events[:routingProtectionMaxEvents]
	}
	c.mu.Unlock()
}

func (c *routingPolicyController) recentEvents() []routingProtectionEvent {
	if c == nil {
		return []routingProtectionEvent{}
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return append([]routingProtectionEvent{}, c.events...)
}

func routingProtectionStatusMatches(values []int, status int) bool {
	for _, value := range values {
		if value == status {
			return true
		}
	}
	return false
}

func routingProtectionHasQuotaEvidence(record coreusage.Record) bool {
	headers := record.ResponseHeaders
	if headers.Get("Retry-After") != "" {
		return true
	}
	for _, key := range []string{"x-codex-primary-used-percent", "x-codex-secondary-used-percent"} {
		if value, err := strconv.ParseFloat(strings.TrimSpace(headers.Get(key)), 64); err == nil && value >= 99.5 {
			return true
		}
	}
	body := strings.ToLower(strings.TrimSpace(record.Fail.Body))
	for _, marker := range []string{
		"usage_limit_reached",
		"rate_limit_exceeded",
		"insufficient_quota",
		"free-usage-exhausted",
		"quota exceeded",
		"quota_exceeded",
		"used all the included free usage",
		"resource_exhausted",
	} {
		if strings.Contains(body, marker) {
			return true
		}
	}
	return false
}

func routingProtectionReleaseAt(record coreusage.Record, policy config.RequestProtectionProviderPolicy, now time.Time) time.Time {
	if !policy.AutoEnable {
		return time.Time{}
	}
	candidates := make([]time.Time, 0, 4)
	if retryAt := routingProtectionRetryAfter(record.ResponseHeaders.Get("Retry-After"), now); !retryAt.IsZero() {
		candidates = append(candidates, retryAt)
	}
	for _, key := range []string{"x-codex-primary-reset-at", "x-codex-secondary-reset-at"} {
		if unix, err := strconv.ParseInt(strings.TrimSpace(record.ResponseHeaders.Get(key)), 10, 64); err == nil && unix > now.Unix() {
			candidates = append(candidates, time.Unix(unix, 0))
		}
	}
	if bodyAt := routingProtectionBodyResetAt(record.Fail.Body, now); !bodyAt.IsZero() {
		candidates = append(candidates, bodyAt)
	}
	var releaseAt time.Time
	for _, candidate := range candidates {
		if candidate.After(releaseAt) {
			releaseAt = candidate
		}
	}
	if releaseAt.IsZero() && policy.FallbackDisableMinutes > 0 {
		releaseAt = now.Add(time.Duration(policy.FallbackDisableMinutes) * time.Minute)
	}
	return releaseAt
}

func routingProtectionRetryAfter(value string, now time.Time) time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return time.Time{}
	}
	if seconds, err := strconv.Atoi(value); err == nil && seconds > 0 {
		return now.Add(time.Duration(seconds) * time.Second)
	}
	if parsed, err := http.ParseTime(value); err == nil && parsed.After(now) {
		return parsed
	}
	return time.Time{}
}

func routingProtectionBodyResetAt(body string, now time.Time) time.Time {
	var payload map[string]any
	if json.Unmarshal([]byte(body), &payload) != nil {
		return time.Time{}
	}
	errorPayload, _ := payload["error"].(map[string]any)
	for _, source := range []map[string]any{errorPayload, payload} {
		if source == nil {
			continue
		}
		if unix, ok := routingProtectionAnyInt64(source["resets_at"]); ok && unix > now.Unix() {
			return time.Unix(unix, 0)
		}
		if seconds, ok := routingProtectionAnyInt64(source["resets_in_seconds"]); ok && seconds > 0 {
			return now.Add(time.Duration(seconds) * time.Second)
		}
	}
	return time.Time{}
}

func routingProtectionAnyInt64(value any) (int64, bool) {
	switch typed := value.(type) {
	case float64:
		return int64(typed), typed > 0
	case int64:
		return typed, typed > 0
	case json.Number:
		parsed, err := typed.Int64()
		return parsed, err == nil && parsed > 0
	case string:
		parsed, err := strconv.ParseInt(strings.TrimSpace(typed), 10, 64)
		return parsed, err == nil && parsed > 0
	default:
		return 0, false
	}
}

func routingProtectionReason(record coreusage.Record) string {
	body := strings.TrimSpace(record.Fail.Body)
	if body != "" {
		return body
	}
	return fmt.Sprintf("HTTP %d", record.Fail.StatusCode)
}

func routingProtectionMetadata(auth *coreauth.Auth) map[string]any {
	if auth == nil || auth.Metadata == nil {
		return nil
	}
	metadata, _ := auth.Metadata[routingProtectionMetadataKey].(map[string]any)
	return metadata
}

func routingProtectionOwned(auth *coreauth.Auth) bool {
	return strings.EqualFold(strings.TrimSpace(stringFromAny(routingProtectionMetadata(auth)["owner"])), routingProtectionOwner)
}

func clearRoutingProtectionOwnership(auth *coreauth.Auth) {
	if auth == nil || auth.Metadata == nil {
		return
	}
	delete(auth.Metadata, routingProtectionMetadataKey)
}

func routingProtectionMetadataInt64(metadata map[string]any, key string) int64 {
	value, _ := routingProtectionAnyInt64(metadata[key])
	return value
}

func defaultRoutingRequestProtectionConfig() config.RequestProtectionConfig {
	return normalizeRoutingRequestProtectionConfig(config.RequestProtectionConfig{})
}

func normalizeRoutingRequestProtectionConfig(input config.RequestProtectionConfig) config.RequestProtectionConfig {
	input.Mode = normalizeRoutingProtectionMode(input.Mode)
	providers := make(map[string]config.RequestProtectionProviderPolicy, len(routingProtectionProviders))
	for _, provider := range routingProtectionProviders {
		policy := input.Providers[provider]
		policy.StatusCodes = normalizeRoutingProtectionStatusCodes(policy.StatusCodes)
		if len(policy.StatusCodes) == 0 {
			policy.StatusCodes = []int{http.StatusTooManyRequests}
		}
		if policy.Confirmations <= 0 {
			policy.Confirmations = 1
		}
		if policy.Confirmations > 5 {
			policy.Confirmations = 5
		}
		if policy.ConfirmationWindowSeconds <= 0 {
			policy.ConfirmationWindowSeconds = 600
		}
		if policy.ConfirmationWindowSeconds > 86400 {
			policy.ConfirmationWindowSeconds = 86400
		}
		if policy.FallbackDisableMinutes < 0 {
			policy.FallbackDisableMinutes = 0
		}
		if policy.FallbackDisableMinutes > 10080 {
			policy.FallbackDisableMinutes = 10080
		}
		providers[provider] = policy
	}
	input.Providers = providers
	return input
}

func normalizeRoutingProtectionMode(value string) string {
	if strings.EqualFold(strings.TrimSpace(value), routingProtectionModeEnforce) {
		return routingProtectionModeEnforce
	}
	return routingProtectionModeObserve
}

func normalizeRoutingProtectionStatusCodes(values []int) []int {
	seen := make(map[int]struct{})
	out := make([]int, 0, len(values))
	for _, value := range values {
		if value < 100 || value > 599 {
			continue
		}
		if _, ok := seen[value]; ok {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Ints(out)
	return out
}

func (h *Handler) RegisterRoutingPolicyRoutes(group *gin.RouterGroup) {
	group.GET("/routing-policy", h.GetRoutingPolicy)
	group.PUT("/routing-policy", h.PutRoutingPolicy)
	group.PATCH("/routing-policy", h.PutRoutingPolicy)
	group.POST("/routing-policy/release", h.ReleaseRoutingProtectedAuth)
}

func (h *Handler) GetRoutingPolicy(c *gin.Context) {
	c.JSON(http.StatusOK, h.routingPolicyResponse())
}

func (h *Handler) PutRoutingPolicy(c *gin.Context) {
	var request struct {
		Global            routingPolicyGlobalSettings    `json:"global"`
		RequestProtection config.RequestProtectionConfig `json:"requestProtection"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request body"})
		return
	}
	request.Global.Strategy = strings.ToLower(strings.TrimSpace(request.Global.Strategy))
	if request.Global.Strategy != "fill-first" {
		request.Global.Strategy = "round-robin"
	}
	request.Global.SessionAffinityTTL = strings.TrimSpace(request.Global.SessionAffinityTTL)
	if request.Global.SessionAffinityTTL == "" {
		request.Global.SessionAffinityTTL = "1h"
	}
	if _, err := time.ParseDuration(request.Global.SessionAffinityTTL); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid session affinity TTL"})
		return
	}
	request.Global.RequestRetry = clampRoutingPolicyInt(request.Global.RequestRetry, 0, 10)
	request.Global.MaxRetryCredentials = clampRoutingPolicyInt(request.Global.MaxRetryCredentials, 0, 100)
	request.Global.MaxRetryInterval = clampRoutingPolicyInt(request.Global.MaxRetryInterval, 0, 3600)
	request.Global.TransientErrorCooldownSeconds = clampRoutingPolicyInt(request.Global.TransientErrorCooldownSeconds, -1, 86400)
	request.RequestProtection = normalizeRoutingRequestProtectionConfig(request.RequestProtection)

	h.mu.Lock()
	if h.cfg == nil {
		h.mu.Unlock()
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "config unavailable"})
		return
	}
	h.cfg.Routing.Strategy = request.Global.Strategy
	h.cfg.Routing.SessionAffinity = request.Global.SessionAffinity
	h.cfg.Routing.SessionAffinityTTL = request.Global.SessionAffinityTTL
	h.cfg.Routing.RequestProtection = request.RequestProtection
	h.cfg.RequestRetry = request.Global.RequestRetry
	h.cfg.MaxRetryCredentials = request.Global.MaxRetryCredentials
	h.cfg.MaxRetryInterval = request.Global.MaxRetryInterval
	h.cfg.DisableCooling = !request.Global.CoolingEnabled
	h.cfg.SaveCooldownStatus = request.Global.SaveCooldownStatus
	h.cfg.TransientErrorCooldownSeconds = request.Global.TransientErrorCooldownSeconds
	h.cfg.QuotaExceeded.SwitchProject = request.Global.QuotaSwitchProject
	h.cfg.QuotaExceeded.SwitchPreviewModel = request.Global.QuotaSwitchPreviewModel
	h.cfg.QuotaExceeded.AntigravityCredits = request.Global.QuotaAntigravityCredits
	h.cfg.Codex.IdentityConfuse = request.Global.CodexIdentityConfuse
	snapshot, ok := h.saveConfigAndSnapshotLocked(c)
	h.mu.Unlock()
	if !ok {
		return
	}
	h.reloadConfigAfterManagementSave(c.Request.Context(), snapshot)
	c.JSON(http.StatusOK, h.routingPolicyResponse())
}

func (h *Handler) ReleaseRoutingProtectedAuth(c *gin.Context) {
	controller := routingPolicyControllerForHandler(h)
	if controller == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"error": "routing policy controller unavailable"})
		return
	}
	var request routingPolicyReleaseRequest
	if err := c.ShouldBindJSON(&request); err != nil || strings.TrimSpace(request.AuthIndex) == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "authIndex is required"})
		return
	}
	auth := h.authByIndex(strings.TrimSpace(request.AuthIndex))
	if auth == nil {
		c.JSON(http.StatusNotFound, gin.H{"error": "auth not found"})
		return
	}
	auth.EnsureIndex()
	if !routingProtectionOwned(auth) {
		c.JSON(http.StatusConflict, gin.H{"error": "auth is not managed by routing request protection"})
		return
	}
	if err := controller.releaseAuth(c.Request.Context(), auth); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"error": err.Error()})
		return
	}
	c.JSON(http.StatusOK, h.routingPolicyResponse())
}

func (h *Handler) routingPolicyResponse() routingPolicyResponse {
	response := routingPolicyResponse{
		RequestProtection:  defaultRoutingRequestProtectionConfig(),
		AvailableProviders: []string{},
		Active:             []routingProtectionActiveAccount{},
		RecentEvents:       []routingProtectionEvent{},
	}
	h.mu.Lock()
	if h.cfg != nil {
		response.Global = routingPolicyGlobalSettings{
			Strategy:                      h.cfg.Routing.Strategy,
			SessionAffinity:               h.cfg.Routing.SessionAffinity,
			SessionAffinityTTL:            h.cfg.Routing.SessionAffinityTTL,
			RequestRetry:                  h.cfg.RequestRetry,
			MaxRetryCredentials:           h.cfg.MaxRetryCredentials,
			MaxRetryInterval:              h.cfg.MaxRetryInterval,
			CoolingEnabled:                !h.cfg.DisableCooling,
			SaveCooldownStatus:            h.cfg.SaveCooldownStatus,
			TransientErrorCooldownSeconds: h.cfg.TransientErrorCooldownSeconds,
			QuotaSwitchProject:            h.cfg.QuotaExceeded.SwitchProject,
			QuotaSwitchPreviewModel:       h.cfg.QuotaExceeded.SwitchPreviewModel,
			QuotaAntigravityCredits:       h.cfg.QuotaExceeded.AntigravityCredits,
			CodexIdentityConfuse:          h.cfg.Codex.IdentityConfuse,
		}
		if strings.TrimSpace(response.Global.Strategy) == "" {
			response.Global.Strategy = "round-robin"
		}
		if strings.TrimSpace(response.Global.SessionAffinityTTL) == "" {
			response.Global.SessionAffinityTTL = "1h"
		}
		response.RequestProtection = normalizeRoutingRequestProtectionConfig(h.cfg.Routing.RequestProtection)
	}
	h.mu.Unlock()
	response.AvailableProviders = h.routingProtectionAvailableProviders()
	response.Active = h.routingProtectionActiveAccounts()
	if controller := routingPolicyControllerForHandler(h); controller != nil {
		response.RecentEvents = controller.recentEvents()
	}
	return response
}

func (h *Handler) routingProtectionAvailableProviders() []string {
	if h == nil {
		return []string{}
	}
	h.mu.Lock()
	available := routingProtectionConfiguredProviderSet(h.cfg)
	manager := h.authManager
	h.mu.Unlock()
	var auths []*coreauth.Auth
	if manager != nil {
		auths = manager.List()
	}
	return orderedRoutingProtectionAvailableProviders(available, auths)
}

func routingProtectionConfiguredProviderSet(cfg *config.Config) map[string]struct{} {
	available := make(map[string]struct{}, len(routingProtectionProviders))
	if cfg == nil {
		return available
	}
	configured := map[string]bool{
		"xai":                 len(cfg.XAIKey) > 0,
		"codex":               len(cfg.CodexKey) > 0,
		"gemini":              len(cfg.GeminiKey) > 0,
		"gemini-interactions": len(cfg.InteractionsKey) > 0,
		"vertex":              len(cfg.VertexCompatAPIKey) > 0,
		"claude":              len(cfg.ClaudeKey) > 0,
	}
	for provider, ok := range configured {
		if ok {
			available[provider] = struct{}{}
		}
	}
	return available
}

func orderedRoutingProtectionAvailableProviders(available map[string]struct{}, auths []*coreauth.Auth) []string {
	for _, auth := range auths {
		if auth == nil {
			continue
		}
		provider := strings.ToLower(strings.TrimSpace(auth.Provider))
		if provider == "anthropic" {
			provider = "claude"
		}
		available[provider] = struct{}{}
	}
	providers := make([]string, 0, len(routingProtectionProviders))
	for _, provider := range routingProtectionProviders {
		if _, ok := available[provider]; ok {
			providers = append(providers, provider)
		}
	}
	return providers
}

func (h *Handler) routingProtectionActiveAccounts() []routingProtectionActiveAccount {
	if h == nil || h.authManager == nil {
		return []routingProtectionActiveAccount{}
	}
	active := make([]routingProtectionActiveAccount, 0)
	for _, auth := range h.authManager.List() {
		if auth == nil || !routingProtectionOwned(auth) {
			continue
		}
		auth.EnsureIndex()
		metadata := routingProtectionMetadata(auth)
		active = append(active, routingProtectionActiveAccount{
			Provider:    strings.ToLower(strings.TrimSpace(auth.Provider)),
			AuthID:      auth.ID,
			AuthIndex:   auth.Index,
			FileName:    routingProtectionAuthFileName(auth),
			StatusCode:  int(routingProtectionMetadataInt64(metadata, "status_code")),
			Reason:      stringFromAny(metadata["reason"]),
			TriggeredAt: routingProtectionMetadataInt64(metadata, "triggered_at"),
			ReleaseAt:   routingProtectionMetadataInt64(metadata, "release_at"),
		})
	}
	sort.Slice(active, func(i, j int) bool {
		if active[i].ReleaseAt == active[j].ReleaseAt {
			return active[i].AuthIndex < active[j].AuthIndex
		}
		if active[i].ReleaseAt == 0 {
			return false
		}
		if active[j].ReleaseAt == 0 {
			return true
		}
		return active[i].ReleaseAt < active[j].ReleaseAt
	})
	return active
}

func routingProtectionAuthFileName(auth *coreauth.Auth) string {
	if auth == nil {
		return ""
	}
	for _, candidate := range []string{
		auth.FileName,
		authAttribute(auth, coreauth.AttributeVirtualSource),
		authAttribute(auth, "path"),
	} {
		candidate = strings.TrimSpace(candidate)
		if candidate == "" {
			continue
		}
		name := filepath.Base(filepath.Clean(candidate))
		if name != "" && name != "." && name != string(filepath.Separator) {
			return name
		}
	}
	return ""
}

func clampRoutingPolicyInt(value, minValue, maxValue int) int {
	if value < minValue {
		return minValue
	}
	if value > maxValue {
		return maxValue
	}
	return value
}
