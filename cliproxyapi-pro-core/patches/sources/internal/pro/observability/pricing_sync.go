package observability

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"reflect"
	"sort"
	"strings"
	"time"
)

const modelsDevAPIURL = "https://models.dev/api.json"
const modelsDevResponseLimit = 32 << 20
const modelsDevSearchCacheTTL = 10 * time.Minute

type ModelPriceSyncState struct {
	Status          string          `json:"status"`
	ETag            string          `json:"etag,omitempty"`
	ObservedHash    string          `json:"observedHash,omitempty"`
	LastAttempt     int64           `json:"lastAttemptMs,omitempty"`
	LastSuccess     int64           `json:"lastSuccessMs,omitempty"`
	Matched         int             `json:"matched"`
	Added           int             `json:"added"`
	Updated         int             `json:"updated"`
	Unchanged       int             `json:"unchanged"`
	Locked          int             `json:"locked"`
	Unmatched       int             `json:"unmatched"`
	UnmatchedModels []ObservedModel `json:"unmatchedModels"`
	Recalculated    int64           `json:"recalculated"`
	Error           string          `json:"error,omitempty"`
}

type ModelPriceSyncResult struct {
	DryRun       bool                   `json:"dryRun"`
	NotModified  bool                   `json:"notModified"`
	Matched      int                    `json:"matched"`
	Added        int                    `json:"added"`
	Updated      int                    `json:"updated"`
	Overridden   int                    `json:"overridden"`
	Unchanged    int                    `json:"unchanged"`
	Locked       int                    `json:"locked"`
	Unmatched    []ObservedModel        `json:"unmatched"`
	Changes      []ModelPriceSyncChange `json:"changes"`
	Recalculated int64                  `json:"recalculated"`
}

type ModelPriceSyncChange struct {
	Action         string          `json:"action"`
	Model          string          `json:"model"`
	Requests       int64           `json:"requests"`
	SourceProvider string          `json:"sourceProvider,omitempty"`
	SourceModel    string          `json:"sourceModel,omitempty"`
	Before         *ModelPriceRule `json:"before,omitempty"`
	After          *ModelPriceRule `json:"after,omitempty"`
}

func modelPriceSyncChangeRank(action string) int {
	switch action {
	case "added":
		return 0
	case "updated":
		return 1
	case "overridden":
		return 2
	case "locked":
		return 3
	case "unmatched":
		return 4
	default:
		return 5
	}
}

type modelsDevCost struct {
	Input      float64             `json:"input"`
	Output     float64             `json:"output"`
	CacheRead  float64             `json:"cache_read"`
	CacheWrite float64             `json:"cache_write"`
	Reasoning  float64             `json:"reasoning"`
	Tiers      []modelsDevCostTier `json:"tiers"`
}

type modelsDevCostTier struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cache_read"`
	CacheWrite float64 `json:"cache_write"`
	Reasoning  float64 `json:"reasoning"`
	Tier       struct {
		Type string `json:"type"`
		Size int64  `json:"size"`
	} `json:"tier"`
}

type modelsDevMode struct {
	Cost     *modelsDevCost `json:"cost"`
	Provider struct {
		Body map[string]any `json:"body"`
	} `json:"provider"`
}

type modelsDevModel struct {
	ID           string         `json:"id"`
	Name         string         `json:"name"`
	LastUpdated  string         `json:"last_updated"`
	Cost         *modelsDevCost `json:"cost"`
	Experimental struct {
		Modes map[string]modelsDevMode `json:"modes"`
	} `json:"experimental"`
}

type modelsDevProvider struct {
	ID     string                    `json:"id"`
	Name   string                    `json:"name"`
	Models map[string]modelsDevModel `json:"models"`
}

type ModelsDevPriceSearchItem struct {
	Provider     string         `json:"provider"`
	ProviderName string         `json:"providerName,omitempty"`
	Model        string         `json:"model"`
	ModelName    string         `json:"modelName,omitempty"`
	LastUpdated  string         `json:"lastUpdated,omitempty"`
	Rule         ModelPriceRule `json:"rule"`
}

type rankedModelsDevPriceSearchItem struct {
	item  ModelsDevPriceSearchItem
	score int
}

var modelPriceProviderAliases = map[string]string{
	"codex":      "openai",
	"claude":     "anthropic",
	"gemini":     "google",
	"gemini-cli": "google",
	"vertex":     "google-vertex",
	"vertex-ai":  "google-vertex",
	"x-ai":       "xai",
}

