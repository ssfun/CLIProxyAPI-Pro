package observability

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"math"
	"testing"

	"github.com/router-for-me/CLIProxyAPI/v6/internal/pro/observability/internalusage"
)

func testGPT56PriceRule() ModelPriceRule {
	return ModelPriceRule{
		ID: 7, Provider: "openai", Model: "gpt-5.6-sol", Version: 2, Source: modelPriceSourceModelsDev,
		Base: ModelPriceRate{Input: 5, Output: 30, CacheRead: 0.5, CacheWrite: 6.25},
		Tiers: []ModelPriceTier{{
			ContextSize:    272000,
			ModelPriceRate: ModelPriceRate{Input: 10, Output: 45, CacheRead: 1, CacheWrite: 12.5},
		}},
		ServiceTiers: map[string]ModelPriceRate{
			"fast": {Input: 10, Output: 60, CacheRead: 1, CacheWrite: 12.5},
		},
	}
}

func testClaudeSpeedPriceRule() ModelPriceRule {
	return ModelPriceRule{
		Model: "claude-opus-test",
		Base:  ModelPriceRate{Input: 5, Output: 25, CacheRead: 0.5, CacheWrite: 6.25},
		Tiers: []ModelPriceTier{{
			ContextSize:    200000,
			ModelPriceRate: ModelPriceRate{Input: 8, Output: 40, CacheRead: 0.8, CacheWrite: 10},
		}},
		Speeds: map[string]ModelPriceRate{
			"fast": {Input: 10, Output: 50, CacheRead: 1, CacheWrite: 12.5},
		},
	}
}

func TestSearchModelsDevCatalogReturnsPricedMatchesInUsefulOrder(t *testing.T) {
	catalog := map[string]modelsDevProvider{
		"google": {
			Name: "Google",
			Models: map[string]modelsDevModel{
				"gemini-3.5-flash": {
					Name: "Gemini 3.5 Flash", LastUpdated: "2026-08-01",
					Cost: &modelsDevCost{Input: 0.5, Output: 3, CacheRead: 0.05},
				},
				"gemini-3.5-flash-preview": {
					Name: "Gemini 3.5 Flash Preview",
					Cost: &modelsDevCost{Input: 0.6, Output: 3.5},
				},
				"gemini-3.5-flash-free": {Name: "Gemini 3.5 Flash Free"},
			},
		},
		"other": {
			Name: "Other",
			Models: map[string]modelsDevModel{
				"gemini-3.5-flash": {Cost: &modelsDevCost{Input: 9, Output: 9}},
			},
		},
	}

	items := searchModelsDevCatalog(catalog, "gemini-3.5-flash", "google", 20)
	if len(items) != 2 {
		t.Fatalf("search result count = %d, want 2: %+v", len(items), items)
	}
	if items[0].Provider != "google" || items[0].ProviderName != "Google" || items[0].Model != "gemini-3.5-flash" ||
		items[0].ModelName != "Gemini 3.5 Flash" || items[0].LastUpdated != "2026-08-01" {
		t.Fatalf("first search item = %+v", items[0])
	}
	if items[0].Rule.Model != "gemini-3.5-flash" || items[0].Rule.SourceProvider != "google" ||
		items[0].Rule.SourceModel != "gemini-3.5-flash" || items[0].Rule.Base.Input != 0.5 || items[0].Rule.Base.Output != 3 {
		t.Fatalf("first search rule = %+v", items[0].Rule)
	}
	if items[1].Model != "gemini-3.5-flash-preview" {
		t.Fatalf("second search item = %+v", items[1])
	}
}

func TestSearchModelsDevCatalogRequiresAllQueryTokensAndClampsLimit(t *testing.T) {
	catalog := map[string]modelsDevProvider{
		"google": {
			Name: "Google",
			Models: map[string]modelsDevModel{
				"gemini-flash": {Name: "Gemini Flash", Cost: &modelsDevCost{Input: 1}},
				"gemini-pro":   {Name: "Gemini Pro", Cost: &modelsDevCost{Input: 2}},
			},
		},
	}

	items := searchModelsDevCatalog(catalog, "google flash", "", 1)
	if len(items) != 1 || items[0].Model != "gemini-flash" {
		t.Fatalf("search items = %+v, want only gemini-flash", items)
	}
	if items := searchModelsDevCatalog(catalog, "gemini", "anthropic", 20); len(items) != 0 {
		t.Fatalf("provider-filtered search items = %+v, want none", items)
	}
}

