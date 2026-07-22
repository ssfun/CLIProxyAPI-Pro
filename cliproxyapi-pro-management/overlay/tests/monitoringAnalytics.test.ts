import { describe, expect, test } from 'bun:test';
import {
  addMonitoringSummaryRow,
  buildUsageTrendAnalytics,
  createMonitoringSummaryAccumulator,
  finalizeMonitoringSummary,
} from '../src/features/monitoring/monitoringAnalytics';
import type { MonitoringEventRow } from '../src/features/monitoring/hooks/useMonitoringData';

const event = (overrides: Partial<MonitoringEventRow> = {}): MonitoringEventRow => ({
  id: 'event-1',
  failed: false,
  inputTokens: 10,
  outputTokens: 20,
  reasoningTokens: 3,
  cachedTokens: 4,
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
      latencyMs: null,
      taskKey: 'task-1',
    }), 0, 2000);

    const summary = finalizeMonitoringSummary(accumulator);
    expect(summary.totalCalls).toBe(2);
    expect(summary.failureCalls).toBe(1);
    expect(summary.approxTasks).toBe(1);
    expect(summary.approxTaskFailures).toBe(1);
    expect(summary.averageLatencyMs).toBe(100);
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
    ], 'all', 'key-1', 'All keys');

    expect(analytics.apiKeyOptions.map(({ value }) => value)).toEqual(['all', 'key-1', 'key-2']);
    expect(analytics.modelRows.map(({ model }) => model)).toEqual(['gpt-test']);
    expect(analytics.apiKeyRows).toHaveLength(2);
    expect(analytics.scopedTotals.tokens).toBe(30);
  });
});
