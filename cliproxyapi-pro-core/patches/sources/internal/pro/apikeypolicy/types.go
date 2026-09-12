// Package apikeypolicy owns API-key policy identity, persistence, immutable
// request decisions and Management-facing domain operations.
package apikeypolicy

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/router-for-me/CLIProxyAPI/v7/internal/thinking"
)

const (
	ModePassthrough = "passthrough"
	ModeProfile     = "profile"

	StateUnconfigured = "unconfigured"
	StateConfigured   = "configured"
	StateOrphaned     = "orphaned"
	StateUnavailable  = "unavailable"
)

var (
	ErrUnavailable             = errors.New("api key policy unavailable")
	ErrTakeoverStateChanged    = errors.New("api key policy takeover state changed")
	ErrPolicyNotFound          = errors.New("api key policy not found")
	ErrProfileNotFound         = errors.New("api key profile not found")
	ErrVersionConflict         = errors.New("api key policy version conflict")
	ErrActiveProfileDelete     = errors.New("active api key profile cannot be deleted")
	ErrLastProfileDelete       = errors.New("last api key profile cannot be deleted")
	ErrNoProfileConfirmation   = errors.New("removing the active api key profile requires confirmation")
	ErrPassthroughConfirmation = errors.New("policy deletion requires unrestricted passthrough confirmation")
	ErrOrphaned                = errors.New("api key policy is orphaned")
	ErrNotOrphaned             = errors.New("api key policy belongs to a configured upstream key")
	ErrQuotaUnavailable        = errors.New("api key quota unavailable")
	ErrQuotaSettlementStale    = errors.New("api key quota settlement is stale")
	ErrQuotaNotConfigured      = errors.New("api key quota is not configured")
	ErrQuotaResetConfirmation  = errors.New("api key quota reset requires confirmation")
)

const (
	QuotaResetConfirmation = "RESET_API_KEY_QUOTA"
	NoProfileConfirmation  = "REMOVE_ACTIVE_PROFILE_RESTRICTIONS"
)

type profileValidationContextKey struct{}
type quotaTimezoneAwarenessContextKey struct{}
type profileEnforcementToggleContextKey struct{}

// WithProviderModelLinkageValidation opts one Management write into the
// provider/model relationship contract. Older Management clients omit this
// signal and retain the pre-linkage write semantics.
func WithProviderModelLinkageValidation(ctx context.Context) context.Context {
	return context.WithValue(ctx, profileValidationContextKey{}, true)
}

func providerModelLinkageValidationEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(profileValidationContextKey{}).(bool)
	return enabled
}

// WithQuotaTimezoneAwareness opts one Management write into the calendar
// timezone contract. Older clients omit this signal, so writes from them must
// preserve an existing calendar timezone that they cannot edit deliberately.
func WithQuotaTimezoneAwareness(ctx context.Context) context.Context {
	return context.WithValue(ctx, quotaTimezoneAwarenessContextKey{}, true)
}

func quotaTimezoneAwarenessEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(quotaTimezoneAwarenessContextKey{}).(bool)
	return enabled
}

// WithProfileEnforcementToggle opts one Management workspace write into the
// explicit Profile-enforcement contract. Older clients cannot pause or resume
// enforcement accidentally through omitted fields.
func WithProfileEnforcementToggle(ctx context.Context) context.Context {
	return context.WithValue(ctx, profileEnforcementToggleContextKey{}, true)
}

func profileEnforcementToggleEnabled(ctx context.Context) bool {
	enabled, _ := ctx.Value(profileEnforcementToggleContextKey{}).(bool)
	return enabled
}

const PassthroughConfirmation = "RESTORE_UNRESTRICTED_PASSTHROUGH"

// AuthenticatedAPIKeyIdentity can only be constructed from a successful
// upstream access result. Raw keys never leave this constructor.
type AuthenticatedAPIKeyIdentity struct {
	hash string
}

func NewAuthenticatedAPIKeyIdentity(raw string) (AuthenticatedAPIKeyIdentity, error) {
	if raw == "" {
		return AuthenticatedAPIKeyIdentity{}, errors.New("authenticated api key is empty")
	}
	sum := sha256.Sum256([]byte(raw))
	return AuthenticatedAPIKeyIdentity{hash: hex.EncodeToString(sum[:])}, nil
}

func (i AuthenticatedAPIKeyIdentity) Hash() string { return i.hash }
func (i AuthenticatedAPIKeyIdentity) Valid() bool  { return len(i.hash) == sha256.Size*2 }

type identityContextKey struct{}
type decisionContextKey struct{}
type quotaSettlementContextKey struct{}
type quotaAdmissionContextKey struct{}

