import { describe, expect, test } from 'bun:test';
import {
  formatRemainingTime,
  formatTimeOnly,
  formatTimestamp,
  normalizeSchedulingBoardResponse,
  schedulingBoardModelsLabel,
  schedulingBoardResumeTone,
} from '../src/pro/modules/routing/routingPolicy';

describe('scheduling board service model', () => {
  test('normalizes missing board payloads into empty live state', () => {
    expect(normalizeSchedulingBoardResponse(null)).toEqual({
      generatedAt: 0,
      summary: {
        blocked: 0,
        quota: 0,
        authTransient: 0,
        recheck: 0,
        overlap: 0,
        excluded: 0,
        nextRetryAt: 0,
        nextActionAt: 0,
        nextTransitionAt: 0,
      },
      accounts: [],
    });
  });

  test('keeps live accounts and numeric summary fields', () => {
    const got = normalizeSchedulingBoardResponse({
      generatedAt: 10,
      summary: {
        blocked: 2,
        quota: 1,
        authTransient: 1,
        recheck: 1,
        overlap: 1,
        excluded: 3,
        nextRetryAt: 20,
        nextActionAt: 15,
        nextTransitionAt: 20,
      },
      accounts: [{ authId: 'a', authIndex: 'idx', provider: 'claude' } as never],
    });
    expect(got.generatedAt).toBe(10);
    expect(got.summary.blocked).toBe(2);
    expect(got.summary.excluded).toBe(3);
    expect(got.summary.nextRetryAt).toBe(20);
    expect(got.summary.nextActionAt).toBe(15);
    expect(got.summary.nextTransitionAt).toBe(20);
    expect(got.accounts).toHaveLength(1);
  });

  test('whole-account scope does not narrow the display to model details', () => {
    expect(schedulingBoardModelsLabel({ scope: 'credential', models: ['model-a'] }, 'All models')).toBe('All models');
    expect(schedulingBoardModelsLabel({ scope: 'model', models: ['model-a'] }, 'All models')).toBe('model-a');
    expect(schedulingBoardModelsLabel({ scope: 'model', models: [] }, 'All models')).toBe('-');
  });

  test('maps resume types to semantic tones', () => {
    expect(schedulingBoardResumeTone('auto-expire')).toBe('good');
    expect(schedulingBoardResumeTone('recheck-quota')).toBe('info');
    expect(schedulingBoardResumeTone('probe-request')).toBe('warning');
    expect(schedulingBoardResumeTone('refresh-token')).toBe('warning');
    expect(schedulingBoardResumeTone('multiple')).toBe('warning');
    expect(schedulingBoardResumeTone('manual')).toBe('danger');
    expect(schedulingBoardResumeTone('reauthenticate')).toBe('danger');
    expect(schedulingBoardResumeTone('await-state-change')).toBe('neutral');
  });

  test('formats remaining countdowns correctly', () => {
    const t = (key: string, opts?: any) => {
      if (key === 'routing_policy.runtime.remaining_seconds') return `${opts.count}s left`;
      if (key === 'routing_policy.runtime.remaining_minutes') return `${opts.count}m left`;
      if (key === 'routing_policy.runtime.due_recheck') return 'Due for recheck';
      if (key === 'routing_policy.runtime.due_probe') return 'Due for probe';
      if (key === 'routing_policy.runtime.due_now') return 'Due now';
      return key;
    };

    expect(formatRemainingTime(45, 1000, 'auto-expire', t)).toBe('45s left');
    expect(formatRemainingTime(180, 1000, 'auto-expire', t)).toBe('3m left');
    expect(formatRemainingTime(0, 50, 'recheck-quota', t)).toBe('Due for recheck');
    expect(formatRemainingTime(0, 50, 'probe-request', t)).toBe('Due for probe');
    expect(formatRemainingTime(0, 50, 'auto-expire', t)).toBe('Due now');
    expect(formatRemainingTime(undefined, 0, 'auto-expire', t)).toBe('-');
  });

  test('formats timestamps consistently', () => {
    const timestamp = 1726650000000;
    expect(formatTimestamp(undefined, 'en-US', '-')).toBe('-');
    expect(formatTimestamp(timestamp, 'en-US', '-')).toContain('2024');
    expect(formatTimeOnly(undefined, 'en-US', '-')).toBe('-');
    expect(formatTimeOnly(timestamp, 'en-US', '-')).toMatch(/\d{1,2}:\d{2}:\d{2}/);
  });
});