func TestModelPriceRulePublicJSONOmitsLegacyProvider(t *testing.T) {
	raw, err := json.Marshal(testGPT56PriceRule())
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte(`"provider"`)) {
		t.Fatalf("public model price rule exposed provider: %s", raw)
	}
}

func assertCostClose(t *testing.T, got, want float64) {
	t.Helper()
	if math.Abs(got-want) > 0.000000001 {
		t.Fatalf("cost = %.12f, want %.12f", got, want)
	}
}

func TestEvaluateEventCostUsesContextTierPerRequest(t *testing.T) {
	rule := testGPT56PriceRule()
	below := internalusage.Event{InputTokens: 271999, OutputTokens: 1000}
	atBoundary := internalusage.Event{InputTokens: 272000, OutputTokens: 1000}

	belowCost, belowBreakdown := evaluateEventCost(below, rule)
	boundaryCost, boundaryBreakdown := evaluateEventCost(atBoundary, rule)

	assertCostClose(t, belowCost, float64(271999)/1_000_000*5+float64(1000)/1_000_000*30)
	assertCostClose(t, boundaryCost, float64(272000)/1_000_000*10+float64(1000)/1_000_000*45)
	if belowBreakdown.ContextTierSize != 0 || belowBreakdown.PricingMode != modelPriceModeBase ||
		belowBreakdown.ServiceTierSource != serviceTierSourceNone || boundaryBreakdown.ContextTierSize != 272000 ||
		boundaryBreakdown.PricingMode != modelPriceModeContext || boundaryBreakdown.ServiceTierSource != serviceTierSourceNone {
		t.Fatalf("tier sizes = %d/%d, want 0/272000", belowBreakdown.ContextTierSize, boundaryBreakdown.ContextTierSize)
	}
}

func TestEstimateUsageCostMicrosUsesActivePriceRuleAndRejectsUnknownModel(t *testing.T) {
	store := openTestStore(t)
	rule := testGPT56PriceRule()
	rule.ID = 0
	rule.Version = 0
	if _, _, err := store.UpsertModelPriceRule(context.Background(), rule, true); err != nil {
		t.Fatal(err)
	}
	micros, err := store.EstimateUsageCostMicros(context.Background(), UsageCostInput{
		Provider: "codex", Model: rule.Model, InputTokens: 10, OutputTokens: 2,
	})
	if err != nil || micros != 110 {
		t.Fatalf("estimated cost micros = %d, %v; want 110", micros, err)
	}
	fastMicros, err := store.EstimateUsageCostMicros(context.Background(), UsageCostInput{
		Provider: "codex", AuthType: "oauth", Model: rule.Model, InputTokens: 10, OutputTokens: 2,
		ServiceTier: "fast", EffectiveServiceTier: "default",
	})
	if err != nil || fastMicros != 220 {
		t.Fatalf("Codex OAuth fast estimated cost micros = %d, %v; want 220", fastMicros, err)
	}
	if _, err := store.EstimateUsageCostMicros(context.Background(), UsageCostInput{Model: "unknown", InputTokens: 1}); !errors.Is(err, ErrModelPriceUnavailable) {
		t.Fatalf("unknown model error = %v, want ErrModelPriceUnavailable", err)
	}
	var unavailable *Store
	if _, err := unavailable.EstimateUsageCostMicros(context.Background(), UsageCostInput{Model: rule.Model}); err == nil || errors.Is(err, ErrModelPriceUnavailable) {
		t.Fatalf("unavailable price store error = %v, want storage failure", err)
	}
}

func TestEvaluateEventCostUsesServiceTierOverride(t *testing.T) {
	rule := testGPT56PriceRule()
	event := internalusage.Event{InputTokens: 100000, OutputTokens: 1000, ServiceTier: "priority"}
	cost, breakdown := evaluateEventCost(event, rule)
	assertCostClose(t, cost, float64(100000)/1_000_000*10+float64(1000)/1_000_000*60)
	if breakdown.ContextTierSize != 0 || breakdown.ServiceTier != "priority" || breakdown.MatchedServiceTier != "fast" ||
		breakdown.ServiceTierSource != serviceTierSourceRequest || breakdown.PricingMode != modelPriceModeServiceTier {
		t.Fatalf("breakdown = %+v, want legacy priority request fallback to fast override", breakdown)
	}
}

