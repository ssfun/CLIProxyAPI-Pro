import { startTransition, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { authFilesApi } from '@/services/api/authFiles';
import { apiClient } from '@/services/api/client';
import { useAuthStore } from '@/stores/useAuthStore';
import type { AuthFileItem } from '@/types/authFile';
import type { Config } from '@/types/config';
import type { CredentialInfo } from '@/pro/modules/monitoring/features/sourceInfo';
import { resolveProviderDisplayLabel } from '@/pro/shared/provider';
import { isRecordValue, readBooleanValue, readStringValue } from '@/pro/shared/value';
import { buildSourceInfoMap, resolveSourceDisplay, type SourceInfoMapInput } from '@/pro/modules/monitoring/features/sourceResolver';
import {
  calculateCost,
  collectUsageDetailsWithEndpoint,
  extractTotalTokens,
  normalizeAuthIndex,
  type ModelPrice,
  type UsageCostBreakdown,
  type UsageDetailWithEndpoint,
  type UsageTokenBreakdown,
} from '@/pro/modules/monitoring/features/usage';
import {
  buildConfiguredApiKeyMap,
  type MonitoringApiKeyIdentity,
} from '../apiKeyIdentity';

const padNumber = (value: number) => String(value).padStart(2, '0');

export const buildLocalDayKey = (timestampMs: number) => {
  const date = new Date(timestampMs);
  return `${date.getFullYear()}-${padNumber(date.getMonth() + 1)}-${padNumber(date.getDate())}`;
};

export const buildHourLabel = (timestampMs: number) => `${padNumber(new Date(timestampMs).getHours())}:00`;

export const buildDayLabel = (dayKey: string) => dayKey.slice(5).replace('-', '/');

export const formatShortDateTime = (timestampMs: number) => {
  const date = new Date(timestampMs);
  return `${date.getMonth() + 1}/${date.getDate()} ${padNumber(date.getHours())}:${padNumber(date.getMinutes())}`;
};

const DELETED_CREDENTIAL_FALLBACK_LABEL = 'Deleted credential';

const maskEmailLike = (value: string) => {
  const trimmed = value.trim();
  const match = trimmed.match(/^([^@\s]{1,3})[^@\s]*@(.+)$/);
  if (!match) return trimmed;
  return `${match[1]}***@${match[2]}`;
};

const maskAuthIndex = (value: string) => {
  const trimmed = value.trim();
  if (!trimmed || trimmed === '-') return '-';
  if (trimmed.length <= 10) return trimmed;
  return `${trimmed.slice(0, 4)}...${trimmed.slice(-4)}`;
};

const maskHash = (value: string) => {
  const trimmed = value.trim();
  if (!trimmed || trimmed === '-') return '-';
  if (trimmed.length <= 12) return trimmed;
  return `${trimmed.slice(0, 6)}...${trimmed.slice(-6)}`;
};

const extractArrayPayload = (payload: unknown, key: string): unknown[] => {
  if (Array.isArray(payload)) return payload;
  if (!isRecordValue(payload)) return [];
  const candidate = payload[key] ?? payload.items ?? payload.data ?? payload;
  return Array.isArray(candidate) ? candidate : [];
};

const extractHost = (baseUrl: string) => {
  const trimmed = readStringValue(baseUrl);
  if (!trimmed) return '-';

  try {
    return new URL(trimmed).host || trimmed;
  } catch {
    return trimmed.replace(/^https?:\/\//i, '').split('/')[0] || trimmed;
  }
};

const buildSearchText = (...parts: Array<string | number | boolean | null | undefined>) =>
  parts
    .map((part) => (part === null || part === undefined ? '' : String(part).trim().toLowerCase()))
    .filter(Boolean)
    .join(' ');

type MonitoringChannelMeta = {
  key: string;
  name: string;
  baseUrl: string;
  host: string;
  disabled: boolean;
  authIndices: string[];
  modelNames: string[];
  authType?: 'oauth' | 'apikey' | '';
};

type MonitoringAuthMeta = {
  authIndex: string;
  label: string;
  account: string;
  provider: string;
  status: string;
  disabled: boolean;
  unavailable: boolean;
  runtimeOnly: boolean;
  planType: string;
  updatedAt: string;
};

export type MonitoringStatusTone = 'good' | 'warn' | 'bad';


export type MonitoringEventRow = {
  id: string;
  timestamp: string;
  timestampMs: number;
  dayKey: string;
  hourLabel: string;
  model: string;
  modelAlias: string;
  endpoint: string;
  endpointMethod: string;
  endpointPath: string;
  sourceKey: string;
  source: string;
  sourceMasked: string;
  account: string;
  accountMasked: string;
  authIndex: string;
  authIndexMasked: string;
  clientApiKey: MonitoringApiKeyIdentity;
  apiKeyPolicyId: string;
  profileId: string;
  profileName: string;
  policyMode: string;
  requestedModel: string;
  effectiveModel: string;
  upstreamModel?: string;
  responseModel?: string;
  modelMatchStatus?: string;
  authLabel: string;
  provider: string;
  executorType: string;
  planType: string;
  channel: string;
  channelHost: string;
  channelDisabled: boolean;
  credentialDeleted: boolean;
  failed: boolean;
  statsIncluded: boolean;
  latencyMs: number | null;
  ttftMs: number | null;
  statusCode: number | null;
  errorCode: string;
  errorMessage: string;
  upstreamRequestId: string;
  retryAfter: string;
  attemptIndex: number | null;
  accountingVersion: number | null;
  accountingQuality: string;
  tokenBreakdown: UsageTokenBreakdown | null;
  clientIP: string;
  xForwardedFor: string;
  userAgent: string;
  stream: boolean;
  reasoningEffort: string;
  serviceTier: string;
  effectiveServiceTier: string;
  speed: string;
  effectiveSpeed: string;
  costBreakdown: UsageCostBreakdown | null;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cachedTokens: number;
  cacheInputTokens: number;
  totalTokens: number;
  totalCost: number;
  taskKey: string;
  searchText: string;
};

export type MonitoringSummary = {
  totalCalls: number;
  successCalls: number;
  failureCalls: number;
  successRate: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cachedTokens: number;
  cacheInputTokens: number;
  totalTokens: number;
  totalCost: number;
  averageLatencyMs: number | null;
  rpm30m: number;
  tpm30m: number;
  avgDailyRequests: number;
  avgDailyTokens: number;
  approxTasks: number;
  approxTaskFailures: number;
  approxTaskSuccessRate: number;
  zeroTokenCalls: number;
  zeroTokenModels: string[];
};

export type MonitoringAccountGroupBy = 'account' | 'apiKey' | 'model';

export type MonitoringAccountModelSpendRow = {
  model: string;
  totalCalls: number;
  successCalls: number;
  failureCalls: number;
  successRate: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  totalTokens: number;
  totalCost: number;
  lastSeenAt: number;
};

export type MonitoringAccountRow = {
  id: string;
  group: MonitoringAccountGroupBy;
  model: string;
  apiKeyHash: string;
  apiKeyMasked: string;
  account: string;
  accountMasked: string;
  authLabels: string[];
  authIndices: string[];
  channels: string[];
  providers: string[];
  totalCalls: number;
  successCalls: number;
  failureCalls: number;
  successRate: number;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  totalTokens: number;
  totalCost: number;
  averageLatencyMs: number | null;
  lastSeenAt: number;
  recentPattern: boolean[];
  rows?: MonitoringEventRow[];
  models: MonitoringAccountModelSpendRow[];
};

export interface UseMonitoringEventRowsParams {
  usage: unknown;
  logUsage?: unknown;
  config: Config | null | undefined;
  modelPrices: Record<string, ModelPrice>;
  deletedCredentialLabel?: string;
  unattributedApiKeyLabel?: string;
  apiKeyNames?: ReadonlyMap<string, string>;
}

export interface UseMonitoringEventRowsReturn {
  loading: boolean;
  error: string;
  authFiles: AuthFileItem[];
  allRows: MonitoringEventRow[];
  filteredRows: MonitoringEventRow[];
  refreshMeta: (showLoading?: boolean) => Promise<void>;
}

type MonitoringMetaPayload = {
  authFiles: AuthFileItem[];
  channels: MonitoringChannelMeta[];
  error: string;
};

const normalizeOpenAIChannel = (value: unknown, index: number): MonitoringChannelMeta | null => {
  if (!isRecordValue(value)) return null;

  const name = readStringValue(value.name || value.id) || `openai-${index + 1}`;
  const baseUrl = readStringValue(value['base-url'] ?? value.baseUrl);
  if (!baseUrl) return null;

  const authIndices = new Set<string>();
  const providerAuthIndex = normalizeAuthIndex(
    value['auth-index'] ?? value.authIndex ?? value['auth_index']
  );
  if (providerAuthIndex) {
    authIndices.add(providerAuthIndex);
  }

  const apiKeyEntries = Array.isArray(value['api-key-entries']) ? value['api-key-entries'] : [];
  apiKeyEntries.forEach((entry) => {
    if (!isRecordValue(entry)) return;
    const authIndex = normalizeAuthIndex(
      entry['auth-index'] ?? entry.authIndex ?? entry['auth_index']
    );
    if (authIndex) {
      authIndices.add(authIndex);
    }
  });

  const modelNames = Array.isArray(value.models)
    ? value.models
        .map((item) => {
          if (typeof item === 'string') return readStringValue(item);
          if (!isRecordValue(item)) return '';
          return readStringValue(item.name ?? item.alias ?? item.id ?? item.model);
        })
        .filter(Boolean)
    : [];

  return {
    key: `${name}:${index}`,
    name,
    baseUrl,
    host: extractHost(baseUrl),
    disabled: readBooleanValue(value.disabled),
    authIndices: Array.from(authIndices),
    modelNames: Array.from(new Set(modelNames)),
  };
};

const readAuthTimestamp = (entry: AuthFileItem) =>
  readStringValue(entry['updated_at'] ?? entry.updatedAt ?? entry['modtime'] ?? entry.modified);

const readNestedString = (value: unknown, path: string[]) => {
  let current = value;
  for (const key of path) {
    if (!isRecordValue(current)) return '';
    current = current[key];
  }
  return readStringValue(current);
};

const looksLikeAuthFileName = (value: string) => /_oauth_creds\.json$/i.test(value) || /\.json$/i.test(value);

const normalizeProviderLabel = (value: string) => value.trim().toLowerCase().replace(/[_\s]+/g, '-');

const isWeakAuthDisplayValue = (value: string, authIndex: string, providerLabel: string) => {
  if (!value) return true;
  if (looksLikeAuthFileName(value)) return true;
  const normalized = normalizeProviderLabel(value);
  return normalized === providerLabel ||
    normalized === `${providerLabel}-oauth-creds-json` ||
    normalizeAuthIndex(value) === authIndex;
};

const resolveAuthDisplayName = (entry: AuthFileItem, authIndex: string) => {
  const provider = readStringValue(entry.provider) || readStringValue(entry.type);
  const providerLabel = normalizeProviderLabel(provider);
  const label = readStringValue(entry.label);
  const name = readStringValue(entry.name);
  const email = readStringValue(entry.email) || readNestedString(entry, ['id_token', 'email']);
  const account = readStringValue(entry.account) || readNestedString(entry, ['id_token', 'account']);
  const username = readNestedString(entry, ['id_token', 'preferred_username']);
  const subject = readNestedString(entry, ['id_token', 'sub']);
  const fallback = [email, account, username, label, subject].find(
    (value) => !isWeakAuthDisplayValue(value, authIndex, providerLabel)
  );

  if (fallback) return fallback;
  if (name && normalizeAuthIndex(name) !== authIndex) return name;
  if (label && normalizeAuthIndex(label) !== authIndex) return label;
  return resolveProviderDisplayLabel(provider) || authIndex;
};

const normalizeAuthMeta = (entry: AuthFileItem): MonitoringAuthMeta | null => {
  const authIndex = normalizeAuthIndex(entry['auth_index'] ?? entry.authIndex);
  if (!authIndex) return null;

  const label = resolveAuthDisplayName(entry, authIndex);

  const planType = readStringValue(
    isRecordValue(entry.id_token) ? entry.id_token.plan_type : entry['plan_type']
  );

  const provider = readStringValue(entry.provider) || readStringValue(entry.type) || '-';
  const email = readStringValue(entry.email) || readNestedString(entry, ['id_token', 'email']);
  const name = readStringValue(entry.name);
  const account = email ||
    readStringValue(entry.account) ||
    readNestedString(entry, ['id_token', 'account']) ||
    (name && normalizeAuthIndex(name) !== authIndex ? name : '') ||
    (label && normalizeAuthIndex(label) !== authIndex ? label : '');

  return {
    authIndex,
    label,
    account: account || provider || '-',
    provider,
    status: readStringValue(entry.status) || 'unknown',
    disabled: readBooleanValue(entry.disabled),
    unavailable: readBooleanValue(entry.unavailable),
    runtimeOnly: readBooleanValue(entry.runtime_only ?? entry.runtimeOnly),
    planType: planType || '-',
    updatedAt: readAuthTimestamp(entry),
  };
};


const buildEventRows = (
  details: UsageDetailWithEndpoint[],
  authMetaMap: Map<string, MonitoringAuthMeta>,
  authFileMap: Map<string, CredentialInfo>,
  sourceInfoMap: ReturnType<typeof buildSourceInfoMap>,
  channelByAuthIndex: Map<string, MonitoringChannelMeta>,
  configuredApiKeys: ReturnType<typeof buildConfiguredApiKeyMap>,
  modelPrices: Record<string, ModelPrice>,
  deletedCredentialLabel: string,
  unattributedApiKeyLabel: string
) => {
  const rows: MonitoringEventRow[] = [];
  let isDescending = true;
  let previousTimestampMs = Number.POSITIVE_INFINITY;

  details.forEach((detail, index) => {
    const timestampMs =
      typeof detail.__timestampMs === 'number' && detail.__timestampMs > 0
        ? detail.__timestampMs
        : Date.parse(detail.timestamp);
    if (!Number.isFinite(timestampMs) || timestampMs <= 0) {
      return;
    }
    if (timestampMs > previousTimestampMs) {
      isDescending = false;
    }
    previousTimestampMs = timestampMs;

    const authIndex = normalizeAuthIndex(detail.auth_index) ?? '-';
    const authMeta = authMetaMap.get(authIndex);
    const sourceMeta = resolveSourceDisplay(detail.source, detail.auth_index, sourceInfoMap, authFileMap);
    const resolvedProvider = (detail.provider || authMeta?.provider || sourceMeta.type || '-').toLowerCase();
    const resolvedAuthType = detail.auth_type || (authMeta ? (authMeta.runtimeOnly ? '' : 'oauth') : '');
    const channelMeta = channelByAuthIndex.get(authIndex)
      ?? (resolvedProvider !== '-'
        ? (channelByAuthIndex.get(`provider:${resolvedAuthType === 'apikey' ? 'apikey' : 'oauth'}:${resolvedProvider}`)
          ?? channelByAuthIndex.get(`provider:oauth:${resolvedProvider}`)
          ?? channelByAuthIndex.get(`provider:apikey:${resolvedProvider}`))
        : undefined);
    const hasAuthIndex = authIndex !== '-';
    const hasKnownAuthIndex = hasAuthIndex && (
      authMetaMap.has(authIndex) ||
      authFileMap.has(authIndex) ||
      channelByAuthIndex.has(authIndex) ||
      sourceInfoMap.byAuthIndex.has(authIndex)
    );
    const sourceIdentityKey = sourceMeta.identityKey || '';
    const isConfiguredSourceCredential = Boolean(sourceIdentityKey) &&
      !sourceIdentityKey.startsWith('auth:') &&
      !sourceIdentityKey.startsWith('source:');
    const isApiKeyCredential = resolvedAuthType === 'apikey' ||
      channelMeta?.authType === 'apikey' ||
      (isConfiguredSourceCredential && !authMeta);
    const isDeletedCredential = hasAuthIndex && !hasKnownAuthIndex && !isApiKeyCredential;
    const sourceLabel = isDeletedCredential
      ? deletedCredentialLabel
      : authMeta?.label || sourceMeta.displayName || authIndex;
    const sourceMasked = maskEmailLike(sourceLabel);
    const account = isDeletedCredential ? sourceLabel : authMeta?.account || sourceLabel;
    const accountMasked = maskEmailLike(account);
    const channelLabel = channelMeta?.name || resolvedProvider;
    const endpoint = readStringValue(detail.__endpoint) || '-';
    const endpointMethod = readStringValue(detail.__endpointMethod) || '-';
    const endpointPath = readStringValue(detail.__endpointPath) || endpoint;
    const inputTokens = Math.max(Number(detail.tokens?.input_tokens) || 0, 0);
    const outputTokens = Math.max(Number(detail.tokens?.output_tokens) || 0, 0);
    const reasoningTokens = Math.max(Number(detail.tokens?.reasoning_tokens) || 0, 0);
    const cachedTokens = Math.max(
      Math.max(Number(detail.tokens?.cached_tokens) || 0, 0),
      Math.max(Number(detail.tokens?.cache_read_tokens) || 0, 0)
    );
    const cacheInputTokens = Math.max(Number(detail.tokens?.cache_input_tokens) || 0, 0);
    const totalTokens = Math.max(Number(detail.tokens?.total_tokens) || 0, extractTotalTokens(detail));
    const totalCost = calculateCost(detail, modelPrices);
    const apiKeyHash = readStringValue(detail.api_key_hash) || '-';
    const configuredApiKey = apiKeyHash === '-' ? null : configuredApiKeys.byHash.get(apiKeyHash);
    const clientApiKeyIdentity: MonitoringApiKeyIdentity = configuredApiKey
      ?? (apiKeyHash !== '-'
        ? {
            id: `clientApiKey:${apiKeyHash}`,
            hash: apiKeyHash,
            masked: maskHash(apiKeyHash),
          }
        : {
            id: 'clientApiKey:unknown',
            hash: '-',
            masked: unattributedApiKeyLabel,
          });
    const dayKey = buildLocalDayKey(timestampMs);
    const hourLabel = buildHourLabel(timestampMs);
    const sourceKey = sourceMeta.identityKey || `source:${sourceLabel}`;
    const taskKey = `${detail.timestamp}|${sourceKey}|${authIndex}`;
    const model = readStringValue(detail.__modelName) || '-';
    const modelAlias = readStringValue(detail.alias);
    const executorType = readStringValue(detail.executor_type);

    rows.push({
      id: `${detail.timestamp}-${model}-${sourceKey}-${authIndex}-${index}`,
      timestamp: detail.timestamp,
      timestampMs,
      dayKey,
      hourLabel,
      model,
      modelAlias,
      endpoint,
      endpointMethod,
      endpointPath,
      sourceKey,
      source: sourceLabel,
      sourceMasked,
      account,
      accountMasked,
      authIndex,
      authIndexMasked: maskAuthIndex(authIndex),
      clientApiKey: clientApiKeyIdentity,
      apiKeyPolicyId: detail.api_key_policy_id || '',
      profileId: detail.profile_id || '',
      profileName: detail.profile_name_snapshot || '',
      policyMode: detail.policy_mode || '',
      requestedModel: detail.requested_model || '',
      effectiveModel: detail.effective_model || '',
      upstreamModel: detail.upstream_model || '',
      responseModel: detail.response_model || '',
      modelMatchStatus: detail.model_match_status || 'unknown',
      authLabel: isDeletedCredential ? deletedCredentialLabel : authMeta?.label || sourceMasked,
      provider: resolvedProvider,
      executorType,
      planType: authMeta?.planType || '-',
      channel: channelLabel,
      channelHost: channelMeta?.host || '-',
      channelDisabled: channelMeta?.disabled || false,
      credentialDeleted: isDeletedCredential,
      failed: detail.failed === true,
      statsIncluded: true,
      latencyMs: typeof detail.latency_ms === 'number' ? detail.latency_ms : null,
      ttftMs: typeof detail.ttft_ms === 'number' ? detail.ttft_ms : null,
      statusCode: typeof detail.status_code === 'number' ? detail.status_code : null,
      errorCode: detail.error_code || '',
      errorMessage: detail.error_message || '',
      upstreamRequestId: detail.upstream_request_id || '',
      retryAfter: detail.retry_after || '',
      attemptIndex: typeof detail.attempt_index === 'number' ? detail.attempt_index : null,
      accountingVersion:
        typeof detail.accounting_version === 'number' ? detail.accounting_version : null,
      accountingQuality: detail.accounting_quality || '',
      tokenBreakdown: detail.token_breakdown ?? null,
      clientIP: detail.client_ip || '',
      xForwardedFor: detail.x_forwarded_for || '',
      userAgent: detail.user_agent || '',
      stream: detail.stream === true,
      reasoningEffort: detail.reasoning_effort || '',
      serviceTier: detail.service_tier || '',
      effectiveServiceTier: detail.effective_service_tier || '',
      speed: detail.speed || '',
      effectiveSpeed: detail.effective_speed || '',
      costBreakdown: detail.cost_breakdown ?? null,
      inputTokens,
      outputTokens,
      reasoningTokens,
      cachedTokens,
      cacheInputTokens,
      totalTokens,
      totalCost,
      taskKey,
      searchText: buildSearchText(
        model,
        modelAlias,
        isDeletedCredential ? deletedCredentialLabel : sourceLabel,
        isDeletedCredential ? '' : authMeta?.account,
        authMeta?.label,
        authIndex,
        channelLabel,
        channelMeta?.host,
        endpointPath,
        endpointMethod,
        resolvedProvider,
        executorType,
        detail.upstream_request_id,
        detail.retry_after,
        detail.attempt_index,
        detail.accounting_version,
        detail.accounting_quality,
        detail.client_ip,
        detail.x_forwarded_for,
        detail.user_agent,
        detail.reasoning_effort,
        detail.service_tier,
        detail.effective_service_tier,
        detail.speed,
        detail.effective_speed,
        authMeta?.planType,
        clientApiKeyIdentity.masked
        ,detail.api_key_policy_id,
        detail.profile_id,
        detail.profile_name_snapshot,
        detail.policy_mode,
        detail.requested_model,
        detail.effective_model,
        detail.upstream_model,
        detail.response_model
      ),
    });
  });

  return isDescending
    ? rows
    : rows.sort((left, right) => right.timestampMs - left.timestampMs);
};

const buildNativeProviderChannels = (
  config: Config | null | undefined,
  authFiles: AuthFileItem[]
): MonitoringChannelMeta[] => {
  type ChannelBucket = {
    authIndices: Set<string>;
    modelNames: Set<string>;
    disabled: boolean;
  };

  const bucketMap = new Map<string, ChannelBucket>();

  const ensureBucket = (key: string) => {
    let bucket = bucketMap.get(key);
    if (!bucket) {
      bucket = { authIndices: new Set(), modelNames: new Set(), disabled: false };
      bucketMap.set(key, bucket);
    }
    return bucket;
  };

  const apiKeyProviders: Array<{
    items: Array<{ apiKey?: string; prefix?: string; authIndex?: string; models?: unknown[] }> | undefined;
    type: string;
  }> = [
    { items: config?.geminiApiKeys, type: 'gemini' },
    { items: config?.claudeApiKeys, type: 'claude' },
    { items: config?.codexApiKeys, type: 'codex' },
    { items: config?.vertexApiKeys, type: 'vertex' },
  ];

  apiKeyProviders.forEach(({ items, type }) => {
    if (!items?.length) return;
    const key = `apikey:${type}`;
    const bucket = ensureBucket(key);
    items.forEach((item) => {
      const authIndex = normalizeAuthIndex(item.authIndex);
      if (authIndex) bucket.authIndices.add(authIndex);
      if (Array.isArray(item.models)) {
        item.models.forEach((m) => {
          const name = typeof m === 'string' ? m.trim() : '';
          if (name) bucket.modelNames.add(name);
        });
      }
    });
  });

  authFiles.forEach((file) => {
    const provider = (readStringValue(file.provider) || readStringValue(file.type)).toLowerCase();
    if (!provider) return;
    const key = `oauth:${provider}`;
    const bucket = ensureBucket(key);
    const authIndex = normalizeAuthIndex(file['auth_index'] ?? file.authIndex);
    if (authIndex) bucket.authIndices.add(authIndex);
  });

  const channels: MonitoringChannelMeta[] = [];
  bucketMap.forEach((bucket, bucketKey) => {
    if (bucket.authIndices.size === 0) return;
    const [authType, provider] = bucketKey.split(':', 2) as ['oauth' | 'apikey', string];
    const label = resolveProviderDisplayLabel(provider);
    const suffix = authType === 'apikey' ? ' (API Key)' : '';
    channels.push({
      key: `provider:${bucketKey}`,
      name: `${label}${suffix}`,
      baseUrl: '',
      host: provider,
      disabled: bucket.disabled,
      authIndices: Array.from(bucket.authIndices),
      modelNames: Array.from(bucket.modelNames),
      authType,
    });
  });

  return channels;
};

const loadMonitoringMetaPayload = async (
  config: Config | null | undefined
): Promise<MonitoringMetaPayload> => {
  const [authResult, channelResult] = await Promise.allSettled([
    authFilesApi.list(),
    apiClient.get('/openai-compatibility'),
  ]);

  const authFiles =
    authResult.status === 'fulfilled' && Array.isArray(authResult.value.files)
      ? authResult.value.files
      : [];

  let channels: MonitoringChannelMeta[] = [];

  if (channelResult.status === 'fulfilled') {
    channels = extractArrayPayload(channelResult.value, 'openai-compatibility')
      .map((item, index) => normalizeOpenAIChannel(item, index))
      .filter(Boolean) as MonitoringChannelMeta[];
  } else if (config?.openaiCompatibility?.length) {
    channels = config.openaiCompatibility
      .map((item, index) =>
        normalizeOpenAIChannel(
          {
            ...item,
            'base-url': item.baseUrl,
            'api-key-entries': item.apiKeyEntries,
            models: item.models,
          },
          index
        )
      )
      .filter(Boolean) as MonitoringChannelMeta[];
  }

  const nativeChannels = buildNativeProviderChannels(config, authFiles);
  const openaiChannelAuthIndices = new Set(channels.flatMap((ch) => ch.authIndices));
  nativeChannels.forEach((nativeCh) => {
    const hasOverlap = nativeCh.authIndices.some((idx) => openaiChannelAuthIndices.has(idx));
    if (!hasOverlap) {
      channels.push(nativeCh);
    }
  });

  const error = [authResult, channelResult]
    .filter((result) => result.status === 'rejected')
    .map((result) => (result.status === 'rejected' ? result.reason : null))
    .filter(Boolean)
    .map((err) => (err instanceof Error ? err.message : String(err)))
    .join('；');

  return { authFiles, channels, error };
};

export function useMonitoringEventRows({
  usage,
  logUsage,
  config,
  modelPrices,
  deletedCredentialLabel = DELETED_CREDENTIAL_FALLBACK_LABEL,
  unattributedApiKeyLabel = 'Unattributed API Key',
  apiKeyNames,
}: UseMonitoringEventRowsParams): UseMonitoringEventRowsReturn {
  const [authFiles, setAuthFiles] = useState<AuthFileItem[]>([]);
  const [channels, setChannels] = useState<MonitoringChannelMeta[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const requestIdRef = useRef(0);
  const activeConnectionKeyRef = useRef('');
  const apiBase = useAuthStore((state) => state.apiBase);
  const managementKey = useAuthStore((state) => state.managementKey);
  const connectionStatus = useAuthStore((state) => state.connectionStatus);
  const connectionKey = `${apiBase}\n${managementKey}`;

  const applyMetaPayload = useCallback((payload: MonitoringMetaPayload, deferred = false) => {
    const apply = () => {
      setAuthFiles(payload.authFiles);
      setChannels(payload.channels);
      setError(payload.error);
      setLoading(false);
    };
    if (deferred) {
      startTransition(apply);
      return;
    }
    apply();
  }, []);

  const refreshMeta = useCallback(async (showLoading: boolean = true) => {
    const requestId = requestIdRef.current + 1;
    requestIdRef.current = requestId;
    if (connectionStatus !== 'connected' || !apiBase || !managementKey) {
      setAuthFiles([]);
      setChannels([]);
      setError('');
      setLoading(false);
      return;
    }
    if (showLoading) {
      setLoading(true);
      setError('');
    }

    try {
      const payload = await loadMonitoringMetaPayload(config);
      if (requestIdRef.current !== requestId) return;
      applyMetaPayload(payload, true);
    } catch (reason) {
      if (requestIdRef.current !== requestId) return;
      setError(reason instanceof Error ? reason.message : String(reason));
      setLoading(false);
    }
  }, [apiBase, applyMetaPayload, config, connectionStatus, managementKey]);

  useEffect(() => {
    const connectionChanged = activeConnectionKeyRef.current !== connectionKey;
    activeConnectionKeyRef.current = connectionKey;
    requestIdRef.current += 1;
    if (connectionChanged || connectionStatus !== 'connected') {
      setAuthFiles([]);
      setChannels([]);
      setError('');
    }
    void refreshMeta(true);
    return () => {
      requestIdRef.current += 1;
    };
  }, [connectionKey, connectionStatus, refreshMeta]);

  const authMetaMap = useMemo(() => {
    const map = new Map<string, MonitoringAuthMeta>();
    authFiles.forEach((entry) => {
      const normalized = normalizeAuthMeta(entry);
      if (!normalized) return;
      map.set(normalized.authIndex, normalized);
    });
    return map;
  }, [authFiles]);

  const authFileMap = useMemo(() => {
    const map = new Map<string, CredentialInfo>();
    authFiles.forEach((entry) => {
      const authIndex = normalizeAuthIndex(entry['auth_index'] ?? entry.authIndex);
      if (!authIndex) return;
      map.set(authIndex, {
        name: resolveAuthDisplayName(entry, authIndex),
        type: readStringValue(entry.provider) || readStringValue(entry.type),
      });
    });
    return map;
  }, [authFiles]);

  const sourceInfoMap = useMemo(
    () =>
      buildSourceInfoMap({
        geminiApiKeys: config?.geminiApiKeys || [],
        claudeApiKeys: config?.claudeApiKeys || [],
        codexApiKeys: config?.codexApiKeys || [],
        antigravityApiKeys: (config as Config & { antigravityApiKeys?: SourceInfoMapInput['antigravityApiKeys'] } | null | undefined)?.antigravityApiKeys || [],
        vertexApiKeys: config?.vertexApiKeys || [],
        openaiCompatibility: config?.openaiCompatibility || [],
      }),
    [config]
  );

  const configuredApiKeys = useMemo(() => buildConfiguredApiKeyMap(config?.apiKeys), [config?.apiKeys]);

  const channelByAuthIndex = useMemo(() => {
    const map = new Map<string, MonitoringChannelMeta>();
    channels.forEach((channel) => {
      channel.authIndices.forEach((authIndex) => {
        map.set(authIndex, channel);
      });
      if (channel.key.startsWith('provider:')) {
        map.set(channel.key, channel);
      }
    });
    return map;
  }, [channels]);

  const withApiKeyNames = useCallback((rows: MonitoringEventRow[]) => rows.map((row) => ({
    ...row,
    clientApiKey: { ...row.clientApiKey, name: apiKeyNames?.get(row.clientApiKey.hash) },
  })), [apiKeyNames]);

  const allRows = useMemo(() => {
    const details = collectUsageDetailsWithEndpoint(usage);
    return withApiKeyNames(buildEventRows(
      details,
      authMetaMap,
      authFileMap,
      sourceInfoMap,
      channelByAuthIndex,
      configuredApiKeys,
      modelPrices,
      deletedCredentialLabel,
      unattributedApiKeyLabel
    ));
  }, [withApiKeyNames, authFileMap, authMetaMap, channelByAuthIndex, configuredApiKeys, deletedCredentialLabel, modelPrices, sourceInfoMap, unattributedApiKeyLabel, usage]);

  const logRows = useMemo(() => {
    if (logUsage === undefined) return allRows;
    const details = collectUsageDetailsWithEndpoint(logUsage);
    return withApiKeyNames(buildEventRows(
      details,
      authMetaMap,
      authFileMap,
      sourceInfoMap,
      channelByAuthIndex,
      configuredApiKeys,
      modelPrices,
      deletedCredentialLabel,
      unattributedApiKeyLabel
    ));
  }, [withApiKeyNames, allRows, authFileMap, authMetaMap, channelByAuthIndex, configuredApiKeys, deletedCredentialLabel, logUsage, modelPrices, sourceInfoMap, unattributedApiKeyLabel]);

  return {
    loading,
    error,
    authFiles,
    allRows,
    filteredRows: logRows,
    refreshMeta,
  };
}