var modelPriceCanonicalProviders = []string{
	"openai", "anthropic", "google", "google-vertex", "xai", "deepseek", "mistral", "cohere", "meta", "amazon-bedrock",
}

func modelPriceRateFromModelsDev(cost modelsDevCost) ModelPriceRate {
	return ModelPriceRate{Input: cost.Input, Output: cost.Output, CacheRead: cost.CacheRead, CacheWrite: cost.CacheWrite, Reasoning: cost.Reasoning}
}

func modelPriceRuleFromModelsDev(observed ObservedModel, providerID, modelID string, model modelsDevModel, now int64) ModelPriceRule {
	rule := ModelPriceRule{
		Model: observed.Model,
		Base:  modelPriceRateFromModelsDev(*model.Cost), Source: modelPriceSourceModelsDev,
		SourceProvider: providerID, SourceModel: modelID, EffectiveFrom: now, FetchedAt: now,
		UpstreamUpdate: model.LastUpdated,
	}
	for _, tier := range model.Cost.Tiers {
		if tier.Tier.Type != "" && tier.Tier.Type != "context" {
			continue
		}
		rule.Tiers = append(rule.Tiers, ModelPriceTier{
			ContextSize:    tier.Tier.Size,
			ModelPriceRate: ModelPriceRate{Input: tier.Input, Output: tier.Output, CacheRead: tier.CacheRead, CacheWrite: tier.CacheWrite, Reasoning: tier.Reasoning},
		})
	}
	for _, mode := range model.Experimental.Modes {
		if mode.Cost == nil {
			continue
		}
		rate := modelPriceRateFromModelsDev(*mode.Cost)
		serviceTier, _ := mode.Provider.Body["service_tier"].(string)
		serviceTier = normalizeServiceTierName(serviceTier)
		if serviceTier != "" {
			if rule.ServiceTiers == nil {
				rule.ServiceTiers = map[string]ModelPriceRate{}
			}
			rule.ServiceTiers[serviceTier] = rate
		}
		speed, _ := mode.Provider.Body["speed"].(string)
		speed = normalizeSpeedName(speed)
		if speed != "" {
			if rule.Speeds == nil {
				rule.Speeds = map[string]ModelPriceRate{}
			}
			rule.Speeds[speed] = rate
		}
	}
	return normalizePriceRule(rule)
}

func modelsDevSearchScore(query string, providerID string, provider modelsDevProvider, modelID string, model modelsDevModel) (int, bool) {
	query = strings.ToLower(strings.TrimSpace(query))
	if query == "" {
		return 0, false
	}
	modelIDLower := strings.ToLower(modelID)
	modelNameLower := strings.ToLower(strings.TrimSpace(model.Name))
	qualifiedID := strings.ToLower(providerID + "/" + modelID)
	searchText := strings.Join([]string{qualifiedID, strings.ToLower(provider.Name), modelNameLower}, "\n")
	for _, token := range strings.Fields(query) {
		if !strings.Contains(searchText, token) {
			return 0, false
		}
	}
	switch {
	case query == modelIDLower || query == qualifiedID:
		return 0, true
	case strings.HasPrefix(modelIDLower, query):
		return 1, true
	case strings.Contains(modelIDLower, query):
		return 2, true
	case modelNameLower != "" && strings.HasPrefix(modelNameLower, query):
		return 3, true
	default:
		return 4, true
	}
}

func searchModelsDevCatalog(catalog map[string]modelsDevProvider, query, providerFilter string, limit int) []ModelsDevPriceSearchItem {
	providerFilter = normalizePriceProvider(providerFilter)
	if limit <= 0 {
		limit = 20
	}
	if limit > 50 {
		limit = 50
	}
	ranked := make([]rankedModelsDevPriceSearchItem, 0)
	for providerID, provider := range catalog {
		if providerFilter != "" && normalizePriceProvider(providerID) != providerFilter {
			continue
		}
		for modelID, model := range provider.Models {
			if model.Cost == nil {
				continue
			}
			score, matched := modelsDevSearchScore(query, providerID, provider, modelID, model)
			if !matched {
				continue
			}
			rule := modelPriceRuleFromModelsDev(ObservedModel{Model: modelID}, providerID, modelID, model, time.Now().UnixMilli())
			ranked = append(ranked, rankedModelsDevPriceSearchItem{
				score: score,
				item: ModelsDevPriceSearchItem{
					Provider: providerID, ProviderName: provider.Name, Model: modelID, ModelName: model.Name,
					LastUpdated: model.LastUpdated, Rule: rule,
				},
			})
		}
	}
	sort.Slice(ranked, func(left, right int) bool {
		if ranked[left].score != ranked[right].score {
			return ranked[left].score < ranked[right].score
		}
		if ranked[left].item.Provider != ranked[right].item.Provider {
			return ranked[left].item.Provider < ranked[right].item.Provider
		}
		return ranked[left].item.Model < ranked[right].item.Model
	})
	if len(ranked) > limit {
		ranked = ranked[:limit]
	}
	items := make([]ModelsDevPriceSearchItem, len(ranked))
	for index := range ranked {
		items[index] = ranked[index].item
	}
	return items
}