func TestEvaluateEventCostUsesEffectivePriorityForFastAndOverridesContextTier(t *testing.T) {
	rule := testGPT56PriceRule()
	event := internalusage.Event{InputTokens: 300000, OutputTokens: 1000, ServiceTier: " FAST ", EffectiveServiceTier: " PRIORITY "}
	cost, breakdown := evaluateEventCost(event, rule)
	assertCostClose(t, cost, float64(300000)/1_000_000*10+float64(1000)/1_000_000*60)
	if breakdown.ContextTierSize != 0 || breakdown.RequestedServiceTier != "fast" || breakdown.EffectiveServiceTier != "priority" ||
		breakdown.MatchedServiceTier != "fast" || breakdown.ServiceTierSource != serviceTierSourceResponse || breakdown.PricingMode != modelPriceModeServiceTier {
		t.Fatalf("breakdown = %+v, want effective priority to match canonical fast override", breakdown)
	}
}

func TestEvaluateEventCostEffectiveDefaultDowngradeUsesStandardPricing(t *testing.T) {
	rule := testGPT56PriceRule()
	event := internalusage.Event{Provider: "codex", AuthType: "apikey", InputTokens: 300000, OutputTokens: 1000, ServiceTier: "fast", EffectiveServiceTier: "default"}
	cost, breakdown := evaluateEventCost(event, rule)
	assertCostClose(t, cost, float64(300000)/1_000_000*10+float64(1000)/1_000_000*45)
	if breakdown.ContextTierSize != 272000 || breakdown.PricingMode != modelPriceModeContext || breakdown.MatchedServiceTier != "" ||
		breakdown.EffectiveServiceTier != "default" || breakdown.ServiceTierSource != serviceTierSourceResponse {
		t.Fatalf("breakdown = %+v, want authoritative standard downgrade with context pricing", breakdown)
	}
}

func TestEvaluateEventCostCodexOAuthFastUsesRequestedTierWhenResponseIsDefault(t *testing.T) {
	rule := testGPT56PriceRule()
	for _, requestedTier := range []string{"fast", "priority"} {
		t.Run(requestedTier, func(t *testing.T) {
			event := internalusage.Event{
				Provider: " CoDeX ", AuthType: " OAUTH ", InputTokens: 300000, OutputTokens: 1000,
				ServiceTier: requestedTier, EffectiveServiceTier: "default",
			}
			cost, breakdown := evaluateEventCost(event, rule)
			assertCostClose(t, cost, float64(300000)/1_000_000*10+float64(1000)/1_000_000*60)
			if breakdown.ContextTierSize != 0 || breakdown.PricingMode != modelPriceModeServiceTier ||
				breakdown.MatchedServiceTier != "fast" || breakdown.EffectiveServiceTier != "default" ||
				breakdown.ServiceTierSource != serviceTierSourceCodexOAuthRequest {
				t.Fatalf("breakdown = %+v, want Codex OAuth request-authoritative fast pricing", breakdown)
			}
		})
	}
}

func TestEvaluateEventCostDoesNotUseCodexOAuthRuleOutsideExactBoundary(t *testing.T) {
	rule := testGPT56PriceRule()
	for _, event := range []internalusage.Event{
		{Provider: "codex", AuthType: "apikey", ServiceTier: "fast", EffectiveServiceTier: "default"},
		{Provider: "openai", AuthType: "oauth", ServiceTier: "fast", EffectiveServiceTier: "default"},
		{Provider: "codex", AuthType: "oauth", ServiceTier: "default", EffectiveServiceTier: "priority"},
	} {
		event.InputTokens = 100000
		event.OutputTokens = 1000
		_, breakdown := evaluateEventCost(event, rule)
		if breakdown.ServiceTierSource != serviceTierSourceResponse {
			t.Fatalf("event = %+v breakdown = %+v, want response-authoritative pricing", event, breakdown)
		}
	}
}

