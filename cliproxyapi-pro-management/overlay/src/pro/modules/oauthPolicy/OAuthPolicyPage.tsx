import {
  useCallback,
  useEffect,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { useTranslation } from "react-i18next";
import { createPortal } from "react-dom";
import { Button } from "@/components/ui/Button";
import { Select } from "@/components/ui/Select";
import { ToggleSwitch } from "@/components/ui/ToggleSwitch";
import { authFilesApi } from "@/services/api/authFiles";
import {
  IconAlertTriangle,
  IconCheck,
  IconCheckCircle2,
  IconInfo,
  IconModelCluster,
  IconPlus,
  IconRefreshCw,
  IconSettings,
  IconX,
} from "@/components/ui/icons";
import {
  defaultOAuthPolicyConfig,
  countOAuthPolicyProvidersWithRules,
  isPositiveDuration,
  isValidOAuthModelPattern,
  normalizeOAuthPolicyPrefix,
  normalizeOAuthModelPlanKey,
  oauthPolicyConfiguredProviderKeys,
  oauthModelProviderDefinitionsForAuthProviders,
  oauthPolicyDurationValue,
  oauthPolicyApi,
  planDefinitionsForProvider,
  resolveOAuthPolicyActiveProvider,
  serializeOAuthPolicyDuration,
  type OAuthModelPlanKey,
  type OAuthModelPlanRule,
  type OAuthPolicyConfig,
  type OAuthPolicyDurationUnit,
  type OAuthPolicySnapshot,
} from "@/pro/modules/oauthPolicy/oauthPolicy";
import { useActionBarHeightVar } from "@/hooks/useActionBarHeightVar";
import { useAuthStore, useNotificationStore } from "@/stores";
import { resolveAuthProvider } from "@/utils/quota";
import { isRuntimeOnlyAuthFile } from "@/pro/modules/quota";
import { DurationInput, type DurationFieldProps } from '@/pro/shared/DurationInput';
import configStyles from "@/pro/shared/FloatingActionBar.module.scss";
import { ProFeatureHeader } from "@/pro/shared/ProFeatureHeader";
import { ProFeatureTabs } from "@/pro/shared/ProFeatureTabs";
import styles from "./OAuthPolicyPage.module.scss";

const errorMessage = (error: unknown): string =>
  error instanceof Error ? error.message : String(error || "Unknown error");

function OAuthDurationInput(props: DurationFieldProps<OAuthPolicyDurationUnit>) {
  return (
    <DurationInput
      {...props}
      className={styles.durationControl}
      min={1}
      step={1}
      inputMode="numeric"
      parse={oauthPolicyDurationValue}
      normalize={(value) => Math.max(1, Math.round(value))}
      serialize={serializeOAuthPolicyDuration}
    />
  );
}

interface PatternEditorProps {
  planKey: OAuthModelPlanKey;
  disabled: boolean;
  patterns: string[];
  onChange: (patterns: string[]) => void;
}

function PatternEditor({
  planKey,
  disabled,
  patterns,
  onChange,
}: PatternEditorProps) {
  const { t } = useTranslation();
  const [value, setValue] = useState("");

  const addPatterns = () => {
    const seen = new Set(patterns.map((pattern) => pattern.toLowerCase()));
    const additions = value
      .split(/[\n,]/)
      .map((pattern) => pattern.trim().toLowerCase())
      .filter((pattern) => pattern && !seen.has(pattern));
    if (additions.length === 0) return;
    onChange([...patterns, ...additions]);
    setValue("");
  };

  const handleKeyDown = (event: KeyboardEvent<HTMLInputElement>) => {
    if (event.key !== "Enter") return;
    event.preventDefault();
    addPatterns();
  };

  return (
    <div className={styles.patternEditor}>
      <div className={styles.patternList}>
        {patterns.length === 0 ? (
          <span className={styles.patternEmpty}>
            {t("oauth_policy.no_exclusions", {
              defaultValue: "No excluded models",
            })}
          </span>
        ) : (
          patterns.map((pattern) => (
            <span
              key={pattern}
              className={`${styles.patternChip} ${
                isValidOAuthModelPattern(pattern) ? "" : styles.patternInvalid
              }`}
            >
              <code>{pattern}</code>
              <button
                type="button"
                disabled={disabled}
                onClick={() =>
                  onChange(patterns.filter((item) => item !== pattern))
                }
                aria-label={t("oauth_policy.remove_pattern", {
                  defaultValue: "Remove {{pattern}}",
                  pattern,
                })}
              >
                <IconX size={13} />
              </button>
            </span>
          ))
        )}
      </div>
      <div className={styles.patternInputRow}>
        <input
          className={styles.patternInput}
          value={value}
          disabled={disabled}
          onChange={(event) => setValue(event.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t("oauth_policy.pattern_placeholder", {
            defaultValue: "e.g. grok-pro-*",
          })}
          aria-label={t("oauth_policy.pattern_input", {
            defaultValue: "Model pattern for {{plan}}",
            plan: planKey,
          })}
        />
        <Button
          variant="secondary"
          size="sm"
          disabled={disabled || !value.trim()}
          onClick={addPatterns}
        >
          <IconPlus size={14} />
          {t("common.add", { defaultValue: "Add" })}
        </Button>
      </div>
      <p className={styles.patternHint}>
        {t("oauth_policy.pattern_hint", {
          defaultValue:
            "Supports *, ?, and character ranges. Enter or commas add multiple rules.",
        })}
      </p>
    </div>
  );
}

export function OAuthPolicyPage() {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const showNotification = useNotificationStore(
    (state) => state.showNotification,
  );
  const [snapshot, setSnapshot] = useState<OAuthPolicySnapshot | null>(
    null,
  );
  const [draft, setDraft] = useState<OAuthPolicyConfig>(
    defaultOAuthPolicyConfig,
  );
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [authProviders, setAuthProviders] = useState<string[] | null>(null);
  const [authProviderError, setAuthProviderError] = useState("");
  const [activeProvider, setActiveProvider] = useState("xai");
  const [customPlan, setCustomPlan] = useState("");
  const [effectiveProvider, setEffectiveProvider] = useState("all");
  const [effectivePlan, setEffectivePlan] = useState("all");
  const actionBarRef = useRef<HTMLDivElement>(null);
  const dirtyRef = useRef(false);
  const draftRevisionRef = useRef(0);
  useActionBarHeightVar(
    actionBarRef,
    "--oauth-policy-action-bar-height",
    dirty,
  );

  const load = useCallback(
    async (replaceDraft = false) => {
      if (connectionStatus !== "connected") {
        setLoading(false);
        return;
      }
      setLoading(true);
      try {
        const [next, authFileResult] = await Promise.all([
          oauthPolicyApi.load(),
          authFilesApi
            .list()
            .then((response) => ({ response, error: "" }))
            .catch((error) => ({ response: null, error: errorMessage(error) })),
        ]);
        setSnapshot(next);
        if (authFileResult.response) {
          const nextAuthProviders = Array.from(
            new Set(
              (Array.isArray(authFileResult.response.files)
                ? authFileResult.response.files
                : []
              )
                .filter((file) => !isRuntimeOnlyAuthFile(file))
                .map(resolveAuthProvider)
                .filter(Boolean),
            ),
          );
          setAuthProviders(nextAuthProviders);
        } else {
          setAuthProviders(null);
        }
        setAuthProviderError(authFileResult.error);
        if (!dirtyRef.current || replaceDraft) setDraft(next.config);
        setLoadError("");
      } catch (error) {
        setLoadError(errorMessage(error));
      } finally {
        setLoading(false);
      }
    },
    [connectionStatus],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const updateDraft = useCallback(
    (
      next:
        | OAuthPolicyConfig
        | ((current: OAuthPolicyConfig) => OAuthPolicyConfig),
    ) => {
      setDraft((current) =>
        typeof next === "function" ? next(current) : next,
      );
      draftRevisionRef.current += 1;
      setDirty(true);
      dirtyRef.current = true;
    },
    [],
  );

  const patchPlan = (
    provider: string,
    key: OAuthModelPlanKey,
    patch: Partial<OAuthModelPlanRule>,
  ) => {
    updateDraft((current) => ({
      ...current,
      providers: {
        ...current.providers,
        [provider]: {
          plans: {
            ...current.providers[provider].plans,
            [key]: { ...current.providers[provider].plans[key], ...patch },
          },
        },
      },
    }));
  };

  const addCustomPlan = () => {
    const key = normalizeOAuthModelPlanKey(customPlan, resolvedActiveProvider);
    if (!key || key.startsWith("_")) return;
    const plans = draft.providers[resolvedActiveProvider].plans;
    if (plans[key]) {
      showNotification(
        t("oauth_policy.plan_exists", {
          defaultValue: "Plan key already exists: {{plan}}",
          plan: key,
        }),
        "warning",
      );
      return;
    }
    patchPlan(resolvedActiveProvider, key, {
      configured: true,
      excludedModels: [],
    });
    setCustomPlan("");
  };

  const removeCustomPlan = (key: OAuthModelPlanKey) => {
    updateDraft((current) => {
      const plans = { ...current.providers[resolvedActiveProvider].plans };
      delete plans[key];
      return {
        ...current,
        providers: {
          ...current.providers,
          [resolvedActiveProvider]: { plans },
        },
      };
    });
  };

  const providerDefinitions = useMemo(
    () =>
      oauthModelProviderDefinitionsForAuthProviders(
        draft.providers,
        authProviders ?? oauthPolicyConfiguredProviderKeys(draft.providers),
      ),
    [authProviders, draft.providers],
  );
  const resolvedActiveProvider = resolveOAuthPolicyActiveProvider(
    activeProvider,
    draft.providers,
    providerDefinitions,
  );
  const activeProviderDefinition = providerDefinitions.find(
    ({ key }) => key === resolvedActiveProvider,
  ) ?? providerDefinitions[0];
  const activePlans = draft.providers[resolvedActiveProvider]?.plans ?? {};
  const activePlanDefinitions = activeProviderDefinition
    ? planDefinitionsForProvider(activeProviderDefinition, activePlans)
    : [];

  useEffect(() => {
    if (activeProvider === resolvedActiveProvider) return;
    setActiveProvider(resolvedActiveProvider);
    setCustomPlan("");
  }, [activeProvider, resolvedActiveProvider]);

  const configuredCount = useMemo(
    () =>
      Object.values(draft.providers).reduce(
        (total, provider) =>
          total +
          Object.values(provider.plans).filter(({ configured }) => configured)
            .length,
        0,
      ),
    [draft.providers],
  );
  const configuredProviderCount = useMemo(
    () => countOAuthPolicyProvidersWithRules(draft.providers),
    [draft.providers],
  );
  const excludedCount = useMemo(
    () =>
      Object.values(draft.providers).reduce(
        (total, provider) =>
          total +
          Object.values(provider.plans).reduce(
            (providerTotal, rule) =>
              providerTotal +
              (rule.configured ? rule.excludedModels.length : 0),
            0,
          ),
        0,
      ),
    [draft.providers],
  );

  const effectiveProviderOptions = useMemo(
    () => [
      {
        value: "all",
        label: t("oauth_policy.filter_all_providers", {
          defaultValue: "All providers",
        }),
      },
      ...Array.from(
        new Set(snapshot?.effective.map((item) => item.provider).filter(Boolean)),
      )
        .sort((left, right) => left.localeCompare(right))
        .map((provider) => ({ value: provider, label: provider })),
    ],
    [snapshot?.effective, t],
  );

  const effectivePlanOptions = useMemo(
    () => [
      {
        value: "all",
        label: t("oauth_policy.filter_all_plans", {
          defaultValue: "All plans",
        }),
      },
      ...Array.from(
        new Set(
          snapshot?.effective
            .filter(
              (item) =>
                effectiveProvider === "all" ||
                item.provider === effectiveProvider,
            )
            .map((item) => item.planKey)
            .filter(Boolean),
        ),
      )
        .sort((left, right) => left.localeCompare(right))
        .map((plan) => ({ value: plan, label: plan })),
    ],
    [effectiveProvider, snapshot?.effective, t],
  );

  useEffect(() => {
    if (!effectivePlanOptions.some((option) => option.value === effectivePlan)) {
      setEffectivePlan("all");
    }
  }, [effectivePlan, effectivePlanOptions]);

  const filteredEffectivePolicies = useMemo(
    () =>
      (snapshot?.effective ?? []).filter(
        (item) =>
          (effectiveProvider === "all" ||
            item.provider === effectiveProvider) &&
          (effectivePlan === "all" || item.planKey === effectivePlan),
      ),
    [effectivePlan, effectiveProvider, snapshot?.effective],
  );

  const inheritedRule = (key: OAuthModelPlanKey): string => {
    const plans = activePlans;
    if (plans[key].configured) return "";
    if (key === "_default")
      return t("oauth_policy.no_rule", { defaultValue: "No rule" });
    if (key === "_unknown" && plans._unknown.configured) return "";
    if (!key.startsWith("_") && plans._default.configured)
      return t("oauth_policy.inherits_default", {
        defaultValue: "Uses _default",
      });
    return t("oauth_policy.no_rule", { defaultValue: "No policy rule" });
  };

  const validate = (config = draft): string => {
    const cacheTTLSeconds = oauthPolicyDurationValue(config.cacheTTL, "s");
    if (cacheTTLSeconds === null)
      return t("oauth_policy.invalid_cache_ttl", {
        defaultValue: "Cache TTL must be a positive Go duration, such as 30m.",
      });
    const maxStaleSeconds = oauthPolicyDurationValue(config.maxStale, "s");
    if (maxStaleSeconds === null)
      return t("oauth_policy.invalid_max_stale", {
        defaultValue: "Maximum stale age must be a positive Go duration, such as 24h.",
      });
    if (maxStaleSeconds < cacheTTLSeconds)
      return t("oauth_policy.max_stale_before_cache_ttl", {
        defaultValue: "Maximum stale age must be greater than or equal to the cache TTL.",
      });
    if (!isPositiveDuration(config.resolveTimeout))
      return t("oauth_policy.invalid_resolve_timeout", {
        defaultValue:
          "Resolve timeout must be a positive Go duration, such as 15s.",
      });
    for (const provider of Object.values(config.providers)) {
      for (const rule of Object.values(provider.plans)) {
		if (rule.prefix?.includes("/"))
		  return t("oauth_policy.invalid_prefix", {
		    defaultValue: "Prefix must be one path segment without a slash.",
		  });
		if (rule.weight !== undefined && (!Number.isInteger(rule.weight) || rule.weight < 0 || rule.weight > 1_000_000))
		  return t("oauth_policy.invalid_weight", {
		    defaultValue: "Weight must be an integer from 0 to 1,000,000.",
		  });
        const invalid = rule.excludedModels.find(
          (pattern) => !isValidOAuthModelPattern(pattern),
        );
        if (invalid)
          return t("oauth_policy.invalid_pattern", {
            defaultValue: "Invalid model pattern: {{pattern}}",
            pattern: invalid,
          });
      }
    }
    return "";
  };

  const save = async (nextDraft = draft) => {
    const validation = validate(nextDraft);
    if (validation) {
      showNotification(validation, "error");
      return;
    }
    const savedRevision = draftRevisionRef.current;
    setSaving(true);
    try {
      const next = await oauthPolicyApi.save(nextDraft);
      setSnapshot(next);
      if (draftRevisionRef.current === savedRevision) {
        setDraft(next.config);
        setDirty(false);
        dirtyRef.current = false;
      }
      setLoadError("");
      showNotification(
        t("oauth_policy.save_success", {
          defaultValue: "OAuth account policy saved",
        }),
        "success",
      );
    } catch (error) {
      showNotification(
        t("oauth_policy.save_failed", {
          defaultValue: "Save failed: {{message}}",
          message: errorMessage(error),
        }),
        "error",
      );
    } finally {
      setSaving(false);
    }
  };

  const toggleEnabled = () => {
    const enabled = !(snapshot?.status.enabled ?? draft.enabled);
    void save({ ...draft, enabled });
  };

  const refreshPlanDetection = async () => {
    if (connectionStatus !== "connected") return;
    setLoading(true);
    try {
      await oauthPolicyApi.refreshPlanDetection();
      let next = await oauthPolicyApi.load();
      for (let attempt = 0; attempt < 60 && next.status.refreshing; attempt += 1) {
        await new Promise((resolve) => window.setTimeout(resolve, 500));
        next = await oauthPolicyApi.load();
      }
      if (next.status.refreshing) {
        throw new Error(
          t("oauth_policy.refresh_timeout", {
            defaultValue: "Plan re-identification is still running. Try again shortly.",
          }),
        );
      }
      setSnapshot(next);
      if (!dirtyRef.current) setDraft(next.config);
      setLoadError("");
      await load();
      showNotification(
        t("oauth_policy.refresh_success", {
          defaultValue: "Account plans were re-identified",
        }),
        "success",
      );
    } catch (error) {
      showNotification(
        t("oauth_policy.refresh_failed", {
          defaultValue: "Plan re-identification failed: {{message}}",
          message: errorMessage(error),
        }),
        "error",
      );
    } finally {
      setLoading(false);
    }
  };

  const discard = () => {
    draftRevisionRef.current += 1;
    setDraft(snapshot?.config ?? defaultOAuthPolicyConfig());
    setDirty(false);
    dirtyRef.current = false;
  };

  return (
    <div className={`${styles.page} ${dirty ? styles.pageWithSave : ""}`}>
      <ProFeatureHeader
        title={t("oauth_policy.title", { defaultValue: "Account Policy" })}
        subtitle={t("oauth_policy.subtitle", {
          defaultValue:
            "Apply model availability and routing attributes by provider and detected OAuth plan.",
        })}
        icon={<IconModelCluster size={20} />}
        active={snapshot?.status.enabled === true}
        loading={loading}
        actionBusy={saving}
        actionDisabled={!snapshot}
        onRefresh={() => void refreshPlanDetection()}
        onToggle={toggleEnabled}
      />

      {loadError && <div className={styles.errorBanner}>{loadError}</div>}
      {authProviderError && (
        <div className={styles.warningBanner}>
          {t("oauth_policy.auth_providers_load_failed", {
            defaultValue:
              "Authentication-file providers could not be refreshed: {{message}}. Existing configured providers remain available.",
            message: authProviderError,
          })}
        </div>
      )}
      {snapshot?.status.lastError && (
        <div className={styles.warningBanner}>{snapshot.status.lastError}</div>
      )}

      {!snapshot ? (
        <div className={styles.noticeCard}>
          <IconInfo size={21} />
          <div>
            <strong>
              {loading
                ? t("oauth_policy.loading", {
                    defaultValue: "Loading account policy...",
                  })
                : t("oauth_policy.load_unavailable", {
                    defaultValue: "Account policy is unavailable",
                  })}
            </strong>
            <p>
              {t("oauth_policy.loading_hint", {
                defaultValue:
                  "Reading built-in account policy configuration.",
              })}
            </p>
          </div>
        </div>
      ) : (
          <>
            <section className={styles.statusGrid}>
              <div>
                <span
                  className={
                    snapshot.status.enabled
                      ? styles.statusGood
                      : styles.statusMuted
                  }
                >
                  {snapshot.status.enabled ? (
                    <IconCheckCircle2 size={18} />
                  ) : (
                    <IconAlertTriangle size={18} />
                  )}
                </span>
                <small>
                  {t("oauth_policy.runtime", { defaultValue: "Runtime" })}
                </small>
                <strong>
                  {snapshot.status.enabled
                    ? t("oauth_policy.running", {
                        defaultValue: "Enabled",
                      })
                    : t("oauth_policy.stopped", {
                        defaultValue: "Disabled",
                      })}
                </strong>
              </div>
              <div>
                <span className={styles.statusAccent}>{configuredCount}</span>
                <small>
                  {t("oauth_policy.configured_plans", {
                    defaultValue: "Plan rules",
                  })}
                </small>
                <strong>
                  {t("oauth_policy.configured_count", {
                    defaultValue: "{{count}} configured",
                    count: configuredCount,
                  })}
                </strong>
              </div>
              <div>
                <span className={styles.statusAccent}>{excludedCount}</span>
                <small>
                  {t("oauth_policy.model_patterns", {
                    defaultValue: "Model patterns",
                  })}
                </small>
                <strong>
                  {t("oauth_policy.pattern_count", {
                    defaultValue: "{{count}} exclusions",
                    count: excludedCount,
                  })}
                </strong>
              </div>
              <div>
                <span className={styles.statusMuted}>
                  {configuredProviderCount}
                </span>
                <small>
                  {t("oauth_policy.providers_with_rules", {
                    defaultValue: "Rule providers",
                  })}
                </small>
                <strong>
                  {t("oauth_policy.providers_with_rules_hint", {
                    defaultValue: "Rules enabled",
                  })}
                </strong>
              </div>
            </section>

            <section className={styles.settingsPanel}>
              <div className={styles.sectionHeading}>
                <span>
                  <IconSettings size={19} />
                </span>
                <div>
                  <h2>
                    {t("oauth_policy.discovery_settings", {
                      defaultValue: "Plan discovery",
                    })}
                  </h2>
                  <p>
                    {t("oauth_policy.discovery_hint", {
                      defaultValue:
                        "Auth metadata is preferred; supported provider APIs are queried only when the plan is missing.",
                    })}
                  </p>
                </div>
              </div>
              <div className={styles.settingsGrid}>
                <OAuthDurationInput
                  label={t("oauth_policy.cache_ttl", {
                    defaultValue: "Plan cache TTL",
                  })}
                  value={draft.cacheTTL}
                  unit="m"
                  unitLabel={t("oauth_policy.unit_minutes", {
                    defaultValue: "minutes",
                  })}
                  fallback={30}
                  disabled={saving}
                  onChange={(cacheTTL) => updateDraft({ ...draft, cacheTTL })}
                />
                <OAuthDurationInput
                  label={t("oauth_policy.max_stale", {
                    defaultValue: "Maximum stale age",
                  })}
                  value={draft.maxStale}
                  unit="m"
                  unitLabel={t("oauth_policy.unit_minutes", {
                    defaultValue: "minutes",
                  })}
                  fallback={1440}
                  disabled={saving}
                  onChange={(maxStale) => updateDraft({ ...draft, maxStale })}
                />
                <OAuthDurationInput
                  label={t("oauth_policy.resolve_timeout", {
                    defaultValue: "Provider resolve timeout",
                  })}
                  value={draft.resolveTimeout}
                  unit="s"
                  unitLabel={t("oauth_policy.unit_seconds", {
                    defaultValue: "seconds",
                  })}
                  fallback={15}
                  disabled={saving}
                  onChange={(resolveTimeout) =>
                    updateDraft({ ...draft, resolveTimeout })
                  }
                />
              </div>
            </section>

            <section className={styles.policyPanel}>
              {providerDefinitions.length === 0 ? (
                <div className={styles.emptyProviders}>
                  <IconInfo size={21} />
                  <div>
                    <strong>
                      {t("oauth_policy.no_auth_providers", {
                        defaultValue: "No authentication-file providers",
                      })}
                    </strong>
                    <p>
                      {t("oauth_policy.no_auth_providers_hint", {
                        defaultValue:
                          "Add an authentication file to configure provider rules.",
                      })}
                    </p>
                  </div>
                </div>
              ) : (
                <>
                  <ProFeatureTabs
                    className={styles.providerTabBar}
                    ariaLabel={t("oauth_policy.providers", {
                      defaultValue: "Providers",
                    })}
                    activeKey={resolvedActiveProvider}
                    onChange={(key) => {
                      setActiveProvider(key);
                      setCustomPlan("");
                    }}
                    items={providerDefinitions.map((provider) => {
                      const count = Object.values(
                        draft.providers[provider.key].plans,
                      ).filter(({ configured }) => configured).length;
                      return {
                        key: provider.key,
                        label: t(
                          `oauth_policy.provider_${provider.key.replace(/-/g, "_")}`,
                          { defaultValue: provider.key },
                        ),
                        badge: count > 0 ? count : undefined,
                      };
                    })}
                  />
              <div className={styles.policyHeader}>
                <div>
                  <h2>
                    {t("oauth_policy.provider_rules", {
                      defaultValue: "{{provider}} plan rules",
                      provider: t(
                        `oauth_policy.provider_${resolvedActiveProvider.replace(/-/g, "_")}`,
                        { defaultValue: resolvedActiveProvider },
                      ),
                    })}
                  </h2>
                  <p>
                    {t(
                      `oauth_policy.provider_${resolvedActiveProvider.replace(/-/g, "_")}_hint`,
                      {
                        defaultValue:
                          "Each enabled rule subtracts matching model IDs from that account only.",
                      },
                    )}
                  </p>
                </div>
                <span className={styles.flowBadge}>
                  {t("oauth_policy.processing_order", {
                    defaultValue:
                      "excluded_models → plan policy → alias / prefix",
                  })}
                </span>
              </div>
              <div className={styles.customPlanRow}>
                <div>
                  <strong>
                    {t("oauth_policy.custom_plan", {
                      defaultValue: "Custom plan key",
                    })}
                  </strong>
                  <span>
                    {t("oauth_policy.custom_plan_hint", {
                      defaultValue:
                        "Add a provider plan value observed in auth metadata or a provider API.",
                    })}
                  </span>
                </div>
                <div>
                  <input
                    value={customPlan}
                    disabled={saving}
                    onChange={(event) => setCustomPlan(event.target.value)}
                    onKeyDown={(event) => {
                      if (event.key !== "Enter") return;
                      event.preventDefault();
                      addCustomPlan();
                    }}
                    placeholder={t(
                      "oauth_policy.custom_plan_placeholder",
                      {
                        defaultValue: "e.g. enterprise",
                      },
                    )}
                    aria-label={t("oauth_policy.custom_plan", {
                      defaultValue: "Custom plan key",
                    })}
                  />
                  <Button
                    variant="secondary"
                    size="sm"
                    disabled={saving || !customPlan.trim()}
                    onClick={addCustomPlan}
                  >
                    <IconPlus size={14} />
                    {t("common.add", { defaultValue: "Add" })}
                  </Button>
                </div>
              </div>
              <div className={styles.ruleGrid}>
                {activePlanDefinitions.map((definition) => {
                  const rule = activePlans[definition.key];
                  const inherited = inheritedRule(definition.key);
                  return (
                    <article
                      key={definition.key}
                      className={`${styles.ruleCard} ${
                        rule.configured ? styles.ruleCardActive : ""
                      }`}
                    >
                      <div className={styles.ruleHeader}>
                        <div>
                          <div className={styles.ruleTitleLine}>
                            <h3>
                              {definition.kind === "custom"
                                ? t("oauth_policy.plan_custom", {
                                    defaultValue: "Custom plan",
                                  })
                                : t(
                                    `oauth_policy.plan_${definition.localeSuffix}`,
                                    { defaultValue: definition.key },
                                  )}
                            </h3>
                            <code>{definition.key}</code>
                            {definition.kind === "custom" && (
                              <button
                                type="button"
                                className={styles.removeCustomPlan}
                                disabled={saving}
                                onClick={() => removeCustomPlan(definition.key)}
                                title={t(
                                  "oauth_policy.remove_custom_plan",
                                  {
                                    defaultValue: "Remove custom plan",
                                  },
                                )}
                                aria-label={t(
                                  "oauth_policy.remove_custom_plan_label",
                                  {
                                    defaultValue: "Remove {{plan}}",
                                    plan: definition.key,
                                  },
                                )}
                              >
                                <IconX size={13} />
                              </button>
                            )}
                          </div>
                          <p>
                            {t(
                              `oauth_policy.plan_${definition.localeSuffix}_hint`,
                              {
                                defaultValue:
                                  definition.kind === "fallback"
                                    ? "Fallback policy"
                                    : definition.kind === "custom"
                                      ? "Provider-specific custom plan"
                                      : "Detected OAuth subscription plan",
                              },
                            )}
                          </p>
                          {definition.monthlyLimitCents !== undefined && (
                            <small>
                              {definition.monthlyLimitCents === 0
                                ? t("oauth_policy.no_paid_limit", {
                                    defaultValue: "Free plan",
                                  })
                                : t("oauth_policy.monthly_limit", {
                                    defaultValue:
                                      "{{count}} cents monthly limit",
                                    count: definition.monthlyLimitCents,
                                  })}
                            </small>
                          )}
                        </div>
                        <ToggleSwitch
                          checked={rule.configured}
                          onChange={(configured) =>
                            patchPlan(resolvedActiveProvider, definition.key, {
                              configured,
                            })
                          }
                          ariaLabel={t("oauth_policy.configure_plan", {
                            defaultValue: "Configure {{plan}} rule",
                            plan: definition.key,
                          })}
                        />
                      </div>
                      {rule.configured ? (
                        <>
                          <div className={styles.accountPolicyFields}>
                            <label>
                              <span>{t("oauth_policy.prefix", { defaultValue: "Prefix" })}</span>
                              <input
                                className={styles.patternInput}
                                value={rule.prefix ?? ""}
                                disabled={saving}
                                spellCheck={false}
                                placeholder={t("oauth_policy.prefix_placeholder", { defaultValue: "e.g. grok (empty = inherit)" })}
                                onChange={(event) => patchPlan(resolvedActiveProvider, definition.key, {
                                  prefix: normalizeOAuthPolicyPrefix(event.target.value),
                                })}
                              />
                              <small>{t("oauth_policy.prefix_hint", { defaultValue: "Namespaces this plan's exposed model IDs." })}</small>
                            </label>
                            <label>
                              <span>{t("oauth_policy.priority", { defaultValue: "Priority" })}</span>
                              <input
                                className={styles.patternInput}
                                type="number"
                                value={rule.priority ?? ""}
                                disabled={saving}
                                inputMode="numeric"
                                placeholder={t("oauth_policy.priority_placeholder", { defaultValue: "e.g. 100 (empty = inherit)" })}
                                onChange={(event) => patchPlan(resolvedActiveProvider, definition.key, {
                                  priority: event.target.value === "" ? undefined : Math.trunc(Number(event.target.value)),
                                })}
                              />
                              <small>{t("oauth_policy.priority_hint", { defaultValue: "Higher values form the preferred routing tier." })}</small>
                            </label>
                            <label>
                              <span>{t("oauth_policy.weight", { defaultValue: "Weight" })}</span>
                              <input
                                className={styles.patternInput}
                                type="number"
                                min={0}
                                max={1_000_000}
                                value={rule.weight ?? ""}
                                disabled={saving}
                                inputMode="numeric"
                                placeholder={t("oauth_policy.weight_placeholder", { defaultValue: "e.g. 1 (empty = inherit)" })}
                                onChange={(event) => patchPlan(resolvedActiveProvider, definition.key, {
                                  weight: event.target.value === "" ? undefined : Math.trunc(Number(event.target.value)),
                                })}
                              />
                              <small>{t("oauth_policy.weight_hint", { defaultValue: "Used only by weighted round robin within the same priority." })}</small>
                            </label>
                          </div>
                          <PatternEditor
                            planKey={definition.key}
                            disabled={saving}
                            patterns={rule.excludedModels}
                            onChange={(excludedModels) =>
                              patchPlan(resolvedActiveProvider, definition.key, { excludedModels })
                            }
                          />
                        </>
                      ) : (
                        <div className={styles.inheritedRule}>
                          <IconInfo size={15} />
                          <span>{inherited}</span>
                        </div>
                      )}
                    </article>
                  );
                })}
              </div>
              <div className={styles.behaviorNote}>
                <IconInfo size={18} />
                <p>
                  {t("oauth_policy.empty_rule_behavior", {
                    defaultValue:
                      "An enabled rule with no patterns explicitly allows the full current model set and stops fallback matching.",
                  })}
                </p>
              </div>
                </>
              )}
            </section>
            <section className={styles.effectivePanel}>
              <div className={styles.effectiveHeader}>
                <div className={styles.sectionHeading}>
                  <span><IconCheckCircle2 size={19} /></span>
                  <div>
                    <h2>{t("oauth_policy.effective_title", { defaultValue: "Effective account policies" })}</h2>
                    <p>{t("oauth_policy.effective_hint", { defaultValue: "Runtime-only values resolved from the latest account plan. Authentication files are not modified." })}</p>
                  </div>
                </div>
                <span className={styles.effectiveCount}>
                  {t("oauth_policy.filter_result_count", {
                    defaultValue: "{{visible}} / {{total}} accounts",
                    visible: filteredEffectivePolicies.length,
                    total: snapshot.effective.length,
                  })}
                </span>
              </div>
              {snapshot.effective.length > 0 && (
                <div className={styles.effectiveFilters}>
                  <Select
                    value={effectiveProvider}
                    options={effectiveProviderOptions}
                    onChange={(value) => {
                      setEffectiveProvider(value);
                      setEffectivePlan("all");
                    }}
                    size="sm"
                    ariaLabel={t("oauth_policy.filter_provider", {
                      defaultValue: "Filter by provider",
                    })}
                  />
                  <Select
                    value={effectivePlan}
                    options={effectivePlanOptions}
                    onChange={setEffectivePlan}
                    size="sm"
                    ariaLabel={t("oauth_policy.filter_plan", {
                      defaultValue: "Filter by plan",
                    })}
                  />
                </div>
              )}
              {snapshot.effective.length === 0 ? (
                <div className={styles.inheritedRule}>
                  <IconInfo size={15} />
                  <span>{t("oauth_policy.effective_empty", { defaultValue: "No account has matched a configured policy yet." })}</span>
                </div>
              ) : filteredEffectivePolicies.length === 0 ? (
                <div className={styles.inheritedRule}>
                  <IconInfo size={15} />
                  <span>{t("oauth_policy.filter_empty", { defaultValue: "No account matches the selected filters." })}</span>
                </div>
              ) : (
                <div className={styles.effectiveTableWrap}>
                  <table className={styles.effectiveTable}>
                    <thead><tr>
                      <th>{t("oauth_policy.account", { defaultValue: "Account" })}</th>
                      <th>{t("oauth_policy.provider", { defaultValue: "Provider" })}</th>
                      <th>{t("oauth_policy.plan", { defaultValue: "Plan" })}</th>
                      <th>{t("oauth_policy.matched_rule", { defaultValue: "Matched rule" })}</th>
                      <th>{t("oauth_policy.prefix", { defaultValue: "Prefix" })}</th>
                      <th>{t("oauth_policy.priority", { defaultValue: "Priority" })}</th>
                      <th>{t("oauth_policy.weight", { defaultValue: "Weight" })}</th>
                    </tr></thead>
                    <tbody>{filteredEffectivePolicies.map((item) => (
                      <tr key={item.authId}>
                        <td><code>{item.authId}</code></td><td>{item.provider}</td>
                        <td>
                          {item.planKey}<small>{item.planSource}</small>
                          {item.planError && (
                            <small className={styles.planError} title={item.planError}>
                              {t("oauth_policy.plan_error", {
                                defaultValue: "Detection error: {{message}}",
                                message: item.planError,
                              })}
                            </small>
                          )}
                        </td>
                        <td><code>{item.matchedRule}</code></td>
                        <td>{item.prefix ?? "—"}</td><td>{item.priority ?? "—"}</td><td>{item.weight ?? "—"}</td>
                      </tr>
                    ))}</tbody>
                  </table>
                </div>
              )}
            </section>
          </>
      )}

      {dirty &&
        createPortal(
          <div
            className={configStyles.floatingActionContainer}
            ref={actionBarRef}
          >
            <div className={configStyles.floatingActionList}>
              <div
                className={`${configStyles.floatingStatus} ${configStyles.modified}`}
              >
                {saving
                  ? t("config_management.status_saving_short", {
                      defaultValue: "Saving",
                    })
                  : t("config_management.status_dirty_short", {
                      defaultValue: "Unsaved",
                    })}
              </div>
              <button
                type="button"
                className={configStyles.floatingActionButton}
                onClick={discard}
                disabled={saving}
                title={t("oauth_policy.discard_changes", {
                  defaultValue: "Discard changes",
                })}
                aria-label={t("oauth_policy.discard_changes", {
                  defaultValue: "Discard changes",
                })}
              >
                <IconRefreshCw size={16} />
              </button>
              <button
                type="button"
                className={configStyles.floatingActionButton}
                onClick={() => void save()}
                disabled={saving}
                title={t("common.save")}
                aria-label={t("common.save")}
              >
                <IconCheck size={16} />
                {!saving && (
                  <span className={configStyles.dirtyDot} aria-hidden="true" />
                )}
              </button>
            </div>
          </div>,
          document.body,
        )}
    </div>
  );
}