func (s *Store) SearchModelsDevPrices(ctx context.Context, query, provider string, limit int) ([]ModelsDevPriceSearchItem, error) {
	s.priceCatalogMu.Lock()
	defer s.priceCatalogMu.Unlock()
	now := time.Now()
	if s.priceCatalog != nil && now.Before(s.priceCatalogExpiresAt) {
		return searchModelsDevCatalog(s.priceCatalog, query, provider, limit), nil
	}
	catalog, etag, notModified, err := fetchModelsDevCatalog(ctx, s.priceCatalogETag)
	if err != nil {
		return nil, err
	}
	if !notModified {
		s.priceCatalog = catalog
		s.priceCatalogETag = etag
	}
	if s.priceCatalog == nil {
		return nil, fmt.Errorf("models.dev catalog is unavailable")
	}
	s.priceCatalogExpiresAt = now.Add(modelsDevSearchCacheTTL)
	return searchModelsDevCatalog(s.priceCatalog, query, provider, limit), nil
}

func providerCandidates(value string) []string {
	normalized := normalizePriceProvider(value)
	candidates := make([]string, 0, 2)
	if normalized != "" {
		candidates = append(candidates, normalized)
	}
	if alias := modelPriceProviderAliases[normalized]; alias != "" && alias != normalized {
		candidates = append(candidates, alias)
	}
	return candidates
}

func inferredProviderCandidates(model string) []string {
	model = strings.ToLower(strings.TrimSpace(model))
	switch {
	case strings.Contains(model, "gemini"):
		return []string{"google", "google-vertex"}
	case strings.Contains(model, "claude"):
		return []string{"anthropic"}
	case strings.Contains(model, "grok"):
		return []string{"xai"}
	case strings.Contains(model, "deepseek"):
		return []string{"deepseek"}
	case strings.Contains(model, "mistral") || strings.Contains(model, "codestral"):
		return []string{"mistral"}
	case strings.HasPrefix(model, "gpt-") || strings.HasPrefix(model, "chatgpt-") || strings.HasPrefix(model, "codex-") || strings.HasPrefix(model, "o1") || strings.HasPrefix(model, "o3") || strings.HasPrefix(model, "o4"):
		return []string{"openai"}
	default:
		return nil
	}
}

func catalogProviderCandidates(catalog map[string]modelsDevProvider, observed ObservedModel) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, len(catalog))
	appendProvider := func(value string) {
		value = normalizePriceProvider(value)
		if value == "" {
			return
		}
		if _, exists := catalog[value]; !exists {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	for _, providerID := range providerCandidates(observed.Provider) {
		appendProvider(providerID)
	}
	for _, providerID := range inferredProviderCandidates(observed.Model) {
		appendProvider(providerID)
	}
	for _, providerID := range modelPriceCanonicalProviders {
		appendProvider(providerID)
	}
	rest := make([]string, 0, len(catalog))
	for providerID := range catalog {
		if _, exists := seen[providerID]; !exists {
			rest = append(rest, providerID)
		}
	}
	sort.Strings(rest)
	for _, providerID := range rest {
		appendProvider(providerID)
	}
	return out
}

func modelCandidates(observed ObservedModel, providerID string) []string {
	seen := map[string]struct{}{}
	out := make([]string, 0, 4)
	appendCandidate := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, exists := seen[value]; exists {
			return
		}
		seen[value] = struct{}{}
		out = append(out, value)
	}
	appendCandidate(observed.Model)
	appendCandidate(observed.Alias)
	for _, value := range []string{observed.Model, observed.Alias} {
		prefix := providerID + "/"
		if strings.HasPrefix(strings.ToLower(value), prefix) {
			appendCandidate(value[len(prefix):])
		}
	}
	return out
}

func matchModelsDevModel(catalog map[string]modelsDevProvider, observed ObservedModel) (string, string, modelsDevModel, bool) {
	for _, providerID := range catalogProviderCandidates(catalog, observed) {
		provider, ok := catalog[providerID]
		if !ok {
			continue
		}
		for _, modelID := range modelCandidates(observed, providerID) {
			if model, ok := provider.Models[modelID]; ok && model.Cost != nil {
				return providerID, modelID, model, true
			}
		}
	}
	return "", "", modelsDevModel{}, false
}

