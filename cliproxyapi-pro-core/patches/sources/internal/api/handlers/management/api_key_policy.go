package management

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/router-for-me/CLIProxyAPI/v7/internal/pro/apikeypolicy"
)

const (
	apiKeyReferenceLifetime        = 10 * time.Minute
	apiKeyReferencePerSessionLimit = 2048
	apiKeyReferenceGlobalLimit     = 8192
)

type apiKeyReference struct {
	identity   apikeypolicy.AuthenticatedAPIKeyIdentity
	generation uint64
	session    string
	expiresAt  time.Time
}

type apiKeyPolicyBinding struct {
	Disabled  bool                 `json:"disabled"`
	MaskedKey string               `json:"maskedKey"`
	KeyRef    string               `json:"keyRef"`
	State     string               `json:"state"`
	Policy    *apikeypolicy.Policy `json:"policy,omitempty"`
	WeakKey   bool                 `json:"weakKey"`
}

type apiKeyPolicyCursor struct {
	CreatedAtMS int64  `json:"createdAtMs"`
	ID          string `json:"id"`
	Generation  uint64 `json:"generation"`
}

func parseAPIKeyPolicyCursor(raw string) (apiKeyPolicyCursor, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return apiKeyPolicyCursor{}, nil
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return apiKeyPolicyCursor{}, errors.New("invalid orphaned policy cursor")
	}
	var cursor apiKeyPolicyCursor
	if err = json.Unmarshal(payload, &cursor); err != nil || cursor.CreatedAtMS < 0 || strings.TrimSpace(cursor.ID) == "" {
		return apiKeyPolicyCursor{}, errors.New("invalid orphaned policy cursor")
	}
	return cursor, nil
}

func encodeAPIKeyPolicyCursor(policy apikeypolicy.Policy, generation uint64) string {
	payload, _ := json.Marshal(apiKeyPolicyCursor{CreatedAtMS: policy.CreatedAtMS, ID: policy.ID, Generation: generation})
	return base64.RawURLEncoding.EncodeToString(payload)
}

func (h *Handler) RegisterAPIKeyPolicyRoutes(group *gin.RouterGroup) {
	if h == nil || group == nil {
		return
	}
	group.GET("/api-key-policy-bindings", h.ListAPIKeyPolicyBindings)
	group.GET("/api-key-policy-profile-catalog", h.GetAPIKeyPolicyProfileCatalog)
	group.GET("/api-key-policy-quota-summaries", h.ListAPIKeyPolicyQuotaSummaries)
	group.POST("/api-key-policy-usage-target", h.ResolveAPIKeyPolicyUsageTarget)
	group.POST("/api-key-policy-key", h.ReadAPIKeyPolicyKey)
	group.PUT("/api-key-policy-key-state", h.UpdateAPIKeyPolicyKeyState)
	group.GET("/api-key-policy-capabilities", h.GetAPIKeyPolicyCapabilities)
	group.GET("/api-key-policy-status", h.GetAPIKeyPolicyStatus)
	group.PUT("/api-key-policy-takeover", h.UpdateAPIKeyPolicyTakeover)
	group.GET("/api-key-policy-catalog", h.GetAPIKeyPolicyCatalog)
	group.POST("/api-key-policies", h.CreateAPIKeyPolicy)
	group.GET("/api-key-policies/:policyId", h.GetAPIKeyPolicy)
	group.GET("/api-key-policies/:policyId/delete-preview", h.PreviewDeleteAPIKeyPolicy)
	group.PATCH("/api-key-policies/:policyId", h.UpdateAPIKeyPolicy)
	group.DELETE("/api-key-policies/:policyId", h.DeleteAPIKeyPolicy)
	group.POST("/api-key-policies/:policyId/profiles", h.CreateAPIKeyProfile)
	group.PUT("/api-key-policies/:policyId/profiles/:profileId", h.ReplaceAPIKeyProfile)
	group.DELETE("/api-key-policies/:policyId/profiles/:profileId", h.DeleteAPIKeyProfile)
	group.PUT("/api-key-policies/:policyId/active-profile", h.ActivateAPIKeyProfile)
	group.POST("/api-key-policies/:policyId/quota/reset", h.ResetAPIKeyQuota)
	group.DELETE("/orphaned-api-key-policies/:policyId", h.PurgeOrphanedAPIKeyPolicy)
}

