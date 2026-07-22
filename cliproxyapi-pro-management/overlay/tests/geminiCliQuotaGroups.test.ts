import { describe, expect, test } from 'bun:test';
import { buildGeminiCliQuotaBuckets } from '../src/extensions/quota/geminiCliQuotaConfig';

describe('Gemini CLI quota families', () => {
  test('groups future models conservatively and preserves unknown models', () => {
    const buckets = buildGeminiCliQuotaBuckets([
      {
        modelId: 'gemini-4-flash-preview',
        tokenType: 'input',
        remainingFraction: 0.6,
        remainingAmount: 60,
        resetTime: '2026-06-22T02:00:00Z',
      },
      {
        modelId: 'gemini-2.5-flash',
        tokenType: 'input',
        remainingFraction: 0.4,
        remainingAmount: 40,
        resetTime: '2026-06-22T03:00:00Z',
      },
      {
        modelId: 'gemini-4-pro-preview',
        tokenType: 'output',
        remainingFraction: 0.2,
        remainingAmount: 20,
        resetTime: '2026-06-22T04:00:00Z',
      },
      {
        modelId: 'gemini-experimental',
        tokenType: null,
        remainingFraction: 0.9,
        remainingAmount: null,
        resetTime: undefined,
      },
    ]);

    expect(buckets.map((bucket) => bucket.id)).toEqual([
      'gemini-flash-series-input',
      'gemini-pro-series-output',
      'gemini-experimental',
    ]);
    expect(buckets[0]).toMatchObject({
      remainingFraction: 0.4,
      remainingAmount: 40,
      modelIds: ['gemini-4-flash-preview', 'gemini-2.5-flash'],
    });
  });
});
