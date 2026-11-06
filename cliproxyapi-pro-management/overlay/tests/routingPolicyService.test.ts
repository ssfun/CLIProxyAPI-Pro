import { describe, expect, test } from 'bun:test';
import {
  formatTimestamp,
  normalizeSchedulingBoardResponse,
  schedulingBoardModelsLabel,
  schedulingRecoveryResultTone,
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
    expect(
      schedulingBoardModelsLabel({ scope: 'credential', models: ['model-a'] }, 'All models')
    ).toBe('All models');
    expect(schedulingBoardModelsLabel({ scope: 'model', models: ['model-a'] }, 'All models')).toBe(
      'model-a'
    );
    expect(schedulingBoardModelsLabel({ scope: 'model', models: [] }, 'All models')).toBe('-');
  });

  test('uses success tone only when recovery clears the restriction without a failed probe', () => {
    expect(schedulingRecoveryResultTone({ after: undefined, test: { success: true } })).toBe('success');
    expect(schedulingRecoveryResultTone({ after: { authId: 'a' } as never })).toBe('warning');
    expect(schedulingRecoveryResultTone({ after: undefined, test: { success: false } })).toBe('error');
    expect(schedulingRecoveryResultTone({ after: undefined, phases: [
      { source: 'inspection', status: 'completed' },
      { source: 'upstream', status: 'timeout' },
    ] })).toBe('error');
  });

  test('formats timestamps consistently', () => {
    const timestamp = 1726650000000;
    expect(formatTimestamp(undefined, 'en-US', '-')).toBe('-');
    expect(formatTimestamp(timestamp, 'en-US', '-')).toContain('2024');
  });
});