func (h *Handler) GetAPIKeyPolicyCapabilities(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"apiVersion": 3,
		"features": []string{
			"policy_crud",
			"profile_crud",
			"optimistic_concurrency",
			"atomic_workspace_save",
			"policy_backup_restore",
			"policy_delete_preview",
			"orphaned_purge_guard",
			"takeover_control",
			"key_quota_requests_tokens",
			"key_quota_cost_period",
			"key_quota_calendar_timezone",
			"key_quota_explicit_reset",
			"key_quota_overview",
			"usage_key_target",
			"provider_model_linkage",
			"optional_profile",
			"profile_enforcement_toggle",
			"key_lifecycle_controls",
		},
	})
}

func (h *Handler) ListAPIKeyPolicyQuotaSummaries(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil || !service.Healthy() {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	nowMS := time.Now().UnixMilli()
	items, err := service.ListQuotaSummaries(c.Request.Context(), nowMS)
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "snapshotAtMs": nowMS})
}

func (h *Handler) GetAPIKeyPolicyStatus(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	writeAPIKeyPolicyStatus(c, service)
}

func (h *Handler) UpdateAPIKeyPolicyTakeover(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	var request struct {
		Enabled              *bool   `json:"enabled" binding:"required"`
		ConfiguredGeneration *uint64 `json:"configuredGeneration"`
		PolicyGeneration     *uint64 `json:"policyGeneration"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || request.Enabled == nil {
		writeAPIKeyPolicyHTTPError(c, http.StatusBadRequest, "invalid_api_key_policy_takeover", "enabled must be a boolean")
		return
	}
	if !*request.Enabled {
		if err := service.SetTakeover(c.Request.Context(), false); err != nil {
			writeAPIKeyPolicyError(c, err)
			return
		}
		writeAPIKeyPolicyStatus(c, service)
		return
	}
	if !service.Healthy() {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	if request.ConfiguredGeneration == nil || request.PolicyGeneration == nil {
		writeAPIKeyPolicyHTTPError(c, http.StatusBadRequest, "invalid_api_key_policy_takeover", "configuredGeneration and policyGeneration are required when enabling takeover")
		return
	}
	if err := service.SetTakeoverIfGeneration(
		c.Request.Context(),
		true,
		*request.PolicyGeneration,
		*request.ConfiguredGeneration,
	); err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	writeAPIKeyPolicyStatus(c, service)
}

func writeAPIKeyPolicyStatus(c *gin.Context, service *apikeypolicy.Service) {
	c.JSON(http.StatusOK, gin.H{
		"takeoverEnabled":      service.TakeoverEnabled(),
		"healthy":              service.Healthy(),
		"policyGeneration":     service.PolicyGeneration(),
		"configuredGeneration": service.ConfiguredGeneration(),
	})
}

func (h *Handler) apiKeyPolicyService() *apikeypolicy.Service {
	application := h.proApplication()
	if application == nil {
		return nil
	}
	return application.APIKeyPolicy()
}

func (h *Handler) apiKeyConfigSnapshot() ([]string, uint64) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.cfg == nil {
		return nil, h.configGeneration
	}
	return append([]string(nil), h.cfg.APIKeys...), h.configGeneration
}

func managementSessionID(c *gin.Context) string {
	if c == nil {
		return ""
	}
	return c.GetString(apiKeyPolicyManagementSessionContextKey)
}

func (h *Handler) issueAPIKeyReference(c *gin.Context, identity apikeypolicy.AuthenticatedAPIKeyIdentity, generation uint64) (string, error) {
	session := managementSessionID(c)
	if session == "" {
		return "", errors.New("management session is required")
	}
	now := time.Now()
	h.apiKeyRefsMu.Lock()
	defer h.apiKeyRefsMu.Unlock()
	sessionReferences := 0
	for existing, reference := range h.apiKeyRefs {
		if !reference.expiresAt.After(now) || reference.generation < generation {
			delete(h.apiKeyRefs, existing)
			continue
		}
		if reference.generation != generation {
			continue
		}
		if reference.session != session {
			continue
		}
		sessionReferences++
		if reference.identity.Hash() == identity.Hash() {
			reference.expiresAt = now.Add(apiKeyReferenceLifetime)
			h.apiKeyRefs[existing] = reference
			return existing, nil
		}
	}
	if sessionReferences >= apiKeyReferencePerSessionLimit || len(h.apiKeyRefs) >= apiKeyReferenceGlobalLimit {
		return "", errors.New("API key reference capacity exceeded")
	}
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	h.apiKeyRefs[token] = apiKeyReference{identity: identity, generation: generation, session: session, expiresAt: now.Add(apiKeyReferenceLifetime)}
	return token, nil
}

func (h *Handler) consumeAPIKeyReference(c *gin.Context, token string) (apikeypolicy.AuthenticatedAPIKeyIdentity, uint64, error) {
	token = strings.TrimSpace(token)
	h.apiKeyRefsMu.Lock()
	reference, ok := h.apiKeyRefs[token]
	delete(h.apiKeyRefs, token)
	h.apiKeyRefsMu.Unlock()
	if !ok || !reference.expiresAt.After(time.Now()) || reference.session == "" || reference.session != managementSessionID(c) {
		return apikeypolicy.AuthenticatedAPIKeyIdentity{}, 0, errors.New("api key reference is expired or belongs to another management session")
	}
	_, currentGeneration := h.apiKeyConfigSnapshot()
	if reference.generation != currentGeneration {
		return apikeypolicy.AuthenticatedAPIKeyIdentity{}, 0, errors.New("api key reference configuration generation changed")
	}
	return reference.identity, reference.generation, nil
}

func (h *Handler) resolveAPIKeyReference(c *gin.Context, token string) (apikeypolicy.AuthenticatedAPIKeyIdentity, uint64, error) {
	token = strings.TrimSpace(token)
	h.apiKeyRefsMu.Lock()
	reference, ok := h.apiKeyRefs[token]
	h.apiKeyRefsMu.Unlock()
	if !ok || !reference.expiresAt.After(time.Now()) || reference.session == "" || reference.session != managementSessionID(c) {
		return apikeypolicy.AuthenticatedAPIKeyIdentity{}, 0, errors.New("api key reference is expired or belongs to another management session")
	}
	keys, currentGeneration := h.apiKeyConfigSnapshot()
	if reference.generation != currentGeneration {
		return apikeypolicy.AuthenticatedAPIKeyIdentity{}, 0, errors.New("api key reference configuration generation changed")
	}
	if _, exists := configuredAPIKeyIdentities(keys)[reference.identity.Hash()]; !exists {
		return apikeypolicy.AuthenticatedAPIKeyIdentity{}, 0, errors.New("upstream API key no longer exists")
	}
	return reference.identity, reference.generation, nil
}

func (h *Handler) ResolveAPIKeyPolicyUsageTarget(c *gin.Context) {
	var request struct {
		KeyRef string `json:"keyRef" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil || strings.TrimSpace(request.KeyRef) == "" {
		writeAPIKeyPolicyHTTPError(c, http.StatusBadRequest, "invalid_api_key_reference", "keyRef is required")
		return
	}
	identity, generation, errRef := h.resolveAPIKeyReference(c, request.KeyRef)
	if errRef != nil {
		writeAPIKeyPolicyHTTPError(c, http.StatusConflict, "api_key_reference_stale", errRef.Error())
		return
	}
	c.JSON(http.StatusOK, gin.H{
		"apiKeyHash":       identity.Hash(),
		"configGeneration": generation,
	})
}

func configuredAPIKeyIdentities(keys []string) map[string]apikeypolicy.AuthenticatedAPIKeyIdentity {
	out := make(map[string]apikeypolicy.AuthenticatedAPIKeyIdentity, len(keys))
	for _, key := range keys {
		identity, err := apikeypolicy.NewAuthenticatedAPIKeyIdentity(strings.TrimSpace(key))
		if err == nil {
			out[identity.Hash()] = identity
		}
	}
	return out
}

func maskAPIKey(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	visibleChars := 2
	if len(value) < 4 {
		visibleChars = 1
	}
	maskedLength := 10 - visibleChars*2
	if maskedLength < 1 {
		maskedLength = 1
	}
	return value[:visibleChars] + strings.Repeat("*", maskedLength) + value[len(value)-visibleChars:]
}

func weakAPIKey(value string) bool { return len(strings.TrimSpace(value)) < 16 }

func (h *Handler) ListAPIKeyPolicyBindings(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil || !service.Healthy() {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	keys, generation := h.apiKeyConfigSnapshot()
	type configuredKey struct {
		raw      string
		identity apikeypolicy.AuthenticatedAPIKeyIdentity
	}
	uniqueKeys := make([]configuredKey, 0, len(keys))
	items := make([]apiKeyPolicyBinding, 0, len(keys))
	configured := make(map[string]struct{}, len(keys))
	for _, rawKey := range keys {
		rawKey = strings.TrimSpace(rawKey)
		identity, errIdentity := apikeypolicy.NewAuthenticatedAPIKeyIdentity(rawKey)
		if errIdentity != nil {
			continue
		}
		if _, duplicate := configured[identity.Hash()]; duplicate {
			continue
		}
		configured[identity.Hash()] = struct{}{}
		uniqueKeys = append(uniqueKeys, configuredKey{raw: rawKey, identity: identity})
	}
	configuredHashes := make([]string, 0, len(configured))
	for hash := range configured {
		configuredHashes = append(configuredHashes, hash)
	}
	policies, err := service.ListConfigured(c.Request.Context(), configuredHashes)
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	byHash := make(map[string]apikeypolicy.Policy, len(policies))
	for _, policy := range policies {
		byHash[policy.APIKeyHash] = policy
	}
	for _, key := range uniqueKeys {
		keyRef, errRef := h.issueAPIKeyReference(c, key.identity, generation)
		if errRef != nil {
			writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
			return
		}
		binding := apiKeyPolicyBinding{Disabled: service.KeyDisabled(key.identity), MaskedKey: maskAPIKey(key.raw), KeyRef: keyRef, State: apikeypolicy.StateUnconfigured, WeakKey: weakAPIKey(key.raw)}
		if policy, exists := byHash[key.identity.Hash()]; exists {
			policy.State = apikeypolicy.StateConfigured
			binding.State, binding.Policy = apikeypolicy.StateConfigured, &policy
		}
		items = append(items, binding)
	}
	cursor, errCursor := parseAPIKeyPolicyCursor(c.Query("orphaned_cursor"))
	if errCursor != nil {
		writeAPIKeyPolicyHTTPError(c, http.StatusBadRequest, "invalid_api_key_policy_cursor", errCursor.Error())
		return
	}
	if c.Query("orphaned_cursor") != "" && cursor.Generation != generation {
		writeAPIKeyPolicyHTTPError(c, http.StatusConflict, "api_key_policy_config_changed", "API key configuration changed during pagination; restart from the first page")
		return
	}
	limit := 50
	if requested, errLimit := strconv.Atoi(c.Query("orphaned_limit")); errLimit == nil && requested >= 1 && requested <= 100 {
		limit = requested
	}
	orphaned, errPage := service.ListOrphanedPage(c.Request.Context(), configuredHashes, cursor.CreatedAtMS, cursor.ID, limit+1)
	if errPage != nil {
		writeAPIKeyPolicyError(c, errPage)
		return
	}
	nextCursor := ""
	if len(orphaned) > limit {
		nextCursor = encodeAPIKeyPolicyCursor(orphaned[limit-1], generation)
		orphaned = orphaned[:limit]
	}
	for index := range orphaned {
		orphaned[index].State = apikeypolicy.StateOrphaned
	}
	if _, currentGeneration := h.apiKeyConfigSnapshot(); currentGeneration != generation {
		writeAPIKeyPolicyHTTPError(c, http.StatusConflict, "api_key_policy_config_changed", "API key configuration changed while building the binding snapshot; retry the request")
		return
	}
	c.JSON(http.StatusOK, gin.H{"items": items, "orphaned": orphaned, "nextCursor": nextCursor, "configGeneration": generation})
}

func (h *Handler) GetAPIKeyPolicyCatalog(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil || !service.Healthy() {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	catalog, err := service.Catalog()
	if err != nil {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	c.JSON(http.StatusOK, catalog)
}

func (h *Handler) GetAPIKeyPolicyProfileCatalog(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil || !service.Healthy() {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	catalog, err := service.ListProfileCatalog(c.Request.Context())
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	c.JSON(http.StatusOK, catalog)
}

type createAPIKeyPolicyRequest struct {
	KeyRef         string                     `json:"keyRef" binding:"required"`
	DisplayName    string                     `json:"displayName"`
	InitialProfile *apikeypolicy.ProfileInput `json:"initialProfile"`
	Quota          *apikeypolicy.QuotaInput   `json:"quota"`
	ClientFeatures []string                   `json:"clientFeatures"`
}

func apiKeyPolicyWriteContext(c *gin.Context, clientFeatures []string) context.Context {
	ctx := c.Request.Context()
	for _, feature := range clientFeatures {
		switch feature {
		case "provider_model_linkage":
			ctx = apikeypolicy.WithProviderModelLinkageValidation(ctx)
		case "key_quota_calendar_timezone":
			ctx = apikeypolicy.WithQuotaTimezoneAwareness(ctx)
		case "profile_enforcement_toggle":
			ctx = apikeypolicy.WithProfileEnforcementToggle(ctx)
		}
	}
	return ctx
}

func (h *Handler) CreateAPIKeyPolicy(c *gin.Context) {
	var request createAPIKeyPolicyRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, http.StatusBadRequest, "invalid_api_key_profile", "invalid request body")
		return
	}
	identity, generation, errRef := h.consumeAPIKeyReference(c, request.KeyRef)
	if errRef != nil {
		writeAPIKeyPolicyHTTPError(c, http.StatusConflict, "api_key_reference_stale", errRef.Error())
		return
	}
	service := h.apiKeyPolicyService()
	if service == nil {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	keys, currentGeneration := h.apiKeyConfigSnapshot()
	if currentGeneration != generation {
		writeAPIKeyPolicyHTTPError(c, http.StatusConflict, "api_key_reference_stale", "API key configuration changed; refresh the key list")
		return
	}
	if _, exists := configuredAPIKeyIdentities(keys)[identity.Hash()]; !exists {
		writeAPIKeyPolicyHTTPError(c, http.StatusNotFound, "upstream_api_key_not_found", "upstream API key no longer exists")
		return
	}
	policy, err := service.CreateOptionalProfile(apiKeyPolicyWriteContext(c, request.ClientFeatures), identity, request.DisplayName, request.InitialProfile, request.Quota)
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	policy.State = h.policyState(policy)
	if policy.State == apikeypolicy.StateOrphaned {
		c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "api_key_policy_orphaned", "message": "upstream API key no longer exists"}, "policy": policy})
		return
	}
	c.JSON(http.StatusCreated, policy)
}

func (h *Handler) GetAPIKeyPolicy(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	policy, err := service.Get(c.Request.Context(), c.Param("policyId"))
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	policy.State = h.policyState(policy)
	c.JSON(http.StatusOK, policy)
}

func (h *Handler) policyState(policy apikeypolicy.Policy) string {
	keys, _ := h.apiKeyConfigSnapshot()
	if _, exists := configuredAPIKeyIdentities(keys)[policy.APIKeyHash]; exists {
		return apikeypolicy.StateConfigured
	}
	return apikeypolicy.StateOrphaned
}

func (h *Handler) requireConfiguredPolicy(c *gin.Context) (*apikeypolicy.Service, apikeypolicy.Policy, bool) {
	service := h.apiKeyPolicyService()
	if service == nil {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return nil, apikeypolicy.Policy{}, false
	}
	policy, err := service.Get(c.Request.Context(), c.Param("policyId"))
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return nil, apikeypolicy.Policy{}, false
	}
	if h.policyState(policy) == apikeypolicy.StateOrphaned {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrOrphaned)
		return nil, apikeypolicy.Policy{}, false
	}
	return service, policy, true
}

