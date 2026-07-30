import { describe, expect, test } from 'bun:test';
import {
  buildAggregateSummary,
} from '../src/features/monitoring/monitoringAggregates';
import type { UsageAggregateBucket } from '../src/features/monitoring/hooks/useUsageAggregates';

const bucket = (overrides: Partial<UsageAggregateBucket> = {}): UsageAggregateBucket => ({
  bucketStartMs: 100,
  bucketStart: '1970-01-01T00:00:00Z',
  provider: 'codex',
  model: 'gpt-test',
  authIndex: 'auth-1',
  totalRequests: 2,
  successCount: 1,
  failureCount: 1,
  totalTokens: 30,
  inputTokens: 10,
  outputTokens: 20,
  reasoningTokens: 3,
  cacheTokens: 4,
  cacheInputTokens: 10,
  cacheReadTokens: 4,
  cacheWriteTokens: 0,
  estimatedCost: 1.25,
  avgLatencyMs: 50,
  ...overrides,
});

describe('monitoring server aggregates', () => {
  test('combines summary totals and weights latency by request count', () => {
    const summary = buildAggregateSummary([
      bucket(),
      bucket({ totalRequests: 1, successCount: 1, failureCount: 0, avgLatencyMs: 200, estimatedCost: 0.75 }),
    ], {});

    expect(summary.totalCalls).toBe(3);
    expect(summary.successCalls).toBe(2);
    expect(summary.totalCost).toBe(2);
    expect(summary.cacheInputTokens).toBe(20);
    expect(summary.averageLatencyMs).toBeCloseTo(100);
  });
});