func TestEvaluateEventCostFallsBackWhenServiceTierHasNoOverride(t *testing.T) {
	rule := testGPT56PriceRule()
	baseEvent := internalusage.Event{InputTokens: 100000, OutputTokens: 1000, ServiceTier: "flex"}
	_, baseBreakdown := evaluateEventCost(baseEvent, rule)
	if baseBreakdown.PricingMode != modelPriceModeBase || baseBreakdown.ContextTierSize != 0 {
		t.Fatalf("base breakdown = %+v, want base fallback", baseBreakdown)
	}

	contextEvent := internalusage.Event{InputTokens: 300000, OutputTokens: 1000, ServiceTier: "flex"}
	_, contextBreakdown := evaluateEventCost(contextEvent, rule)
	if contextBreakdown.PricingMode != modelPriceModeContext || contextBreakdown.ContextTierSize != 272000 {
		t.Fatalf("context breakdown = %+v, want context fallback", contextBreakdown)
	}
}

func TestEvaluateEventCostUsesEffectiveFastSpeedOverride(t *testing.T) {
	rule := testClaudeSpeedPriceRule()
	event := internalusage.Event{InputTokens: 300000, OutputTokens: 1000, Speed: "fast", EffectiveSpeed: " FAST "}
	cost, breakdown := evaluateEventCost(event, rule)
	assertCostClose(t, cost, float64(300000)/1_000_000*10+float64(1000)/1_000_000*50)
	if breakdown.ContextTierSize != 0 || breakdown.PricingMode != modelPriceModeSpeed || breakdown.RequestedSpeed != "fast" ||
		breakdown.EffectiveSpeed != "fast" || breakdown.MatchedSpeed != "fast" || breakdown.SpeedSource != speedSourceResponse {
		t.Fatalf("breakdown = %+v, want response-confirmed fast speed override", breakdown)
	}
}

func TestEvaluateEventCostEffectiveStandardSpeedFallsBackToContext(t *testing.T) {
	rule := testClaudeSpeedPriceRule()
	event := internalusage.Event{InputTokens: 300000, OutputTokens: 1000, Speed: "fast", EffectiveSpeed: "standard"}
	cost, breakdown := evaluateEventCost(event, rule)
	assertCostClose(t, cost, float64(300000)/1_000_000*8+float64(1000)/1_000_000*40)
	if breakdown.ContextTierSize != 200000 || breakdown.PricingMode != modelPriceModeContext || breakdown.MatchedSpeed != "" ||
		breakdown.EffectiveSpeed != "standard" || breakdown.SpeedSource != speedSourceResponse {
		t.Fatalf("breakdown = %+v, want authoritative standard-speed context pricing", breakdown)
	}
}

func TestEvaluateEventCostFallsBackToRequestedFastSpeed(t *testing.T) {
	rule := testClaudeSpeedPriceRule()
	event := internalusage.Event{InputTokens: 100000, OutputTokens: 1000, Speed: " FAST "}
	_, breakdown := evaluateEventCost(event, rule)
	if breakdown.PricingMode != modelPriceModeSpeed || breakdown.MatchedSpeed != "fast" || breakdown.SpeedSource != speedSourceRequest {
		t.Fatalf("breakdown = %+v, want request fast-speed fallback", breakdown)
	}
}

func TestUpsertModelPriceRuleRejectsInvalidServiceTierNames(t *testing.T) {
	store := openTestStore(t)
	rate := ModelPriceRate{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 0.75}

	blank := testGPT56PriceRule()
	blank.ServiceTiers = map[string]ModelPriceRate{" ": rate}
	if _, _, err := store.UpsertModelPriceRule(context.Background(), blank, true); err == nil {
		t.Fatal("UpsertModelPriceRule() accepted a blank service tier name")
	}

	duplicate := testGPT56PriceRule()
	duplicate.ServiceTiers = map[string]ModelPriceRate{"priority": rate, " PRIORITY ": rate}
	if _, _, err := store.UpsertModelPriceRule(context.Background(), duplicate, true); err == nil {
		t.Fatal("UpsertModelPriceRule() accepted duplicate normalized service tier names")
	}

	aliasDuplicate := testGPT56PriceRule()
	aliasDuplicate.ServiceTiers = map[string]ModelPriceRate{"fast": rate, "priority": rate}
	if _, _, err := store.UpsertModelPriceRule(context.Background(), aliasDuplicate, true); err == nil {
		t.Fatal("UpsertModelPriceRule() accepted fast/priority alias duplicates")
	}
}

