package embeddedusage

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/embeddedusage/internalusage"
)

const modelPriceSourceManual = "manual"
const modelPriceSourceModelsDev = "models.dev"

type ModelPriceRate struct {
	Input      float64 `json:"input"`
	Output     float64 `json:"output"`
	CacheRead  float64 `json:"cacheRead"`
	CacheWrite float64 `json:"cacheWrite"`
	Reasoning  float64 `json:"reasoning,omitempty"`
}

type ModelPriceTier struct {
	ContextSize int64 `json:"contextSize"`
	ModelPriceRate
}

type ModelPriceRule struct {
	ID             int64                     `json:"id"`
	Provider       string                    `json:"provider"`
	Model          string                    `json:"model"`
	Base           ModelPriceRate            `json:"base"`
	Tiers          []ModelPriceTier          `json:"tiers,omitempty"`
	ServiceTiers   map[string]ModelPriceRate `json:"serviceTiers,omitempty"`
	Source         string                    `json:"source"`
	SourceProvider string                    `json:"sourceProvider,omitempty"`
	SourceModel    string                    `json:"sourceModel,omitempty"`
	Locked         bool                      `json:"locked"`
	Version        int                       `json:"version"`
	EffectiveFrom  int64                     `json:"effectiveFromMs"`
	FetchedAt      int64                     `json:"fetchedAtMs,omitempty"`
	UpstreamUpdate string                    `json:"upstreamUpdated,omitempty"`
	UpdatedAt      int64                     `json:"updatedAtMs"`
}

type ModelPriceCostBreakdown struct {
	RuleID           int64   `json:"ruleId"`
	RuleVersion      int     `json:"ruleVersion"`
	Provider         string  `json:"provider"`
	Model            string  `json:"model"`
	Source           string  `json:"source"`
	ContextTokens    int64   `json:"contextTokens"`
	ContextTierSize  int64   `json:"contextTierSize,omitempty"`
	ServiceTier      string  `json:"serviceTier,omitempty"`
	InputTokens      int64   `json:"inputTokens"`
	OutputTokens     int64   `json:"outputTokens"`
	CacheReadTokens  int64   `json:"cacheReadTokens"`
	CacheWriteTokens int64   `json:"cacheWriteTokens"`
	ReasoningTokens  int64   `json:"reasoningTokens"`
	InputCost        float64 `json:"inputCost"`
	OutputCost       float64 `json:"outputCost"`
	CacheReadCost    float64 `json:"cacheReadCost"`
	CacheWriteCost   float64 `json:"cacheWriteCost"`
	ReasoningCost    float64 `json:"reasoningCost"`
	TotalCost        float64 `json:"totalCost"`
}

type ObservedModel struct {
	Provider   string `json:"provider"`
	Model      string `json:"model"`
	Alias      string `json:"alias,omitempty"`
	Requests   int64  `json:"requests"`
	LastSeenAt int64  `json:"lastSeenAtMs"`
}

func normalizePriceProvider(value string) string {
	return strings.ToLower(strings.TrimSpace(value))
}

func normalizePriceRule(rule ModelPriceRule) ModelPriceRule {
	rule.Provider = normalizePriceProvider(rule.Provider)
	rule.Model = strings.TrimSpace(rule.Model)
	rule.Source = strings.TrimSpace(rule.Source)
	if rule.Source == "" {
		rule.Source = modelPriceSourceManual
	}
	rule.SourceProvider = normalizePriceProvider(rule.SourceProvider)
	rule.SourceModel = strings.TrimSpace(rule.SourceModel)
	if rule.EffectiveFrom <= 0 {
		rule.EffectiveFrom = time.Now().UnixMilli()
	}
	for index := range rule.Tiers {
		if rule.Tiers[index].ContextSize < 0 {
			rule.Tiers[index].ContextSize = 0
		}
	}
	sort.Slice(rule.Tiers, func(left, right int) bool {
		return rule.Tiers[left].ContextSize < rule.Tiers[right].ContextSize
	})
	if len(rule.ServiceTiers) > 0 {
		normalized := make(map[string]ModelPriceRate, len(rule.ServiceTiers))
		for key, rate := range rule.ServiceTiers {
			if value := strings.ToLower(strings.TrimSpace(key)); value != "" {
				normalized[value] = rate
			}
		}
		rule.ServiceTiers = normalized
	}
	return rule
}

