import type { ReactNode } from 'react';
import type { TFunction } from 'i18next';
import {
  ANTIGRAVITY_CONFIG,
  CLAUDE_CONFIG,
  CODEX_CONFIG,
  KIMI_CONFIG,
  XAI_CONFIG,
  type QuotaConfig,
  type QuotaStore,
} from '@/components/quota/quotaConfigs';
import type { QuotaRenderHelpers, QuotaStatusState } from '@/components/quota/QuotaCard';
import type { MonitoringEventRow } from './hooks/useMonitoringData';
import type { AuthFileItem } from '@/types';
import { getStatusFromError, isAntigravityFile, isClaudeFile, isCodexFile, isKimiFile, isXaiFile } from '@/utils/quota';
import { normalizeAuthIndex } from '@/utils/usage';

export type AnyQuotaConfig = {
  type: QuotaConfig<QuotaStatusState, unknown>['type'];
  i18nPrefix: string;
  fetchQuota: (file: AuthFileItem, t: TFunction) => Promise<unknown>;
  storeSelector: (state: QuotaStore) => Record<string, QuotaStatusState>;
  storeSetter: keyof QuotaStore;
  buildLoadingState: () => QuotaStatusState;
  buildSuccessState: (data: unknown) => QuotaStatusState;
  buildErrorState: (message: string, status?: number) => QuotaStatusState;
  renderQuotaItems: (quota: QuotaStatusState, t: TFunction, helpers: QuotaRenderHelpers) => ReactNode;
};

const adaptQuotaConfig = <TState extends QuotaStatusState, TData>(
  config: QuotaConfig<TState, TData>
): AnyQuotaConfig => ({
  type: config.type,
  i18nPrefix: config.i18nPrefix,
  fetchQuota: config.fetchQuota,
  storeSelector: config.storeSelector,
  storeSetter: config.storeSetter,
  buildLoadingState: config.buildLoadingState,
  buildSuccessState: (data) => config.buildSuccessState(data as TData),
  buildErrorState: config.buildErrorState,
  renderQuotaItems: (quota, t, helpers) => config.renderQuotaItems(quota as TState, t, helpers),
});

const ACCOUNT_ANTIGRAVITY_QUOTA_CONFIG = adaptQuotaConfig(ANTIGRAVITY_CONFIG);
const ACCOUNT_CLAUDE_QUOTA_CONFIG = adaptQuotaConfig(CLAUDE_CONFIG);
const ACCOUNT_CODEX_QUOTA_CONFIG = adaptQuotaConfig(CODEX_CONFIG);
const ACCOUNT_KIMI_QUOTA_CONFIG = adaptQuotaConfig(KIMI_CONFIG);
const ACCOUNT_XAI_QUOTA_CONFIG = adaptQuotaConfig(XAI_CONFIG);

export type AccountQuotaTarget = {
  key: string;
  authIndex: string;
  authLabel: string;
  fileName: string;
  file: AuthFileItem;
  config: AnyQuotaConfig;
};

export type AccountQuotaEntry = {
  key: string;
  authLabel: string;
  fileName: string;
  providerLabel: string;
  quota?: QuotaStatusState;
  config: AnyQuotaConfig;
};

export type AccountQuotaState = {
  status: 'idle' | 'loading' | 'success' | 'error';
  targetKey: string;
  error?: string;
  lastRefreshedAt?: number;
};

export type AccountQuotaSourceRow = Pick<MonitoringEventRow, 'authIndex' | 'account' | 'authLabel'>;

export const getQuotaProviderLabel = (config: AnyQuotaConfig, t: TFunction) => {
  const titleKey = `${config.i18nPrefix}.title`;
  const translated = t(titleKey);
  if (translated !== titleKey) return translated;
  return config.type;
};

export const getAccountQuotaConfig = (file: AuthFileItem): AnyQuotaConfig | undefined => {
  if (isAntigravityFile(file)) return ACCOUNT_ANTIGRAVITY_QUOTA_CONFIG;
  if (isClaudeFile(file)) return ACCOUNT_CLAUDE_QUOTA_CONFIG;
  if (isCodexFile(file)) return ACCOUNT_CODEX_QUOTA_CONFIG;
  if (isKimiFile(file)) return ACCOUNT_KIMI_QUOTA_CONFIG;
  if (isXaiFile(file)) return ACCOUNT_XAI_QUOTA_CONFIG;
  return undefined;
};