type quotaAdmissionFunc func(context.Context, RequestPolicyDecision) (RequestPolicyDecision, error)
type quotaUsageSettlementFunc func(context.Context, string, QuotaUsageDelta) error

func WithIdentity(ctx context.Context, identity AuthenticatedAPIKeyIdentity) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if !identity.Valid() {
		return ctx
	}
	return context.WithValue(ctx, identityContextKey{}, identity)
}

func IdentityFromContext(ctx context.Context) (AuthenticatedAPIKeyIdentity, bool) {
	if ctx == nil {
		return AuthenticatedAPIKeyIdentity{}, false
	}
	identity, ok := ctx.Value(identityContextKey{}).(AuthenticatedAPIKeyIdentity)
	return identity, ok && identity.Valid()
}

// InheritContext copies only server-issued API-key policy values from source.
// It is used when handlers intentionally create a new execution context.
func InheritContext(destination, source context.Context) context.Context {
	if destination == nil {
		destination = context.Background()
	}
	if identity, ok := IdentityFromContext(source); ok {
		destination = WithIdentity(destination, identity)
	}
	_, destinationHasDecision := DecisionFromContext(destination)
	if !destinationHasDecision {
		if decision, ok := DecisionFromContext(source); ok {
			destination = WithDecision(destination, decision)
		}
	}
	if admit, ok := source.Value(quotaAdmissionContextKey{}).(quotaAdmissionFunc); ok && admit != nil {
		destination = WithQuotaAdmission(destination, admit)
	}
	if !destinationHasDecision {
		if settle, ok := source.Value(quotaSettlementContextKey{}).(quotaUsageSettlementFunc); ok && settle != nil {
			destination = WithQuotaUsageSettlement(destination, settle)
		}
		if settle, ok := source.Value(quotaSettlementContextKey{}).(func(context.Context, string, int64) error); ok && settle != nil {
			destination = WithQuotaSettlement(destination, settle)
		}
	}
	return destination
}

// WithQuotaAdmission installs a server-owned admission callback for protocols
// that multiplex multiple chargeable turns over one authenticated connection.
func WithQuotaAdmission(ctx context.Context, admit func(context.Context, RequestPolicyDecision) (RequestPolicyDecision, error)) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if admit == nil {
		return ctx
	}
	return context.WithValue(ctx, quotaAdmissionContextKey{}, quotaAdmissionFunc(admit))
}

// AdmitQuotaTurn reserves one request unit and returns a context containing a
// fresh admission/settlement pair. Control frames can skip this call.
func AdmitQuotaTurn(ctx context.Context) (context.Context, error) {
	if ctx == nil {
		ctx = context.Background()
	}
	admit, _ := ctx.Value(quotaAdmissionContextKey{}).(quotaAdmissionFunc)
	decision, ok := DecisionFromContext(ctx)
	if admit == nil || !ok {
		return ctx, nil
	}
	admitted, err := admit(ctx, decision)
	if err != nil {
		return ctx, err
	}
	return WithDecision(ctx, admitted), nil
}

func WithDecision(ctx context.Context, decision RequestPolicyDecision) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	var settle quotaUsageSettlementFunc
	var legacySettle func(context.Context, string, int64) error
	if decision.Snapshot != nil {
		settle = decision.Snapshot.QuotaUsageSettlement
		legacySettle = decision.Snapshot.QuotaSettlement
	}
	ctx = context.WithValue(ctx, decisionContextKey{}, decision.Clone())
	if settle == nil {
		return WithQuotaSettlement(ctx, legacySettle)
	}
	return WithQuotaUsageSettlement(ctx, settle)
}

func DecisionFromContext(ctx context.Context) (RequestPolicyDecision, bool) {
	if ctx == nil {
		return RequestPolicyDecision{}, false
	}
	decision, ok := ctx.Value(decisionContextKey{}).(RequestPolicyDecision)
	return decision.Clone(), ok
}

// WithQuotaSettlement installs synchronous token accounting before a usage
// record enters the asynchronous monitoring bus.
func WithQuotaSettlement(ctx context.Context, settle func(context.Context, string, int64) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if settle == nil {
		return ctx
	}
	return context.WithValue(ctx, quotaSettlementContextKey{}, settle)
}

func SettleQuotaTokens(ctx context.Context, eventID string, totalTokens int64) error {
	if ctx == nil || totalTokens <= 0 {
		return nil
	}
	if settle, ok := ctx.Value(quotaSettlementContextKey{}).(quotaUsageSettlementFunc); ok && settle != nil {
		return settle(ctx, eventID, QuotaUsageDelta{TotalTokens: totalTokens})
	}
	settle, _ := ctx.Value(quotaSettlementContextKey{}).(func(context.Context, string, int64) error)
	if settle != nil {
		return settle(ctx, eventID, totalTokens)
	}
	return nil
}

