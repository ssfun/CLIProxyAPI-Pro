import React from 'react';
import type { ReactNode } from 'react';
import type { TFunction } from 'i18next';
import type {
  ApiError,
  AuthFileItem,
  GeminiCliCodeAssistPayload,
  GeminiCliCredits,
  GeminiCliParsedBucket,
  GeminiCliQuotaBucket,
  GeminiCliQuotaBucketState,
  GeminiCliQuotaPayload,
  GeminiCliQuotaState,
  GeminiCliUserTier,
} from '@/types';
import { apiCallApi, getApiCallErrorMessage } from '@/services/api';
import { apiClient } from '@/services/api/client';
import { useQuotaStore } from '@/stores';
import {
  createStatusError,
  formatQuotaResetTime,
  isDisabledAuthFile,
  isGeminiCliFile,
  isRuntimeOnlyAuthFile,
  normalizeNumberValue,
  normalizeQuotaFraction,
  normalizeStringValue,
  resolveGeminiCliProjectId,
} from '@/utils/quota';
import { normalizeAuthIndex } from '@/utils/authIndex';
import type { QuotaConfig } from '@/components/quota/quotaConfigs';
import type { QuotaRenderHelpers } from '@/components/quota/QuotaCard';
import styles from '@/pages/QuotaPage.module.scss';
import { resolveGeminiCliTierDisplayLabel } from './geminiCliTierLabels';

const GEMINI_CLI_QUOTA_URL = 'https://cloudcode-pa.googleapis.com/v1internal:retrieveUserQuota';
const GEMINI_CLI_CODE_ASSIST_URL =
  'https://cloudcode-pa.googleapis.com/v1internal:loadCodeAssist';
const GEMINI_CLI_REQUEST_HEADERS = {
  Authorization: 'Bearer $TOKEN$',
  'Content-Type': 'application/json',
};
const GEMINI_CLI_G1_CREDIT_TYPE = 'GOOGLE_ONE_AI';
const QUOTA_PROGRESS_HIGH_THRESHOLD = 70;
const QUOTA_PROGRESS_MEDIUM_THRESHOLD = 30;
const geminiCliSupplementaryRequestIds = new Map<string, number>();
const geminiCliSupplementaryCache = new Map<
  string,
  { requestId: number; tierLabel: string | null; tierId: string | null; creditBalance: number | null }
>();

type GeminiCliQuotaData = {
  fileName: string;
  supplementaryRequestId: number;
  buckets: GeminiCliQuotaBucketState[];
  projectId: string;
  tierLabel: string | null;
  tierId: string | null;
  creditBalance: number | null;
  pluginSnapshot: boolean;
};

type GeminiCliQuotaGroupDefinition = {
  id: string;
  label: string;
};

const resolveGeminiCliTierDisplay = (
  tierId: unknown,
  upstreamLabel: unknown,
  t: TFunction
): string | null =>
  resolveGeminiCliTierDisplayLabel(tierId, upstreamLabel, (labelKey) =>
    t(`gemini_cli_quota.${labelKey}`)
  );

type PluginQuotaItem = {
  id: string;
  label: string;
  remaining_fraction?: number;
  remaining_amount?: number;
  reset_at?: string;
  model_ids?: string[];
  metadata?: Record<string, unknown>;
};

type PluginQuotaSnapshot = {
  schema_version: number;
  observed_at_ms: number;
  items: PluginQuotaItem[];
  plan?: {
    id?: string;
    label?: string;
    credit_balance?: number;
  };
  metadata?: Record<string, unknown>;
};

type PluginQuotaFetchResponse = {
  snapshot?: PluginQuotaSnapshot;
};

const GEMINI_CLI_QUOTA_GROUPS: GeminiCliQuotaGroupDefinition[] = [
  {
    id: 'gemini-flash-lite-series',
    label: 'Gemini Flash Lite Series',
  },
  {
    id: 'gemini-flash-series',
    label: 'Gemini Flash Series',
  },
  {
    id: 'gemini-pro-series',
    label: 'Gemini Pro Series',
  },
];

const GEMINI_CLI_GROUP_ORDER = new Map(
  GEMINI_CLI_QUOTA_GROUPS.map((group, index) => [group.id, index] as const)
);