func TestUpsertModelPriceRuleRejectsInvalidSpeedNames(t *testing.T) {
	store := openTestStore(t)
	rate := ModelPriceRate{Input: 1, Output: 2, CacheRead: 0.5, CacheWrite: 0.75}

	blank := testClaudeSpeedPriceRule()
	blank.Speeds = map[string]ModelPriceRate{" ": rate}
	if _, _, err := store.UpsertModelPriceRule(context.Background(), blank, true); err == nil {
		t.Fatal("UpsertModelPriceRule() accepted a blank speed name")
	}

	duplicate := testClaudeSpeedPriceRule()
	duplicate.Speeds = map[string]ModelPriceRate{"fast": rate, " FAST ": rate}
	if _, _, err := store.UpsertModelPriceRule(context.Background(), duplicate, true); err == nil {
		t.Fatal("UpsertModelPriceRule() accepted duplicate normalized speed names")
	}
}

func TestModelPriceRuleFromModelsDevCanonicalizesFastMode(t *testing.T) {
	model := modelsDevModel{ID: "gpt-test", Cost: &modelsDevCost{Input: 1, Output: 2, Reasoning: 3}}
	mode := modelsDevMode{Cost: &modelsDevCost{Input: 4, Output: 5, CacheRead: 0.4, CacheWrite: 0.5, Reasoning: 6}}
	mode.Provider.Body = map[string]any{"service_tier": " Priority "}
	model.Experimental.Modes = map[string]modelsDevMode{"fast": mode}

	rule := modelPriceRuleFromModelsDev(ObservedModel{Model: "gpt-test"}, "openai", "gpt-test", model, 1234)
	fast, ok := rule.ServiceTiers["fast"]
	if !ok || fast.Input != 4 || fast.Output != 5 || fast.Reasoning != 6 {
		t.Fatalf("service tiers = %+v, want canonical fast reasoning rates", rule.ServiceTiers)
	}
	if _, legacy := rule.ServiceTiers["priority"]; legacy {
		t.Fatalf("service tiers = %+v, legacy priority key should not be synced", rule.ServiceTiers)
	}
}

func TestModelPriceRuleFromModelsDevDoesNotTreatProviderFastModeAsServiceTier(t *testing.T) {
	model := modelsDevModel{ID: "claude-test", Cost: &modelsDevCost{Input: 1, Output: 2}}
	mode := modelsDevMode{Cost: &modelsDevCost{Input: 4, Output: 5}}
	mode.Provider.Body = map[string]any{"speed": "fast"}
	model.Experimental.Modes = map[string]modelsDevMode{"fast": mode}

	rule := modelPriceRuleFromModelsDev(ObservedModel{Model: "claude-test"}, "anthropic", "claude-test", model, 1234)
	if len(rule.ServiceTiers) != 0 {
		t.Fatalf("service tiers = %+v, provider-specific fast mode must not become a service-tier override", rule.ServiceTiers)
	}
	fast, ok := rule.Speeds["fast"]
	if !ok || fast.Input != 4 || fast.Output != 5 {
		t.Fatalf("speeds = %+v, want Anthropic fast speed rates", rule.Speeds)
	}
}

func TestNormalizePriceRulePrefersCanonicalFastOverLegacyPriority(t *testing.T) {
	legacyRate := ModelPriceRate{Input: 1, Output: 2}
	fastRate := ModelPriceRate{Input: 3, Output: 4}
	rule := normalizePriceRule(ModelPriceRule{ServiceTiers: map[string]ModelPriceRate{
		"priority": legacyRate,
		"fast":     fastRate,
	}})
	if len(rule.ServiceTiers) != 1 || rule.ServiceTiers["fast"] != fastRate {
		t.Fatalf("service tiers = %+v, want canonical fast rate", rule.ServiceTiers)
	}
}