func validateModelPriceRate(rate ModelPriceRate) error {
	if rate.Input < 0 || rate.Output < 0 || rate.CacheRead < 0 || rate.CacheWrite < 0 || rate.Reasoning < 0 {
		return fmt.Errorf("model price rates cannot be negative")
	}
	return nil
}

func validateModelPriceRule(rule ModelPriceRule) error {
	if strings.TrimSpace(rule.Model) == "" {
		return fmt.Errorf("model is required")
	}
	if err := validateModelPriceRate(rule.Base); err != nil {
		return err
	}
	seen := map[int64]struct{}{}
	for _, tier := range rule.Tiers {
		if tier.ContextSize <= 0 {
			return fmt.Errorf("context tier size must be positive")
		}
		if _, exists := seen[tier.ContextSize]; exists {
			return fmt.Errorf("duplicate context tier size %d", tier.ContextSize)
		}
		seen[tier.ContextSize] = struct{}{}
		if err := validateModelPriceRate(tier.ModelPriceRate); err != nil {
			return err
		}
	}
	for _, rate := range rule.ServiceTiers {
		if err := validateModelPriceRate(rate); err != nil {
			return err
		}
	}
	return nil
}

func priceRuleLookupKey(_ string, model string) string {
	return strings.TrimSpace(model)
}

func selectModelPriceRate(rule ModelPriceRule, event internalusage.Event) (ModelPriceRate, int64) {
	rate := rule.Base
	contextTokens := event.InputTokens
	if contextTokens <= 0 {
		contextTokens = event.CacheReadTokens + event.CacheWriteTokens
	}
	selectedSize := int64(0)
	for _, tier := range rule.Tiers {
		if contextTokens >= tier.ContextSize {
			rate = tier.ModelPriceRate
			selectedSize = tier.ContextSize
		}
	}
	if serviceTier := strings.ToLower(strings.TrimSpace(event.ServiceTier)); serviceTier != "" {
		if override, ok := rule.ServiceTiers[serviceTier]; ok {
			rate = override
			selectedSize = 0
		}
	}
	return rate, selectedSize
}

func evaluateEventCost(event internalusage.Event, rule ModelPriceRule) (float64, ModelPriceCostBreakdown) {
	rate, contextTierSize := selectModelPriceRate(rule, event)
	cacheReadTokens := event.CacheReadTokens
	if cacheReadTokens <= 0 {
		cacheReadTokens = maxPricingInt64(event.CachedTokens, event.CacheTokens-event.CacheWriteTokens)
	}
	cacheWriteTokens := event.CacheWriteTokens
	cachedTokens := cacheReadTokens + cacheWriteTokens
	inputTokens := event.InputTokens
	uncachedInputTokens := inputTokens
	if inputTokens >= cachedTokens {
		uncachedInputTokens = inputTokens - cachedTokens
	}
	reasoningTokens := event.ReasoningTokens
	outputTokens := event.OutputTokens
	if rate.Reasoning > 0 && outputTokens >= reasoningTokens {
		outputTokens -= reasoningTokens
	}
	const unit = 1_000_000.0
	breakdown := ModelPriceCostBreakdown{
		RuleID: rule.ID, RuleVersion: rule.Version, Provider: normalizePriceProvider(event.Provider), Model: rule.Model,
		Source: rule.Source, ContextTokens: event.InputTokens, ContextTierSize: contextTierSize,
		ServiceTier: event.ServiceTier, InputTokens: uncachedInputTokens, OutputTokens: outputTokens,
		CacheReadTokens: cacheReadTokens, CacheWriteTokens: cacheWriteTokens, ReasoningTokens: reasoningTokens,
		InputCost:      float64(uncachedInputTokens) / unit * rate.Input,
		OutputCost:     float64(outputTokens) / unit * rate.Output,
		CacheReadCost:  float64(cacheReadTokens) / unit * rate.CacheRead,
		CacheWriteCost: float64(cacheWriteTokens) / unit * rate.CacheWrite,
		ReasoningCost:  float64(reasoningTokens) / unit * rate.Reasoning,
	}
	breakdown.TotalCost = breakdown.InputCost + breakdown.OutputCost + breakdown.CacheReadCost + breakdown.CacheWriteCost + breakdown.ReasoningCost
	return breakdown.TotalCost, breakdown
}

