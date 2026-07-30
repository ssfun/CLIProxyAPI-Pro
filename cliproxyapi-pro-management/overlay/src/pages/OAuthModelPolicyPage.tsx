import {
  useCallback,
  useEffect,
  useId,
  useMemo,
  useRef,
  useState,
  type KeyboardEvent,
} from "react";
import { useTranslation } from "react-i18next";
import { createPortal } from "react-dom";
import { Button } from "@/components/ui/Button";
import { Input } from "@/components/ui/Input";
import { ToggleSwitch } from "@/components/ui/ToggleSwitch";
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
  defaultOAuthModelPolicyConfig,
  isPositiveDuration,
  normalizeOAuthModelPlanKey,
  oauthModelPolicyDurationValue,
  oauthModelPolicyApi,
  OAUTH_MODEL_PROVIDER_DEFINITIONS,
  planDefinitionsForProvider,
  serializeOAuthModelPolicyDuration,
  type OAuthModelPlanKey,
  type OAuthModelPlanRule,
  type OAuthModelPolicyConfig,
  type OAuthModelPolicyDurationUnit,
  type OAuthModelPolicySnapshot,
  type OAuthModelProviderKey,
} from "@/services/api/oauthModelPolicy";
import { useActionBarHeightVar } from "@/hooks/useActionBarHeightVar";
import { useAuthStore, useNotificationStore } from "@/stores";
import configStyles from "@/pages/ConfigPage.module.scss";
import styles from "./OAuthModelPolicyPage.module.scss";

const errorMessage = (error: unknown): string =>
  error instanceof Error ? error.message : String(error || "Unknown error");

const isLikelyValidGlob = (value: string): boolean => {
  let escaped = false;
  for (let index = 0; index < value.length; index += 1) {
    const character = value[index];
    if (escaped) {
      escaped = false;
      continue;
    }
    if (character === "\\") {
      escaped = true;
      continue;
    }
    if (character !== "[") continue;
    let closing = index + 1;
    while (closing < value.length && value[closing] !== "]") closing += 1;
    if (closing >= value.length || closing === index + 1) return false;
    index = closing;
  }
  return !escaped;
};

interface PatternEditorProps {
  planKey: OAuthModelPlanKey;
  disabled: boolean;
  patterns: string[];
  onChange: (patterns: string[]) => void;
}

interface DurationInputProps {
  label: string;
  value: string;
  unit: OAuthModelPolicyDurationUnit;
  unitLabel: string;
  fallback: number;
  disabled?: boolean;
  onChange: (value: string) => void;
}

const formatDurationNumber = (value: number): string =>
  String(Math.round(value * 1000) / 1000);

