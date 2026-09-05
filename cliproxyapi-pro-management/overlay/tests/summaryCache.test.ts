import { describe, expect, test } from 'bun:test';
import { createSummaryCache } from '../src/pro/modules/monitoring/features/summaryCache';

describe('monitoring summary cache', () => {
  test('reuses expensive summaries but invalidates for manual refresh and dataset changes', async () => {
    const cache = createSummaryCache<number>();
    let calls = 0;
    const fetch = async () => ++calls;
    expect(await cache.load('connection-a:g1', fetch)).toBe(1);
    expect(await cache.load('connection-a:g1', fetch)).toBe(1);
    expect(await cache.load('connection-a:g1', fetch, true)).toBe(2);
    expect(await cache.load('connection-a:g2', fetch)).toBe(3);
    expect(await cache.load('connection-b:g2', fetch)).toBe(4);
    cache.clear();
    expect(await cache.load('connection-b:g2', fetch)).toBe(5);
  });

  test('refreshes expired entries and never caches errors', async () => {
    const cache = createSummaryCache<number>(0);
    await expect(
      cache.load('a', async () => {
        throw new Error('offline');
      })
    ).rejects.toThrow('offline');
    expect(await cache.load('a', async () => 1)).toBe(1);
    expect(await cache.load('a', async () => 2)).toBe(2);
  });

  test('an old response cannot repopulate a cleared cache', async () => {
    const cache = createSummaryCache<number>();
    let release!: (value: number) => void;
    const pending = cache.load(
      'a',
      () =>
        new Promise<number>((resolve) => {
          release = resolve;
        })
    );
    cache.clear();
    await cache.load('b', async () => 2);
    release(1);
    await pending;
    expect(await cache.load('b', async () => 3)).toBe(2);
  });
});