export const resolveQuotaErrorMessage = (t: TFunction, quota?: QuotaStatusState): string => {
  if (!quota) return t('common.unknown_error');
  if (quota.errorStatus === 404) return t('common.quota_update_required');
  if (quota.errorStatus === 403) return t('common.quota_check_credential');
  return quota.error || t('common.unknown_error');
};

export const hasUsableQuotaContent = (quota?: QuotaStatusState) => {
  if (!quota || quota.status !== 'success') return false;
  const record = quota as unknown as Record<string, unknown>;
  const billing = record.billing;
  return ['groups', 'windows', 'buckets', 'rows'].some((key) => {
    const value = record[key];
    return Array.isArray(value) && value.length > 0;
  }) || Boolean(
    record.planType
    || record.tierLabel
    || record.creditBalance !== undefined
    || (billing && typeof billing === 'object' && !Array.isArray(billing))
  );
};

export const getQuotaForTarget = (store: QuotaStore, target: AccountQuotaTarget): QuotaStatusState | undefined => {
  return target.config.storeSelector(store)[target.fileName] as QuotaStatusState | undefined;
};

export const requestAccountQuota = async (
  target: AccountQuotaTarget,
  t: TFunction
): Promise<QuotaStatusState> => {
  try {
    const data = await target.config.fetchQuota(target.file, t);
    return target.config.buildSuccessState(data) as QuotaStatusState;
  } catch (err: unknown) {
    const message = err instanceof Error ? err.message : t('common.unknown_error');
    return target.config.buildErrorState(message, getStatusFromError(err)) as QuotaStatusState;
  }
};

export const settleWithConcurrency = async <T, R>(
  items: T[],
  concurrency: number,
  worker: (item: T) => Promise<R>
): Promise<Array<PromiseSettledResult<R>>> => {
  const results = new Array<PromiseSettledResult<R>>(items.length);
  let nextIndex = 0;
  const workerCount = Math.min(Math.max(concurrency, 1), items.length);
  await Promise.all(Array.from({ length: workerCount }, async () => {
    while (nextIndex < items.length) {
      const index = nextIndex;
      nextIndex += 1;
      try {
        results[index] = { status: 'fulfilled', value: await worker(items[index]) };
      } catch (reason) {
        results[index] = { status: 'rejected', reason };
      }
    }
  }));
  return results;
};

export const buildAccountQuotaTargetsByAccount = (
  rows: AccountQuotaSourceRow[],
  authFilesByAuthIndex: Map<string, AuthFileItem>
) => {
  const grouped = new Map<string, Map<string, AccountQuotaTarget>>();

  rows.forEach((row) => {
    const authIndex = normalizeAuthIndex(row.authIndex);
    if (!authIndex || !row.account) return;

    const file = authFilesByAuthIndex.get(authIndex);
    if (!file) return;

    const quotaConfig = getAccountQuotaConfig(file);
    if (!quotaConfig) return;

    const dedupeKey = `${quotaConfig.type}::${authIndex}::${file.name}`;
    const bucket = grouped.get(row.account) ?? new Map<string, AccountQuotaTarget>();
    if (!bucket.has(dedupeKey)) {
      bucket.set(dedupeKey, {
        key: dedupeKey,
        authIndex,
        authLabel: row.authLabel || file.name || authIndex,
        fileName: file.name,
        file,
        config: quotaConfig,
      });
    }
    grouped.set(row.account, bucket);
  });

  return new Map(
    Array.from(grouped.entries()).map(([account, bucket]) => [
      account,
      Array.from(bucket.values()).sort((left, right) => left.authLabel.localeCompare(right.authLabel)),
    ])
  );
};

export const buildAccountQuotaEntriesByAccount = (
  targetsByAccount: Map<string, AccountQuotaTarget[]>,
  quotaStore: QuotaStore,
  t: TFunction
) => new Map(
  Array.from(targetsByAccount.entries()).map(([account, targets]) => [
    account,
    targets.map((target) => ({
      key: target.key,
      authLabel: target.authLabel,
      fileName: target.fileName,
      providerLabel: getQuotaProviderLabel(target.config, t),
      quota: getQuotaForTarget(quotaStore, target),
      config: target.config,
    } satisfies AccountQuotaEntry)),
  ])
);
