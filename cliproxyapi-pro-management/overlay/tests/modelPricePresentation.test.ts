import { describe, expect, test } from 'bun:test';
import {
  createPriceDraft,
  formatDeltaPercent,
  parsePriceValue,
} from '../src/features/monitoring/modelPricePresentation';

describe('model price presentation model', () => {
  test('creates editable strings without mutating the source rule', () => {
    const draft = createPriceDraft({
      id: 1,
      version: 2,
      provider: '',
      model: 'gpt-test',
      source: 'manual',
      base: { input: 1, output: 2, cacheRead: 0.5, cacheWrite: 0.75 },
      tiers: [{ contextSize: 1000, input: 3, output: 4, cacheRead: 1, cacheWrite: 1.5 }],
    });

    expect(draft.input).toBe('1');
    expect(draft.tiers[0]).toMatchObject({ contextSize: '1000', output: '4' });
  });

  test('normalizes invalid rates and reports rounded deltas', () => {
    expect(parsePriceValue('-1')).toBe(0);
    expect(parsePriceValue('not-a-number')).toBe(0);
    expect(formatDeltaPercent(1.5, 1)).toBe('+50.0%');
    expect(formatDeltaPercent(0, 0)).toBe('0.0%');
  });
});
