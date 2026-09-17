import { describe, expect, test } from 'bun:test';
import { normalizeSchedulingBoardResponse } from '../src/pro/modules/routing/routingPolicy';

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
      },
      accounts: [],
    });
  });

  test('keeps live accounts and numeric summary fields', () => {
    const got = normalizeSchedulingBoardResponse({
      generatedAt: 10,
      summary: { blocked: 2, quota: 1, authTransient: 1, recheck: 1, overlap: 1, excluded: 3, nextRetryAt: 20 },
      accounts: [{ authId: 'a', authIndex: 'idx', provider: 'claude' } as never],
    });
    expect(got.generatedAt).toBe(10);
    expect(got.summary.blocked).toBe(2);
    expect(got.summary.nextRetryAt).toBe(20);
    expect(got.accounts).toHaveLength(1);
  });
});
