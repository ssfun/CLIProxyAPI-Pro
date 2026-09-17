package config

import (
	"testing"
	"time"
)

func TestParseNormalizesXAIPlans(t *testing.T) {
	cfg, errParse := Parse([]byte(`
cache-ttl: 10m
providers:
  XAI:
    plans:
      SUPER_GROK:
        excluded-models: [" GROK-4-* ", "grok-4-*"]
      _unknown:
        excluded-models: ["grok-pro-*"]
`))
	if errParse != nil {
		t.Fatalf("Parse() error = %v", errParse)
	}
	plan := cfg.Providers["xai"].Plans["supergrok"]
	if len(plan.ExcludedModels) != 1 || plan.ExcludedModels[0] != "grok-4-*" {
		t.Fatalf("excluded models = %#v", plan.ExcludedModels)
	}
	if _, ok := cfg.Providers["xai"].Plans["_unknown"]; !ok {
		t.Fatal("_unknown fallback plan was not preserved")
	}
}

func TestParseDefaultsAndValidatesMaxStale(t *testing.T) {
	cfg, err := Parse([]byte(`cache-ttl: 48h`))
	if err != nil {
		t.Fatal(err)
	}
	if cfg.MaxStale != 48*time.Hour {
		t.Fatalf("default max-stale = %s, want 48h", cfg.MaxStale)
	}
	if _, err = Parse([]byte("cache-ttl: 30m\nmax-stale: 10m\n")); err == nil {
		t.Fatal("accepted max-stale shorter than cache-ttl")
	}
}

func TestParseRejectsInvalidModelPattern(t *testing.T) {
	_, errParse := Parse([]byte(`
providers:
  xai:
    plans:
      free:
        excluded-models: ["grok-["]
`))
	if errParse == nil {
		t.Fatal("Parse() error = nil, want invalid pattern error")
	}
}

func TestParseNormalizesAccountRoutingFields(t *testing.T) {
	cfg, err := Parse([]byte(`
providers:
  codex:
    plans:
      pro:
        prefix: " /team-pro/ "
        priority: 50
        weight: -2
`))
	if err != nil {
		t.Fatal(err)
	}
	plan := cfg.Providers["codex"].Plans["pro"]
	if plan.Prefix == nil || *plan.Prefix != "team-pro" || plan.Priority == nil || *plan.Priority != 50 || plan.Weight == nil || *plan.Weight != 0 {
		t.Fatalf("normalized account policy = %#v", plan)
	}
	if _, err := Parse([]byte(`providers: {codex: {plans: {pro: {prefix: "bad/path"}}}}`)); err == nil {
		t.Fatal("accepted multi-segment prefix")
	}
	if _, err := Parse([]byte(`providers: {codex: {plans: {pro: {weight: 1000001}}}}`)); err == nil {
		t.Fatal("accepted oversized weight")
	}
}

func TestParseCanonicalizesProviderPlanAliases(t *testing.T) {
	cfg, errParse := Parse([]byte(`
providers:
  claude:
    plans:
      plan_max: {excluded-models: [claude-opus-*]}
  gemini-cli:
    plans:
      g1-ultra-tier: {excluded-models: [gemini-pro-*]}
  antigravity:
    plans:
      g1-ultra-lite-tier: {excluded-models: [claude-*]}
`))
	if errParse != nil {
		t.Fatalf("Parse() error = %v", errParse)
	}
	for provider, plan := range map[string]string{
		"claude": "max", "gemini-cli": "ultra", "antigravity": "ultra-lite",
	} {
		if _, ok := cfg.Providers[provider].Plans[plan]; !ok {
			t.Fatalf("providers.%s.plans.%s was not canonicalized", provider, plan)
		}
	}
}

func TestParseRejectsDuplicateNormalizedKeys(t *testing.T) {
	if _, err := Parse([]byte(`
providers:
  xai: {plans: {free: {excluded-models: [first-*]}}}
  XAI: {plans: {free: {excluded-models: [second-*]}}}
`)); err == nil {
		t.Fatal("accepted duplicate normalized provider keys")
	}
	if _, err := Parse([]byte(`
providers:
  xai:
    plans:
      super-grok: {excluded-models: [first-*]}
      supergrok: {excluded-models: [second-*]}
`)); err == nil {
		t.Fatal("accepted duplicate normalized plan keys")
	}
	if _, err := Parse([]byte(`
providers:
  codex:
    plans:
      "Pro Lite": {excluded-models: [first-*]}
      pro__lite: {excluded-models: [second-*]}
`)); err == nil {
		t.Fatal("accepted duplicate whitespace/underscore-normalized plan keys")
	}
}

func TestCanonicalPlanKeyCollapsesWhitespaceAndUnderscores(t *testing.T) {
	for raw, want := range map[string]string{
		" Pro  Lite ": "pro-lite",
		"pro__lite":   "pro-lite",
		" _unknown ":  "_unknown",
	} {
		if got := CanonicalPlanKey("codex", raw); got != want {
			t.Fatalf("CanonicalPlanKey(%q) = %q, want %q", raw, got, want)
		}
	}
}
