import { describe, expect, test } from 'bun:test';
import {
  buildAccountStatusData,
  buildAccountStatusRange,
} from '../src/features/monitoring/accountHealth';
import type { MonitoringAccountRow, MonitoringEventRow } from '../src/features/monitoring/hooks/useMonitoringData';

describe('account health analytics', () => {
  test('uses the earliest retained account event for the all-time range', () => {
    const rows = [{
      rows: [{ timestampMs: 1000 }, { timestampMs: 3000 }],
    }] as MonitoringAccountRow[];

    expect(buildAccountStatusRange(rows, 'all', 5000)).toEqual({ startTime: 1000, endTime: 5000 });
  });

  test('places successes and failures into fixed status blocks', () => {
    const rows = [
      { timestampMs: 1000, failed: false },
      { timestampMs: 1999, failed: true },
      { timestampMs: 3000, failed: false },
    ] as MonitoringEventRow[];
    const result = buildAccountStatusData(rows, { startTime: 1000, endTime: 3000 });

    expect(result.blockDetails).toHaveLength(20);
    expect(result.totalSuccess).toBe(2);
    expect(result.totalFailure).toBe(1);
    expect(result.successRate).toBeCloseTo(66.6667, 3);
  });
});