func priceRuleComparable(rule ModelPriceRule) ModelPriceRule {
	rule.ID = 0
	rule.Version = 0
	rule.EffectiveFrom = 0
	rule.FetchedAt = 0
	rule.UpdatedAt = 0
	return rule
}

func observedModelsHash(models []ObservedModel) string {
	keys := make([]string, 0, len(models))
	for _, item := range models {
		keys = append(keys, strings.TrimSpace(item.Model))
	}
	sort.Strings(keys)
	sum := sha256.Sum256([]byte(strings.Join(keys, "\n")))
	return hex.EncodeToString(sum[:])
}

func (s *Store) GetModelPriceSyncState(ctx context.Context) (ModelPriceSyncState, error) {
	var raw string
	if err := s.executor(ctx).QueryRowContext(ctx, `select state_json from model_price_sync_state where id = 1`).Scan(&raw); err != nil {
		if err == sql.ErrNoRows {
			return ModelPriceSyncState{Status: "idle"}, nil
		}
		return ModelPriceSyncState{}, err
	}
	var state ModelPriceSyncState
	if err := json.Unmarshal([]byte(raw), &state); err != nil {
		return ModelPriceSyncState{}, err
	}
	return state, nil
}

func (s *Store) SetModelPriceSyncState(ctx context.Context, state ModelPriceSyncState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	_, err = s.executor(ctx).ExecContext(ctx, `insert into model_price_sync_state(id, state_json, updated_at_ms) values(1, ?, ?)
		on conflict(id) do update set state_json=excluded.state_json, updated_at_ms=excluded.updated_at_ms`, string(raw), time.Now().UnixMilli())
	return err
}

func fetchModelsDevCatalog(ctx context.Context, etag string) (map[string]modelsDevProvider, string, bool, error) {
	parsed, err := url.Parse(modelsDevAPIURL)
	if err != nil || parsed.Scheme != "https" || parsed.Host != "models.dev" {
		return nil, "", false, fmt.Errorf("invalid models.dev endpoint")
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, modelsDevAPIURL, nil)
	if err != nil {
		return nil, "", false, err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "CLIProxyAPI-Pro model-price-sync")
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	client := &http.Client{Timeout: 45 * time.Second}
	response, err := client.Do(req)
	if err != nil {
		return nil, "", false, err
	}
	defer response.Body.Close()
	if response.StatusCode == http.StatusNotModified {
		return nil, etag, true, nil
	}
	if response.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("models.dev returned status %d", response.StatusCode)
	}
	reader := io.LimitReader(response.Body, modelsDevResponseLimit+1)
	payload, err := io.ReadAll(reader)
	if err != nil {
		return nil, "", false, err
	}
	if len(payload) > modelsDevResponseLimit {
		return nil, "", false, fmt.Errorf("models.dev response exceeds %d bytes", modelsDevResponseLimit)
	}
	catalog := map[string]modelsDevProvider{}
	if err := json.Unmarshal(payload, &catalog); err != nil {
		return nil, "", false, err
	}
	return catalog, response.Header.Get("ETag"), false, nil
}