func (h *Handler) UpdateAPIKeyPolicy(c *gin.Context) {
	service, _, ok := h.requireConfiguredPolicy(c)
	if !ok {
		return
	}
	var request struct {
		DisplayName     string                     `json:"displayName"`
		Version         int64                      `json:"version" binding:"required"`
		ProfileID       string                     `json:"profileId"`
		Profile         *apikeypolicy.ProfileInput `json:"profile"`
		CreateProfile   bool                       `json:"createProfile"`
		ProfileEnabled  *bool                      `json:"profileEnabled"`
		ActiveProfileID string                     `json:"activeProfileId"`
		Quota           *apikeypolicy.QuotaInput   `json:"quota"`
		QuotaPresent    bool                       `json:"-"`
		ClientFeatures  []string                   `json:"clientFeatures"`
	}
	var raw map[string]json.RawMessage
	body, errRead := io.ReadAll(c.Request.Body)
	if errRead != nil || json.Unmarshal(body, &raw) != nil || json.Unmarshal(body, &request) != nil {
		writeAPIKeyPolicyHTTPError(c, 400, "invalid_api_key_profile", "invalid request body")
		return
	}
	_, request.QuotaPresent = raw["quota"]
	policy, err := service.UpdateWorkspace(apiKeyPolicyWriteContext(c, request.ClientFeatures), c.Param("policyId"), request.Version, apikeypolicy.WorkspaceUpdate{
		DisplayName: request.DisplayName, ProfileID: request.ProfileID,
		Profile: request.Profile, CreateProfile: request.CreateProfile,
		ProfileEnabled: request.ProfileEnabled, ActiveProfileID: request.ActiveProfileID,
		Quota: apikeypolicy.QuotaUpdate{Present: request.QuotaPresent, Value: request.Quota},
	})
	h.writeAPIKeyPolicyResult(c, policy, err, http.StatusOK)
}