// WithQuotaUsageSettlement installs synchronous token and cost accounting.
func WithQuotaUsageSettlement(ctx context.Context, settle func(context.Context, string, QuotaUsageDelta) error) context.Context {
	if ctx == nil {
		ctx = context.Background()
	}
	if settle == nil {
		return ctx
	}
	return context.WithValue(ctx, quotaSettlementContextKey{}, quotaUsageSettlementFunc(settle))
}

// SettleQuotaUsage settles the complete usage shape required by token and cost
// quotas. The legacy token-only callback remains supported for older callers.
func SettleQuotaUsage(ctx context.Context, eventID string, usage QuotaUsageDelta) error {
	if ctx == nil || usage.empty() {
		return nil
	}
	if settle, ok := ctx.Value(quotaSettlementContextKey{}).(quotaUsageSettlementFunc); ok && settle != nil {
		return settle(ctx, eventID, usage)
	}
	if settle, ok := ctx.Value(quotaSettlementContextKey{}).(func(context.Context, string, int64) error); ok && settle != nil {
		return settle(ctx, eventID, usage.TotalTokens)
	}
	return nil
}

// QuotaUsageSettlementFromContext freezes only the server-owned settlement
// capability needed by a protocol session that outlives its bootstrap request.
func QuotaUsageSettlementFromContext(ctx context.Context) func(context.Context, string, QuotaUsageDelta) error {
	if ctx == nil {
		return nil
	}
	if settle, ok := ctx.Value(quotaSettlementContextKey{}).(quotaUsageSettlementFunc); ok && settle != nil {
		return func(settleCtx context.Context, eventID string, usage QuotaUsageDelta) error {
			return settle(settleCtx, eventID, usage)
		}
	}
	if settle, ok := ctx.Value(quotaSettlementContextKey{}).(func(context.Context, string, int64) error); ok && settle != nil {
		return func(settleCtx context.Context, eventID string, usage QuotaUsageDelta) error {
			return settle(settleCtx, eventID, usage.TotalTokens)
		}
	}
	return nil
}

type ModelMapping struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type Profile struct {
	ID          string         `json:"id"`
	PolicyID    string         `json:"policyId"`
	Name        string         `json:"name"`
	Providers   []string       `json:"providers"`
	Models      []string       `json:"models"`
	Mappings    []ModelMapping `json:"mappings"`
	CreatedAtMS int64          `json:"createdAtMs"`
	UpdatedAtMS int64          `json:"updatedAtMs"`
}

// ProfileCatalogItem is the lightweight Management-facing identity for one
// persisted Profile. It intentionally excludes policy rules and API-key data.
type ProfileCatalogItem struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	UpdatedAtMS int64  `json:"updatedAtMs"`
}

type ProfileCatalogSnapshot struct {
	Items            []ProfileCatalogItem `json:"items"`
	PolicyGeneration uint64               `json:"policyGeneration"`
}

type Policy struct {
	ID              string    `json:"id"`
	APIKeyHash      string    `json:"-"`
	DisplayName     string    `json:"displayName"`
	ProfileEnabled  bool      `json:"profileEnabled"`
	ActiveProfileID string    `json:"activeProfileId"`
	Version         int64     `json:"version"`
	CreatedAtMS     int64     `json:"createdAtMs"`
	UpdatedAtMS     int64     `json:"updatedAtMs"`
	Profiles        []Profile `json:"profiles"`
	Quota           *Quota    `json:"quota,omitempty"`
	State           string    `json:"state,omitempty"`
}

// Quota is the API-key-wide budget. Profile IDs remain usage attribution only:
// switching, renaming or recreating a Profile never creates another key budget.
type Quota struct {
	Enabled     bool        `json:"enabled"`
	Requests    *int64      `json:"requests,omitempty"`
	TotalTokens *int64      `json:"totalTokens,omitempty"`
	Cost        *float64    `json:"cost,omitempty"`
	Period      QuotaPeriod `json:"period"`
	Epoch       int64       `json:"epoch"`
	StartedAtMS int64       `json:"startedAtMs"`
	UpdatedAtMS int64       `json:"updatedAtMs"`
	Usage       QuotaUsage  `json:"usage"`
}

type QuotaInput struct {
	Enabled     bool        `json:"enabled"`
	Requests    *int64      `json:"requests,omitempty"`
	TotalTokens *int64      `json:"totalTokens,omitempty"`
	Cost        *float64    `json:"cost,omitempty"`
	Period      QuotaPeriod `json:"period"`
}

