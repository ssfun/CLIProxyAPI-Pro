import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import {
  addMonitoringSummaryRow,
  aggregateTrendPointsForDisplay,
  buildServerUsageTrendAnalytics,
  buildUsageTrendAnalytics,
  createMonitoringSummaryAccumulator,
  finalizeMonitoringSummary,
  hasCompleteUsageAnalyticsSource,
} from '../src/pro/modules/monitoring/features/monitoringAnalytics';
import type { MonitoringEventRow } from '../src/pro/modules/monitoring/features/hooks/useMonitoringData';
import type { UsageAggregates } from '../src/pro/modules/monitoring/features/hooks/useUsageAggregates';

const event = (overrides: Partial<MonitoringEventRow> = {}): MonitoringEventRow => ({
  id: 'event-1',
  failed: false,
  inputTokens: 10,
  outputTokens: 20,
  reasoningTokens: 3,
  cachedTokens: 4,
  cacheInputTokens: 10,
  totalTokens: 30,
  totalCost: 1.5,
  dayKey: '2026-07-22',
  hourLabel: '10:00',
  latencyMs: 100,
  taskKey: 'task-1',
  model: 'gpt-test',
  timestampMs: 1000,
  clientApiKey: { id: 'clientApiKey:key-1', hash: 'key-1', masked: 'sk***01' },
  authLabel: 'account.json',
  authIndexMasked: 'auth***01',
  channel: 'openai',
  provider: 'codex',
  ...overrides,
} as MonitoringEventRow);

describe('monitoring analytics', () => {
  test('summarizes request, task, token, latency, and zero-token metrics', () => {
    const accumulator = createMonitoringSummaryAccumulator();
    addMonitoringSummaryRow(accumulator, event(), 0, 2000);
    addMonitoringSummaryRow(accumulator, event({
      id: 'event-2',
      failed: true,
      totalTokens: 0,
      inputTokens: 0,
      outputTokens: 0,
      reasoningTokens: 0,
      cachedTokens: 0,
      cacheInputTokens: 0,
      latencyMs: null,
      taskKey: 'task-1',
    }), 0, 2000);

    const summary = finalizeMonitoringSummary(accumulator);
    expect(summary.totalCalls).toBe(2);
    expect(summary.failureCalls).toBe(1);
    expect(summary.approxTasks).toBe(1);
    expect(summary.approxTaskFailures).toBe(1);
    expect(summary.averageLatencyMs).toBe(100);
    expect(summary.cacheInputTokens).toBe(10);
    expect(summary.zeroTokenModels).toEqual(['gpt-test']);
  });

  test('keeps API-key options global while applying the selected key to trend and model totals', () => {
    const analytics = buildUsageTrendAnalytics([
      event(),
      event({
        id: 'event-2',
        model: 'gpt-other',
        totalTokens: 50,
        clientApiKey: { id: 'clientApiKey:key-2', hash: 'key-2', masked: 'sk***02' },
      }),
    ], { type: 'preset', preset: 'all' }, 'key-1', 'All keys');

    expect(analytics.apiKeyOptions.map(({ value }) => value)).toEqual(['all', 'key-1', 'key-2']);
    expect(analytics.modelRows.map(({ model }) => model)).toEqual(['gpt-test']);
    expect(analytics.apiKeyRows).toHaveLength(2);
    expect(analytics.scopedTotals.tokens).toBe(30);
  });

  test('never treats truncated client details as a complete analytics source', () => {
    expect(hasCompleteUsageAnalyticsSource(false, true, true)).toBe(false);
    expect(hasCompleteUsageAnalyticsSource(false, true, false)).toBe(true);
    expect(hasCompleteUsageAnalyticsSource(true, true, true)).toBe(true);
    expect(hasCompleteUsageAnalyticsSource(false, false, false)).toBe(false);
  });

  test('waits for server aggregates before using the initial client snapshot', () => {
    expect(hasCompleteUsageAnalyticsSource(false, true, false, false)).toBe(false);
    expect(hasCompleteUsageAnalyticsSource(true, true, false, false)).toBe(true);
    // A failed aggregate request can fall back only to fully rendered, complete details.
    expect(hasCompleteUsageAnalyticsSource(false, false, false, true)).toBe(false);
    expect(hasCompleteUsageAnalyticsSource(false, true, true, true)).toBe(false);
    expect(hasCompleteUsageAnalyticsSource(false, true, false, true)).toBe(true);
  });

  test('keeps every aggregate bucket for the all-time range', () => {
    const start = new Date(2026, 0, 1).getTime();
    const aggregates = {
      trend: Array.from({ length: 40 }, (_, index) => ({
        bucketStartMs: start + index * 24 * 60 * 60 * 1000,
        totalRequests: 1,
        successCount: 1,
        failureCount: 0,
        totalTokens: 10,
        inputTokens: 4,
        outputTokens: 6,
        reasoningTokens: 0,
        cacheTokens: 0,
        cacheInputTokens: 0,
        cacheReadTokens: 0,
        cacheWriteTokens: 0,
        estimatedCost: 0,
      })),
      models: [],
      apiKeys: [],
      providers: [],
      allSummary: [],
      recentDailySummary: [],
      latestId: 40,
      snapshotAtMs: start,
      scopeTimeRange: { type: 'preset', preset: 'all' },
      scopeTimeRangeKey: 'all',
      scopeApiKeyHash: 'all',
    } as UsageAggregates;

    const analytics = buildServerUsageTrendAnalytics(
      aggregates,
      { type: 'preset', preset: 'all' },
      {},
      [{ value: 'all', label: 'All' }],
      'all',
      'Unattributed'
    );

    expect(analytics?.trendPoints).toHaveLength(40);
    expect(analytics?.tokenDistributionPoints).toHaveLength(40);
  });

  test('aggregates full-range chart points without losing totals', () => {
    const points = Array.from({ length: 40 }, (_, index) => ({
      key: String(index),
      label: `Day ${index + 1}`,
      requests: 1,
      failures: index % 2,
      tokens: 10,
      cost: 0.5,
    }));

    const displayPoints = aggregateTrendPointsForDisplay(points, 30);
    expect(displayPoints).toHaveLength(30);
    expect(displayPoints.reduce((sum, point) => sum + point.requests, 0)).toBe(40);
    expect(displayPoints.reduce((sum, point) => sum + point.tokens, 0)).toBe(400);
    expect(displayPoints[0].key).toBe('0');
    expect(displayPoints.at(-1)?.key).toBe('38..39');
  });

  test('uses one keyboard stop to browse the aggregated trend chart', () => {
    const source = readFileSync(resolve(
      import.meta.dir,
      '../src/pro/modules/monitoring/features/components/UsageAnalyticsPanels.tsx'
    ), 'utf8');
    expect(source.match(/tabIndex=\{0\}/g)).toHaveLength(1);
    expect(source).toContain('aria-keyshortcuts="ArrowLeft ArrowRight Home End"');
    expect(source).not.toContain('className={styles.trendHoverTarget}\n                  onMouseEnter={() => setHoveredIndex(index)}\n                  onMouseLeave={() => setHoveredIndex(null)}\n                  tabIndex={0}');
    expect(source).not.toContain('points.length * 60');
    expect(source.match(/Math\.max\(durationMinutes, 1\)/g)).toHaveLength(2);
  });
});
