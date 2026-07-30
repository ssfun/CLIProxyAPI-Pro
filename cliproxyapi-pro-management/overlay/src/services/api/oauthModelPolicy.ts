import { parseDocument } from "yaml";
import { configFileApi } from "./configFile";
import { pluginsApi } from "./plugins";

export const OAUTH_MODEL_POLICY_PLUGIN_ID = "oauth-model-policy";

export const OAUTH_MODEL_PROVIDER_KEYS = [
  "xai",
  "codex",
  "claude",
  "gemini-cli",
  "antigravity",
  "kimi",
] as const;

export type OAuthModelProviderKey = (typeof OAUTH_MODEL_PROVIDER_KEYS)[number];
export type OAuthModelPlanKey = string;
export type OAuthModelPolicyDurationUnit = "s" | "m";

export interface OAuthModelPlanDefinition {
  key: OAuthModelPlanKey;
  monthlyLimitCents?: number;
  kind: "plan" | "fallback" | "custom";
  localeSuffix: string;
}

export interface OAuthModelProviderDefinition {
  key: OAuthModelProviderKey;
  plans: OAuthModelPlanDefinition[];
}

export interface OAuthModelPlanRule {
  configured: boolean;
  excludedModels: string[];
}

export interface OAuthModelPolicyConfig {
  priority: number;
  cacheTTL: string;
  resolveTimeout: string;
  providers: Record<
    string,
    { plans: Record<OAuthModelPlanKey, OAuthModelPlanRule> }
  >;
}

export interface OAuthModelPolicySnapshot {
  pluginsEnabled: boolean;
  pluginDiscovered: boolean;
  pluginEnabled: boolean;
  pluginRegistered: boolean;
  pluginVersion: string;
  config: OAuthModelPolicyConfig;
}

const fallbackPlans: OAuthModelPlanDefinition[] = [
  { key: "_unknown", kind: "fallback", localeSuffix: "unknown" },
  { key: "_default", kind: "fallback", localeSuffix: "default" },
];

export const OAUTH_MODEL_PROVIDER_DEFINITIONS: OAuthModelProviderDefinition[] =
  [
    {
      key: "xai",
      plans: [
        {
          key: "free",
          monthlyLimitCents: 0,
          kind: "plan",
          localeSuffix: "free",
        },
        {
          key: "supergrok",
          monthlyLimitCents: 15_000,
          kind: "plan",
          localeSuffix: "supergrok",
        },
        {
          key: "x-premium-plus",
          monthlyLimitCents: 20_000,
          kind: "plan",
          localeSuffix: "x_premium_plus",
        },
        {
          key: "supergrok-heavy",
          monthlyLimitCents: 150_000,
          kind: "plan",
          localeSuffix: "supergrok_heavy",
        },
        { key: "paid-unknown", kind: "plan", localeSuffix: "paid_unknown" },
        ...fallbackPlans,
      ],
    },
    {
      key: "codex",
      plans: [
        { key: "free", kind: "plan", localeSuffix: "free" },
        { key: "plus", kind: "plan", localeSuffix: "plus" },
        { key: "pro", kind: "plan", localeSuffix: "pro" },
        { key: "pro-lite", kind: "plan", localeSuffix: "pro_lite" },
        { key: "team", kind: "plan", localeSuffix: "team" },
        ...fallbackPlans,
      ],
    },
    {
      key: "claude",
      plans: [
        { key: "free", kind: "plan", localeSuffix: "free" },
        { key: "pro", kind: "plan", localeSuffix: "pro" },
        { key: "max", kind: "plan", localeSuffix: "max" },
        { key: "team", kind: "plan", localeSuffix: "team" },
        ...fallbackPlans,
      ],
    },
    {
      key: "gemini-cli",
      plans: [
        { key: "free", kind: "plan", localeSuffix: "free" },
        { key: "legacy", kind: "plan", localeSuffix: "legacy" },
        { key: "standard", kind: "plan", localeSuffix: "standard" },
        { key: "pro", kind: "plan", localeSuffix: "pro" },
        { key: "ultra", kind: "plan", localeSuffix: "ultra" },
        ...fallbackPlans,
      ],
    },
    {
      key: "antigravity",
      plans: [
        { key: "free", kind: "plan", localeSuffix: "free" },
        { key: "pro", kind: "plan", localeSuffix: "pro" },
        { key: "ultra", kind: "plan", localeSuffix: "ultra" },
        { key: "ultra-lite", kind: "plan", localeSuffix: "ultra_lite" },
        ...fallbackPlans,
      ],
    },
    { key: "kimi", plans: [...fallbackPlans] },
  ];

export const XAI_PLAN_DEFINITIONS = OAUTH_MODEL_PROVIDER_DEFINITIONS[0].plans;