func (h *Handler) ResetAPIKeyQuota(c *gin.Context) {
	service, _, ok := h.requireConfiguredPolicy(c)
	if !ok {
		return
	}
	var request struct {
		Version int64  `json:"version" binding:"required"`
		Confirm string `json:"confirmReset" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, http.StatusBadRequest, "invalid_api_key_quota", "invalid request body")
		return
	}
	policy, err := service.ResetQuota(c.Request.Context(), c.Param("policyId"), request.Version, request.Confirm)
	h.writeAPIKeyPolicyResult(c, policy, err, http.StatusOK)
}

func (h *Handler) CreateAPIKeyProfile(c *gin.Context) {
	service, _, ok := h.requireConfiguredPolicy(c)
	if !ok {
		return
	}
	var request struct {
		apikeypolicy.ProfileInput
		Version        int64    `json:"version" binding:"required"`
		ClientFeatures []string `json:"clientFeatures"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, 400, "invalid_api_key_profile", "invalid request body")
		return
	}
	policy, err := service.CreateProfile(apiKeyPolicyWriteContext(c, request.ClientFeatures), c.Param("policyId"), request.Version, request.ProfileInput)
	h.writeAPIKeyPolicyResult(c, policy, err, http.StatusCreated)
}

func (h *Handler) ReplaceAPIKeyProfile(c *gin.Context) {
	service, _, ok := h.requireConfiguredPolicy(c)
	if !ok {
		return
	}
	var request struct {
		apikeypolicy.ProfileInput
		Version        int64    `json:"version" binding:"required"`
		ClientFeatures []string `json:"clientFeatures"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, 400, "invalid_api_key_profile", "invalid request body")
		return
	}
	policy, err := service.ReplaceProfile(apiKeyPolicyWriteContext(c, request.ClientFeatures), c.Param("policyId"), c.Param("profileId"), request.Version, request.ProfileInput)
	h.writeAPIKeyPolicyResult(c, policy, err, http.StatusOK)
}

func (h *Handler) DeleteAPIKeyProfile(c *gin.Context) {
	service, _, ok := h.requireConfiguredPolicy(c)
	if !ok {
		return
	}
	var request struct {
		Version          int64  `json:"version" binding:"required"`
		ConfirmNoProfile string `json:"confirmNoProfile"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, 400, "invalid_api_key_profile", "invalid request body")
		return
	}
	policy, err := service.DeleteProfile(c.Request.Context(), c.Param("policyId"), c.Param("profileId"), request.Version, request.ConfirmNoProfile)
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	if h.policyState(policy) == apikeypolicy.StateOrphaned {
		policy.State = apikeypolicy.StateOrphaned
		writeAPIKeyPolicyOrphaned(c, policy)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) ActivateAPIKeyProfile(c *gin.Context) {
	service, _, ok := h.requireConfiguredPolicy(c)
	if !ok {
		return
	}
	var request struct {
		ProfileID string `json:"profileId" binding:"required"`
		Version   int64  `json:"version" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, 400, "invalid_api_key_profile", "invalid request body")
		return
	}
	policy, err := service.ActivateProfile(c.Request.Context(), c.Param("policyId"), request.ProfileID, request.Version)
	h.writeAPIKeyPolicyResult(c, policy, err, http.StatusOK)
}

func (h *Handler) PreviewDeleteAPIKeyPolicy(c *gin.Context) {
	_, policy, ok := h.requireConfiguredPolicy(c)
	if !ok {
		return
	}
	var active *apikeypolicy.Profile
	for index := range policy.Profiles {
		if policy.Profiles[index].ID == policy.ActiveProfileID {
			active = &policy.Profiles[index]
			break
		}
	}
	preview := gin.H{
		"policyId":               policy.ID,
		"version":                policy.Version,
		"change":                 "restricted_profile_to_unrestricted_passthrough",
		"targetPolicyMode":       apikeypolicy.ModePassthrough,
		"affectsNewRequestsOnly": true,
		"requiresConfirmation":   apikeypolicy.PassthroughConfirmation,
	}
	if active != nil {
		preview["activeProfile"] = gin.H{
			"id": active.ID, "name": active.Name,
			"providers": active.Providers, "models": active.Models,
		}
	}
	c.JSON(http.StatusOK, preview)
}

func (h *Handler) DeleteAPIKeyPolicy(c *gin.Context) {
	service, _, ok := h.requireConfiguredPolicy(c)
	if !ok {
		return
	}
	var request struct {
		Version int64  `json:"version" binding:"required"`
		Confirm string `json:"confirmPassthrough" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, 400, "invalid_api_key_profile", "invalid request body")
		return
	}
	if err := service.DeletePolicy(c.Request.Context(), c.Param("policyId"), request.Version, request.Confirm); err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) PurgeOrphanedAPIKeyPolicy(c *gin.Context) {
	service := h.apiKeyPolicyService()
	if service == nil {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	policy, err := service.Get(c.Request.Context(), c.Param("policyId"))
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	var request struct {
		Version          int64  `json:"version" binding:"required"`
		ConfigGeneration uint64 `json:"configGeneration" binding:"required"`
	}
	if err = c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, http.StatusBadRequest, "invalid_api_key_profile", "invalid request body")
		return
	}
	// Keep the committed config snapshot fixed from the orphan check through
	// deletion. Otherwise restoring the same upstream key between those steps
	// could turn a data purge into an unconfirmed permission expansion.
	h.mu.Lock()
	defer h.mu.Unlock()
	if request.ConfigGeneration != h.configGeneration {
		writeAPIKeyPolicyHTTPError(c, http.StatusConflict, "api_key_policy_config_changed", "API key configuration changed; refresh the policy list")
		return
	}
	var configuredKeys []string
	if h.cfg != nil {
		configuredKeys = h.cfg.APIKeys
	}
	if _, exists := configuredAPIKeyIdentities(configuredKeys)[policy.APIKeyHash]; exists {
		writeAPIKeyPolicyHTTPError(c, http.StatusConflict, "api_key_policy_not_orphaned", "policy still belongs to an upstream API key")
		return
	}
	if err = service.PurgeOrphaned(c.Request.Context(), policy.ID, request.Version); err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}