func maxPricingInt64(left, right int64) int64 {
	if left > right {
		return left
	}
	return right
}

func (s *Store) activeModelPriceRules(ctx context.Context, preserveProvider bool) ([]ModelPriceRule, error) {
	return activeModelPriceRulesFrom(ctx, s.db, preserveProvider)
}

func activeModelPriceRulesFrom(ctx context.Context, queryer sqlQueryer, preserveProvider bool) ([]ModelPriceRule, error) {
	rows, err := queryer.QueryContext(ctx, `select v.id, a.provider, a.model, v.rule_json, a.source, a.source_provider, a.source_model,
		a.locked, a.active_version, v.effective_from_ms, a.fetched_at_ms, a.upstream_updated, a.updated_at_ms
		from model_price_rules a join model_price_rule_versions v
		on v.provider = a.provider and v.model = a.model and v.version = a.active_version
		order by a.provider, a.model`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	rules := make([]ModelPriceRule, 0)
	for rows.Next() {
		var rule ModelPriceRule
		var id int64
		var provider string
		var raw string
		var locked int
		if err := rows.Scan(&id, &provider, &rule.Model, &raw, &rule.Source, &rule.SourceProvider, &rule.SourceModel,
			&locked, &rule.Version, &rule.EffectiveFrom, &rule.FetchedAt, &rule.UpstreamUpdate, &rule.UpdatedAt); err != nil {
			return nil, err
		}
		if err := json.Unmarshal([]byte(raw), &rule); err != nil {
			return nil, err
		}
		rule.Provider = provider
		rule.ID = id
		rule.Locked = locked != 0
		if !preserveProvider {
			rule.Provider = ""
		}
		rules = append(rules, normalizePriceRule(rule))
	}
	return rules, rows.Err()
}

func (s *Store) ActiveModelPriceRules(ctx context.Context) ([]ModelPriceRule, error) {
	return s.activeModelPriceRules(ctx, false)
}

func (s *Store) migrateLegacyModelPrices(ctx context.Context) error {
	var existing int
	if err := s.db.QueryRowContext(ctx, `select count(*) from model_price_rules`).Scan(&existing); err != nil {
		return err
	}
	if existing > 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `select model, prompt_price, completion_price, cache_price, updated_at_ms from model_prices`)
	if err != nil {
		return err
	}
	type legacyPrice struct {
		model      string
		prompt     float64
		completion float64
		cache      float64
		updatedAt  int64
	}
	items := make([]legacyPrice, 0)
	for rows.Next() {
		var item legacyPrice
		if err := rows.Scan(&item.model, &item.prompt, &item.completion, &item.cache, &item.updatedAt); err != nil {
			_ = rows.Close()
			return err
		}
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return err
	}
	for _, item := range items {
		_, _, err := s.UpsertModelPriceRule(ctx, ModelPriceRule{
			Model:         item.model,
			Base:          ModelPriceRate{Input: item.prompt, Output: item.completion, CacheRead: item.cache},
			Source:        modelPriceSourceManual,
			Locked:        true,
			EffectiveFrom: item.updatedAt,
		}, true)
		if err != nil {
			return err
		}
	}
	return nil
}

