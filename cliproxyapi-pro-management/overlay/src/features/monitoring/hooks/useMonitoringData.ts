import { startTransition, useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { authFilesApi } from '@/services/api/authFiles';
import { apiClient } from '@/services/api/client';
import { useAuthStore } from '@/stores/useAuthStore';
import type { AuthFileItem } from '@/types/authFile';
import type { Config } from '@/types/config';
import type { CredentialInfo } from '@/types/sourceInfo';
import { sha256Hex } from '@/utils/hash';
import { isRecordValue, readBooleanValue, readStringValue } from '@/utils/quota';
import { buildSourceInfoMap, resolveProviderDisplayLabel, resolveSourceDisplay, type SourceInfoMapInput } from '@/utils/sourceResolver';
import {
  calculateCost,
  collectUsageDetailsWithEndpoint,
  extractTotalTokens,
  normalizeAuthIndex,
  type ModelPrice,
  type UsageCostBreakdown,
  type UsageDetailWithEndpoint,
} from '@/utils/usage';

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

const startOfTodayMs = (nowMs: number) => {
  const now = new Date(nowMs);
  now.setHours(0, 0, 0, 0);
  return now.getTime();
};

export const getRangeStartMs = (range: MonitoringTimeRange, nowMs: number) => {
  const todayStart = startOfTodayMs(nowMs);

  switch (range) {
    case 'today':
      return todayStart;
    case '7d':
      return todayStart - 6 * 24 * 60 * 60 * 1000;
    case '14d':
      return todayStart - 13 * 24 * 60 * 60 * 1000;
    case '30d':
      return todayStart - 29 * 24 * 60 * 60 * 1000;
    case 'all':
    default:
      return Number.NEGATIVE_INFINITY;
  }
};

const DELETED_CREDENTIAL_FALLBACK_LABEL = 'Deleted credential';
const EMPTY_MONITORING_SUMMARY: MonitoringSummary = {
  totalCalls: 0,
  successCalls: 0,
  failureCalls: 0,
  successRate: 1,
  inputTokens: 0,
  outputTokens: 0,
  reasoningTokens: 0,
  cachedTokens: 0,
  totalTokens: 0,
  totalCost: 0,
  averageLatencyMs: null,
  rpm30m: 0,
  tpm30m: 0,
  avgDailyRequests: 0,
  avgDailyTokens: 0,
  approxTasks: 0,
  approxTaskFailures: 0,
  approxTaskSuccessRate: 1,
  zeroTokenCalls: 0,
  zeroTokenModels: [],
};

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

const maskClientApiKey = (value: string) => {
  const trimmed = value.trim();
  if (!trimmed || trimmed === '-') return '-';
  const visibleChars = trimmed.length < 4 ? 1 : 2;
  return `${trimmed.slice(0, visibleChars)}${'*'.repeat(Math.max(10 - visibleChars * 2, 1))}${trimmed.slice(-visibleChars)}`;
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

export const joinUnique = (values: Iterable<string>, limit = 3) => {
  const unique = Array.from(new Set(Array.from(values).map((value) => value.trim()).filter(Boolean)));
  if (unique.length <= limit) {
    return unique.join(', ');
  }
  return `${unique.slice(0, limit).join(', ')} +${unique.length - limit}`;
};

const buildSearchText = (...parts: Array<string | number | boolean | null | undefined>) =>
  parts
    .map((part) => (part === null || part === undefined ? '' : String(part).trim().toLowerCase()))
    .filter(Boolean)
    .join(' ');

const buildConfiguredApiKeyMap = (apiKeys: readonly string[] | undefined) => {
  const keys = (apiKeys || [])
    .map((key) => key.trim())
    .filter(Boolean)
    .map((key, index): MonitoringApiKeyIdentity => {
      const hash = sha256Hex(key);
      return {
        id: `clientApiKey:${hash || index}`,
        hash,
        masked: maskClientApiKey(key),
      };
    });

  return {
    keys,
    byHash: new Map(keys.map((key) => [key.hash, key])),
  };
};

type MonitoringApiKeyIdentity = {
  id: string;
  hash: string;
  masked: string;
};

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

export type MonitoringTimeRange = 'today' | '7d' | '14d' | '30d' | 'all';

export type MonitoringStatusTone = 'good' | 'warn' | 'bad';

export type MonitoringStatusChip = {
  key: string;
  label: string;
  value: string;
  tone: MonitoringStatusTone;
};

export type MonitoringKpi = {
  key: string;
  label: string;
  value: number;
  meta: number;
};

export type MonitoringTimelinePoint = {
  label: string;
  requests: number;
  tokens: number;
  cost: number;
};

export type MonitoringModelShareRow = {
  model: string;
  requests: number;
  totalTokens: number;
  totalCost: number;
  successRate: number;
};

export type MonitoringChannelRow = {
  id: string;
  label: string;
  host: string;
  provider: string;
  planTypes: string[];
  disabled: boolean;
  authCount: number;
  modelCount: number;
  requests: number;
  failures: number;
  successRate: number;
  totalTokens: number;
  totalCost: number;
  averageLatencyMs: number | null;
  authLabels: string[];
};

export type MonitoringModelRow = {
  model: string;
  requests: number;
  failures: number;
  successRate: number;
  totalTokens: number;
  totalCost: number;
  averageLatencyMs: number | null;
  sources: number;
  channels: number;
};

export type MonitoringFailureSourceRow = {
  id: string;
  label: string;
  channel: string;
  failures: number;
  totalRequests: number;
  failureRate: number;
  lastSeenAt: number;
  averageLatencyMs: number | null;
};

export type MonitoringTaskBucketRow = {
  id: string;
  timestampMs: number;
  timestamp: string;
  source: string;
  sourceMasked: string;
  channel: string;
  authLabel: string;
  planType: string;
  calls: number;
  failedCalls: number;
  failed: boolean;
  modelsText: string;
  totalTokens: number;
  totalCost: number;
  averageLatencyMs: number | null;
  maxLatencyMs: number | null;
  endpointsText: string;
};

export type MonitoringFailureRow = {
  id: string;
  timestampMs: number;
  timestamp: string;
  model: string;
  source: string;
  channel: string;
  authIndex: string;
  latencyMs: number | null;
};

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
  stream: boolean;
  reasoningEffort: string;
  serviceTier: string;
  costBreakdown: UsageCostBreakdown | null;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cachedTokens: number;
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

export type MonitoringRealtimeRow = {
  id: string;
  account: string;
  accountMasked: string;
  authLabel: string;
  authIndexMasked: string;
  provider: string;
  requestType: string;
  model: string;
  channel: string;
  latestFailed: boolean;
  successRate: number;
  totalCalls: number;
  successCalls: number;
  failureCalls: number;
  averageLatencyMs: number | null;
  latestLatencyMs: number | null;
  inputTokens: number;
  outputTokens: number;
  cachedTokens: number;
  totalTokens: number;
  totalCost: number;
  lastSeenAt: number;
  recentPattern: boolean[];
};

export type MonitoringMetadata = {
  totalAuthFiles: number;
  activeAuthFiles: number;
  unavailableAuthFiles: number;
  runtimeOnlyAuthFiles: number;
  totalChannels: number;
  enabledChannels: number;
  configuredModels: number;
  planTypes: string[];
};

export interface UseMonitoringDataParams {
  usage: unknown;
  logUsage?: unknown;
  serverFilteredLogs?: boolean;
  config: Config | null | undefined;
  modelPrices: Record<string, ModelPrice>;
  timeRange: MonitoringTimeRange;
  searchQuery: string;
  filteredRowLimit?: number;
  deletedCredentialLabel?: string;
  unattributedApiKeyLabel?: string;
}

export interface UseMonitoringDataReturn {
  loading: boolean;
  error: string;
  authFiles: AuthFileItem[];
  channels: MonitoringChannelMeta[];
  summary: MonitoringSummary;
  metadata: MonitoringMetadata;
  statusChips: MonitoringStatusChip[];
  timeline: MonitoringTimelinePoint[];
  timelineGranularity: 'hour' | 'day';
  hourlyDistribution: MonitoringTimelinePoint[];
  modelShareRows: MonitoringModelShareRow[];
  channelRows: MonitoringChannelRow[];
  modelRows: MonitoringModelRow[];
  failureSourceRows: MonitoringFailureSourceRow[];
  taskBuckets: MonitoringTaskBucketRow[];
  recentFailures: MonitoringFailureRow[];
  allRows: MonitoringEventRow[];
  filteredRows: MonitoringEventRow[];
  filteredRowCount: number;
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

const buildRangeFilteredRows = (
  rows: MonitoringEventRow[],
  timeRange: MonitoringTimeRange,
  searchQuery: string,
  limit = 0
) => {
  const nowMs = Date.now();
  const startMs = getRangeStartMs(timeRange, nowMs);
  const normalizedQuery = searchQuery.trim().toLowerCase();
  const matchedRows: MonitoringEventRow[] = [];
  let total = 0;

  rows.forEach((row) => {
    if (row.timestampMs > nowMs || row.timestampMs < startMs) {
      return;
    }

    if (normalizedQuery && !row.searchText.includes(normalizedQuery)) {
      return;
    }

    total += 1;
    if (limit <= 0 || matchedRows.length < limit) {
      matchedRows.push(row);
    }
  });

  return { rows: matchedRows, total };
};

const addRecentPatternRow = (recentRows: MonitoringEventRow[], row: MonitoringEventRow, limit = 10) => {
  const insertAt = recentRows.findIndex((item) => row.timestampMs > item.timestampMs);
  if (insertAt < 0) {
    if (recentRows.length < limit) {
      recentRows.push(row);
    }
    return;
  }
  recentRows.splice(insertAt, 0, row);
  if (recentRows.length > limit) {
    recentRows.pop();
  }
};

const recentPatternFromRows = (recentRows: MonitoringEventRow[]) =>
  [...recentRows].reverse().map((row) => !row.failed);

export const buildMonitoringSummary = (rows: MonitoringEventRow[]): MonitoringSummary => {
  const totalCalls = rows.length;
  let failureCalls = 0;
  let inputTokens = 0;
  let outputTokens = 0;
  let reasoningTokens = 0;
  let cachedTokens = 0;
  let totalTokens = 0;
  let totalCost = 0;
  let latencySum = 0;
  let latencyCount = 0;
  let recentCalls = 0;
  let recentTokens = 0;
  let zeroTokenCalls = 0;
  const taskMap = new Map<string, boolean>();
  const activeDays = new Set<string>();
  const zeroTokenModels = new Set<string>();
  const nowMs = Date.now();
  const windowStart = nowMs - 30 * 60 * 1000;

  rows.forEach((row) => {
    if (row.failed) failureCalls += 1;
    inputTokens += row.inputTokens;
    outputTokens += row.outputTokens;
    reasoningTokens += row.reasoningTokens;
    cachedTokens += row.cachedTokens;
    totalTokens += row.totalTokens;
    totalCost += row.totalCost;
    activeDays.add(row.dayKey);

    if (row.latencyMs !== null) {
      latencySum += row.latencyMs;
      latencyCount += 1;
    }

    taskMap.set(row.taskKey, (taskMap.get(row.taskKey) ?? false) || row.failed);

    if (row.totalTokens === 0) {
      zeroTokenCalls += 1;
      zeroTokenModels.add(row.model);
    }

    if (row.timestampMs >= windowStart && row.timestampMs <= nowMs) {
      recentCalls += 1;
      recentTokens += row.totalTokens;
    }
  });

  const successCalls = Math.max(totalCalls - failureCalls, 0);
  const approxTasks = taskMap.size;
  let approxTaskFailures = 0;
  taskMap.forEach((failed) => {
    if (failed) approxTaskFailures += 1;
  });
  const activeDayCount = Math.max(activeDays.size, 1);

  return {
    totalCalls,
    successCalls,
    failureCalls,
    successRate: totalCalls > 0 ? successCalls / totalCalls : 1,
    inputTokens,
    outputTokens,
    reasoningTokens,
    cachedTokens,
    totalTokens,
    totalCost,
    averageLatencyMs: latencyCount > 0 ? latencySum / latencyCount : null,
    rpm30m: recentCalls / 30,
    tpm30m: recentTokens / 30,
    avgDailyRequests: totalCalls / activeDayCount,
    avgDailyTokens: totalTokens / activeDayCount,
    approxTasks,
    approxTaskFailures,
    approxTaskSuccessRate:
      approxTasks > 0 ? Math.max(approxTasks - approxTaskFailures, 0) / approxTasks : 1,
    zeroTokenCalls,
    zeroTokenModels: Array.from(zeroTokenModels).sort(),
  };
};

export const buildAccountRows = ({
  rows,
  groupBy = 'account',
  includeRows = false,
}: {
  rows: MonitoringEventRow[];
  groupBy?: MonitoringAccountGroupBy;
  includeRows?: boolean;
}): MonitoringAccountRow[] => {
  const grouped = new Map<
    string,
    {
      id: string;
      account: string;
      accountMasked: string;
      apiKey: MonitoringApiKeyIdentity;
      authLabels: Set<string>;
      authIndices: Set<string>;
      channels: Set<string>;
      providers: Set<string>;
      modelMap: Map<
        string,
        {
          model: string;
          totalCalls: number;
          successCalls: number;
          failureCalls: number;
          inputTokens: number;
          outputTokens: number;
          cachedTokens: number;
          totalTokens: number;
          totalCost: number;
          lastSeenAt: number;
        }
      >;
      rows: MonitoringEventRow[];
      recentRows: MonitoringEventRow[];
      totalCalls: number;
      successCalls: number;
      failureCalls: number;
      inputTokens: number;
      outputTokens: number;
      cachedTokens: number;
      totalTokens: number;
      totalCost: number;
      latencySum: number;
      latencyCount: number;
      lastSeenAt: number;
    }
  >();

  const getGroupIdentity = (row: MonitoringEventRow) => {
    if (groupBy === 'apiKey') {
      return {
        id: row.clientApiKey.id,
        account: row.clientApiKey.masked,
        accountMasked: row.clientApiKey.masked,
        apiKey: row.clientApiKey,
      };
    }
    if (groupBy === 'model') {
      return {
        id: `model:${row.model}`,
        account: row.model,
        accountMasked: row.model,
        apiKey: {
          id: 'clientApiKey:none',
          hash: '-',
          masked: '-',
        },
      };
    }
    const accountKey = row.account || row.authLabel || row.source;
    const deletedCredentialKey = row.credentialDeleted && row.authIndex !== '-'
      ? `::${row.authIndex}`
      : '';
    const channelKey = row.channel && row.channel !== '-' ? row.channel : '';
    const groupId = channelKey
      ? `account:${accountKey}${deletedCredentialKey}::${channelKey}`
      : `account:${accountKey}${deletedCredentialKey}`;
    return {
      id: groupId,
      account: row.account,
      accountMasked: row.accountMasked,
      apiKey: row.clientApiKey,
    };
  };

  rows.forEach((row) => {
    const identity = getGroupIdentity(row);
    const existing = grouped.get(identity.id) ?? {
      id: identity.id,
      account: identity.account,
      accountMasked: identity.accountMasked,
      apiKey: identity.apiKey,
      authLabels: new Set<string>(),
      authIndices: new Set<string>(),
      channels: new Set<string>(),
      providers: new Set<string>(),
      modelMap: new Map(),
      rows: [] as MonitoringEventRow[],
      recentRows: [] as MonitoringEventRow[],
      totalCalls: 0,
      successCalls: 0,
      failureCalls: 0,
      inputTokens: 0,
      outputTokens: 0,
      cachedTokens: 0,
      totalTokens: 0,
      totalCost: 0,
      latencySum: 0,
      latencyCount: 0,
      lastSeenAt: 0,
    };

    if (includeRows) {
      existing.rows.push(row);
      addRecentPatternRow(existing.recentRows, row);
    }
    existing.authLabels.add(row.authLabel);
    existing.authIndices.add(row.authIndexMasked);
    existing.channels.add(row.channel);
    existing.providers.add(row.provider);
    existing.totalCalls += 1;
    existing.successCalls += row.failed ? 0 : 1;
    existing.failureCalls += row.failed ? 1 : 0;
    existing.inputTokens += row.inputTokens;
    existing.outputTokens += row.outputTokens;
    existing.cachedTokens += row.cachedTokens;
    existing.totalTokens += row.totalTokens;
    existing.totalCost += row.totalCost;
    existing.lastSeenAt = Math.max(existing.lastSeenAt, row.timestampMs);

    if (row.latencyMs !== null) {
      existing.latencySum += row.latencyMs;
      existing.latencyCount += 1;
    }

    const modelEntry = existing.modelMap.get(row.model) ?? {
      model: row.model,
      totalCalls: 0,
      successCalls: 0,
      failureCalls: 0,
      inputTokens: 0,
      outputTokens: 0,
      cachedTokens: 0,
      totalTokens: 0,
      totalCost: 0,
      lastSeenAt: 0,
    };

    modelEntry.totalCalls += 1;
    modelEntry.successCalls += row.failed ? 0 : 1;
    modelEntry.failureCalls += row.failed ? 1 : 0;
    modelEntry.inputTokens += row.inputTokens;
    modelEntry.outputTokens += row.outputTokens;
    modelEntry.cachedTokens += row.cachedTokens;
    modelEntry.totalTokens += row.totalTokens;
    modelEntry.totalCost += row.totalCost;
    modelEntry.lastSeenAt = Math.max(modelEntry.lastSeenAt, row.timestampMs);
    existing.modelMap.set(row.model, modelEntry);

    grouped.set(identity.id, existing);
  });

  return Array.from(grouped.values())
    .map((item) => ({
      id: item.id,
      group: groupBy,
      model: groupBy === 'model' ? item.account : '-',
      apiKeyHash: item.apiKey.hash,
      apiKeyMasked: item.apiKey.masked,
      account: item.account,
      accountMasked: item.accountMasked,
      authLabels: Array.from(item.authLabels).sort(),
      authIndices: Array.from(item.authIndices).sort(),
      channels: Array.from(item.channels).sort(),
      providers: Array.from(item.providers).sort(),
      totalCalls: item.totalCalls,
      successCalls: item.successCalls,
      failureCalls: item.failureCalls,
      successRate: item.totalCalls > 0 ? item.successCalls / item.totalCalls : 1,
      inputTokens: item.inputTokens,
      outputTokens: item.outputTokens,
      cachedTokens: item.cachedTokens,
      totalTokens: item.totalTokens,
      totalCost: item.totalCost,
      averageLatencyMs: item.latencyCount > 0 ? item.latencySum / item.latencyCount : null,
      lastSeenAt: item.lastSeenAt,
      recentPattern: includeRows ? recentPatternFromRows(item.recentRows) : [],
      rows: includeRows ? item.rows : undefined,
      models: Array.from(item.modelMap.values())
        .map((model) => ({
          ...model,
          successRate: model.totalCalls > 0 ? model.successCalls / model.totalCalls : 1,
        }))
        .sort((left, right) => right.totalCost - left.totalCost || right.totalCalls - left.totalCalls),
    }))
    .sort(
      (left, right) =>
        right.lastSeenAt - left.lastSeenAt ||
        right.totalCalls - left.totalCalls ||
        right.totalCost - left.totalCost
    );
};

export const buildAccountRowsByAccount = (rows: MonitoringEventRow[], includeRows = false) =>
  buildAccountRows({ rows, groupBy: 'account', includeRows });

export const buildAccountRowsByApiKey = (rows: MonitoringEventRow[]) =>
  buildAccountRows({ rows, groupBy: 'apiKey' });

export const buildAccountRowsByModel = (rows: MonitoringEventRow[]) =>
  buildAccountRows({ rows, groupBy: 'model' });

export const buildRealtimeMonitorRows = (rows: MonitoringEventRow[]): MonitoringRealtimeRow[] => {
  const grouped = new Map<
    string,
    {
      id: string;
      account: string;
      accountMasked: string;
      authLabel: string;
      authIndexMasked: string;
      provider: string;
      requestType: string;
      model: string;
      channel: string;
      rows: MonitoringEventRow[];
      recentRows: MonitoringEventRow[];
      latestFailed: boolean;
      successCalls: number;
      failureCalls: number;
      inputTokens: number;
      outputTokens: number;
      cachedTokens: number;
      totalTokens: number;
      totalCost: number;
      latencySum: number;
      latencyCount: number;
      latestLatencyMs: number | null;
      lastSeenAt: number;
    }
  >();

  rows.forEach((row) => {
    const requestType = `${row.endpointMethod} ${row.endpointPath}`.trim();
    const key = [
      row.account || row.authLabel || row.source,
      row.authIndexMasked,
      row.provider,
      row.model,
      row.channel,
      requestType,
    ].join('::');

    const existing = grouped.get(key) ?? {
      id: key,
      account: row.account,
      accountMasked: row.accountMasked,
      authLabel: row.authLabel,
      authIndexMasked: row.authIndexMasked,
      provider: row.provider,
      requestType,
      model: row.model,
      channel: row.channel,
      rows: [] as MonitoringEventRow[],
      recentRows: [] as MonitoringEventRow[],
      latestFailed: row.failed,
      successCalls: 0,
      failureCalls: 0,
      inputTokens: 0,
      outputTokens: 0,
      cachedTokens: 0,
      totalTokens: 0,
      totalCost: 0,
      latencySum: 0,
      latencyCount: 0,
      latestLatencyMs: null,
      lastSeenAt: 0,
    };

    existing.rows.push(row);
    addRecentPatternRow(existing.recentRows, row);
    existing.successCalls += row.failed ? 0 : 1;
    existing.failureCalls += row.failed ? 1 : 0;
    existing.inputTokens += row.inputTokens;
    existing.outputTokens += row.outputTokens;
    existing.cachedTokens += row.cachedTokens;
    existing.totalTokens += row.totalTokens;
    existing.totalCost += row.totalCost;

    if (row.timestampMs >= existing.lastSeenAt) {
      existing.lastSeenAt = row.timestampMs;
      existing.latestFailed = row.failed;
      existing.latestLatencyMs = row.latencyMs;
    }

    if (row.latencyMs !== null) {
      existing.latencySum += row.latencyMs;
      existing.latencyCount += 1;
    }

    grouped.set(key, existing);
  });

  return Array.from(grouped.values())
    .map((item) => {
      const totalCalls = item.successCalls + item.failureCalls;
      return {
        id: item.id,
        account: item.account,
        accountMasked: item.accountMasked,
        authLabel: item.authLabel,
        authIndexMasked: item.authIndexMasked,
        provider: item.provider,
        requestType: item.requestType,
        model: item.model,
        channel: item.channel,
        latestFailed: item.latestFailed,
        successRate: totalCalls > 0 ? item.successCalls / totalCalls : 1,
        totalCalls,
        successCalls: item.successCalls,
        failureCalls: item.failureCalls,
        averageLatencyMs: item.latencyCount > 0 ? item.latencySum / item.latencyCount : null,
        latestLatencyMs: item.latestLatencyMs,
        inputTokens: item.inputTokens,
        outputTokens: item.outputTokens,
        cachedTokens: item.cachedTokens,
        totalTokens: item.totalTokens,
        totalCost: item.totalCost,
        lastSeenAt: item.lastSeenAt,
        recentPattern: recentPatternFromRows(item.recentRows),
      };
    })
    .sort((left, right) => right.lastSeenAt - left.lastSeenAt || right.totalCalls - left.totalCalls);
};

const buildStatusChips = (metadata: MonitoringMetadata): MonitoringStatusChip[] => [
  {
    key: 'credentials',
    label: 'credentials',
    value: `${metadata.activeAuthFiles}/${metadata.totalAuthFiles}`,
    tone:
      metadata.totalAuthFiles === 0
        ? 'warn'
        : metadata.unavailableAuthFiles > 0
          ? 'warn'
          : 'good',
  },
  {
    key: 'channels',
    label: 'channels',
    value: `${metadata.enabledChannels}/${metadata.totalChannels}`,
    tone: metadata.enabledChannels === 0 ? 'bad' : metadata.enabledChannels < metadata.totalChannels ? 'warn' : 'good',
  },
  {
    key: 'runtime_only',
    label: 'runtime_only',
    value: String(metadata.runtimeOnlyAuthFiles),
    tone: metadata.runtimeOnlyAuthFiles > 0 ? 'warn' : 'good',
  },
  {
    key: 'models',
    label: 'models',
    value: String(metadata.configuredModels),
    tone: metadata.configuredModels > 0 ? 'good' : 'warn',
  },
];

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
      Math.max(Number(detail.tokens?.cache_tokens) || 0, 0)
    );
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
      stream: detail.stream === true,
      reasoningEffort: detail.reasoning_effort || '',
      serviceTier: detail.service_tier || '',
      costBreakdown: detail.cost_breakdown ?? null,
      inputTokens,
      outputTokens,
      reasoningTokens,
      cachedTokens,
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
        detail.reasoning_effort,
        authMeta?.planType,
        clientApiKeyIdentity.masked
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

export function useMonitoringData({
  usage,
  logUsage,
  serverFilteredLogs = false,
  config,
  modelPrices,
  timeRange,
  searchQuery,
  filteredRowLimit = 0,
  deletedCredentialLabel = DELETED_CREDENTIAL_FALLBACK_LABEL,
  unattributedApiKeyLabel = 'Unattributed API Key',
}: UseMonitoringDataParams): UseMonitoringDataReturn {
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

  const allRows = useMemo(() => {
    const details = collectUsageDetailsWithEndpoint(usage);
    return buildEventRows(
      details,
      authMetaMap,
      authFileMap,
      sourceInfoMap,
      channelByAuthIndex,
      configuredApiKeys,
      modelPrices,
      deletedCredentialLabel,
      unattributedApiKeyLabel
    );
  }, [authFileMap, authMetaMap, channelByAuthIndex, configuredApiKeys, deletedCredentialLabel, modelPrices, sourceInfoMap, unattributedApiKeyLabel, usage]);

  const logRows = useMemo(() => {
    if (logUsage === undefined) return allRows;
    const details = collectUsageDetailsWithEndpoint(logUsage);
    return buildEventRows(
      details,
      authMetaMap,
      authFileMap,
      sourceInfoMap,
      channelByAuthIndex,
      configuredApiKeys,
      modelPrices,
      deletedCredentialLabel,
      unattributedApiKeyLabel
    );
  }, [allRows, authFileMap, authMetaMap, channelByAuthIndex, configuredApiKeys, deletedCredentialLabel, logUsage, modelPrices, sourceInfoMap, unattributedApiKeyLabel]);

  const filteredRowState = useMemo(
    () => serverFilteredLogs
      ? { rows: logRows, total: logRows.length }
      : buildRangeFilteredRows(logRows, timeRange, searchQuery, filteredRowLimit),
    [filteredRowLimit, logRows, searchQuery, serverFilteredLogs, timeRange]
  );
  const filteredRows = filteredRowState.rows;
  const filteredRowCount = filteredRowState.total;
  const metadata = useMemo<MonitoringMetadata>(() => {
    const planTypes = Array.from(
      new Set(Array.from(authMetaMap.values()).map((item) => item.planType).filter((item) => item && item !== '-'))
    ).sort();

    return {
      totalAuthFiles: authFiles.length,
      activeAuthFiles: Array.from(authMetaMap.values()).filter(
        (item) => !item.disabled && !item.unavailable && item.status === 'active'
      ).length,
      unavailableAuthFiles: Array.from(authMetaMap.values()).filter((item) => item.unavailable).length,
      runtimeOnlyAuthFiles: Array.from(authMetaMap.values()).filter((item) => item.runtimeOnly).length,
      totalChannels: channels.length,
      enabledChannels: channels.filter((item) => !item.disabled).length,
      configuredModels: Array.from(new Set(channels.flatMap((item) => item.modelNames))).length,
      planTypes,
    };
  }, [authFiles.length, authMetaMap, channels]);

  const statusChips = useMemo(() => buildStatusChips(metadata), [metadata]);

  return {
    loading,
    error,
    authFiles,
    channels,
    summary: EMPTY_MONITORING_SUMMARY,
    metadata,
    statusChips,
    timeline: [],
    timelineGranularity: 'day',
    hourlyDistribution: [],
    modelShareRows: [],
    channelRows: [],
    modelRows: [],
    failureSourceRows: [],
    taskBuckets: [],
    recentFailures: [],
    allRows,
    filteredRows,
    filteredRowCount,
    refreshMeta,
  };
}