export const normalizeOAuthModelPlanKey = (
  value: string,
  provider?: string,
): string => {
  const normalized = value.trim().toLowerCase();
  if (normalized.startsWith("_")) {
    return `_${normalized.slice(1).replace(/_/g, "-")}`;
  }
  let key = normalized.replace(/_/g, "-");
  if (key.startsWith("plan-")) key = key.slice(5);
  if (provider === "codex" && key === "prolite") return "pro-lite";
  const aliases: Record<string, Record<string, string>> = {
    xai: {
      "premium-plus": "x-premium-plus",
      "x-premium+": "x-premium-plus",
      "super-grok": "supergrok",
      "super-grok-heavy": "supergrok-heavy",
    },
    "gemini-cli": {
      "free-tier": "free",
      "legacy-tier": "legacy",
      "standard-tier": "standard",
      "g1-pro-tier": "pro",
      "pro-tier": "pro",
      "g1-ultra-tier": "ultra",
      "ultra-tier": "ultra",
    },
    antigravity: {
      "free-tier": "free",
      "g1-pro-tier": "pro",
      "g1-ultra-tier": "ultra",
      "g1-ultra-lite-tier": "ultra-lite",
    },
  };
  return (provider && aliases[provider]?.[key]) || key;
};

export const planDefinitionsForProvider = (
  provider: OAuthModelProviderDefinition,
  plans: Record<string, OAuthModelPlanRule>,
): OAuthModelPlanDefinition[] => {
  const fallback = provider.plans.filter(({ kind }) => kind === "fallback");
  const regular = provider.plans.filter(({ kind }) => kind !== "fallback");
  const known = new Set(provider.plans.map(({ key }) => key));
  const custom = Object.keys(plans)
    .filter((key) => !known.has(key) && !key.startsWith("_"))
    .sort((left, right) => left.localeCompare(right))
    .map((key) => ({ key, kind: "custom" as const, localeSuffix: "custom" }));
  return [...regular, ...custom, ...fallback];
};

const asRecord = (value: unknown): Record<string, unknown> =>
  value && typeof value === "object" && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};

const asString = (value: unknown, fallback = ""): string =>
  typeof value === "string" ? value : value == null ? fallback : String(value);

const asNumber = (value: unknown, fallback: number): number => {
  const parsed = Number(value);
  return Number.isFinite(parsed) ? parsed : fallback;
};

const hasOwn = (source: Record<string, unknown>, key: string): boolean =>
  Object.prototype.hasOwnProperty.call(source, key);

export const normalizeModelPatterns = (value: unknown): string[] => {
  if (!Array.isArray(value)) return [];
  const seen = new Set<string>();
  return value
    .map((item) => asString(item).trim().toLowerCase())
    .filter((item) => {
      if (!item || seen.has(item)) return false;
      seen.add(item);
      return true;
    });
};

export const defaultOAuthModelPolicyConfig = (): OAuthModelPolicyConfig => ({
  priority: 10,
  cacheTTL: "30m",
  resolveTimeout: "15s",
  providers: Object.fromEntries(
    OAUTH_MODEL_PROVIDER_DEFINITIONS.map((provider) => [
      provider.key,
      {
        plans: Object.fromEntries(
          provider.plans.map(({ key }) => [
            key,
            { configured: false, excludedModels: [] },
          ]),
        ),
      },
    ]),
  ),
});

export const normalizeOAuthModelPolicyConfig = (
  value: unknown,
): OAuthModelPolicyConfig => {
  const source = asRecord(value);
  const defaults = defaultOAuthModelPolicyConfig();
  const providers = asRecord(source.providers);
  const providerDefinitions = new Map(
    OAUTH_MODEL_PROVIDER_DEFINITIONS.map((provider) => [
      provider.key,
      provider,
    ]),
  );
  const providerKeys = new Set([
    ...OAUTH_MODEL_PROVIDER_KEYS,
    ...Object.keys(providers),
  ]);
  const normalizedProviders = Object.fromEntries(
    [...providerKeys].map((providerKey) => {
      const provider = providerDefinitions.get(
        providerKey as OAuthModelProviderKey,
      );
      const providerSource = asRecord(providers[providerKey]);
      const plans = asRecord(providerSource.plans);
      const normalizedPlanSources = Object.fromEntries(
        Object.entries(plans).map(([key, plan]) => [
          normalizeOAuthModelPlanKey(key, providerKey),
          plan,
        ]),
      );
      const keys = new Set([
        ...(provider?.plans.map(({ key }) => key) ?? []),
        ...Object.keys(normalizedPlanSources),
      ]);
      const normalizedPlans = Object.fromEntries(
        [...keys].map((key) => {
          const configured = hasOwn(normalizedPlanSources, key);
          const plan = asRecord(normalizedPlanSources[key]);
          return [
            key,
            {
              configured,
              excludedModels: normalizeModelPatterns(plan["excluded-models"]),
            },
          ];
        }),
      );
      return [providerKey, { plans: normalizedPlans }];
    }),
  ) as OAuthModelPolicyConfig["providers"];
  return {
    priority: Math.trunc(asNumber(source.priority, defaults.priority)),
    cacheTTL:
      asString(source["cache-ttl"], defaults.cacheTTL).trim() ||
      defaults.cacheTTL,
    resolveTimeout:
      asString(source["resolve-timeout"], defaults.resolveTimeout).trim() ||
      defaults.resolveTimeout,
    providers: normalizedProviders,
  };
};

