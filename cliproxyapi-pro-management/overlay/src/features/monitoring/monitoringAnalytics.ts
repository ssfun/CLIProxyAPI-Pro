import type { TFunction } from 'i18next';
import {
  buildDayLabel,
  buildHourLabel,
  buildLocalDayKey,
  formatShortDateTime,
  getRangeStartMs,
  type MonitoringAccountRow,
  type MonitoringEventRow,
  type MonitoringSummary,
  type MonitoringTimeRange,
} from './hooks/useMonitoringData';
import type { UsageAggregateBucket, UsageAggregates } from './hooks/useUsageAggregates';
import { calculateAggregateCost } from './monitoringAggregates';
import { maskSensitiveText } from '@/utils/format';
import { formatCompactNumber, formatUsd, type ModelPrice } from '@/utils/usage';

export const formatPercent = (value: number) => `${(value * 100).toFixed(1)}%`;

export type RankingMetric = 'requests' | 'tokens' | 'cost';
export type AccountSortMetric = 'recent' | RankingMetric;

export type TrendPoint = {
  key: string;
  label: string;
  requests: number;
  failures: number;
  tokens: number;
  cost: number;
};

export type TokenDistributionPoint = {
  key: string;
  label: string;
  requests: number;
  totalTokens: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cachedTokens: number;
  totalCost: number;
};

type RankingRowAccumulator = {
  id: string;
  group: 'apiKey' | 'model';
  model: string;
  apiKeyHash: string;
  apiKeyMasked: string;
  account: string;
  accountMasked: string;
  authLabels: Set<string>;
  authIndices: Set<string>;
  channels: Set<string>;
  providers: Set<string>;
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
};

export type UsageTrendAnalytics = {
  apiKeyOptions: Array<{ value: string; label: string }>;
  trendPoints: TrendPoint[];
  tokenDistributionPoints: TokenDistributionPoint[];
  modelRows: MonitoringAccountRow[];
  apiKeyRows: MonitoringAccountRow[];
  scopedTotals: Record<RankingMetric, number>;
};

export type AccountHealthTone = 'good' | 'warn' | 'bad';

type MonitoringSummaryAccumulator = {
  totalCalls: number;
  failureCalls: number;
  inputTokens: number;
  outputTokens: number;
  reasoningTokens: number;
  cachedTokens: number;
  totalTokens: number;
  totalCost: number;
  latencySum: number;
  latencyCount: number;
  recentCalls: number;
  recentTokens: number;
  zeroTokenCalls: number;
  taskMap: Map<string, boolean>;
  activeDays: Set<string>;
  zeroTokenModels: Set<string>;
};

export const createMonitoringSummaryAccumulator = (): MonitoringSummaryAccumulator => ({
  totalCalls: 0,
  failureCalls: 0,
  inputTokens: 0,
  outputTokens: 0,
  reasoningTokens: 0,
  cachedTokens: 0,
  totalTokens: 0,
  totalCost: 0,
  latencySum: 0,
  latencyCount: 0,
  recentCalls: 0,
  recentTokens: 0,
  zeroTokenCalls: 0,
  taskMap: new Map(),
  activeDays: new Set(),
  zeroTokenModels: new Set(),
});

export const addMonitoringSummaryRow = (
  accumulator: MonitoringSummaryAccumulator,
  row: MonitoringEventRow,
  windowStartMs: number,
  nowMs: number
) => {
  accumulator.totalCalls += 1;
  if (row.failed) accumulator.failureCalls += 1;
  accumulator.inputTokens += row.inputTokens;
  accumulator.outputTokens += row.outputTokens;
  accumulator.reasoningTokens += row.reasoningTokens;
  accumulator.cachedTokens += row.cachedTokens;
  accumulator.totalTokens += row.totalTokens;
  accumulator.totalCost += row.totalCost;
  accumulator.activeDays.add(row.dayKey);

  if (row.latencyMs !== null) {
    accumulator.latencySum += row.latencyMs;
    accumulator.latencyCount += 1;
  }

  accumulator.taskMap.set(row.taskKey, (accumulator.taskMap.get(row.taskKey) ?? false) || row.failed);

  if (row.totalTokens === 0) {
    accumulator.zeroTokenCalls += 1;
    accumulator.zeroTokenModels.add(row.model);
  }

  if (row.timestampMs >= windowStartMs && row.timestampMs <= nowMs) {
    accumulator.recentCalls += 1;
    accumulator.recentTokens += row.totalTokens;
  }
};

