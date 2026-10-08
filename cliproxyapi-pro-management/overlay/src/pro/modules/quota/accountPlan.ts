import type { TFunction } from 'i18next';
import { resolveXaiPlanType } from '@/pro/modules/quota/extensions/xaiQuota';
import { useQuotaStore } from '@/stores';
import type { AuthFileItem } from '@/types';
import { normalizeNumberValue, resolveAuthProvider } from '@/utils/quota';

export type AccountPlanQuotaStore = Pick<
  ReturnType<typeof useQuotaStore.getState>,
  'antigravityQuota' | 'claudeQuota' | 'codexQuota' | 'devinQuota' | 'geminiCliQuota' | 'kimiQuota' | 'metaQuota' | 'xaiQuota'
>;

type ResolveAccountPlanLabelOptions = {
  authFile?: AuthFileItem;
  fileName?: string;
  provider?: string;
  fallbackPlan?: unknown;
  quotaStore: AccountPlanQuotaStore;
  t: TFunction;
  emptyLabel?: string;
};

const toPlanRecord = (value: unknown): Record<string, unknown> | null =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : null;

const readPlanValue = (value: unknown): string =>
  typeof value === 'string' ? value.trim() : '';

const readNestedPlanValue = (file: AuthFileItem | undefined, ...keys: string[]): string => {
  if (!file) return '';
  const record = file as Record<string, unknown>;
  const containers = [
    record,
    toPlanRecord(record.metadata),
    toPlanRecord(record.attributes),
    toPlanRecord(record.id_token),
    toPlanRecord(record.idToken),
  ];
  for (const container of containers) {
    if (!container) continue;
    for (const key of keys) {
      const value = readPlanValue(container[key]);
      if (value) return value;
    }
  }
  return '';
};

const translatedPlanLabel = (t: TFunction, key: string, fallback: string) => {
  const translated = String(t(key));
  return translated && translated !== key ? translated : fallback;
};

const formatRawAccountPlanLabel = (value: unknown): string => {
  const raw = readPlanValue(value);
  if (!raw) return '';
  return raw
    .replace(/^plan[_-]/i, '')
    .replace(/[_-]+/g, ' ')
    .replace(/\b\w/g, (character) => character.toUpperCase());
};

const formatCodexPlanLabel = (value: unknown, t: TFunction): string => {
  const normalized = readPlanValue(value).toLowerCase().replace(/_/g, '-');
  const labels: Record<string, string> = {
    pro: translatedPlanLabel(t, 'codex_quota.plan_pro', 'Pro'),
    prolite: translatedPlanLabel(t, 'codex_quota.plan_prolite', 'Pro Lite'),
    'pro-lite': translatedPlanLabel(t, 'codex_quota.plan_prolite', 'Pro Lite'),
    plus: translatedPlanLabel(t, 'codex_quota.plan_plus', 'Plus'),
    team: translatedPlanLabel(t, 'codex_quota.plan_team', 'Team'),
    free: translatedPlanLabel(t, 'codex_quota.plan_free', 'Free'),
  };
  return labels[normalized] ?? formatRawAccountPlanLabel(value);
};

const formatAntigravityPlanLabel = (value: unknown, t: TFunction): string => {
  const subscription = toPlanRecord(value);
  const raw = subscription?.plan ?? subscription?.tierName ?? subscription?.tierId ?? value;
  const normalized = readPlanValue(raw).toLowerCase().replace(/_/g, '-');
  const labels: Record<string, string> = {
    free: translatedPlanLabel(t, 'antigravity_subscription.plan_free', 'Free'),
    pro: translatedPlanLabel(t, 'antigravity_subscription.plan_pro', 'Pro'),
    ultra: translatedPlanLabel(t, 'antigravity_subscription.plan_ultra', 'Ultra'),
    'ultra-lite': translatedPlanLabel(t, 'antigravity_subscription.plan_ultra_lite', 'Ultra Lite'),
  };
  return labels[normalized] ?? formatRawAccountPlanLabel(raw);
};

const formatClaudePlanLabel = (value: unknown, t: TFunction): string => {
  const raw = readPlanValue(value);
  if (!raw) return '';
  return translatedPlanLabel(t, `claude_quota.${raw}`, formatRawAccountPlanLabel(raw));
};