func TestInsertEventsSnapshotsPriceAndAggregatesCost(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	_, changed, err := store.UpsertModelPriceRule(ctx, testGPT56PriceRule(), true)
	if err != nil || !changed {
		t.Fatalf("UpsertModelPriceRule() = changed:%v err:%v", changed, err)
	}

	first := testUsageEvent(0, false, 151000)
	first.Provider = "openai"
	first.Model = "gpt-5.6-sol"
	first.InputTokens = 150000
	first.OutputTokens = 1000
	second := testUsageEvent(1, false, 151000)
	second.Provider = "openai"
	second.Model = "gpt-5.6-sol"
	second.InputTokens = 150000
	second.OutputTokens = 1000
	insertTestUsageEvents(t, store, first, second)

	events, err := store.RecentEvents(ctx, 10)
	if err != nil || len(events) != 2 {
		t.Fatalf("RecentEvents() len:%d err:%v", len(events), err)
	}
	wantEach := float64(150000)/1_000_000*5 + float64(1000)/1_000_000*30
	for _, event := range events {
		if event.EstimatedCost == nil || event.PriceRuleID <= 0 {
			t.Fatalf("event price snapshot missing: %+v", event)
		}
		assertCostClose(t, *event.EstimatedCost, wantEach)
	}

	buckets, err := store.UsageAggregates(ctx, UsageAggregateOptions{Interval: "all", GroupBy: []string{"model"}, Limit: 10})
	if err != nil || len(buckets) != 1 {
		t.Fatalf("UsageAggregates() = %+v err:%v", buckets, err)
	}
	assertCostClose(t, buckets[0].EstimatedCost, wantEach*2)
	if buckets[0].InputTokens != 300000 {
		t.Fatalf("aggregate input tokens = %d, want 300000", buckets[0].InputTokens)
	}
}

func TestMatchModelsDevModelUsesProviderAlias(t *testing.T) {
	catalog := map[string]modelsDevProvider{
		"openai": {Models: map[string]modelsDevModel{
			"gpt-5.6-sol": {ID: "gpt-5.6-sol", Cost: &modelsDevCost{Input: 5, Output: 30}},
		}},
	}
	provider, model, _, ok := matchModelsDevModel(catalog, ObservedModel{Provider: "codex", Model: "gpt-5.6-sol"})
	if !ok || provider != "openai" || model != "gpt-5.6-sol" {
		t.Fatalf("match = %v %q/%q, want openai/gpt-5.6-sol", ok, provider, model)
	}
}

func TestModelPriceSyncChangeRank(t *testing.T) {
	actions := []string{"added", "updated", "overridden", "locked", "unmatched", "unknown"}
	for index, action := range actions {
		if got := modelPriceSyncChangeRank(action); got != index {
			t.Fatalf("modelPriceSyncChangeRank(%q) = %d, want %d", action, got, index)
		}
	}
}

func TestLockedModelPriceRequiresExplicitOverride(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	manual := testGPT56PriceRule()
	manual.Source = modelPriceSourceManual
	manual.Locked = true
	if _, changed, err := store.UpsertModelPriceRule(ctx, manual, true); err != nil || !changed {
		t.Fatalf("manual UpsertModelPriceRule() = changed:%v err:%v", changed, err)
	}

	synced := testGPT56PriceRule()
	synced.Source = modelPriceSourceModelsDev
	synced.Locked = false
	synced.Base.Input = 9
	if _, changed, err := store.UpsertModelPriceRule(ctx, synced, false); err != nil || changed {
		t.Fatalf("locked UpsertModelPriceRule() = changed:%v err:%v; want false, nil", changed, err)
	}
	if _, changed, err := store.UpsertModelPriceRule(ctx, synced, true); err != nil || !changed {
		t.Fatalf("override UpsertModelPriceRule() = changed:%v err:%v", changed, err)
	}
	rules, err := store.ActiveModelPriceRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].Locked || rules[0].Source != modelPriceSourceModelsDev || rules[0].Base.Input != 9 {
		t.Fatalf("ActiveModelPriceRules() = %+v err:%v", rules, err)
	}
}

func TestMatchModelsDevModelUsesModelFamilyWhenObservedProviderIsWrong(t *testing.T) {
	catalog := map[string]modelsDevProvider{
		"openai": {Models: map[string]modelsDevModel{}},
		"google": {Models: map[string]modelsDevModel{
			"gemini-3.1-flash-lite": {ID: "gemini-3.1-flash-lite", Cost: &modelsDevCost{Input: 0.25, Output: 1.5}},
		}},
	}
	provider, model, _, ok := matchModelsDevModel(catalog, ObservedModel{Provider: "codex", Model: "gemini-3.1-flash-lite"})
	if !ok || provider != "google" || model != "gemini-3.1-flash-lite" {
		t.Fatalf("match = %v %q/%q, want google/gemini-3.1-flash-lite", ok, provider, model)
	}
}

