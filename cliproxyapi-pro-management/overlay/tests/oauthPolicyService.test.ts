import { describe, expect, it } from "vitest";
import {
  countOAuthPolicyProvidersWithRules,
  isPositiveDuration,
  isValidOAuthModelPattern,
  normalizeOAuthModelPlanKey,
  normalizeOAuthPolicyPrefix,
  normalizeOAuthPolicyConfig,
  oauthModelProviderDefinitions,
  oauthModelProviderDefinitionsForAuthProviders,
  oauthPolicyConfiguredProviderKeys,
  oauthPolicyDurationValue,
  OAUTH_MODEL_PROVIDER_DEFINITIONS,
  planDefinitionsForProvider,
  resolveOAuthPolicyActiveProvider,
  serializeOAuthPolicyDuration,
  serializeOAuthPolicyConfig,
} from "@/pro/modules/oauthPolicy/oauthPolicy";

describe("oauth account policy service", () => {
  it("normalizes known plans and preserves fallback distinction", () => {
    const config = normalizeOAuthPolicyConfig({
      enabled: true,
      "cache-ttl": "45m",
      "resolve-timeout": "8s",
      providers: {
        xai: {
          plans: {
            free: { "excluded-models": [" GROK-PRO-* ", "grok-pro-*"], prefix: "pro", priority: 10, weight: 5 },
            _unknown: { "excluded-models": ["grok-preview-*"] },
          },
        },
      },
    });

    expect(config.enabled).toBe(true);
    expect(config.cacheTTL).toBe("45m");
    expect(config.maxStale).toBe("24h");
    expect(config.providers.xai.plans.free).toEqual({
      configured: true,
      excludedModels: ["grok-pro-*"],
      prefix: "pro",
      priority: 10,
      weight: 5,
    });
    expect(config.providers.xai.plans._unknown.configured).toBe(true);
    expect(config.providers.xai.plans._default.configured).toBe(false);

    const xai = OAUTH_MODEL_PROVIDER_DEFINITIONS.find(({ key }) => key === "xai")!;
    expect(xai.plans.map(({ key }) => key)).toEqual([
      "free",
      "supergrok",
      "x-basic",
      "x-premium",
      "x-premium-plus",
      "supergrok-heavy",
      "supergrok-lite",
      "paid-unknown",
      "_unknown",
      "_default",
    ]);
  });

  it("serializes only explicitly configured rules", () => {
    const config = normalizeOAuthPolicyConfig({});
    config.enabled = true;
    config.providers.xai.plans["xai-custom"] = {
      configured: true,
      excludedModels: [],
    };
    config.providers.xai.plans._default = {
      configured: true,
      excludedModels: ["grok-experimental-*"],
    };

    const serialized = serializeOAuthPolicyConfig(config);
    expect(serialized).toMatchObject({
      enabled: true,
      "cache-ttl": "30m",
      "max-stale": "24h",
      "resolve-timeout": "15s",
      providers: {
        xai: {
          plans: {
            "xai-custom": { "excluded-models": [] },
            _default: { "excluded-models": ["grok-experimental-*"] },
          },
        },
      },
    });
    expect(
      (
        serialized.providers as {
          xai: { plans: Record<string, unknown> };
        }
      ).xai.plans.free,
    ).toBeUndefined();
  });

  it("preserves the disabled state when serializing", () => {
    const config = normalizeOAuthPolicyConfig({ enabled: false });
    expect(serializeOAuthPolicyConfig(config).enabled).toBe(false);
  });

  it("validates positive Go duration fields", () => {
    expect(isPositiveDuration("30m")).toBe(true);
    expect(isPositiveDuration("1.5s")).toBe(true);
    expect(isPositiveDuration("1h30m")).toBe(true);
    expect(isPositiveDuration("0s")).toBe(false);
    expect(isPositiveDuration("30")).toBe(false);
  });

  it("canonicalizes whitespace and underscore plan keys consistently", () => {
    expect(normalizeOAuthModelPlanKey(" Pro  Lite ", "codex")).toBe("pro-lite");
    expect(normalizeOAuthModelPlanKey("pro__lite", "codex")).toBe("pro-lite");
    expect(normalizeOAuthModelPlanKey(" _unknown ", "codex")).toBe("_unknown");

    const config = normalizeOAuthPolicyConfig({
      providers: {
        codex: { plans: { "Pro Lite": { "excluded-models": ["gpt-5-pro"] } } },
      },
    });
    expect(config.providers.codex.plans["pro-lite"]).toMatchObject({
      configured: true,
      excludedModels: ["gpt-5-pro"],
    });
  });

  it("matches backend Go glob syntax validation", () => {
    expect(isValidOAuthModelPattern("grok-4-*")).toBe(true);
    expect(isValidOAuthModelPattern("model-[a-z]")).toBe(true);
    expect(isValidOAuthModelPattern("model-[z-a]")).toBe(true);
    expect(isValidOAuthModelPattern("model-[a\\-z]")).toBe(true);
    expect(isValidOAuthModelPattern("model-[a-]")).toBe(false);
    expect(isValidOAuthModelPattern("model-[-a]")).toBe(false);
    expect(isValidOAuthModelPattern("model-[a-b-c]")).toBe(false);
    expect(isValidOAuthModelPattern("model-\\")).toBe(false);
  });

  it("treats a cleared prefix as inheriting the account value", () => {
    expect(normalizeOAuthPolicyPrefix(" /team/ ")).toBe("team");
    expect(normalizeOAuthPolicyPrefix("  ")).toBeUndefined();

    const config = normalizeOAuthPolicyConfig({
      providers: { codex: { plans: { pro: { prefix: "" } } } },
    });
    expect(config.providers.codex.plans.pro.prefix).toBeUndefined();
    const serialized = serializeOAuthPolicyConfig(config) as {
      providers: Record<string, { plans: Record<string, Record<string, unknown>> }>;
    };
    expect(serialized.providers.codex.plans.pro).not.toHaveProperty("prefix");
  });

  it("converts duration values for fixed-unit controls", () => {
    expect(oauthPolicyDurationValue("1h30m", "m")).toBe(90);
    expect(oauthPolicyDurationValue("1500ms", "s")).toBe(1.5);
    expect(oauthPolicyDurationValue("invalid", "s")).toBeNull();
    expect(serializeOAuthPolicyDuration(12.3456, "s")).toBe("12.346s");
  });

  it("normalizes every provider and preserves custom plan keys", () => {
    const config = normalizeOAuthPolicyConfig({
      providers: {
        codex: { plans: { plus: { "excluded-models": ["gpt-5-pro"] } } },
        claude: {
          plans: { plan_max: { "excluded-models": ["claude-opus-*"] } },
        },
        "gemini-cli": { plans: { ultra: { "excluded-models": [] } } },
        antigravity: { plans: { "ultra-lite": { "excluded-models": [] } } },
        kimi: { plans: { enterprise: { "excluded-models": ["kimi-k2-*"] } } },
        "future-provider": {
          plans: { premium: { "excluded-models": ["future-pro-*"] } },
        },
      },
    });

    expect(config.providers.codex.plans.plus.configured).toBe(true);
    expect(config.providers.claude.plans.max).toEqual({
      configured: true,
      excludedModels: ["claude-opus-*"],
    });
    expect(config.providers["gemini-cli"].plans.ultra.configured).toBe(true);
    expect(config.providers.antigravity.plans["ultra-lite"].configured).toBe(
      true,
    );
    expect(config.providers.kimi.plans.enterprise.configured).toBe(true);
    expect(config.providers["future-provider"].plans.premium.configured).toBe(
      true,
    );
    expect(config.providers["future-provider"].plans._unknown.configured).toBe(
      false,
    );
    expect(
      oauthModelProviderDefinitions(config.providers).map(({ key }) => key),
    ).toContain("future-provider");

    const kimi = OAUTH_MODEL_PROVIDER_DEFINITIONS.find(
      ({ key }) => key === "kimi",
    )!;
    expect(
      planDefinitionsForProvider(kimi, config.providers.kimi.plans).map(
        ({ key }) => key,
      ),
    ).toEqual(["enterprise", "_unknown", "_default"]);

    const serialized = serializeOAuthPolicyConfig(config) as {
      providers: Record<string, { plans: Record<string, unknown> }>;
    };
    expect(serialized.providers.kimi.plans.enterprise).toEqual({
      "excluded-models": ["kimi-k2-*"],
    });
    expect(serialized.providers["future-provider"].plans.premium).toEqual({
      "excluded-models": ["future-pro-*"],
    });
  });

  it("falls back when the active custom provider is removed", () => {
    const withCustomProvider = normalizeOAuthPolicyConfig({
      providers: {
        "future-provider": { plans: {} },
      },
    });
    expect(
      resolveOAuthPolicyActiveProvider(
        "future-provider",
        withCustomProvider.providers,
      ),
    ).toBe("future-provider");

    const afterRemoval = normalizeOAuthPolicyConfig({ providers: {} });
    expect(
      resolveOAuthPolicyActiveProvider(
        "future-provider",
        afterRemoval.providers,
      ),
    ).toBe("xai");
  });

  it("shows only providers backed by authentication files", () => {
    const config = normalizeOAuthPolicyConfig({
      providers: {
        "future-provider": { plans: { premium: { "excluded-models": [] } } },
      },
    });
    const visible = oauthModelProviderDefinitionsForAuthProviders(
      config.providers,
      ["codex", "future-provider", "not-policy-aware"],
    );

    expect(visible.map(({ key }) => key)).toEqual([
      "codex",
      "future-provider",
    ]);
    expect(
      resolveOAuthPolicyActiveProvider("xai", config.providers, visible),
    ).toBe("codex");
    expect(resolveOAuthPolicyActiveProvider("codex", config.providers, [])).toBe(
      "",
    );
  });

  it("counts providers with at least one enabled rule", () => {
    const config = normalizeOAuthPolicyConfig({
      providers: {
        xai: { plans: { free: { "excluded-models": [] } } },
        codex: {
          plans: {
            plus: { "excluded-models": [] },
            pro: { "excluded-models": ["gpt-5-pro"] },
          },
        },
      },
    });

    expect(countOAuthPolicyProvidersWithRules(config.providers)).toBe(2);
    config.providers.xai.plans.free.configured = false;
    expect(countOAuthPolicyProvidersWithRules(config.providers)).toBe(1);
  });

  it("retains configured providers when authentication-file discovery fails", () => {
    const config = normalizeOAuthPolicyConfig({
      providers: {
        xai: { plans: { free: { "excluded-models": [] } } },
        codex: { plans: {} },
        "future-provider": {
          plans: { enterprise: { "excluded-models": ["future-preview-*"] } },
        },
      },
    });
    const fallbackProviders = oauthPolicyConfiguredProviderKeys(
      config.providers,
    );
    expect(fallbackProviders.sort()).toEqual(["future-provider", "xai"]);
    expect(
      oauthModelProviderDefinitionsForAuthProviders(
        config.providers,
        fallbackProviders,
      ).map(({ key }) => key),
    ).toEqual(["xai", "future-provider"]);
  });
});