const (
	QuotaPeriodAllTime          = "all_time"
	QuotaPeriodPastDuration     = "past_duration"
	QuotaPeriodCalendarDuration = "calendar_duration"
)

type QuotaPeriod struct {
	Type     string `json:"type"`
	Value    *int64 `json:"value,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Timezone string `json:"timezone,omitempty"`
}

type QuotaUpdate struct {
	Present bool
	Value   *QuotaInput
}

type QuotaUsage struct {
	RequestsUsed      int64    `json:"requestsUsed"`
	TotalTokensUsed   int64    `json:"totalTokensUsed"`
	CostUsed          float64  `json:"costUsed"`
	RequestsRemaining *int64   `json:"requestsRemaining,omitempty"`
	TokensRemaining   *int64   `json:"totalTokensRemaining,omitempty"`
	CostRemaining     *float64 `json:"costRemaining,omitempty"`
	WindowStartedAtMS int64    `json:"windowStartedAtMs"`
	WindowEndsAtMS    int64    `json:"windowEndsAtMs,omitempty"`
	Exhausted         []string `json:"exhausted"`
}

const (
	QuotaAdmissionAvailable = "available"
	QuotaAdmissionDisabled  = "disabled"
	QuotaAdmissionExhausted = "exhausted"
	QuotaAdmissionBlocked   = "blocked"

	QuotaBlockPricingStore    = "pricing_store_unavailable"
	QuotaBlockSettlementStore = "settlement_store_unavailable"
)

// QuotaSummary is the lightweight Management view of one Key-wide budget.
// It intentionally excludes API-key fingerprints and Profile rules.
type QuotaSummary struct {
	PolicyID        string `json:"policyId"`
	PolicyVersion   int64  `json:"policyVersion"`
	Quota           *Quota `json:"quota,omitempty"`
	AdmissionState  string `json:"admissionState"`
	BlockedReason   string `json:"blockedReason,omitempty"`
	NextRecoverAtMS int64  `json:"nextRecoverAtMs,omitempty"`
}

// QuotaUsageDelta is the provider usage required to settle both token and
// price quotas. Cost is evaluated server-side from the active model-price rule.
type QuotaUsageDelta struct {
	Provider             string `json:"provider,omitempty"`
	AuthType             string `json:"authType,omitempty"`
	Model                string `json:"model,omitempty"`
	InputTokens          int64  `json:"inputTokens,omitempty"`
	OutputTokens         int64  `json:"outputTokens,omitempty"`
	ReasoningTokens      int64  `json:"reasoningTokens,omitempty"`
	CachedTokens         int64  `json:"cachedTokens,omitempty"`
	CacheTokens          int64  `json:"cacheTokens,omitempty"`
	CacheReadTokens      int64  `json:"cacheReadTokens,omitempty"`
	CacheWriteTokens     int64  `json:"cacheWriteTokens,omitempty"`
	UncachedInputTokens  int64  `json:"uncachedInputTokens,omitempty"`
	AccountingQuality    string `json:"accountingQuality,omitempty"`
	TotalTokens          int64  `json:"totalTokens,omitempty"`
	ServiceTier          string `json:"serviceTier,omitempty"`
	EffectiveServiceTier string `json:"effectiveServiceTier,omitempty"`
	Speed                string `json:"speed,omitempty"`
	EffectiveSpeed       string `json:"effectiveSpeed,omitempty"`
}

func (u QuotaUsageDelta) empty() bool {
	return u.TotalTokens <= 0 && u.InputTokens <= 0 && u.OutputTokens <= 0 && u.ReasoningTokens <= 0 &&
		u.CachedTokens <= 0 && u.CacheTokens <= 0 && u.CacheReadTokens <= 0 && u.CacheWriteTokens <= 0 && u.UncachedInputTokens <= 0
}

type QuotaExceededError struct {
	Metric    string
	Used      int64
	Limit     int64
	ResetAtMS int64
}

func (e *QuotaExceededError) Error() string {
	if e == nil {
		return "api key quota exceeded"
	}
	if e.Metric == "cost" {
		return fmt.Sprintf("api key cost quota exceeded: $%.6f/$%.6f", microsToUSD(e.Used), microsToUSD(e.Limit))
	}
	return fmt.Sprintf("api key %s quota exceeded: %d/%d", e.Metric, e.Used, e.Limit)
}

func (e *QuotaExceededError) StatusCode() int { return 429 }

// AuditRecord is retained across policy backups. Fingerprints and raw keys are
// intentionally absent; policy_id may refer to a policy that was deleted by
// the audited security operation.
type AuditRecord struct {
	ID          int64           `json:"id"`
	PolicyID    string          `json:"policyId"`
	EventType   string          `json:"eventType"`
	Details     json.RawMessage `json:"details"`
	CreatedAtMS int64           `json:"createdAtMs"`
}

type ProfileInput struct {
	Name      string         `json:"name"`
	Providers []string       `json:"providers"`
	Models    []string       `json:"models"`
	Mappings  []ModelMapping `json:"mappings"`
}

// WorkspaceUpdate is the atomic Management write unit for one open policy
// workspace. ProfileID selects an existing profile; CreateProfile requests a
// new profile. ProfileEnabled, when present, atomically pauses or resumes
// enforcement without deleting saved Profiles. ActiveProfileID selects the
// Profile to enforce when resuming. A nil Profile otherwise updates only the
// display name and quota.
type ConcurrencyUpdate struct {
	Limit         int `json:"limit"`
	ExpectedLimit int `json:"expectedLimit"`
}

type WorkspaceUpdate struct {
	Concurrency     *ConcurrencyUpdate
	DisplayName     string
	ProfileID       string
	Profile         *ProfileInput
	CreateProfile   bool
	ProfileEnabled  *bool
	ActiveProfileID string
	Quota           QuotaUpdate
}

// ProfileCatalog is the server-authoritative set of provider and model IDs
// accepted by new Profile writes. The runtime registry may change between
// calls, so callers obtain a fresh catalog for every write.
type ProfileCatalog struct {
	Providers      []string            `json:"providers"`
	Models         []string            `json:"models"`
	ModelProviders map[string][]string `json:"modelProviders"`
}

func NewProfileCatalog(providers, models []string, modelProviders ...map[string][]string) ProfileCatalog {
	normalizedModels := normalizeUnique(models, normalizeModel)
	normalizedModelProviders := make(map[string][]string, len(normalizedModels))
	if len(modelProviders) > 0 {
		for _, model := range normalizedModels {
			normalizedModelProviders[model] = normalizeUnique(modelProviders[0][model], normalizeProvider)
		}
	}
	return ProfileCatalog{
		Providers:      normalizeUnique(providers, normalizeProvider),
		Models:         normalizedModels,
		ModelProviders: normalizedModelProviders,
	}
}

func (c ProfileCatalog) Validate(input ProfileInput) error {
	input, err := normalizeProfileInput(input)
	if err != nil {
		return err
	}
	providers := make(map[string]struct{}, len(c.Providers))
	for _, provider := range normalizeUnique(c.Providers, normalizeProvider) {
		providers[provider] = struct{}{}
	}
	models := make(map[string]struct{}, len(c.Models))
	for _, model := range normalizeUnique(c.Models, normalizeModel) {
		models[model] = struct{}{}
	}
	for _, provider := range input.Providers {
		if _, ok := providers[provider]; !ok {
			return fmt.Errorf("unknown provider %q", provider)
		}
	}
	for _, model := range input.Models {
		if _, ok := models[model]; !ok {
			return fmt.Errorf("unknown model %q", model)
		}
	}
	for _, mapping := range input.Mappings {
		if _, ok := models[mapping.Target]; !ok {
			return fmt.Errorf("unknown model %q", mapping.Target)
		}
	}
	return nil
}

func (c ProfileCatalog) ValidateProviderModelLinkage(input ProfileInput) error {
	input, err := normalizeProfileInput(input)
	if err != nil {
		return err
	}
	for _, model := range input.Models {
		if !c.modelMatchesProviders(model, input.Providers) {
			return fmt.Errorf("model %q is not available from an allowed provider", model)
		}
	}
	for _, mapping := range input.Mappings {
		if !c.modelMatchesProviders(mapping.Target, input.Providers) {
			return fmt.Errorf("model mapping target %q is not available from an allowed provider", mapping.Target)
		}
	}
	return nil
}

func (c ProfileCatalog) modelMatchesProviders(model string, allowedProviders []string) bool {
	if len(allowedProviders) == 0 || len(c.ModelProviders) == 0 {
		return true
	}
	allowed := make(map[string]struct{}, len(allowedProviders))
	for _, provider := range allowedProviders {
		allowed[normalizeProvider(provider)] = struct{}{}
	}
	for _, provider := range c.ModelProviders[normalizeModel(model)] {
		if _, ok := allowed[normalizeProvider(provider)]; ok {
			return true
		}
	}
	return false
}

type RequestPolicySnapshot struct {
	PolicyID               string
	APIKeyHash             string
	ProfileID              string
	ProfileName            string
	Version                int64
	ModelMappings          map[string]string
	AllowedModels          map[string]struct{}
	AllowedProviders       map[string]struct{}
	RequestedModel         string
	EffectiveModel         string
	Quota                  *Quota
	QuotaRuntimeGeneration uint64
	QuotaAdmissionID       string
	QuotaSettlement        func(context.Context, string, int64) error
	QuotaUsageSettlement   quotaUsageSettlementFunc
}

func (s RequestPolicySnapshot) Clone() RequestPolicySnapshot {
	s.ModelMappings = cloneStringMap(s.ModelMappings)
	s.AllowedModels = cloneStringSet(s.AllowedModels)
	s.AllowedProviders = cloneStringSet(s.AllowedProviders)
	s.Quota = cloneQuota(s.Quota)
	s.QuotaSettlement = nil
	s.QuotaUsageSettlement = nil
	return s
}

func cloneQuota(quota *Quota) *Quota {
	if quota == nil {
		return nil
	}
	cloned := *quota
	if quota.Requests != nil {
		value := *quota.Requests
		cloned.Requests = &value
	}
	if quota.TotalTokens != nil {
		value := *quota.TotalTokens
		cloned.TotalTokens = &value
	}
	if quota.Cost != nil {
		value := *quota.Cost
		cloned.Cost = &value
	}
	if quota.Period.Value != nil {
		value := *quota.Period.Value
		cloned.Period.Value = &value
	}
	return &cloned
}

func (s RequestPolicySnapshot) allowsModel(model string) bool {
	if len(s.AllowedModels) == 0 {
		return true
	}
	_, allowed := s.AllowedModels[normalizeModel(model)]
	return allowed
}

func (s RequestPolicySnapshot) allowsProvider(provider string) bool {
	if len(s.AllowedProviders) == 0 {
		return true
	}
	_, allowed := s.AllowedProviders[normalizeProvider(provider)]
	return allowed
}

type RequestPolicyDecision struct {
	Mode     string
	Snapshot *RequestPolicySnapshot
}

// UsageAttribution is copied from the immutable request decision by usage
// sinks. It never contains a raw API key.
type UsageAttribution struct {
	PolicyMode     string
	APIKeyPolicyID string
	ProfileID      string
	ProfileName    string
	RequestedModel string
	EffectiveModel string
}

type QuotaAttribution struct {
	PolicyID    string
	ProfileID   string
	Epoch       int64
	AdmissionID string
}

func QuotaUsageEventID(attribution QuotaAttribution, attemptIndex *int64) string {
	if attribution.AdmissionID == "" {
		return ""
	}
	if attemptIndex == nil {
		return attribution.AdmissionID + ":attempt:unknown"
	}
	return fmt.Sprintf("%s:attempt:%d", attribution.AdmissionID, *attemptIndex)
}

func (d RequestPolicyDecision) QuotaAttribution() (QuotaAttribution, bool) {
	if d.Mode != ModeProfile || d.Snapshot == nil || d.Snapshot.Quota == nil || !d.Snapshot.Quota.Enabled || d.Snapshot.QuotaAdmissionID == "" {
		return QuotaAttribution{}, false
	}
	return QuotaAttribution{
		PolicyID: d.Snapshot.PolicyID, ProfileID: d.Snapshot.ProfileID,
		Epoch: d.Snapshot.Quota.Epoch, AdmissionID: d.Snapshot.QuotaAdmissionID,
	}, true
}

func (d RequestPolicyDecision) UsageAttribution() UsageAttribution {
	attribution := UsageAttribution{PolicyMode: d.Mode}
	if d.Mode != ModeProfile || d.Snapshot == nil {
		return attribution
	}
	attribution.APIKeyPolicyID = d.Snapshot.PolicyID
	attribution.ProfileID = d.Snapshot.ProfileID
	attribution.ProfileName = d.Snapshot.ProfileName
	attribution.RequestedModel = d.Snapshot.RequestedModel
	attribution.EffectiveModel = d.Snapshot.EffectiveModel
	return attribution
}

type ModelCandidate struct {
	ID        string
	Providers []string
}

type VisibleModel struct {
	ID          string
	EffectiveID string
}

func PassthroughDecision() RequestPolicyDecision {
	return RequestPolicyDecision{Mode: ModePassthrough}
}

func (d RequestPolicyDecision) Clone() RequestPolicyDecision {
	if d.Snapshot != nil {
		cloned := d.Snapshot.Clone()
		d.Snapshot = &cloned
	}
	return d
}

func (d RequestPolicyDecision) ApplyModel(requested string) (string, error) {
	if d.Mode == ModePassthrough {
		return requested, nil
	}
	if d.Mode != ModeProfile || d.Snapshot == nil {
		return "", ErrUnavailable
	}
	return d.applyProfileModel(requested, true)
}

// HasExactModelMapping reports whether the active Profile owns the precise
// client-facing model (before auto or thinking-suffix normalization).
func (d RequestPolicyDecision) HasExactModelMapping(model string) bool {
	if d.Mode != ModeProfile || d.Snapshot == nil {
		return false
	}
	_, ok := d.Snapshot.ModelMappings[normalizeModel(model)]
	return ok
}

// ValidateEffectiveModel checks a model selected after the initial profile
// mapping (for example, a ModelRouter target). It deliberately does not apply
// mappings a second time: a router target is an execution model, not another
// client alias.
func (d RequestPolicyDecision) ValidateEffectiveModel(model string) (string, error) {
	if d.Mode == ModePassthrough {
		return model, nil
	}
	if d.Mode != ModeProfile || d.Snapshot == nil {
		return "", ErrUnavailable
	}
	return d.applyProfileModel(model, false)
}

func (d RequestPolicyDecision) applyProfileModel(model string, applyMapping bool) (string, error) {
	model = normalizeModel(model)
	if model == "" {
		return "", profileModelForbiddenError()
	}

	// Exact registered models (including custom IDs ending in parentheses) and
	// exact aliases win before a trailing segment is interpreted as thinking.
	if applyMapping {
		if mapped := normalizeModel(d.Snapshot.ModelMappings[model]); mapped != "" {
			if !d.Snapshot.allowsModel(mapped) {
				return "", profileModelForbiddenError()
			}
			return mapped, nil
		}
	}
	if len(d.Snapshot.AllowedModels) == 0 {
		if applyMapping {
			parsed := thinking.ParseSuffix(model)
			baseModel := normalizeModel(parsed.ModelName)
			if parsed.HasSuffix && baseModel != "" && baseModel != model {
				if mapped := normalizeModel(d.Snapshot.ModelMappings[baseModel]); mapped != "" {
					return mapped + "(" + parsed.RawSuffix + ")", nil
				}
			}
		}
		return model, nil
	}
	if d.Snapshot.allowsModel(model) {
		return model, nil
	}

	parsed := thinking.ParseSuffix(model)
	baseModel := normalizeModel(parsed.ModelName)
	if !parsed.HasSuffix || baseModel == "" || baseModel == model {
		return "", profileModelForbiddenError()
	}
	effectiveBase := baseModel
	if applyMapping {
		if mapped := normalizeModel(d.Snapshot.ModelMappings[baseModel]); mapped != "" {
			effectiveBase = mapped
		}
	}
	if !d.Snapshot.allowsModel(effectiveBase) {
		return "", profileModelForbiddenError()
	}
	return effectiveBase + "(" + parsed.RawSuffix + ")", nil
}

func profileModelForbiddenError() error {
	return &PolicyError{Code: "profile_model_forbidden", Message: "model is not allowed by the active API key profile"}
}

// WithModels records the client-requested and policy-effective model on a
// cloned decision. The returned value is safe to attach to one request.
func (d RequestPolicyDecision) WithModels(requested, effective string) RequestPolicyDecision {
	d = d.Clone()
	if d.Snapshot != nil {
		d.Snapshot.RequestedModel = strings.TrimSpace(requested)
		d.Snapshot.EffectiveModel = strings.TrimSpace(effective)
	}
	return d
}

func (d RequestPolicyDecision) AllowsProvider(provider string) error {
	if d.Mode == ModePassthrough {
		return nil
	}
	if d.Mode != ModeProfile || d.Snapshot == nil {
		return ErrUnavailable
	}
	provider = normalizeProvider(provider)
	if provider == "" {
		return &PolicyError{Code: "profile_provider_forbidden", Message: "execution provider cannot be resolved for the active API key profile"}
	}
	if !d.Snapshot.allowsProvider(provider) {
		return &PolicyError{Code: "profile_provider_forbidden", Message: "provider is not allowed by the active API key profile"}
	}
	return nil
}

func (d RequestPolicyDecision) FilterProviders(providers []string) ([]string, error) {
	if d.Mode == ModePassthrough {
		return append([]string(nil), providers...), nil
	}
	if d.Mode != ModeProfile || d.Snapshot == nil {
		return nil, ErrUnavailable
	}
	out := make([]string, 0, len(providers))
	seen := make(map[string]struct{})
	for _, provider := range providers {
		provider = normalizeProvider(provider)
		if provider == "" {
			continue
		}
		if !d.Snapshot.allowsProvider(provider) {
			continue
		}
		if _, duplicate := seen[provider]; duplicate {
			continue
		}
		seen[provider] = struct{}{}
		out = append(out, provider)
	}
	if len(out) == 0 {
		return nil, &PolicyError{Code: "profile_provider_forbidden", Message: "no provider is allowed by the active API key profile"}
	}
	return out, nil
}

// FilterVisibleModels returns canonical allowed models plus exact mapping
// aliases whose targets are currently carried by at least one allowed provider.
func (d RequestPolicyDecision) FilterVisibleModels(candidates []ModelCandidate) ([]VisibleModel, error) {
	if d.Mode == ModePassthrough {
		out := make([]VisibleModel, 0, len(candidates))
		for _, candidate := range candidates {
			if id := normalizeModel(candidate.ID); id != "" {
				out = append(out, VisibleModel{ID: id, EffectiveID: id})
			}
		}
		return out, nil
	}
	if d.Mode != ModeProfile || d.Snapshot == nil {
		return nil, ErrUnavailable
	}
	available := make(map[string]ModelCandidate, len(candidates))
	order := make([]string, 0, len(candidates))
	for _, candidate := range candidates {
		candidate.ID = normalizeModel(candidate.ID)
		if candidate.ID == "" {
			continue
		}
		if _, exists := available[candidate.ID]; !exists {
			order = append(order, candidate.ID)
		}
		available[candidate.ID] = candidate
	}
	providerVisible := func(candidate ModelCandidate) bool {
		if len(d.Snapshot.AllowedProviders) == 0 {
			return true
		}
		for _, provider := range candidate.Providers {
			if d.Snapshot.allowsProvider(normalizeProvider(provider)) {
				return true
			}
		}
		return false
	}
	out := make([]VisibleModel, 0, len(order)+len(d.Snapshot.ModelMappings))
	for _, id := range order {
		candidate := available[id]
		if d.Snapshot.allowsModel(id) && providerVisible(candidate) {
			out = append(out, VisibleModel{ID: id, EffectiveID: id})
		}
	}
	aliases := make([]string, 0, len(d.Snapshot.ModelMappings))
	for alias := range d.Snapshot.ModelMappings {
		aliases = append(aliases, alias)
	}
	sort.Strings(aliases)
	for _, alias := range aliases {
		target := d.Snapshot.ModelMappings[alias]
		candidate, exists := available[target]
		if exists && providerVisible(candidate) {
			out = append(out, VisibleModel{ID: alias, EffectiveID: target})
		}
	}
	return out, nil
}

type PolicyError struct {
	Code    string
	Message string
}

func (e *PolicyError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func normalizeProfileInput(input ProfileInput) (ProfileInput, error) {
	input.Name = strings.TrimSpace(input.Name)
	if input.Name == "" {
		return ProfileInput{}, errors.New("profile name is required")
	}
	input.Providers = normalizeUnique(input.Providers, normalizeProvider)
	input.Models = normalizeUnique(input.Models, normalizeModel)
	allowedModels := make(map[string]struct{}, len(input.Models))
	for _, model := range input.Models {
		allowedModels[model] = struct{}{}
	}
	mappings := make([]ModelMapping, 0, len(input.Mappings))
	sources := make(map[string]struct{}, len(input.Mappings))
	for _, mapping := range input.Mappings {
		mapping.Source = normalizeModel(mapping.Source)
		mapping.Target = normalizeModel(mapping.Target)
		if mapping.Source == "" || mapping.Target == "" || mapping.Source == mapping.Target {
			return ProfileInput{}, errors.New("model mapping requires distinct source and target models")
		}
		if _, duplicate := sources[mapping.Source]; duplicate {
			return ProfileInput{}, errors.New("model mapping source must be unique")
		}
		if _, allowed := allowedModels[mapping.Target]; len(allowedModels) > 0 && !allowed {
			return ProfileInput{}, errors.New("model mapping target must be allowed")
		}
		sources[mapping.Source] = struct{}{}
		mappings = append(mappings, mapping)
	}
	for _, mapping := range mappings {
		if _, chained := sources[mapping.Target]; chained {
			return ProfileInput{}, errors.New("chained model mappings are not allowed")
		}
	}
	sort.Slice(mappings, func(i, j int) bool { return mappings[i].Source < mappings[j].Source })
	input.Mappings = mappings
	return input, nil
}

func normalizeProvider(value string) string { return strings.ToLower(strings.TrimSpace(value)) }
func normalizeModel(value string) string    { return strings.TrimSpace(value) }

func normalizeUnique(values []string, normalize func(string) string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		value = normalize(value)
		if value == "" {
			continue
		}
		if _, exists := seen[value]; exists {
			continue
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	sort.Strings(out)
	return out
}

func cloneStringMap(in map[string]string) map[string]string {
	out := make(map[string]string, len(in))
	for key, value := range in {
		out[key] = value
	}
	return out
}

func cloneStringSet(in map[string]struct{}) map[string]struct{} {
	out := make(map[string]struct{}, len(in))
	for value := range in {
		out[value] = struct{}{}
	}
	return out
}