func (s *Store) SyncModelsDevPrices(ctx context.Context, dryRun, recalculateUnpriced bool, overrideLockedModels ...string) (result ModelPriceSyncResult, err error) {
	s.priceSyncMu.Lock()
	defer s.priceSyncMu.Unlock()
	result.DryRun = dryRun
	result.Unmatched = make([]ObservedModel, 0)
	result.Changes = make([]ModelPriceSyncChange, 0)
	overrideLocked := make(map[string]struct{}, len(overrideLockedModels))
	for _, model := range overrideLockedModels {
		if key := strings.TrimSpace(model); key != "" {
			overrideLocked[key] = struct{}{}
		}
	}
	state, stateErr := s.GetModelPriceSyncState(ctx)
	if stateErr != nil {
		return result, stateErr
	}
	now := time.Now().UnixMilli()
	state.Status = "syncing"
	state.LastAttempt = now
	state.Error = ""
	if !dryRun {
		_ = s.SetModelPriceSyncState(ctx, state)
	}
	defer func() {
		if dryRun {
			return
		}
		if err != nil {
			state.Status = "error"
			state.Error = err.Error()
		} else {
			state.Status = "success"
			state.LastSuccess = time.Now().UnixMilli()
			if !result.NotModified {
				state.Matched = result.Matched
				state.Added = result.Added
				state.Updated = result.Updated
				state.Unchanged = result.Unchanged
				state.Locked = result.Locked
				state.Unmatched = len(result.Unmatched)
				state.UnmatchedModels = append([]ObservedModel{}, result.Unmatched...)
				state.Recalculated = result.Recalculated
			}
		}
		_ = s.SetModelPriceSyncState(context.Background(), state)
	}()

	observed, err := s.ObservedModels(ctx)
	if err != nil {
		return result, err
	}
	observedHash := observedModelsHash(observed)
	etag := state.ETag
	if dryRun || len(overrideLocked) > 0 || observedHash != state.ObservedHash || (state.Unmatched > 0 && len(state.UnmatchedModels) == 0) {
		etag = ""
	}
	catalog, nextETag, notModified, err := fetchModelsDevCatalog(ctx, etag)
	if err != nil {
		return result, err
	}
	result.NotModified = notModified
	if notModified {
		result.Matched = state.Matched
		result.Added = state.Added
		result.Updated = state.Updated
		result.Unchanged = state.Unchanged
		result.Locked = state.Locked
		result.Unmatched = append([]ObservedModel{}, state.UnmatchedModels...)
		result.Recalculated = state.Recalculated
		return result, nil
	}
	activeRules, err := s.ActiveModelPriceRules(ctx)
	if err != nil {
		return result, err
	}
	active := make(map[string]ModelPriceRule, len(activeRules))
	for _, rule := range activeRules {
		active[strings.TrimSpace(rule.Model)] = rule
	}
	for _, item := range observed {
		providerID, modelID, model, ok := matchModelsDevModel(catalog, item)
		if !ok {
			result.Unmatched = append(result.Unmatched, item)
			result.Changes = append(result.Changes, ModelPriceSyncChange{Action: "unmatched", Model: item.Model, Requests: item.Requests})
			continue
		}
		result.Matched++
		rule := modelPriceRuleFromModelsDev(item, providerID, modelID, model, now)
		key := strings.TrimSpace(rule.Model)
		current, exists := active[key]
		_, shouldOverrideLocked := overrideLocked[key]
		if exists && current.Locked && !shouldOverrideLocked {
			result.Locked++
			currentCopy := current
			ruleCopy := rule
			result.Changes = append(result.Changes, ModelPriceSyncChange{
				Action: "locked", Model: item.Model, Requests: item.Requests, SourceProvider: providerID, SourceModel: modelID,
				Before: &currentCopy, After: &ruleCopy,
			})
			continue
		}
		if exists && reflect.DeepEqual(priceRuleComparable(current), priceRuleComparable(rule)) {
			result.Unchanged++
			continue
		}
		if exists {
			action := "updated"
			if current.Locked && shouldOverrideLocked {
				result.Overridden++
				action = "overridden"
			} else {
				result.Updated++
			}
			currentCopy := current
			ruleCopy := rule
			result.Changes = append(result.Changes, ModelPriceSyncChange{
				Action: action, Model: item.Model, Requests: item.Requests, SourceProvider: providerID, SourceModel: modelID,
				Before: &currentCopy, After: &ruleCopy,
			})
		} else {
			result.Added++
			ruleCopy := rule
			result.Changes = append(result.Changes, ModelPriceSyncChange{
				Action: "added", Model: item.Model, Requests: item.Requests, SourceProvider: providerID, SourceModel: modelID, After: &ruleCopy,
			})
		}
		if dryRun {
			continue
		}
		if _, _, err := s.UpsertModelPriceRule(ctx, rule, shouldOverrideLocked); err != nil {
			return result, err
		}
	}
	sort.Slice(result.Unmatched, func(left, right int) bool {
		if result.Unmatched[left].Provider == result.Unmatched[right].Provider {
			return result.Unmatched[left].Model < result.Unmatched[right].Model
		}
		return result.Unmatched[left].Provider < result.Unmatched[right].Provider
	})
	sort.Slice(result.Changes, func(left, right int) bool {
		if result.Changes[left].Action == result.Changes[right].Action {
			return result.Changes[left].Model < result.Changes[right].Model
		}
		return modelPriceSyncChangeRank(result.Changes[left].Action) < modelPriceSyncChangeRank(result.Changes[right].Action)
	})
	if !dryRun {
		state.ETag = nextETag
		state.ObservedHash = observedHash
		if recalculateUnpriced {
			result.Recalculated, err = s.RecalculateEventCosts(ctx, true)
			if err != nil {
				return result, err
			}
		}
	}
	return result, nil
}
