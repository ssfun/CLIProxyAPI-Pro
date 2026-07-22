import type { AuthFileItem } from '@/types';
import { maskSensitiveText } from '@/utils/format';
import { calculateCost, normalizeAuthIndex, type ModelPrice } from '@/utils/usage';
import type {
  MonitoringAccountRow,
  MonitoringEventRow,
  MonitoringSummary,
} from './hooks/useMonitoringData';
import type { UsageAggregateBucket } from './hooks/useUsageAggregates';

export const calculateAggregateCost = (
  item: Pick<UsageAggregateBucket, 'model' | 'inputTokens' | 'outputTokens' | 'cacheTokens' | 'estimatedCost'>,
  modelPrices: Record<string, ModelPrice>
) => Number.isFinite(Number(item.estimatedCost)) && Number(item.estimatedCost) >= 0
  ? Number(item.estimatedCost)
  : calculateCost({
    __modelName: item.model || '',
    tokens: {
      input_tokens: item.inputTokens,
      output_tokens: item.outputTokens,
      cached_tokens: item.cacheTokens,
      cache_tokens: item.cacheTokens,
    },
  }, modelPrices);

export const buildAggregateSummary = (
  buckets: UsageAggregateBucket[],
  modelPrices: Record<string, ModelPrice>
): MonitoringSummary => {
  let totalCalls = 0;
  let successCalls = 0;
  let failureCalls = 0;
  let inputTokens = 0;
  let outputTokens = 0;
  let reasoningTokens = 0;
  let cachedTokens = 0;
  let totalTokens = 0;
  let totalCost = 0;
  let weightedLatency = 0;
  let latencyCalls = 0;
  buckets.forEach((bucket) => {
    totalCalls += bucket.totalRequests;
    successCalls += bucket.successCount;
    failureCalls += bucket.failureCount;
    inputTokens += bucket.inputTokens;
    outputTokens += bucket.outputTokens;
    reasoningTokens += bucket.reasoningTokens;
    cachedTokens += bucket.cacheTokens;
    totalTokens += bucket.totalTokens;
    totalCost += calculateAggregateCost(bucket, modelPrices);
    if (typeof bucket.avgLatencyMs === 'number' && bucket.totalRequests > 0) {
      weightedLatency += bucket.avgLatencyMs * bucket.totalRequests;
      latencyCalls += bucket.totalRequests;
    }
  });
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
    averageLatencyMs: latencyCalls > 0 ? weightedLatency / latencyCalls : null,
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
};