func (h *Handler) writeAPIKeyPolicyResult(c *gin.Context, policy apikeypolicy.Policy, err error, status int) {
	if err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	policy.State = h.policyState(policy)
	if policy.State == apikeypolicy.StateOrphaned {
		writeAPIKeyPolicyOrphaned(c, policy)
		return
	}
	c.JSON(status, policy)
}

func writeAPIKeyPolicyOrphaned(c *gin.Context, policy apikeypolicy.Policy) {
	c.JSON(http.StatusConflict, gin.H{"error": gin.H{"code": "api_key_policy_orphaned", "message": "upstream API key no longer exists"}, "policy": policy})
}

func writeAPIKeyPolicyError(c *gin.Context, err error) {
	status, code, message := http.StatusBadRequest, "invalid_api_key_profile", err.Error()
	switch {
	case errors.Is(err, apikeypolicy.ErrUnavailable):
		status, code, message = 503, "api_key_policy_unavailable", "API key policy is unavailable"
	case errors.Is(err, apikeypolicy.ErrTakeoverStateChanged):
		status, code, message = 409, "api_key_policy_state_changed", "API key policies changed; refresh the takeover preview"
	case errors.Is(err, apikeypolicy.ErrPolicyNotFound):
		status, code = 404, "api_key_policy_not_found"
	case errors.Is(err, apikeypolicy.ErrProfileNotFound):
		status, code = 404, "api_key_profile_not_found"
	case errors.Is(err, apikeypolicy.ErrVersionConflict):
		status, code = 409, "config_version_conflict"
	case errors.Is(err, apikeypolicy.ErrActiveProfileDelete):
		status, code = 409, "active_profile_delete_forbidden"
	case errors.Is(err, apikeypolicy.ErrLastProfileDelete):
		status, code = 409, "last_profile_delete_forbidden"
	case errors.Is(err, apikeypolicy.ErrNoProfileConfirmation):
		status, code = 409, "profile_delete_requires_no_profile_confirmation"
	case errors.Is(err, apikeypolicy.ErrPassthroughConfirmation):
		status, code = 409, "policy_delete_requires_passthrough_confirmation"
	case errors.Is(err, apikeypolicy.ErrOrphaned):
		status, code = 409, "api_key_policy_orphaned"
	case errors.Is(err, apikeypolicy.ErrNotOrphaned):
		status, code, message = 409, "api_key_policy_not_orphaned", "policy still belongs to an upstream API key"
	case errors.Is(err, apikeypolicy.ErrQuotaNotConfigured):
		status, code = 409, "api_key_quota_not_configured"
	case errors.Is(err, apikeypolicy.ErrQuotaResetConfirmation):
		status, code = 409, "api_key_quota_reset_confirmation_required"
	}
	writeAPIKeyPolicyHTTPError(c, status, code, message)
}