const resolveGeminiCliQuotaGroup = (
  modelId: string
): GeminiCliQuotaGroupDefinition | undefined => {
  const normalized = modelId.toLowerCase();
  if (!normalized.startsWith('gemini-')) return undefined;
  if (normalized.includes('-flash-lite')) return GEMINI_CLI_QUOTA_GROUPS[0];
  if (normalized.includes('-flash')) return GEMINI_CLI_QUOTA_GROUPS[1];
  if (normalized.includes('-pro')) return GEMINI_CLI_QUOTA_GROUPS[2];
  return undefined;
};

const PREMIUM_GEMINI_CLI_TIER_IDS = new Set(['g1-pro-tier', 'g1-ultra-tier']);

const parseGeminiCliQuotaPayload = (payload: unknown): GeminiCliQuotaPayload | null => {
  if (payload === undefined || payload === null) return null;
  if (typeof payload === 'string') {
    const trimmed = payload.trim();
    if (!trimmed) return null;
    try {
      return JSON.parse(trimmed) as GeminiCliQuotaPayload;
    } catch {
      return null;
    }
  }
  return typeof payload === 'object' ? (payload as GeminiCliQuotaPayload) : null;
};

const parseGeminiCliCodeAssistPayload = (
  payload: unknown
): GeminiCliCodeAssistPayload | null => {
  if (payload === undefined || payload === null) return null;
  if (typeof payload === 'string') {
    const trimmed = payload.trim();
    if (!trimmed) return null;
    try {
      return JSON.parse(trimmed) as GeminiCliCodeAssistPayload;
    } catch {
      return null;
    }
  }
  if (typeof payload !== 'object') return null;

  const record = payload as Record<string, unknown>;
  if (
    'currentTier' in record ||
    'current_tier' in record ||
    'paidTier' in record ||
    'paid_tier' in record
  ) {
    return payload as GeminiCliCodeAssistPayload;
  }

  for (const key of ['body', 'bodyText', 'data', 'response', 'result']) {
    const nested = parseGeminiCliCodeAssistPayload(record[key]);
    if (nested) return nested;
  }

  return payload as GeminiCliCodeAssistPayload;
};

const normalizeGeminiCliModelId = (value: unknown): string | null => {
  const modelId = normalizeStringValue(value);
  if (!modelId) return null;
  return modelId.endsWith('_vertex') ? modelId.slice(0, -'_vertex'.length) : modelId;
};

const isIgnoredGeminiCliModel = (modelId: string): boolean =>
  modelId === 'gemini-2.0-flash' || modelId.startsWith('gemini-2.0-flash-');

const pickEarlierResetTime = (current?: string, next?: string): string | undefined => {
  if (!current) return next;
  if (!next) return current;
  const currentTime = new Date(current).getTime();
  const nextTime = new Date(next).getTime();
  if (Number.isNaN(currentTime)) return next;
  if (Number.isNaN(nextTime)) return current;
  return currentTime <= nextTime ? current : next;
};

const minNullableNumber = (current: number | null, next: number | null): number | null => {
  if (current === null) return next;
  if (next === null) return current;
  return Math.min(current, next);
};