func TestModelPriceRuleAppliesAcrossRequestProviders(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rule := testGPT56PriceRule()
	rule.Provider = "openai"
	if _, changed, err := store.UpsertModelPriceRule(ctx, rule, true); err != nil || !changed {
		t.Fatalf("UpsertModelPriceRule() = changed:%v err:%v", changed, err)
	}
	event := testUsageEvent(0, false, 151000)
	event.Provider = "codex"
	event.Model = rule.Model
	event.InputTokens = 150000
	event.OutputTokens = 1000
	insertTestUsageEvents(t, store, event)

	events, err := store.RecentEvents(ctx, 10)
	if err != nil || len(events) != 1 || events[0].EstimatedCost == nil {
		t.Fatalf("RecentEvents() = %+v err:%v", events, err)
	}
	want := float64(150000)/1_000_000*5 + float64(1000)/1_000_000*30
	assertCostClose(t, *events[0].EstimatedCost, want)
}

func TestObservedModelsAggregatesProvidersByModel(t *testing.T) {
	store := openTestStore(t)
	first := testUsageEvent(0, false, 1000)
	first.Provider = "codex"
	first.Model = "shared-model"
	second := testUsageEvent(1, false, 1000)
	second.Provider = "openai"
	second.Model = "shared-model"
	insertTestUsageEvents(t, store, first, second)

	models, err := store.ObservedModels(context.Background())
	if err != nil || len(models) != 1 || models[0].Model != "shared-model" || models[0].Requests != 2 {
		t.Fatalf("ObservedModels() = %+v err:%v", models, err)
	}
}

func TestMigrateProviderBoundModelPriceRules(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rule := testGPT56PriceRule()
	rule.Provider = "codex"
	rule.Source = modelPriceSourceManual
	rule.Locked = true
	rule.Version = 1
	rule.UpdatedAt = 100
	raw, err := json.Marshal(rule)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `insert into model_price_rule_versions(provider, model, version, rule_json, effective_from_ms, created_at_ms)
		values(?, ?, 1, ?, ?, ?)`, rule.Provider, rule.Model, string(raw), rule.EffectiveFrom, rule.UpdatedAt); err != nil {
		t.Fatalf("insert version error = %v", err)
	}
	if _, err := store.db.ExecContext(ctx, `insert into model_price_rules(provider, model, active_version, source, source_provider, source_model, locked, fetched_at_ms, upstream_updated, updated_at_ms)
		values(?, ?, 1, ?, '', '', 1, 0, '', ?)`, rule.Provider, rule.Model, rule.Source, rule.UpdatedAt); err != nil {
		t.Fatalf("insert rule error = %v", err)
	}

	if err := store.migrateProviderBoundModelPriceRules(ctx); err != nil {
		t.Fatalf("migrateProviderBoundModelPriceRules() error = %v", err)
	}
	rules, err := store.ActiveModelPriceRules(ctx)
	if err != nil || len(rules) != 1 || rules[0].Provider != "" || rules[0].Model != rule.Model || !rules[0].Locked {
		t.Fatalf("ActiveModelPriceRules() = %+v err:%v", rules, err)
	}
	var bound int
	if err := store.db.QueryRowContext(ctx, `select count(*) from model_price_rules where provider != ''`).Scan(&bound); err != nil || bound != 0 {
		t.Fatalf("provider-bound rules = %d err:%v", bound, err)
	}
}

func TestRecalculateEventCostsOnlyUpdatesUnpricedEvents(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	priced := testUsageEvent(0, false, 1000)
	priced.Provider = "openai"
	priced.Model = "gpt-5.6-sol"
	existingCost := 99.0
	priced.EstimatedCost = &existingCost
	unpriced := testUsageEvent(1, false, 1000)
	unpriced.Provider = "openai"
	unpriced.Model = "gpt-5.6-sol"
	insertTestUsageEvents(t, store, priced, unpriced)
	_, _, err := store.UpsertModelPriceRule(ctx, testGPT56PriceRule(), true)
	if err != nil {
		t.Fatalf("UpsertModelPriceRule() error = %v", err)
	}

	updated, err := store.RecalculateEventCosts(ctx, true)
	if err != nil || updated != 1 {
		t.Fatalf("RecalculateEventCosts() = %d, %v; want 1, nil", updated, err)
	}
	events, err := store.RecentEvents(ctx, 10)
	if err != nil {
		t.Fatalf("RecentEvents() error = %v", err)
	}
	for _, event := range events {
		if event.EventHash == priced.EventHash {
			if event.EstimatedCost == nil || *event.EstimatedCost != existingCost {
				t.Fatalf("existing cost changed: %+v", event.EstimatedCost)
			}
		}
	}
}

