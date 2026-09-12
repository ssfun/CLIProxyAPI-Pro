import { describe, expect, test } from 'bun:test';
import { formatTokenCount, getCacheHitRate } from '../src/pro/pages/selfUsagePresentation';

describe('public page usage presentation', () => {
  test('keeps missing cache denominators unknown instead of showing zero percent', () => {
    expect(getCacheHitRate({ cachedTokens: 600, cacheInputTokens: 0 })).toBeNull();
    expect(getCacheHitRate({ cachedTokens: 0, cacheInputTokens: 0 })).toBeNull();
    expect(getCacheHitRate({ cachedTokens: 0, cacheInputTokens: 1000 })).toBe(0);
  });
  test('uses cache input rather than total tokens and bounds the display ratio', () => {
    expect(getCacheHitRate({ cachedTokens: 600, cacheInputTokens: 1000 })).toBe(0.6);
    expect(getCacheHitRate({ cachedTokens: 1200, cacheInputTokens: 1000 })).toBe(1);
    expect(getCacheHitRate({ cachedTokens: -1, cacheInputTokens: 1000 })).toBe(0);
  });
  test('renders whole token counts without negatives', () => {
    expect(formatTokenCount(-1)).toBe('0');
    expect(formatTokenCount(Number.NaN)).toBe('0');
    expect(formatTokenCount(1234.4)).toBe((1234).toLocaleString());
  });
});