export const finalizeMonitoringSummary = (accumulator: MonitoringSummaryAccumulator): MonitoringSummary => {
  const successCalls = Math.max(accumulator.totalCalls - accumulator.failureCalls, 0);
  let approxTaskFailures = 0;
  accumulator.taskMap.forEach((failed) => {
    if (failed) approxTaskFailures += 1;
  });
  const activeDayCount = Math.max(accumulator.activeDays.size, 1);

  return {
    totalCalls: accumulator.totalCalls,
    successCalls,
    failureCalls: accumulator.failureCalls,
    successRate: accumulator.totalCalls > 0 ? successCalls / accumulator.totalCalls : 1,
    inputTokens: accumulator.inputTokens,
    outputTokens: accumulator.outputTokens,
    reasoningTokens: accumulator.reasoningTokens,
    cachedTokens: accumulator.cachedTokens,
    totalTokens: accumulator.totalTokens,
    totalCost: accumulator.totalCost,
    averageLatencyMs: accumulator.latencyCount > 0 ? accumulator.latencySum / accumulator.latencyCount : null,
    rpm30m: accumulator.recentCalls / 30,
    tpm30m: accumulator.recentTokens / 30,
    avgDailyRequests: accumulator.totalCalls / activeDayCount,
    avgDailyTokens: accumulator.totalTokens / activeDayCount,
    approxTasks: accumulator.taskMap.size,
    approxTaskFailures,
    approxTaskSuccessRate:
      accumulator.taskMap.size > 0
        ? Math.max(accumulator.taskMap.size - approxTaskFailures, 0) / accumulator.taskMap.size
        : 1,
    zeroTokenCalls: accumulator.zeroTokenCalls,
    zeroTokenModels: Array.from(accumulator.zeroTokenModels).sort(),
  };
};

export const getRankingMetricValue = (row: MonitoringAccountRow, metric: RankingMetric) => {
  if (metric === 'cost') return row.totalCost;
  if (metric === 'tokens') return row.totalTokens;
  return row.totalCalls;
};

export const getAccountSortValue = (row: MonitoringAccountRow, metric: AccountSortMetric) => {
  if (metric === 'recent') return row.lastSeenAt;
  return getRankingMetricValue(row, metric);
};

export const getRankingMetricLabel = (metric: RankingMetric, t: TFunction) => {
  if (metric === 'cost') return t('monitoring.ranking_metric_cost');
  if (metric === 'tokens') return t('monitoring.ranking_metric_tokens');
  return t('monitoring.ranking_metric_requests');
};

export const getRankingSummaryLabel = (metric: RankingMetric, t: TFunction) => {
  if (metric === 'cost') return t('monitoring.ranking_summary_cost');
  if (metric === 'tokens') return t('monitoring.ranking_summary_tokens');
  return t('monitoring.ranking_summary_calls');
};

export const formatRankingMetricValue = (value: number, metric: RankingMetric, hasPrices: boolean) => {
  if (metric === 'cost') return hasPrices ? formatUsd(value) : '--';
  return formatCompactNumber(value);
};

export const getAccountHealthTone = (row: MonitoringAccountRow): AccountHealthTone => {
  if (row.successRate >= 0.95) return 'good';
  if (row.successRate >= 0.85) return 'warn';
  return 'bad';
};

export const getProgressWidth = (value: number) => {
  if (value <= 0) return '0%';
  return `${Math.max(value * 100, 1.5)}%`;
};

export const getChartAxisLabels = <T extends { key: string; label: string }>(points: T[]) => {
  if (points.length <= 10) {
    return points.map((point, index) => ({ key: point.key, label: point.label, index }));
  }

  const step = Math.ceil((points.length - 1) / 8);
  const labels = points
    .map((point, index) => ({ key: point.key, label: point.label, index }))
    .filter((_, index) => index % step === 0);
  const last = points[points.length - 1];
  if (!labels.some((item) => item.key === last.key)) {
    labels.push({ key: last.key, label: last.label, index: points.length - 1 });
  }
  return labels;
};