func TestRecalculateEventCostsUsesStoredAuthTypeForCodexOAuthFast(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	event := testUsageEvent(0, false, 1000)
	event.Provider = "codex"
	event.AuthType = "oauth"
	event.Model = "gpt-5.6-sol"
	event.ServiceTier = "fast"
	event.EffectiveServiceTier = "default"
	insertTestUsageEvents(t, store, event)
	if _, _, err := store.UpsertModelPriceRule(ctx, testGPT56PriceRule(), true); err != nil {
		t.Fatalf("UpsertModelPriceRule() error = %v", err)
	}

	updated, err := store.RecalculateEventCosts(ctx, true)
	if err != nil || updated != 1 {
		t.Fatalf("RecalculateEventCosts() = %d, %v; want 1, nil", updated, err)
	}
	var raw string
	if err := store.db.QueryRowContext(ctx, `select cost_breakdown_json from usage_events where event_hash = ?`, event.EventHash).Scan(&raw); err != nil {
		t.Fatal(err)
	}
	var breakdown ModelPriceCostBreakdown
	if err := json.Unmarshal([]byte(raw), &breakdown); err != nil {
		t.Fatal(err)
	}
	if breakdown.MatchedServiceTier != "fast" || breakdown.ServiceTierSource != serviceTierSourceCodexOAuthRequest {
		t.Fatalf("recalculated breakdown = %+v, want stored Codex OAuth auth type to preserve fast pricing", breakdown)
	}
}

func TestExportJSONLIncludesPriceRulesAndCostSnapshots(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	_, _, err := store.UpsertModelPriceRule(ctx, testGPT56PriceRule(), true)
	if err != nil {
		t.Fatalf("UpsertModelPriceRule() error = %v", err)
	}
	event := testUsageEvent(0, false, 1000)
	event.Provider = "openai"
	event.Model = "gpt-5.6-sol"
	insertTestUsageEvents(t, store, event)

	payload, err := store.ExportJSONL(ctx)
	if err != nil {
		t.Fatalf("ExportJSONL() error = %v", err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(payload))
	foundRules := false
	foundCost := false
	for scanner.Scan() {
		var record map[string]any
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatalf("invalid export line: %v", err)
		}
		if record["record_type"] == modelPricesExportRecordType {
			foundRules = len(record["rules"].([]any)) == 1
		}
		if record["event_hash"] == event.EventHash {
			_, foundCost = record["estimated_cost"]
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("scan export error = %v", err)
	}
	if !foundRules || !foundCost {
		t.Fatalf("export missing rules/cost: rules=%v cost=%v", foundRules, foundCost)
	}
}

func TestEstimateUsageCostMicrosRejectsInt64Overflow(t *testing.T) {
	store := openTestStore(t)
	ctx := context.Background()
	rule := ModelPriceRule{Model: "overflow", Base: ModelPriceRate{Input: float64(math.MaxInt64) / 1_000_000}}
	if _, _, err := store.UpsertModelPriceRule(ctx, rule, true); err != nil {
		t.Fatal(err)
	}
	if micros, err := store.EstimateUsageCostMicros(ctx, UsageCostInput{Model: rule.Model, InputTokens: 1_000_000}); err == nil {
		t.Fatalf("overflow accepted: micros=%d", micros)
	}
	rule.Base.Input = 1.2345678
	if _, _, err := store.UpsertModelPriceRule(ctx, rule, true); err != nil {
		t.Fatal(err)
	}
	if micros, err := store.EstimateUsageCostMicros(ctx, UsageCostInput{Model: rule.Model, InputTokens: 1_000_000}); err != nil || micros != 1234568 {
		t.Fatalf("ordinary rounding: micros=%d err=%v", micros, err)
	}
}
