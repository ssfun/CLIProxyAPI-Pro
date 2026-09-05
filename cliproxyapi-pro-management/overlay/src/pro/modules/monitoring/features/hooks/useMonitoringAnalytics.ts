import { useMemo } from 'react';
import type { MonitoringEventRow } from './useMonitoringData';
import type { UsageAggregates } from './useUsageAggregates';
import type { TimeRangeSelection } from '../timeRange';
import type { ModelPrice } from '../usage';
import {
  addMonitoringSummaryRow,
  buildServerUsageTrendAnalytics,
  buildUsageTrendAnalytics,
  createMonitoringSummaryAccumulator,
  finalizeMonitoringSummary,
} from '@/pro/modules/monitoring/features/monitoringAnalytics';
import { resolveTimeRange } from '@/pro/modules/monitoring/features/timeRange';

export function useMonitoringAnalytics({
  allRows,
  usageAggregates,
  timeRange,
  timeRangeKey,
  usageTrendApiKey,
  modelPrices,
  apiKeyOptions,
  allKeysLabel,
  unattributedLabel,
}: {
  allRows: MonitoringEventRow[];
  usageAggregates: UsageAggregates | null;
  timeRange: TimeRangeSelection;
  timeRangeKey: string;
  usageTrendApiKey: string;
  modelPrices: Record<string, ModelPrice>;
  apiKeyOptions: Array<{ value: string; label: string }>;
  allKeysLabel: string;
  unattributedLabel: string;
}) {
  const aggregateTrendScopeMatches = Boolean(
    usageAggregates &&
    usageAggregates.scopeTimeRangeKey === timeRangeKey &&
    usageAggregates.scopeApiKeyHash === usageTrendApiKey
  );
  const usageRowGroups = useMemo(() => {
    const nowMs = Math.max(
      Number(usageAggregates?.snapshotAtMs) || 0,
      allRows.reduce((latest, row) => Math.max(latest, row.timestampMs), 0)
    );
    const summaryWindowStartMs = nowMs - 30 * 60 * 1000;
    const todayStart = new Date(nowMs);
    todayStart.setHours(0, 0, 0, 0);
    const tomorrowStart = new Date(todayStart);
    tomorrowStart.setDate(tomorrowStart.getDate() + 1);
    const yesterdayStart = new Date(todayStart);
    yesterdayStart.setDate(yesterdayStart.getDate() - 1);
    const resolvedRange = resolveTimeRange(timeRange, nowMs);
    const trendStatsRows: MonitoringEventRow[] = [];
    const topSummaryAccumulator = createMonitoringSummaryAccumulator();
    const todaySummaryAccumulator = createMonitoringSummaryAccumulator();
    const trendSummaryAccumulator = createMonitoringSummaryAccumulator();
    let todayCost = 0;
    let yesterdayCost = 0;

    const fallbackRows = aggregateTrendScopeMatches ? [] : allRows;
    fallbackRows.forEach((row) => {
      if (!row.statsIncluded) return;
      addMonitoringSummaryRow(topSummaryAccumulator, row, summaryWindowStartMs, nowMs);
      if (row.timestampMs >= todayStart.getTime() && row.timestampMs < tomorrowStart.getTime()) {
        addMonitoringSummaryRow(todaySummaryAccumulator, row, summaryWindowStartMs, nowMs);
        todayCost += row.totalCost;
      } else if (
        row.timestampMs >= yesterdayStart.getTime() &&
        row.timestampMs < todayStart.getTime()
      ) {
        yesterdayCost += row.totalCost;
      }
      if (row.timestampMs >= resolvedRange.fromMs && row.timestampMs <= resolvedRange.toMs) {
        trendStatsRows.push(row);
        addMonitoringSummaryRow(trendSummaryAccumulator, row, summaryWindowStartMs, nowMs);
      }
    });

    return {
      trendStatsRows,
      topSummary: finalizeMonitoringSummary(topSummaryAccumulator),
      todaySummary: finalizeMonitoringSummary(todaySummaryAccumulator),
      trendSummary: finalizeMonitoringSummary(trendSummaryAccumulator),
      todayCost,
      yesterdayCost,
    };
  }, [aggregateTrendScopeMatches, allRows, timeRange, usageAggregates?.snapshotAtMs]);
  const { trendStatsRows, topSummary, todaySummary, yesterdayCost } = usageRowGroups;

  const clientUsageTrendAnalytics = useMemo(
    () => buildUsageTrendAnalytics(trendStatsRows, timeRange, usageTrendApiKey, allKeysLabel),
    [trendStatsRows, timeRange, usageTrendApiKey, allKeysLabel]
  );
  const serverUsageTrendAnalytics = useMemo(
    () =>
      buildServerUsageTrendAnalytics(
        usageAggregates,
        usageAggregates?.scopeTimeRange ?? timeRange,
        modelPrices,
        apiKeyOptions,
        usageAggregates?.scopeApiKeyHash ?? usageTrendApiKey,
        unattributedLabel
      ),
    [apiKeyOptions, modelPrices, unattributedLabel, timeRange, usageAggregates, usageTrendApiKey]
  );
  return {
    aggregateTrendScopeMatches,
    topSummary,
    todaySummary,
    yesterdayCost,
    clientUsageTrendAnalytics,
    serverUsageTrendAnalytics,
  };
}