function DurationInput({
  label,
  value,
  unit,
  unitLabel,
  fallback,
  disabled = false,
  onChange,
}: DurationInputProps) {
  const inputId = useId();
  const numericValue = oauthModelPolicyDurationValue(value, unit) ?? fallback;
  const [text, setText] = useState(() => formatDurationNumber(numericValue));

  useEffect(() => {
    setText(formatDurationNumber(numericValue));
  }, [numericValue]);

  const commit = () => {
    const next = Number(text);
    if (!Number.isFinite(next) || next <= 0) {
      setText(formatDurationNumber(numericValue));
      return;
    }
    const normalized = Math.max(1, Math.round(next));
    setText(formatDurationNumber(normalized));
    if (Math.abs(normalized - numericValue) < 0.000001) return;
    onChange(serializeOAuthModelPolicyDuration(normalized, unit));
  };

  return (
    <div className="form-group">
      <label htmlFor={inputId}>{label}</label>
      <div className={styles.durationControl}>
        <input
          id={inputId}
          className="input"
          type="number"
          min="1"
          step="1"
          inputMode="numeric"
          value={text}
          disabled={disabled}
          onChange={(event) => setText(event.target.value)}
          onBlur={commit}
          onKeyDown={(event) => {
            if (event.key === "Enter") event.currentTarget.blur();
          }}
        />
        <span aria-hidden="true">{unitLabel}</span>
      </div>
    </div>
  );
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
            {t("oauth_model_policy.no_exclusions", {
              defaultValue: "No excluded models",
            })}
          </span>
        ) : (
          patterns.map((pattern) => (
            <span
              key={pattern}
              className={`${styles.patternChip} ${
                isLikelyValidGlob(pattern) ? "" : styles.patternInvalid
              }`}
            >
              <code>{pattern}</code>
              <button
                type="button"
                disabled={disabled}
                onClick={() =>
                  onChange(patterns.filter((item) => item !== pattern))
                }
                aria-label={t("oauth_model_policy.remove_pattern", {
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
          value={value}
          disabled={disabled}
          onChange={(event) => setValue(event.target.value)}
          onKeyDown={handleKeyDown}
          placeholder={t("oauth_model_policy.pattern_placeholder", {
            defaultValue: "e.g. grok-pro-*",
          })}
          aria-label={t("oauth_model_policy.pattern_input", {
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
        {t("oauth_model_policy.pattern_hint", {
          defaultValue:
            "Supports *, ?, and character ranges. Enter or commas add multiple rules.",
        })}
      </p>
    </div>
  );
}

export function OAuthModelPolicyPage() {
  const { t } = useTranslation();
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const supportsPlugin = useAuthStore((state) => state.supportsPlugin);
  const showNotification = useNotificationStore(
    (state) => state.showNotification,
  );
  const [snapshot, setSnapshot] = useState<OAuthModelPolicySnapshot | null>(
    null,
  );
  const [draft, setDraft] = useState<OAuthModelPolicyConfig>(
    defaultOAuthModelPolicyConfig,
  );
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [dirty, setDirty] = useState(false);
  const [loadError, setLoadError] = useState("");
  const [activeProvider, setActiveProvider] =
    useState<OAuthModelProviderKey>("xai");
  const [customPlan, setCustomPlan] = useState("");
  const actionBarRef = useRef<HTMLDivElement>(null);
  useActionBarHeightVar(
    actionBarRef,
    "--oauth-model-policy-action-bar-height",
    dirty,
  );

  const load = useCallback(
    async (replaceDraft = false) => {
      if (connectionStatus !== "connected" || !supportsPlugin) {
        setLoading(false);
        return;
      }
      setLoading(true);
      try {
        const next = await oauthModelPolicyApi.load();
        setSnapshot(next);
        if (!dirty || replaceDraft) setDraft(next.config);
        setLoadError("");
      } catch (error) {
        setLoadError(errorMessage(error));
      } finally {
        setLoading(false);
      }
    },
    [connectionStatus, dirty, supportsPlugin],
  );

  useEffect(() => {
    void load();
  }, [load]);

  const updateDraft = useCallback(
    (
      next:
        | OAuthModelPolicyConfig
        | ((current: OAuthModelPolicyConfig) => OAuthModelPolicyConfig),
    ) => {
      setDraft((current) =>
        typeof next === "function" ? next(current) : next,
      );
      setDirty(true);
    },
    [],
  );

  const patchPlan = (
    provider: OAuthModelProviderKey,
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
    const key = normalizeOAuthModelPlanKey(customPlan, activeProvider);
    if (!key || key.startsWith("_")) return;
    const plans = draft.providers[activeProvider].plans;
    if (plans[key]) {
      showNotification(
        t("oauth_model_policy.plan_exists", {
          defaultValue: "Plan key already exists: {{plan}}",
          plan: key,
        }),
        "warning",
      );
      return;
    }
    patchPlan(activeProvider, key, {
      configured: true,
      excludedModels: [],
    });
    setCustomPlan("");
  };

  const removeCustomPlan = (key: OAuthModelPlanKey) => {
    updateDraft((current) => {
      const plans = { ...current.providers[activeProvider].plans };
      delete plans[key];
      return {
        ...current,
        providers: {
          ...current.providers,
          [activeProvider]: { plans },
        },
      };
    });
  };

  const activeProviderDefinition = OAUTH_MODEL_PROVIDER_DEFINITIONS.find(
    ({ key }) => key === activeProvider,
  )!;
  const activePlans = draft.providers[activeProvider].plans;
  const activePlanDefinitions = planDefinitionsForProvider(
    activeProviderDefinition,
    activePlans,
  );

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

  const inheritedRule = (key: OAuthModelPlanKey): string => {
    const plans = activePlans;
    if (plans[key].configured) return "";
    if (key === "_default")
      return t("oauth_model_policy.no_rule", { defaultValue: "No rule" });
    if (key === "_unknown" && plans._unknown.configured) return "";
    if (!key.startsWith("_") && plans._default.configured)
      return t("oauth_model_policy.inherits_default", {
        defaultValue: "Uses _default",
      });
    return t("oauth_model_policy.no_rule", { defaultValue: "No plugin rule" });
  };

  const validate = (): string => {
    if (!isPositiveDuration(draft.cacheTTL))
      return t("oauth_model_policy.invalid_cache_ttl", {
        defaultValue: "Cache TTL must be a positive Go duration, such as 30m.",
      });
    if (!isPositiveDuration(draft.resolveTimeout))
      return t("oauth_model_policy.invalid_resolve_timeout", {
        defaultValue:
          "Resolve timeout must be a positive Go duration, such as 15s.",
      });
    for (const provider of Object.values(draft.providers)) {
      for (const rule of Object.values(provider.plans)) {
        const invalid = rule.excludedModels.find(
          (pattern) => !isLikelyValidGlob(pattern),
        );
        if (invalid)
          return t("oauth_model_policy.invalid_pattern", {
            defaultValue: "Invalid model pattern: {{pattern}}",
            pattern: invalid,
          });
      }
    }
    return "";
  };

  const save = async () => {
    const validation = validate();
    if (validation) {
      showNotification(validation, "error");
      return;
    }
    setSaving(true);
    try {
      const next = await oauthModelPolicyApi.save(draft);
      setSnapshot(next);
      setDraft(next.config);
      setDirty(false);
      setLoadError("");
      showNotification(
        t("oauth_model_policy.save_success", {
          defaultValue: "OAuth model policy saved",
        }),
        "success",
      );
    } catch (error) {
      showNotification(
        t("oauth_model_policy.save_failed", {
          defaultValue: "Save failed: {{message}}",
          message: errorMessage(error),
        }),
        "error",
      );
    } finally {
      setSaving(false);
    }
  };

  const discard = () => {
    setDraft(snapshot?.config ?? defaultOAuthModelPolicyConfig());
    setDirty(false);
  };

  if (!supportsPlugin) {
    return (
      <div className={styles.page}>
        <div className={styles.noticeCard}>
          <IconAlertTriangle size={22} />
          <div>
            <strong>
              {t("oauth_model_policy.unsupported_title", {
                defaultValue: "Plugin runtime required",
              })}
            </strong>
            <p>
              {t("oauth_model_policy.unsupported_body", {
                defaultValue:
                  "Use a standard Pro release instead of a _no-plugin build.",
              })}
            </p>
          </div>
        </div>
      </div>
    );
  }

  return (
    <div className={`${styles.page} ${dirty ? styles.pageWithSave : ""}`}>
      <header className={styles.header}>
        <div className={styles.headerIdentity}>
          <span
            className={`${styles.headerIcon} ${
              snapshot?.pluginRegistered ? styles.headerIconActive : ""
            }`}
          >
            <IconModelCluster size={22} />
          </span>
          <div>
            <div className={styles.titleLine}>
              <h1>
                {t("oauth_model_policy.title", {
                  defaultValue: "OAuth Model Policy",
                })}
              </h1>
              {snapshot?.pluginVersion && (
                <code>v{snapshot.pluginVersion}</code>
              )}
            </div>
            <p>
              {t("oauth_model_policy.subtitle", {
                defaultValue:
                  "Filter each OAuth account model set by provider and detected plan.",
              })}
            </p>
          </div>
        </div>
        <Button
          variant="secondary"
          size="sm"
          disabled={loading || saving}
          onClick={() => void load()}
        >
          <IconRefreshCw size={15} />
          {t("common.refresh")}
        </Button>
      </header>

      {loadError && <div className={styles.errorBanner}>{loadError}</div>}
      {!loading && snapshot && !snapshot.pluginDiscovered && (
        <div className={styles.errorBanner}>
          {t("oauth_model_policy.plugin_missing", {
            defaultValue: "Bundled oauth-model-policy plugin was not found.",
          })}
        </div>
      )}
      {!loading && snapshot?.pluginDiscovered && !snapshot.pluginRegistered && (
        <div className={styles.warningBanner}>
          {t("oauth_model_policy.plugin_not_registered", {
            defaultValue:
              "The plugin is installed but not running. Saving valid settings will enable it.",
          })}
        </div>
      )}

      {!snapshot ? (
        <div className={styles.noticeCard}>
          <IconInfo size={21} />
          <div>
            <strong>
              {loading
                ? t("oauth_model_policy.loading", {
                    defaultValue: "Loading model policy...",
                  })
                : t("oauth_model_policy.load_unavailable", {
                    defaultValue: "Model policy is unavailable",
                  })}
            </strong>
            <p>
              {t("oauth_model_policy.loading_hint", {
                defaultValue:
                  "Reading plugin discovery state and configuration.",
              })}
            </p>
          </div>
        </div>
      ) : (
        snapshot.pluginDiscovered && (
          <>
            <section className={styles.statusGrid}>
              <div>
                <span
                  className={
                    snapshot.pluginRegistered
                      ? styles.statusGood
                      : styles.statusMuted
                  }
                >
                  {snapshot.pluginRegistered ? (
                    <IconCheckCircle2 size={18} />
                  ) : (
                    <IconAlertTriangle size={18} />
                  )}
                </span>
                <small>
                  {t("oauth_model_policy.runtime", { defaultValue: "Runtime" })}
                </small>
                <strong>
                  {snapshot.pluginRegistered
                    ? t("oauth_model_policy.running", {
                        defaultValue: "Running",
                      })
                    : t("oauth_model_policy.stopped", {
                        defaultValue: "Not running",
                      })}
                </strong>
              </div>
              <div>
                <span className={styles.statusAccent}>{configuredCount}</span>
                <small>
                  {t("oauth_model_policy.configured_plans", {
                    defaultValue: "Plan rules",
                  })}
                </small>
                <strong>
                  {t("oauth_model_policy.configured_count", {
                    defaultValue: "{{count}} configured",
                    count: configuredCount,
                  })}
                </strong>
              </div>
              <div>
                <span className={styles.statusAccent}>{excludedCount}</span>
                <small>
                  {t("oauth_model_policy.model_patterns", {
                    defaultValue: "Model patterns",
                  })}
                </small>
                <strong>
                  {t("oauth_model_policy.pattern_count", {
                    defaultValue: "{{count}} exclusions",
                    count: excludedCount,
                  })}
                </strong>
              </div>
              <div>
                <span className={styles.statusMuted}>
                  {OAUTH_MODEL_PROVIDER_DEFINITIONS.length}
                </span>
                <small>
                  {t("oauth_model_policy.providers", {
                    defaultValue: "Providers",
                  })}
                </small>
                <strong>
                  {t("oauth_model_policy.oauth_accounts", {
                    defaultValue: "OAuth accounts",
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
                    {t("oauth_model_policy.discovery_settings", {
                      defaultValue: "Plan discovery",
                    })}
                  </h2>
                  <p>
                    {t("oauth_model_policy.discovery_hint", {
                      defaultValue:
                        "Auth metadata is preferred; supported provider APIs are queried only when the plan is missing.",
                    })}
                  </p>
                </div>
              </div>
              <div className={styles.settingsGrid}>
                <DurationInput
                  label={t("oauth_model_policy.cache_ttl", {
                    defaultValue: "Plan cache TTL",
                  })}
                  value={draft.cacheTTL}
                  unit="m"
                  unitLabel={t("oauth_model_policy.unit_minutes", {
                    defaultValue: "minutes",
                  })}
                  fallback={30}
                  disabled={saving}
                  onChange={(cacheTTL) => updateDraft({ ...draft, cacheTTL })}
                />
                <DurationInput
                  label={t("oauth_model_policy.resolve_timeout", {
                    defaultValue: "Provider resolve timeout",
                  })}
                  value={draft.resolveTimeout}
                  unit="s"
                  unitLabel={t("oauth_model_policy.unit_seconds", {
                    defaultValue: "seconds",
                  })}
                  fallback={15}
                  disabled={saving}
                  onChange={(resolveTimeout) =>
                    updateDraft({ ...draft, resolveTimeout })
                  }
                />
                <Input
                  type="number"
                  label={t("oauth_model_policy.priority", {
                    defaultValue: "Plugin priority",
                  })}
                  value={draft.priority}
                  onChange={(event) =>
                    updateDraft({
                      ...draft,
                      priority: Number(event.target.value) || 0,
                    })
                  }
                />
              </div>
            </section>

            <section className={styles.policyPanel}>
              <div
                className={styles.providerTabs}
                role="tablist"
                aria-label={t("oauth_model_policy.providers", {
                  defaultValue: "Providers",
                })}
              >
                {OAUTH_MODEL_PROVIDER_DEFINITIONS.map((provider) => {
                  const count = Object.values(
                    draft.providers[provider.key].plans,
                  ).filter(({ configured }) => configured).length;
                  return (
                    <button
                      key={provider.key}
                      type="button"
                      role="tab"
                      aria-selected={provider.key === activeProvider}
                      className={
                        provider.key === activeProvider
                          ? styles.providerTabActive
                          : ""
                      }
                      onClick={() => {
                        setActiveProvider(provider.key);
                        setCustomPlan("");
                      }}
                    >
                      <span>
                        {t(
                          `oauth_model_policy.provider_${provider.key.replace(/-/g, "_")}`,
                          { defaultValue: provider.key },
                        )}
                      </span>
                      {count > 0 && <small>{count}</small>}
                    </button>
                  );
                })}
              </div>
              <div className={styles.policyHeader}>
                <div>
                  <h2>
                    {t("oauth_model_policy.provider_rules", {
                      defaultValue: "{{provider}} plan rules",
                      provider: t(
                        `oauth_model_policy.provider_${activeProvider.replace(/-/g, "_")}`,
                        { defaultValue: activeProvider },
                      ),
                    })}
                  </h2>
                  <p>
                    {t(
                      `oauth_model_policy.provider_${activeProvider.replace(/-/g, "_")}_hint`,
                      {
                        defaultValue:
                          "Each enabled rule subtracts matching model IDs from that account only.",
                      },
                    )}
                  </p>
                </div>
                <span className={styles.flowBadge}>
                  {t("oauth_model_policy.processing_order", {
                    defaultValue:
                      "excluded_models → plan policy → alias / prefix",
                  })}
                </span>
              </div>
              <div className={styles.customPlanRow}>
                <div>
                  <strong>
                    {t("oauth_model_policy.custom_plan", {
                      defaultValue: "Custom plan key",
                    })}
                  </strong>
                  <span>
                    {t("oauth_model_policy.custom_plan_hint", {
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
                      "oauth_model_policy.custom_plan_placeholder",
                      {
                        defaultValue: "e.g. enterprise",
                      },
                    )}
                    aria-label={t("oauth_model_policy.custom_plan", {
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
                                ? t("oauth_model_policy.plan_custom", {
                                    defaultValue: "Custom plan",
                                  })
                                : t(
                                    `oauth_model_policy.plan_${definition.localeSuffix}`,
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
                                  "oauth_model_policy.remove_custom_plan",
                                  {
                                    defaultValue: "Remove custom plan",
                                  },
                                )}
                                aria-label={t(
                                  "oauth_model_policy.remove_custom_plan_label",
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
                              `oauth_model_policy.plan_${definition.localeSuffix}_hint`,
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
                                ? t("oauth_model_policy.no_paid_limit", {
                                    defaultValue: "Free plan",
                                  })
                                : t("oauth_model_policy.monthly_limit", {
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
                            patchPlan(activeProvider, definition.key, {
                              configured,
                            })
                          }
                          ariaLabel={t("oauth_model_policy.configure_plan", {
                            defaultValue: "Configure {{plan}} rule",
                            plan: definition.key,
                          })}
                        />
                      </div>
                      {rule.configured ? (
                        <PatternEditor
                          planKey={definition.key}
                          disabled={saving}
                          patterns={rule.excludedModels}
                          onChange={(excludedModels) =>
                            patchPlan(activeProvider, definition.key, {
                              excludedModels,
                            })
                          }
                        />
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
                  {t("oauth_model_policy.empty_rule_behavior", {
                    defaultValue:
                      "An enabled rule with no patterns explicitly allows the full current model set and stops fallback matching.",
                  })}
                </p>
              </div>
            </section>
          </>
        )
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
                title={t("oauth_model_policy.discard", {
                  defaultValue: "Discard changes",
                })}
                aria-label={t("oauth_model_policy.discard", {
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