func preferModelPriceRule(candidate, current ModelPriceRule) bool {
	if candidate.Locked != current.Locked {
		return candidate.Locked
	}
	if (candidate.Source == modelPriceSourceManual) != (current.Source == modelPriceSourceManual) {
		return candidate.Source == modelPriceSourceManual
	}
	if (candidate.Provider == "") != (current.Provider == "") {
		return candidate.Provider == ""
	}
	if candidate.UpdatedAt != current.UpdatedAt {
		return candidate.UpdatedAt > current.UpdatedAt
	}
	return candidate.Provider < current.Provider
}

func (s *Store) migrateProviderBoundModelPriceRules(ctx context.Context) error {
	rules, err := s.activeModelPriceRules(ctx, true)
	if err != nil {
		return err
	}
	selected := make(map[string]ModelPriceRule, len(rules))
	requiresMigration := map[string]bool{}
	for _, rule := range rules {
		key := priceRuleLookupKey("", rule.Model)
		if rule.Provider != "" {
			requiresMigration[key] = true
		}
		if current, exists := selected[key]; !exists || preferModelPriceRule(rule, current) {
			selected[key] = rule
		}
	}
	for key := range requiresMigration {
		rule := selected[key]
		rule.Provider = ""
		if _, _, err := s.UpsertModelPriceRule(ctx, rule, true); err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `delete from model_price_rules where model = ? and provider != ''`, rule.Model); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) activeModelPriceRuleMap(ctx context.Context) (map[string]ModelPriceRule, error) {
	rules, err := s.ActiveModelPriceRules(ctx)
	if err != nil {
		return nil, err
	}
	out := make(map[string]ModelPriceRule, len(rules))
	for _, rule := range rules {
		out[priceRuleLookupKey(rule.Provider, rule.Model)] = rule
	}
	return out, nil
}

func findModelPriceRule(rules map[string]ModelPriceRule, provider, model string) (ModelPriceRule, bool) {
	rule, ok := rules[priceRuleLookupKey(provider, model)]
	return rule, ok
}