export const buildUsageTrendRangeLabel = (range: MonitoringTimeRange, t: TFunction) => {
  if (range === 'all') return t('monitoring.all_retained_logs');

  const nowMs = Date.now();
  return `${formatShortDateTime(getRangeStartMs(range, nowMs))} - ${formatShortDateTime(nowMs)}`;
};

const getEmptyTrendPoint = (key: string, label: string): TrendPoint => ({
  key,
  label,
  requests: 0,
  failures: 0,
  tokens: 0,
  cost: 0,
});

const buildFilledTrendBuckets = (range: MonitoringTimeRange, nowMs: number) => {
  if (range === 'all') return [];

  const startMs = getRangeStartMs(range, nowMs);
  const buckets: TrendPoint[] = [];
  const cursor = new Date(startMs);

  if (range === 'today') {
    const now = new Date(nowMs);
    cursor.setMinutes(0, 0, 0);
    while (cursor.getTime() <= now.getTime()) {
      const dayKey = buildLocalDayKey(cursor.getTime());
      const label = buildHourLabel(cursor.getTime());
      buckets.push(getEmptyTrendPoint(`${dayKey} ${label}`, label));
      cursor.setHours(cursor.getHours() + 1);
    }
    return buckets;
  }

  cursor.setHours(0, 0, 0, 0);
  const end = new Date(nowMs);
  end.setHours(0, 0, 0, 0);
  while (cursor.getTime() <= end.getTime()) {
    const key = buildLocalDayKey(cursor.getTime());
    buckets.push(getEmptyTrendPoint(key, buildDayLabel(key)));
    cursor.setDate(cursor.getDate() + 1);
  }
  return buckets;
};

const buildTimeBucketMeta = (range: MonitoringTimeRange) => {
  const useHourly = range === 'today';
  return {
    useHourly,
    getKey: (row: MonitoringEventRow) => (useHourly ? `${row.dayKey} ${row.hourLabel}` : row.dayKey),
    getLabel: (row: MonitoringEventRow) => (useHourly ? row.hourLabel : buildDayLabel(row.dayKey)),
  };
};