export const buildGeminiCliQuotaBuckets = (
  buckets: GeminiCliParsedBucket[]
): GeminiCliQuotaBucketState[] => {
  if (buckets.length === 0) return [];

  type BucketGroup = {
    id: string;
    label: string;
    tokenType: string | null;
    modelIds: string[];
    remainingFraction: number | null;
    remainingAmount: number | null;
    resetTime: string | undefined;
  };

  const grouped = new Map<string, BucketGroup>();

  buckets.forEach((bucket) => {
    if (isIgnoredGeminiCliModel(bucket.modelId)) return;
    const group = resolveGeminiCliQuotaGroup(bucket.modelId);
    const groupId = group?.id ?? bucket.modelId;
    const label = group?.label ?? bucket.modelId;
    const tokenKey = bucket.tokenType ?? '';
    const mapKey = `${groupId}::${tokenKey}`;
    const existing = grouped.get(mapKey);

    if (!existing) {
      grouped.set(mapKey, {
        id: `${groupId}${tokenKey ? `-${tokenKey}` : ''}`,
        label,
        tokenType: bucket.tokenType,
        modelIds: [bucket.modelId],
        remainingFraction: bucket.remainingFraction,
        remainingAmount: bucket.remainingAmount,
        resetTime: bucket.resetTime,
      });
      return;
    }

    existing.remainingFraction = minNullableNumber(
      existing.remainingFraction,
      bucket.remainingFraction
    );
    existing.remainingAmount = minNullableNumber(
      existing.remainingAmount,
      bucket.remainingAmount
    );
    existing.resetTime = pickEarlierResetTime(existing.resetTime, bucket.resetTime);
    existing.modelIds.push(bucket.modelId);
  });

  const toGroupOrder = (bucket: BucketGroup): number => {
    const tokenSuffix = bucket.tokenType ? `-${bucket.tokenType}` : '';
    const groupId = bucket.id.endsWith(tokenSuffix)
      ? bucket.id.slice(0, bucket.id.length - tokenSuffix.length)
      : bucket.id;
    return GEMINI_CLI_GROUP_ORDER.get(groupId) ?? Number.MAX_SAFE_INTEGER;
  };

  return Array.from(grouped.values())
    .sort((a, b) => {
      const orderDiff = toGroupOrder(a) - toGroupOrder(b);
      if (orderDiff !== 0) return orderDiff;
      return (a.tokenType ?? '').localeCompare(b.tokenType ?? '');
    })
    .map((bucket) => ({
      id: bucket.id,
      label: bucket.label,
      remainingFraction: bucket.remainingFraction,
      remainingAmount: bucket.remainingAmount,
      resetTime: bucket.resetTime,
      tokenType: bucket.tokenType,
      modelIds: Array.from(new Set(bucket.modelIds)),
    }));
};

const resolveGeminiCliRemainingFraction = (bucket: GeminiCliQuotaBucket): number | null => {
  const normalized = normalizeQuotaFraction(bucket.remainingFraction ?? bucket.remaining_fraction);
  if (normalized !== null) return normalized;
  const amount = normalizeNumberValue(bucket.remainingAmount ?? bucket.remaining_amount);
  if (amount !== null && amount <= 0) return 0;
  if (bucket.resetTime || bucket.reset_time) return 0;
  return null;
};

const emptyGeminiCliSupplementary = (): Pick<
  GeminiCliQuotaData,
  'tierLabel' | 'tierId' | 'creditBalance'
> => ({ tierLabel: null, tierId: null, creditBalance: null });

const buildGeminiCliCodeAssistRequestBody = (projectId: string) =>
  JSON.stringify({
    cloudaicompanionProject: projectId,
    metadata: {
      ideType: 'IDE_UNSPECIFIED',
      platform: 'PLATFORM_UNSPECIFIED',
      pluginType: 'GEMINI',
      duetProject: projectId,
    },
  });

const fetchGeminiCliCodeAssistOnce = async (
  authIndex: string,
  projectId: string,
  t: TFunction,
  useExecutor: boolean
): Promise<Pick<GeminiCliQuotaData, 'tierLabel' | 'tierId' | 'creditBalance'>> => {
  const result = await apiCallApi.request({
    authIndex,
    method: 'POST',
    url: GEMINI_CLI_CODE_ASSIST_URL,
    header: useExecutor
      ? { 'Content-Type': 'application/json' }
      : { ...GEMINI_CLI_REQUEST_HEADERS },
    data: buildGeminiCliCodeAssistRequestBody(projectId),
    ...(useExecutor ? { useExecutor: true } : {}),
  });

  if (result.statusCode < 200 || result.statusCode >= 300) {
    return emptyGeminiCliSupplementary();
  }

  const payload = parseGeminiCliCodeAssistPayload(result.body ?? result.bodyText);
  return {
    tierLabel: resolveGeminiCliTierLabel(payload, t),
    tierId: resolveGeminiCliTierId(payload),
    creditBalance: resolveGeminiCliCreditBalance(payload),
  };
};

const hasGeminiCliSupplementaryData = (
  data: Pick<GeminiCliQuotaData, 'tierLabel' | 'tierId' | 'creditBalance'>
): boolean => data.tierLabel !== null || data.tierId !== null || data.creditBalance !== null;

const fetchGeminiCliCodeAssist = async (
  authIndex: string,
  projectId: string,
  t: TFunction
): Promise<Pick<GeminiCliQuotaData, 'tierLabel' | 'tierId' | 'creditBalance'>> => {
  const executorResult = await fetchGeminiCliCodeAssistOnce(authIndex, projectId, t, true).catch(
    emptyGeminiCliSupplementary
  );
  if (hasGeminiCliSupplementaryData(executorResult)) {
    return executorResult;
  }

  return fetchGeminiCliCodeAssistOnce(authIndex, projectId, t, false).catch(
    emptyGeminiCliSupplementary
  );
};