const formatXaiPlanLabel = (billingValue: unknown, fallbackValue: unknown, t: TFunction): string => {
  const billing = toPlanRecord(billingValue);
  const monthlyLimitCents = normalizeNumberValue(billing?.monthlyLimitCents ?? billing?.monthly_limit_cents);
  const storedPlan = readPlanValue(billing?.planType ?? billing?.plan_type);
  const planType = storedPlan || resolveXaiPlanType(monthlyLimitCents, monthlyLimitCents !== null) || readPlanValue(fallbackValue);
  const labels: Record<string, string> = {
    free: 'Free',
    supergrok: translatedPlanLabel(t, 'xai_quota.plan_supergrok', 'SuperGrok'),
    'x-basic': translatedPlanLabel(t, 'xai_quota.plan_x_basic', 'X Basic'),
    'x-premium': translatedPlanLabel(t, 'xai_quota.plan_x_premium', 'X Premium'),
    'x-premium-plus': translatedPlanLabel(t, 'xai_quota.plan_x_premium_plus', 'X Premium+'),
    'supergrok-heavy': translatedPlanLabel(t, 'xai_quota.plan_supergrok_heavy', 'SuperGrok Heavy'),
    'supergrok-lite': translatedPlanLabel(t, 'xai_quota.plan_supergrok_lite', 'SuperGrok Lite'),
    paid: translatedPlanLabel(t, 'xai_quota.plan_paid', 'Paid'),
    'paid-unknown': translatedPlanLabel(t, 'xai_quota.plan_paid_unknown', 'Paid'),
  };
  return labels[planType] ?? formatRawAccountPlanLabel(planType);
};

export const resolveAccountPlanLabel = ({
  authFile,
  fileName = authFile?.name ?? '',
  provider,
  fallbackPlan,
  quotaStore,
  t,
  emptyLabel = '--',
}: ResolveAccountPlanLabelOptions): string => {
  const providerKey = resolveAuthProvider({
    name: fileName,
    provider: provider?.trim() || (authFile ? resolveAuthProvider(authFile) : ''),
  });
  const authFileFallbackPlan = readNestedPlanValue(
    authFile,
    'planType',
    'plan_type',
    'plan',
    'package',
    'tierLabel',
    'tier_label',
    'tierId',
    'tier_id',
    'tier'
  );
  const resolvedFallbackPlan = authFileFallbackPlan || fallbackPlan;

  if (providerKey === 'antigravity') {
    return formatAntigravityPlanLabel(
      quotaStore.antigravityQuota[fileName]?.subscription ??
        (authFile as Record<string, unknown> | undefined)?.subscription ??
        resolvedFallbackPlan,
      t
    ) || emptyLabel;
  }
  if (providerKey === 'claude') {
    return formatClaudePlanLabel(quotaStore.claudeQuota[fileName]?.planType ?? resolvedFallbackPlan, t) || emptyLabel;
  }
  if (providerKey === 'codex') {
    return formatCodexPlanLabel(quotaStore.codexQuota[fileName]?.planType ?? resolvedFallbackPlan, t) || emptyLabel;
  }
  if (providerKey === 'devin') {
    return formatRawAccountPlanLabel(quotaStore.devinQuota[fileName]?.plan ?? resolvedFallbackPlan) || emptyLabel;
  }
  if (providerKey === 'gemini-cli') {
    const quota = quotaStore.geminiCliQuota[fileName];
    return readPlanValue(quota?.tierLabel) || formatRawAccountPlanLabel(quota?.tierId ?? resolvedFallbackPlan) || emptyLabel;
  }
  if (providerKey === 'kimi') {
    const quota = toPlanRecord(quotaStore.kimiQuota[fileName]);
    return formatRawAccountPlanLabel(quota?.planType ?? quota?.tierLabel ?? resolvedFallbackPlan) || emptyLabel;
  }
  if (providerKey === 'meta') {
    return formatRawAccountPlanLabel(quotaStore.metaQuota[fileName]?.data?.planName ?? resolvedFallbackPlan) || emptyLabel;
  }
  if (providerKey === 'xai') {
    return formatXaiPlanLabel(quotaStore.xaiQuota[fileName]?.billing, resolvedFallbackPlan, t) || emptyLabel;
  }
  return formatRawAccountPlanLabel(resolvedFallbackPlan) || emptyLabel;
};