const createRankingRowAccumulator = (
  row: MonitoringEventRow,
  group: 'apiKey' | 'model'
): RankingRowAccumulator => {
  if (group === 'apiKey') {
    return {
      id: row.clientApiKey.id,
      group,
      model: '-',
      apiKeyHash: row.clientApiKey.hash,
      apiKeyMasked: row.clientApiKey.masked,
      account: row.clientApiKey.masked,
      accountMasked: row.clientApiKey.masked,
      authLabels: new Set(),
      authIndices: new Set(),
      channels: new Set(),
      providers: new Set(),
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
  }

  return {
    id: `model:${row.model}`,
    group,
    model: row.model,
    apiKeyHash: '-',
    apiKeyMasked: '-',
    account: row.model,
    accountMasked: row.model,
    authLabels: new Set(),
    authIndices: new Set(),
    channels: new Set(),
    providers: new Set(),
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
};

const addRankingRow = (accumulator: RankingRowAccumulator, row: MonitoringEventRow) => {
  accumulator.authLabels.add(row.authLabel);
  accumulator.authIndices.add(row.authIndexMasked);
  accumulator.channels.add(row.channel);
  accumulator.providers.add(row.provider);
  accumulator.totalCalls += 1;
  accumulator.successCalls += row.failed ? 0 : 1;
  accumulator.failureCalls += row.failed ? 1 : 0;
  accumulator.inputTokens += row.inputTokens;
  accumulator.outputTokens += row.outputTokens;
  accumulator.cachedTokens += row.cachedTokens;
  accumulator.totalTokens += row.totalTokens;
  accumulator.totalCost += row.totalCost;
  accumulator.lastSeenAt = Math.max(accumulator.lastSeenAt, row.timestampMs);

  if (row.latencyMs !== null) {
    accumulator.latencySum += row.latencyMs;
    accumulator.latencyCount += 1;
  }
};

const finalizeRankingRows = (grouped: Map<string, RankingRowAccumulator>): MonitoringAccountRow[] =>
  Array.from(grouped.values()).map((item) => ({
    id: item.id,
    group: item.group,
    model: item.model,
    apiKeyHash: item.apiKeyHash,
    apiKeyMasked: item.apiKeyMasked,
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
    recentPattern: [],
    models: [],
  }));

export const buildUsageTrendAnalytics = (
  rows: MonitoringEventRow[],
  range: MonitoringTimeRange,
  apiKeyFilter: string,
  allApiKeyLabel: string
): UsageTrendAnalytics => {
  const nowMs = Date.now();
  const prefilled = buildFilledTrendBuckets(range, nowMs);
  const trendGrouped = new Map<string, TrendPoint>(prefilled.map((point) => [point.key, point]));
  const tokenGrouped = new Map<string, TokenDistributionPoint>();
  const modelGrouped = new Map<string, RankingRowAccumulator>();
  const apiKeyGrouped = new Map<string, RankingRowAccumulator>();
  const apiKeyLabels = new Map<string, string>();
  const { getKey, getLabel } = buildTimeBucketMeta(range);
  const scopedTotals: Record<RankingMetric, number> = {
    requests: 0,
    tokens: 0,
    cost: 0,
  };

  rows.forEach((row) => {
    const apiKeyHash = row.clientApiKey.hash;
    if (apiKeyHash && apiKeyHash !== '-') {
      apiKeyLabels.set(apiKeyHash, row.clientApiKey.masked);
    }

    const apiKeyAccumulator = apiKeyGrouped.get(row.clientApiKey.id) ?? createRankingRowAccumulator(row, 'apiKey');
    addRankingRow(apiKeyAccumulator, row);
    apiKeyGrouped.set(apiKeyAccumulator.id, apiKeyAccumulator);

    if (apiKeyFilter !== 'all' && apiKeyHash !== apiKeyFilter) {
      return;
    }

    const key = getKey(row);
    const label = getLabel(row);
    const trendPoint = trendGrouped.get(key) ?? {
      key,
      label,
      requests: 0,
      failures: 0,
      tokens: 0,
      cost: 0,
    };
    trendPoint.requests += 1;
    trendPoint.failures += row.failed ? 1 : 0;
    trendPoint.tokens += row.totalTokens;
    trendPoint.cost += row.totalCost;
    trendGrouped.set(key, trendPoint);

    const tokenPoint = tokenGrouped.get(key) ?? {
      key,
      label,
      requests: 0,
      totalTokens: 0,
      inputTokens: 0,
      outputTokens: 0,
      reasoningTokens: 0,
      cachedTokens: 0,
      totalCost: 0,
    };
    tokenPoint.requests += 1;
    tokenPoint.totalTokens += row.totalTokens;
    tokenPoint.inputTokens += row.inputTokens;
    tokenPoint.outputTokens += row.outputTokens;
    tokenPoint.reasoningTokens += row.reasoningTokens;
    tokenPoint.cachedTokens += row.cachedTokens;
    tokenPoint.totalCost += row.totalCost;
    tokenGrouped.set(key, tokenPoint);

    const modelAccumulator = modelGrouped.get(row.model) ?? createRankingRowAccumulator(row, 'model');
    addRankingRow(modelAccumulator, row);
    modelGrouped.set(row.model, modelAccumulator);

    scopedTotals.requests += 1;
    scopedTotals.tokens += row.totalTokens;
    scopedTotals.cost += row.totalCost;
  });

  const apiKeyOptions = [
    { value: 'all', label: allApiKeyLabel },
    ...Array.from(apiKeyLabels.entries())
      .sort((left, right) => left[1].localeCompare(right[1]))
      .map(([value, label]) => ({ value, label })),
  ];

  return {
    apiKeyOptions,
    trendPoints: Array.from(trendGrouped.values()).sort((left, right) => left.key.localeCompare(right.key)).slice(-24),
    tokenDistributionPoints: Array.from(tokenGrouped.values()).sort((left, right) => left.key.localeCompare(right.key)).slice(-24),
    modelRows: finalizeRankingRows(modelGrouped),
    apiKeyRows: finalizeRankingRows(apiKeyGrouped),
    scopedTotals,
  };
};

const createAggregateRankingRow = (
  item: UsageAggregateBucket,
  group: 'apiKey' | 'model',
  modelPrices: Record<string, ModelPrice>,
  apiKeyLabels: Map<string, string>
): MonitoringAccountRow => {
  const model = item.model || '-';
  const apiKeyHash = item.apiKeyHash || '-';
  const apiKeyMasked = apiKeyLabels.get(apiKeyHash) || maskSensitiveText(apiKeyHash);
  const totalCost = calculateAggregateCost(item, modelPrices);
  return {
    id: group === 'model' ? `model:${model}` : `clientApiKey:${apiKeyHash}`,
    group,
    model: group === 'model' ? model : '-',
    apiKeyHash: group === 'apiKey' ? apiKeyHash : '-',
    apiKeyMasked: group === 'apiKey' ? apiKeyMasked : '-',
    account: group === 'model' ? model : apiKeyMasked,
    accountMasked: group === 'model' ? model : apiKeyMasked,
    authLabels: [],
    authIndices: [],
    channels: [],
    providers: item.provider ? [item.provider] : [],
    totalCalls: item.totalRequests,
    successCalls: item.successCount,
    failureCalls: item.failureCount,
    successRate: item.totalRequests > 0 ? item.successCount / item.totalRequests : 1,
    inputTokens: item.inputTokens,
    outputTokens: item.outputTokens,
    cachedTokens: item.cacheTokens,
    totalTokens: item.totalTokens,
    totalCost,
    averageLatencyMs: item.avgLatencyMs ?? null,
    lastSeenAt: item.bucketStartMs,
    recentPattern: [],
    models: [],
  };
};

export const buildServerUsageTrendAnalytics = (
  aggregates: UsageAggregates | null,
  range: MonitoringTimeRange,
  modelPrices: Record<string, ModelPrice>,
  apiKeyOptions: Array<{ value: string; label: string }>,
  apiKeyFilter: string,
  unattributedApiKeyLabel: string
): UsageTrendAnalytics | null => {
  if (!aggregates) return null;
  const nowMs = Date.now();
  const prefilled = buildFilledTrendBuckets(range, nowMs);
  const trendGrouped = new Map<string, TrendPoint>(prefilled.map((point) => [point.key, point]));
  const tokenGrouped = new Map<string, TokenDistributionPoint>();
  const apiKeyLabels = new Map(apiKeyOptions.map((option) => [option.value, option.label]));
  apiKeyLabels.set('-', unattributedApiKeyLabel);
  aggregates.apiKeys.forEach((item) => {
    const apiKeyHash = item.apiKeyHash?.trim();
    if (apiKeyHash && !apiKeyLabels.has(apiKeyHash)) {
      apiKeyLabels.set(apiKeyHash, maskSensitiveText(apiKeyHash));
    }
  });
  const resolvedApiKeyOptions = [
    apiKeyOptions.find((option) => option.value === 'all') ?? { value: 'all', label: 'All' },
    ...Array.from(apiKeyLabels.entries())
      .filter(([value]) => value !== 'all' && value !== '-')
      .sort((left, right) => left[1].localeCompare(right[1]))
      .map(([value, label]) => ({ value, label })),
  ];
  const modelRowMap = new Map<string, MonitoringAccountRow>();
  aggregates.models
    .filter((item) => apiKeyFilter === 'all' || item.apiKeyHash === apiKeyFilter)
    .forEach((item) => {
      const row = createAggregateRankingRow(item, 'model', modelPrices, apiKeyLabels);
      const current = modelRowMap.get(row.model);
      if (!current) {
        modelRowMap.set(row.model, row);
        return;
      }
      const previousCalls = current.totalCalls;
      current.totalCalls += row.totalCalls;
      current.successCalls += row.successCalls;
      current.failureCalls += row.failureCalls;
      current.inputTokens += row.inputTokens;
      current.outputTokens += row.outputTokens;
      current.cachedTokens += row.cachedTokens;
      current.totalTokens += row.totalTokens;
      current.totalCost += row.totalCost;
      current.lastSeenAt = Math.max(current.lastSeenAt, row.lastSeenAt);
      if (row.averageLatencyMs !== null) {
        const weightedCurrent = (current.averageLatencyMs ?? 0) * previousCalls;
        current.averageLatencyMs = (weightedCurrent + row.averageLatencyMs * row.totalCalls) / Math.max(current.totalCalls, 1);
      }
      current.successRate = current.totalCalls > 0 ? current.successCalls / current.totalCalls : 1;
    });
  const modelRows = Array.from(modelRowMap.values());
  const apiKeyRowMap = new Map<string, MonitoringAccountRow>();
  aggregates.apiKeys.forEach((item) => {
    const row = createAggregateRankingRow(item, 'apiKey', modelPrices, apiKeyLabels);
    const current = apiKeyRowMap.get(row.apiKeyHash);
    if (!current) {
      apiKeyRowMap.set(row.apiKeyHash, row);
      return;
    }
    current.totalCalls += row.totalCalls;
    current.successCalls += row.successCalls;
    current.failureCalls += row.failureCalls;
    current.inputTokens += row.inputTokens;
    current.outputTokens += row.outputTokens;
    current.cachedTokens += row.cachedTokens;
    current.totalTokens += row.totalTokens;
    current.totalCost += row.totalCost;
    current.lastSeenAt = Math.max(current.lastSeenAt, row.lastSeenAt);
    current.successRate = current.totalCalls > 0 ? current.successCalls / current.totalCalls : 1;
  });
  const apiKeyRows = Array.from(apiKeyRowMap.values());

  aggregates.trend.forEach((item) => {
    const timestampMs = Number(item.bucketStartMs) || Date.parse(item.bucketStart);
    const dayKey = buildLocalDayKey(timestampMs);
    const useHourly = range === 'today';
    const key = useHourly ? `${dayKey} ${buildHourLabel(timestampMs)}` : dayKey;
    const label = useHourly ? buildHourLabel(timestampMs) : buildDayLabel(dayKey);
    const cost = calculateAggregateCost(item, modelPrices);
    const trendPoint = trendGrouped.get(key) ?? getEmptyTrendPoint(key, label);
    trendPoint.requests += item.totalRequests;
    trendPoint.failures += item.failureCount;
    trendPoint.tokens += item.totalTokens;
    trendPoint.cost += cost;
    trendGrouped.set(key, trendPoint);

    const tokenPoint = tokenGrouped.get(key) ?? {
      key,
      label,
      requests: 0,
      totalTokens: 0,
      inputTokens: 0,
      outputTokens: 0,
      reasoningTokens: 0,
      cachedTokens: 0,
      totalCost: 0,
    };
    tokenPoint.requests += item.totalRequests;
    tokenPoint.totalTokens += item.totalTokens;
    tokenPoint.inputTokens += item.inputTokens;
    tokenPoint.outputTokens += item.outputTokens;
    tokenPoint.reasoningTokens += item.reasoningTokens;
    tokenPoint.cachedTokens += item.cacheTokens;
    tokenPoint.totalCost += cost;
    tokenGrouped.set(key, tokenPoint);
  });

  const scopedTotals = modelRows.reduce<Record<RankingMetric, number>>((totals, row) => ({
    requests: totals.requests + row.totalCalls,
    tokens: totals.tokens + row.totalTokens,
    cost: totals.cost + row.totalCost,
  }), { requests: 0, tokens: 0, cost: 0 });

  return {
    apiKeyOptions: resolvedApiKeyOptions,
    trendPoints: Array.from(trendGrouped.values()).sort((left, right) => left.key.localeCompare(right.key)).slice(-24),
    tokenDistributionPoints: Array.from(tokenGrouped.values()).sort((left, right) => left.key.localeCompare(right.key)).slice(-24),
    modelRows,
    apiKeyRows,
    scopedTotals,
  };
};