const readGeminiCliSupplementarySnapshot = (
  fileName: string,
  requestId: number
): Pick<GeminiCliQuotaData, 'tierLabel' | 'tierId' | 'creditBalance'> => {
  const cached = geminiCliSupplementaryCache.get(fileName);
  if (!cached || cached.requestId !== requestId) {
    return { tierLabel: null, tierId: null, creditBalance: null };
  }

  return {
    tierLabel: cached.tierLabel,
    tierId: cached.tierId,
    creditBalance: cached.creditBalance,
  };
};

const scheduleGeminiCliSupplementaryRefresh = (
  fileName: string,
  authIndex: string,
  projectId: string,
  t: TFunction
): number => {
  const requestId = (geminiCliSupplementaryRequestIds.get(fileName) ?? 0) + 1;
  geminiCliSupplementaryRequestIds.set(fileName, requestId);
  geminiCliSupplementaryCache.delete(fileName);

  void (async () => {
    const supplementary = await fetchGeminiCliCodeAssist(authIndex, projectId, t).catch(() => ({
      tierLabel: null,
      tierId: null,
      creditBalance: null,
    }));

    if (geminiCliSupplementaryRequestIds.get(fileName) !== requestId) return;

    geminiCliSupplementaryCache.set(fileName, { requestId, ...supplementary });

    useQuotaStore.getState().setGeminiCliQuota((prev) => {
      const current = prev[fileName];
      if (!current || current.status !== 'success') return prev;
      if (
        current.tierLabel === supplementary.tierLabel &&
        current.tierId === supplementary.tierId &&
        current.creditBalance === supplementary.creditBalance
      ) {
        return prev;
      }

      return {
        ...prev,
        [fileName]: {
          ...current,
          tierLabel: supplementary.tierLabel,
          tierId: supplementary.tierId,
          creditBalance: supplementary.creditBalance,
          cachedAt: Date.now(),
        },
      };
    });
  })();

  return requestId;
};