export const buildServerAccountRows = (
  buckets: UsageAggregateBucket[],
  realtimeRows: MonitoringEventRow[],
  authFilesByAuthIndex: Map<string, AuthFileItem>,
  modelPrices: Record<string, ModelPrice>,
  deletedCredentialLabel: string
): MonitoringAccountRow[] => {
  const metadataByAuthIndex = new Map<string, MonitoringEventRow>();
  const realtimeRowsByAuthIndex = new Map<string, MonitoringEventRow[]>();
  realtimeRows.forEach((row) => {
    if (row.authIndex !== '-' && !metadataByAuthIndex.has(row.authIndex)) {
      metadataByAuthIndex.set(row.authIndex, row);
    }
    if (row.authIndex !== '-') {
      const items = realtimeRowsByAuthIndex.get(row.authIndex) ?? [];
      items.push(row);
      realtimeRowsByAuthIndex.set(row.authIndex, items);
    }
  });

  const grouped = new Map<string, MonitoringAccountRow>();
  buckets.forEach((bucket) => {
    const authIndex = normalizeAuthIndex(bucket.authIndex) ?? '-';
    const metadata = metadataByAuthIndex.get(authIndex);
    const authFile = authFilesByAuthIndex.get(authIndex);
    const authFileLabel = authFile
      ? [authFile.email, authFile.account, authFile.label, authFile.name]
          .map((value) => typeof value === 'string' ? value.trim() : '')
          .find(Boolean) || ''
      : '';
    const fallbackAccount = authIndex === '-' ? deletedCredentialLabel : maskSensitiveText(authIndex);
    const account = metadata?.account || metadata?.authLabel || authFileLabel || fallbackAccount;
    const accountMasked = metadata?.accountMasked || metadata?.authIndexMasked || maskSensitiveText(account);
    const provider = bucket.provider || metadata?.provider || '-';
    const channel = metadata?.channel || provider;
    const id = `account:${account}::${channel}`;
    const current = grouped.get(id) ?? {
      id,
      group: 'account',
      model: '-',
      apiKeyHash: '-',
      apiKeyMasked: '-',
      account,
      accountMasked,
      authLabels: [],
      authIndices: [],
      channels: [],
      providers: [],
      totalCalls: 0,
      successCalls: 0,
      failureCalls: 0,
      successRate: 1,
      inputTokens: 0,
      outputTokens: 0,
      cachedTokens: 0,
      totalTokens: 0,
      totalCost: 0,
      averageLatencyMs: null,
      lastSeenAt: 0,
      recentPattern: [],
      rows: [],
      models: [],
    } satisfies MonitoringAccountRow;

    current.totalCalls += bucket.totalRequests;
    current.successCalls += bucket.successCount;
    current.failureCalls += bucket.failureCount;
    current.inputTokens += bucket.inputTokens;
    current.outputTokens += bucket.outputTokens;
    current.cachedTokens += bucket.cacheTokens;
    current.totalTokens += bucket.totalTokens;
    current.totalCost += calculateAggregateCost(bucket, modelPrices);
    current.lastSeenAt = Math.max(current.lastSeenAt, Number(bucket.lastSeenAtMs) || bucket.bucketStartMs || 0);
    current.successRate = current.totalCalls > 0 ? current.successCalls / current.totalCalls : 1;
    current.authLabels = Array.from(new Set([...current.authLabels, metadata?.authLabel || account]));
    current.authIndices = Array.from(new Set([...current.authIndices, metadata?.authIndexMasked || maskSensitiveText(authIndex)]));
    current.channels = Array.from(new Set([...current.channels, channel]));
    current.providers = Array.from(new Set([...current.providers, provider]));
    current.rows = Array.from(new Map([
      ...(current.rows ?? []).map((row) => [row.id, row] as const),
      ...(realtimeRowsByAuthIndex.get(authIndex) ?? []).map((row) => [row.id, row] as const),
    ]).values());
    current.recentPattern = (current.rows ?? []).slice(0, 10).reverse().map((row) => !row.failed);

    const existingModel = current.models.find((item) => item.model === (bucket.model || '-'));
    const modelCost = calculateAggregateCost(bucket, modelPrices);
    if (existingModel) {
      existingModel.totalCalls += bucket.totalRequests;
      existingModel.successCalls += bucket.successCount;
      existingModel.failureCalls += bucket.failureCount;
      existingModel.inputTokens += bucket.inputTokens;
      existingModel.outputTokens += bucket.outputTokens;
      existingModel.cachedTokens += bucket.cacheTokens;
      existingModel.totalTokens += bucket.totalTokens;
      existingModel.totalCost += modelCost;
      existingModel.lastSeenAt = Math.max(existingModel.lastSeenAt, Number(bucket.lastSeenAtMs) || 0);
      existingModel.successRate = existingModel.totalCalls > 0 ? existingModel.successCalls / existingModel.totalCalls : 1;
    } else {
      current.models.push({
        model: bucket.model || '-',
        totalCalls: bucket.totalRequests,
        successCalls: bucket.successCount,
        failureCalls: bucket.failureCount,
        successRate: bucket.totalRequests > 0 ? bucket.successCount / bucket.totalRequests : 1,
        inputTokens: bucket.inputTokens,
        outputTokens: bucket.outputTokens,
        cachedTokens: bucket.cacheTokens,
        totalTokens: bucket.totalTokens,
        totalCost: modelCost,
        lastSeenAt: Number(bucket.lastSeenAtMs) || 0,
      });
    }
    grouped.set(id, current);
  });

  return Array.from(grouped.values()).map((row) => ({
    ...row,
    models: [...row.models].sort((left, right) => right.totalCost - left.totalCost || right.totalCalls - left.totalCalls),
  }));
};
