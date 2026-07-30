import { describe, expect, it } from "vitest";
import {
  isPositiveDuration,
  normalizeOAuthModelPolicyConfig,
  oauthModelPolicyDurationValue,
  OAUTH_MODEL_PROVIDER_DEFINITIONS,
  planDefinitionsForProvider,
  serializeOAuthModelPolicyDuration,
  serializeOAuthModelPolicyConfig,
} from "@/services/api/oauthModelPolicy";

describe("oauth model policy service", () => {
  it("normalizes known plans and preserves fallback distinction", () => {
    const config = normalizeOAuthModelPolicyConfig({
      priority: 20,
      "cache-ttl": "45m",
      "resolve-timeout": "8s",
      providers: {
        xai: {
          plans: {
            free: { "excluded-models": [" GROK-PRO-* ", "grok-pro-*"] },
            _unknown: { "excluded-models": ["grok-preview-*"] },
          },
        },
      },
    });

    expect(config.priority).toBe(20);
    expect(config.cacheTTL).toBe("45m");
    expect(config.providers.xai.plans.free).toEqual({
      configured: true,
      excludedModels: ["grok-pro-*"],
    });
    expect(config.providers.xai.plans._unknown.configured).toBe(true);
    expect(config.providers.xai.plans._default.configured).toBe(false);
  });

  it("serializes only explicitly configured rules", () => {
    const config = normalizeOAuthModelPolicyConfig({});
    config.providers.xai.plans["x-premium-plus"] = {
      configured: true,
      excludedModels: [],
    };
    config.providers.xai.plans._default = {
      configured: true,
      excludedModels: ["grok-experimental-*"],
    };

    const serialized = serializeOAuthModelPolicyConfig(config);
    expect(serialized).toMatchObject({
      enabled: true,
      priority: 10,
      "cache-ttl": "30m",
      "resolve-timeout": "15s",
      providers: {
        xai: {
          plans: {
            "x-premium-plus": { "excluded-models": [] },
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

  it("validates positive Go duration fields", () => {
    expect(isPositiveDuration("30m")).toBe(true);
    expect(isPositiveDuration("1.5s")).toBe(true);
    expect(isPositiveDuration("1h30m")).toBe(true);
    expect(isPositiveDuration("0s")).toBe(false);
    expect(isPositiveDuration("30")).toBe(false);
  });

  it("converts duration values for fixed-unit controls", () => {
    expect(oauthModelPolicyDurationValue("1h30m", "m")).toBe(90);
    expect(oauthModelPolicyDurationValue("1500ms", "s")).toBe(1.5);
    expect(oauthModelPolicyDurationValue("invalid", "s")).toBeNull();
    expect(serializeOAuthModelPolicyDuration(12.3456, "s")).toBe("12.346s");
  });

  it("normalizes every provider and preserves custom plan keys", () => {
    const config = normalizeOAuthModelPolicyConfig({
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

    const kimi = OAUTH_MODEL_PROVIDER_DEFINITIONS.find(
      ({ key }) => key === "kimi",
    )!;
    expect(
      planDefinitionsForProvider(kimi, config.providers.kimi.plans).map(
        ({ key }) => key,
      ),
    ).toEqual(["enterprise", "_unknown", "_default"]);

    const serialized = serializeOAuthModelPolicyConfig(config) as {
      providers: Record<string, { plans: Record<string, unknown> }>;
    };
    expect(serialized.providers.kimi.plans.enterprise).toEqual({
      "excluded-models": ["kimi-k2-*"],
    });
    expect(serialized.providers["future-provider"].plans.premium).toEqual({
      "excluded-models": ["future-pro-*"],
    });
  });
});