const fetchGeminiCliQuota = async (
  file: AuthFileItem,
  t: TFunction
): Promise<GeminiCliQuotaData> => {
  const authIndex = normalizeAuthIndex(file['auth_index'] ?? file.authIndex);
  if (!authIndex) {
    throw new Error(t('gemini_cli_quota.missing_auth_index'));
  }

  try {
    const response = await apiClient.post<PluginQuotaFetchResponse>('/quota/fetch', {
      auth_index: authIndex,
    });
    const snapshot = response.snapshot;
    if (!snapshot || !Array.isArray(snapshot.items) || snapshot.items.length === 0) {
      throw new Error(t('gemini_cli_quota.empty_buckets'));
    }
    const buckets: GeminiCliQuotaBucketState[] = snapshot.items.map((item) => ({
      id: item.id,
      label: item.label,
      remainingFraction: normalizeQuotaFraction(item.remaining_fraction),
      remainingAmount: normalizeNumberValue(item.remaining_amount),
      resetTime: normalizeStringValue(item.reset_at) ?? undefined,
      tokenType: normalizeStringValue(item.metadata?.token_type),
      modelIds: Array.isArray(item.model_ids) ? item.model_ids : [],
    }));
    const tierId = normalizeStringValue(snapshot.plan?.id);
    const tierLabel = resolveGeminiCliTierDisplay(tierId, snapshot.plan?.label, t);
    return {
      fileName: file.name,
      supplementaryRequestId: 0,
      buckets,
      projectId:
        normalizeStringValue(snapshot.metadata?.project_id) ?? resolveGeminiCliProjectId(file) ?? '',
      tierLabel,
      tierId,
      creditBalance: normalizeNumberValue(snapshot.plan?.credit_balance),
      pluginSnapshot: true,
    };
  } catch (error) {
    if ((error as ApiError).status !== 404) throw error;
    // Backward compatibility for management clients connected to a core without /quota/fetch.
  }

  const projectId = resolveGeminiCliProjectId(file);
  if (!projectId) {
    throw new Error(t('gemini_cli_quota.missing_project_id'));
  }

  const quotaResponse = await apiCallApi.request({
    authIndex,
    method: 'POST',
    url: GEMINI_CLI_QUOTA_URL,
    header: { 'Content-Type': 'application/json' },
    data: JSON.stringify({ project: projectId }),
    useExecutor: true,
  });

  if (quotaResponse.statusCode < 200 || quotaResponse.statusCode >= 300) {
    throw createStatusError(getApiCallErrorMessage(quotaResponse), quotaResponse.statusCode);
  }

  const payload = parseGeminiCliQuotaPayload(quotaResponse.body ?? quotaResponse.bodyText);
  const rawBuckets = Array.isArray(payload?.buckets) ? payload.buckets : [];
  const parsedBuckets = rawBuckets
    .map((bucket): GeminiCliParsedBucket | null => {
      const modelId = normalizeGeminiCliModelId(bucket.modelId ?? bucket.model_id);
      if (!modelId || isIgnoredGeminiCliModel(modelId)) return null;
      const remainingFraction = resolveGeminiCliRemainingFraction(bucket);
      const remainingAmount = normalizeNumberValue(
        bucket.remainingAmount ?? bucket.remaining_amount
      );
      return {
        modelId,
        tokenType: normalizeStringValue(bucket.tokenType ?? bucket.token_type),
        remainingFraction,
        remainingAmount,
        resetTime: normalizeStringValue(bucket.resetTime ?? bucket.reset_time) ?? undefined,
      };
    })
    .filter((bucket): bucket is GeminiCliParsedBucket => bucket !== null);

  const buckets = buildGeminiCliQuotaBuckets(parsedBuckets);
  if (buckets.length === 0) {
    throw new Error(t('gemini_cli_quota.empty_buckets'));
  }

  const supplementaryRequestId = scheduleGeminiCliSupplementaryRefresh(
    file.name,
    authIndex,
    projectId,
    t
  );
  const supplementary = readGeminiCliSupplementarySnapshot(file.name, supplementaryRequestId);

  return {
    fileName: file.name,
    supplementaryRequestId,
    buckets,
    projectId,
    pluginSnapshot: false,
    ...supplementary,
  };
};

const resolveGeminiCliTier = (
  payload: GeminiCliCodeAssistPayload | null
): GeminiCliUserTier | null => {
  if (!payload) return null;
  const paidTier = payload.paidTier ?? payload.paid_tier ?? null;
  const currentTier = payload.currentTier ?? payload.current_tier ?? null;
  return paidTier?.id ? paidTier : currentTier;
};

const resolveGeminiCliTierId = (payload: GeminiCliCodeAssistPayload | null): string | null =>
  normalizeStringValue(resolveGeminiCliTier(payload)?.id);

const resolveGeminiCliTierLabel = (
  payload: GeminiCliCodeAssistPayload | null,
  t: TFunction
): string | null => {
  const tier = resolveGeminiCliTier(payload);
  const tierId = normalizeStringValue(tier?.id);
  return resolveGeminiCliTierDisplay(tierId, tier?.name, t);
};

const resolveGeminiCliCreditBalance = (
  payload: GeminiCliCodeAssistPayload | null
): number | null => {
  const tier = resolveGeminiCliTier(payload);
  const credits: GeminiCliCredits[] = tier?.availableCredits ?? tier?.available_credits ?? [];
  for (const credit of credits) {
    const creditType = normalizeStringValue(credit.creditType ?? credit.credit_type);
    if (creditType !== GEMINI_CLI_G1_CREDIT_TYPE) continue;
    return normalizeNumberValue(credit.creditAmount ?? credit.credit_amount);
  }
  return null;
};