export const serializeOAuthModelPolicyConfig = (
  config: OAuthModelPolicyConfig,
): Record<string, unknown> => {
  const providers = Object.fromEntries(
    Object.entries(config.providers).map(([providerKey, provider]) => {
      const plans: Record<string, unknown> = {};
      Object.entries(provider.plans).forEach(([key, rule]) => {
        if (!rule.configured) return;
        plans[key] = {
          "excluded-models": normalizeModelPatterns(rule.excludedModels),
        };
      });
      return [providerKey, { plans }];
    }),
  );
  return {
    enabled: true,
    priority: Math.trunc(config.priority),
    "cache-ttl": config.cacheTTL.trim(),
    "resolve-timeout": config.resolveTimeout.trim(),
    providers,
  };
};

const durationUnitMilliseconds: Record<string, number> = {
  ns: 0.000001,
  us: 0.001,
  µs: 0.001,
  μs: 0.001,
  ms: 1,
  s: 1000,
  m: 60_000,
  h: 3_600_000,
};

export const oauthModelPolicyDurationValue = (
  value: string,
  targetUnit: OAuthModelPolicyDurationUnit,
): number | null => {
  const source = value.trim();
  if (!source) return null;
  const pattern = /(-?\d+(?:\.\d+)?)(ns|us|µs|μs|ms|s|m|h)/g;
  let milliseconds = 0;
  let cursor = 0;
  let matched = false;
  for (const match of source.matchAll(pattern)) {
    if (match.index !== cursor) return null;
    milliseconds += Number(match[1]) * durationUnitMilliseconds[match[2]];
    cursor = match.index + match[0].length;
    matched = true;
  }
  if (
    !matched ||
    cursor !== source.length ||
    !Number.isFinite(milliseconds) ||
    milliseconds <= 0
  ) {
    return null;
  }
  return milliseconds / durationUnitMilliseconds[targetUnit];
};

export const serializeOAuthModelPolicyDuration = (
  value: number,
  unit: OAuthModelPolicyDurationUnit,
): string => `${Math.round(value * 1000) / 1000}${unit}`;

export const isPositiveDuration = (value: string): boolean =>
  oauthModelPolicyDurationValue(value, "s") !== null;

const ensureGlobalPluginSwitch = async (): Promise<void> => {
  const raw = await configFileApi.fetchConfigYaml();
  const document = parseDocument(raw || "{}");
  if (document.errors.length > 0) throw document.errors[0];
  document.setIn(["plugins", "enabled"], true);
  await configFileApi.saveConfigYaml(document.toString());
};

const waitForRegistration = async (attempts = 24): Promise<void> => {
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    const list = await pluginsApi.list();
    const plugin = list.plugins.find(
      (entry) => entry.id === OAUTH_MODEL_POLICY_PLUGIN_ID,
    );
    if (plugin?.registered) return;
    await new Promise((resolve) => globalThis.setTimeout(resolve, 250));
  }
  throw new Error("OAuth model policy plugin did not become ready in time");
};

export const oauthModelPolicyApi = {
  async load(): Promise<OAuthModelPolicySnapshot> {
    const [pluginList, rawConfig] = await Promise.all([
      pluginsApi.list(),
      pluginsApi.getConfig(OAUTH_MODEL_POLICY_PLUGIN_ID),
    ]);
    const plugin = pluginList.plugins.find(
      (entry) => entry.id === OAUTH_MODEL_POLICY_PLUGIN_ID,
    );
    return {
      pluginsEnabled: pluginList.pluginsEnabled,
      pluginDiscovered: Boolean(plugin),
      pluginEnabled: Boolean(plugin?.enabled),
      pluginRegistered: Boolean(plugin?.registered),
      pluginVersion: plugin?.metadata?.version ?? "",
      config: normalizeOAuthModelPolicyConfig(rawConfig),
    };
  },

  async save(
    config: OAuthModelPolicyConfig,
  ): Promise<OAuthModelPolicySnapshot> {
    const pluginList = await pluginsApi.list();
    const plugin = pluginList.plugins.find(
      (entry) => entry.id === OAUTH_MODEL_POLICY_PLUGIN_ID,
    );
    if (!plugin)
      throw new Error("Bundled oauth-model-policy plugin was not found");
    await pluginsApi.patchConfig(
      OAUTH_MODEL_POLICY_PLUGIN_ID,
      serializeOAuthModelPolicyConfig(config),
    );
    if (!plugin.enabled) {
      await pluginsApi.updateEnabled(OAUTH_MODEL_POLICY_PLUGIN_ID, true);
    }
    if (!pluginList.pluginsEnabled) await ensureGlobalPluginSwitch();
    await waitForRegistration();
    return this.load();
  },
};
