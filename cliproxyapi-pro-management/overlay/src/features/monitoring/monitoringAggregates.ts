import { calculateCost, type ModelPrice } from '@/utils/usage';
import type { MonitoringSummary } from './hooks/useMonitoringData';
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
  let cacheInputTokens = 0;
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
    cacheInputTokens += bucket.cacheInputTokens;
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
    cacheInputTokens,
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