const renderGeminiCliItems = (
  quota: GeminiCliQuotaState,
  t: TFunction,
  helpers: QuotaRenderHelpers
): ReactNode => {
  const { styles: styleMap, QuotaProgressBar } = helpers;
  const { createElement: h, Fragment } = React;
  const buckets = quota.buckets ?? [];
  const nodes: ReactNode[] = [];
  const tierId = quota.tierId ?? null;
  const tierLabel = resolveGeminiCliTierDisplay(tierId, quota.tierLabel, t);
  const creditBalance = quota.creditBalance ?? null;

  if (tierLabel || creditBalance !== null) {
    nodes.push(
      h(
        'div',
        { key: 'tier', className: styleMap.codexPlan },
        tierLabel
          ? h(
              'span',
              { className: styleMap.codexPlanItem },
              h('span', { className: styleMap.codexPlanLabel }, t('gemini_cli_quota.tier_label')),
              h(
                'span',
                {
                  className:
                    tierId && PREMIUM_GEMINI_CLI_TIER_IDS.has(tierId)
                      ? styleMap.premiumPlanValue
                      : styleMap.codexPlanValue,
                },
                tierLabel
              )
            )
          : null,
        creditBalance !== null
          ? h(
              'span',
              { className: styleMap.codexPlanItem },
              h('span', { className: styleMap.codexPlanLabel }, t('gemini_cli_quota.credit_label')),
              h(
                'span',
                { className: styleMap.codexPlanValue },
                t('gemini_cli_quota.credit_amount', { count: creditBalance })
              )
            )
          : null
      )
    );
  }

  if (buckets.length === 0) {
    nodes.push(
      h('div', { key: 'empty', className: styleMap.quotaMessage }, t('gemini_cli_quota.empty_buckets'))
    );
    return h(Fragment, null, ...nodes);
  }

  nodes.push(
    ...buckets.map((bucket) => {
      const remainingFraction = bucket.remainingFraction;
      const remaining =
        remainingFraction === null ? null : Math.max(0, Math.min(100, remainingFraction * 100));
      const percentLabel = remaining === null ? '--' : `${Math.round(remaining)}%`;
      const amountLabel =
        bucket.remainingAmount === null
          ? null
          : t('gemini_cli_quota.remaining_amount', { count: bucket.remainingAmount });

      return h(
        'div',
        { key: bucket.id, className: styleMap.quotaRow },
        h(
          'div',
          { className: styleMap.quotaRowHeader },
          h('span', { className: styleMap.quotaModel, title: bucket.modelIds?.join(', ') }, bucket.label),
          h(
            'div',
            { className: styleMap.quotaMeta },
            h('span', { className: styleMap.quotaPercent }, percentLabel),
            amountLabel ? h('span', { className: styleMap.quotaAmount }, amountLabel) : null,
            h('span', { className: styleMap.quotaReset }, formatQuotaResetTime(bucket.resetTime))
          )
        ),
        h(QuotaProgressBar, {
          percent: remaining,
          highThreshold: QUOTA_PROGRESS_HIGH_THRESHOLD,
          mediumThreshold: QUOTA_PROGRESS_MEDIUM_THRESHOLD,
        })
      );
    })
  );

  return h(Fragment, null, ...nodes);
};

export const GEMINI_CLI_CONFIG = {
  type: 'gemini-cli',
  i18nPrefix: 'gemini_cli_quota',
  filterFn: (file: AuthFileItem) =>
    isGeminiCliFile(file) && !isRuntimeOnlyAuthFile(file) && !isDisabledAuthFile(file),
  fetchQuota: fetchGeminiCliQuota,
  storeSelector: (state) => state.geminiCliQuota,
  storeSetter: 'setGeminiCliQuota',
  buildLoadingState: () => ({
    status: 'loading',
    buckets: [],
    projectId: '',
    tierLabel: null,
    tierId: null,
    creditBalance: null,
  }),
  buildSuccessState: (data: GeminiCliQuotaData) => {
    const supplementary = readGeminiCliSupplementarySnapshot(
      data.fileName,
      data.supplementaryRequestId
    );

    return {
      status: 'success',
      buckets: data.buckets,
      projectId: data.projectId,
      tierLabel: supplementary.tierLabel ?? data.tierLabel,
      tierId: supplementary.tierId ?? data.tierId,
      creditBalance: supplementary.creditBalance ?? data.creditBalance,
      quotaProviderSnapshot: data.pluginSnapshot || undefined,
      cachedAt: Date.now(),
    };
  },
  buildErrorState: (message: string, status?: number) => ({
    status: 'error',
    buckets: [],
    projectId: '',
    tierLabel: null,
    tierId: null,
    creditBalance: null,
    error: message,
    errorStatus: status,
  }),
  cardClassName: styles.geminiCliCard,
  gridClassName: styles.geminiCliGrid,
  renderQuotaItems: renderGeminiCliItems,
} satisfies QuotaConfig<GeminiCliQuotaState, GeminiCliQuotaData>;