func (s *Store) UpsertModelPriceRule(ctx context.Context, rule ModelPriceRule, allowLockedOverride bool) (ModelPriceRule, bool, error) {
	rule = normalizePriceRule(rule)
	rule.Provider = ""
	if err := validateModelPriceRule(rule); err != nil {
		return ModelPriceRule{}, false, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return ModelPriceRule{}, false, err
	}
	defer func() { _ = tx.Rollback() }()

	var currentLocked int
	var currentRaw string
	err = tx.QueryRowContext(ctx, `select a.locked, v.rule_json
		from model_price_rules a join model_price_rule_versions v
		on v.provider = a.provider and v.model = a.model and v.version = a.active_version
		where a.provider = ? and a.model = ?`, rule.Provider, rule.Model).Scan(&currentLocked, &currentRaw)
	if err != nil && err != sql.ErrNoRows {
		return ModelPriceRule{}, false, err
	}
	if currentLocked != 0 && rule.Source == modelPriceSourceModelsDev && !allowLockedOverride {
		return ModelPriceRule{}, false, nil
	}
	var latestVersion int
	if err := tx.QueryRowContext(ctx, `select coalesce(max(version), 0) from model_price_rule_versions where provider = ? and model = ?`,
		rule.Provider, rule.Model).Scan(&latestVersion); err != nil {
		return ModelPriceRule{}, false, err
	}
	rule.Version = latestVersion + 1
	rule.ID = 0
	rule.UpdatedAt = time.Now().UnixMilli()
	raw, err := json.Marshal(rule)
	if err != nil {
		return ModelPriceRule{}, false, err
	}
	if string(raw) == currentRaw {
		return rule, false, nil
	}
	result, err := tx.ExecContext(ctx, `insert into model_price_rule_versions(provider, model, version, rule_json, effective_from_ms, created_at_ms)
		values(?, ?, ?, ?, ?, ?)`, rule.Provider, rule.Model, rule.Version, string(raw), rule.EffectiveFrom, rule.UpdatedAt)
	if err != nil {
		return ModelPriceRule{}, false, err
	}
	if rule.ID, err = result.LastInsertId(); err != nil {
		return ModelPriceRule{}, false, err
	}
	locked := 0
	if rule.Locked {
		locked = 1
	}
	_, err = tx.ExecContext(ctx, `insert into model_price_rules(provider, model, active_version, source, source_provider, source_model, locked, fetched_at_ms, upstream_updated, updated_at_ms)
		values(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		on conflict(provider, model) do update set active_version=excluded.active_version, source=excluded.source,
		source_provider=excluded.source_provider, source_model=excluded.source_model, locked=excluded.locked,
		fetched_at_ms=excluded.fetched_at_ms, upstream_updated=excluded.upstream_updated, updated_at_ms=excluded.updated_at_ms`,
		rule.Provider, rule.Model, rule.Version, rule.Source, rule.SourceProvider, rule.SourceModel, locked, rule.FetchedAt, rule.UpstreamUpdate, rule.UpdatedAt)
	if err != nil {
		return ModelPriceRule{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ModelPriceRule{}, false, err
	}
	return rule, true, nil
}

func (s *Store) DeleteModelPriceRule(ctx context.Context, provider, model string) error {
	_, err := s.db.ExecContext(ctx, `delete from model_price_rules where model = ?`, strings.TrimSpace(model))
	return err
}

func (s *Store) ObservedModels(ctx context.Context) ([]ObservedModel, error) {
	rows, err := s.db.QueryContext(ctx, `select coalesce(max(provider), ''), model, coalesce(max(alias), ''), count(*), max(timestamp_ms)
		from usage_events where model != '' and model != '-' group by model order by max(timestamp_ms) desc`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	models := make([]ObservedModel, 0)
	for rows.Next() {
		var item ObservedModel
		if err := rows.Scan(&item.Provider, &item.Model, &item.Alias, &item.Requests, &item.LastSeenAt); err != nil {
			return nil, err
		}
		models = append(models, item)
	}
	return models, rows.Err()
}

func (s *Store) RecalculateEventCosts(ctx context.Context, onlyUnpriced bool) (int64, error) {
	rules, err := s.activeModelPriceRuleMap(ctx)
	if err != nil {
		return 0, err
	}
	query := `select id, provider, model, input_tokens, output_tokens, reasoning_tokens, cached_tokens, cache_tokens,
		cache_read_tokens, cache_write_tokens, service_tier from usage_events`
	if onlyUnpriced {
		query += ` where estimated_cost is null`
	}
	rows, err := s.db.QueryContext(ctx, query)
	if err != nil {
		return 0, err
	}
	type pricedEvent struct {
		id    int64
		event internalusage.Event
	}
	items := make([]pricedEvent, 0)
	for rows.Next() {
		var item pricedEvent
		var provider, serviceTier sql.NullString
		if err := rows.Scan(&item.id, &provider, &item.event.Model, &item.event.InputTokens, &item.event.OutputTokens,
			&item.event.ReasoningTokens, &item.event.CachedTokens, &item.event.CacheTokens, &item.event.CacheReadTokens,
			&item.event.CacheWriteTokens, &serviceTier); err != nil {
			_ = rows.Close()
			return 0, err
		}
		item.event.Provider = provider.String
		item.event.ServiceTier = serviceTier.String
		items = append(items, item)
	}
	if err := rows.Close(); err != nil {
		return 0, err
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return 0, err
	}
	defer func() { _ = tx.Rollback() }()
	stmt, err := tx.PrepareContext(ctx, `update usage_events set estimated_cost = ?, price_rule_id = ?, cost_breakdown_json = ? where id = ?`)
	if err != nil {
		return 0, err
	}
	defer stmt.Close()
	var updated int64
	for _, item := range items {
		rule, ok := findModelPriceRule(rules, item.event.Provider, item.event.Model)
		if !ok {
			continue
		}
		cost, breakdown := evaluateEventCost(item.event, rule)
		raw, _ := json.Marshal(breakdown)
		if _, err := stmt.ExecContext(ctx, cost, rule.ID, string(raw), item.id); err != nil {
			return 0, err
		}
		updated++
	}
	if err := tx.Commit(); err != nil {
		return 0, err
	}
	return updated, nil
}