func writeAPIKeyPolicyHTTPError(c *gin.Context, status int, code, message string) {
	c.JSON(status, gin.H{"error": gin.H{"code": code, "message": message}})
}

// Full secrets are returned only on an explicit authenticated request, never in lists.
func (h *Handler) ReadAPIKeyPolicyKey(c *gin.Context) {
	c.Header("Cache-Control", "no-store")
	var request struct {
		KeyRef string `json:"keyRef" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, 400, "invalid_api_key_reference", "keyRef is required")
		return
	}
	identity, generation, err := h.resolveAPIKeyReference(c, request.KeyRef)
	if err != nil {
		writeAPIKeyPolicyHTTPError(c, 409, "api_key_reference_stale", err.Error())
		return
	}
	keys, current := h.apiKeyConfigSnapshot()
	if current == generation {
		for _, key := range keys {
			candidate, err := apikeypolicy.NewAuthenticatedAPIKeyIdentity(strings.TrimSpace(key))
			if err == nil && candidate.Hash() == identity.Hash() {
				c.JSON(200, gin.H{"key": strings.TrimSpace(key)})
				return
			}
		}
	}
	writeAPIKeyPolicyHTTPError(c, 409, "api_key_reference_stale", "API key configuration changed")
}

func (h *Handler) UpdateAPIKeyPolicyKeyState(c *gin.Context) {
	var request struct {
		KeyRef           string `json:"keyRef" binding:"required"`
		Disabled         *bool  `json:"disabled" binding:"required"`
		ExpectedDisabled *bool  `json:"expectedDisabled" binding:"required"`
	}
	if err := c.ShouldBindJSON(&request); err != nil {
		writeAPIKeyPolicyHTTPError(c, 400, "invalid_api_key_reference", "keyRef, disabled and expectedDisabled are required")
		return
	}
	service := h.apiKeyPolicyService()
	if service == nil || !service.Healthy() {
		writeAPIKeyPolicyError(c, apikeypolicy.ErrUnavailable)
		return
	}
	identity, generation, err := h.resolveAPIKeyReference(c, request.KeyRef)
	if err != nil {
		writeAPIKeyPolicyHTTPError(c, 409, "api_key_reference_stale", err.Error())
		return
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.configGeneration != generation {
		writeAPIKeyPolicyHTTPError(c, 409, "api_key_reference_stale", "API key configuration changed")
		return
	}
	if err := service.SetKeyDisabled(c.Request.Context(), identity, *request.Disabled, *request.ExpectedDisabled); err != nil {
		writeAPIKeyPolicyError(c, err)
		return
	}
	c.JSON(200, gin.H{"disabled": *request.Disabled})
}
