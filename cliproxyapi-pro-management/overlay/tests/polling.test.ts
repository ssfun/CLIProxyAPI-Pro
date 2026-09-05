import { describe, expect, test } from 'bun:test';
import { startPolling } from '../src/pro/shared/polling';

const sleep = (ms: number) => new Promise((resolve) => setTimeout(resolve, ms));

describe('completion-based polling', () => {
  test('a request slower than the interval completes before the next request starts', async () => {
    let calls = 0;
    let release!: () => void;
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    const stop = startPolling(async () => {
      calls += 1;
      await pending;
    }, 10);
    try {
      await sleep(60);
      expect(calls).toBe(1);
      release();
      await sleep(30);
      expect(calls).toBeGreaterThan(1);
    } finally {
      release();
      stop();
    }
  });

  test('stopping while a request is running prevents rescheduling', async () => {
    let calls = 0;
    let release!: () => void;
    const pending = new Promise<void>((resolve) => {
      release = resolve;
    });
    const stop = startPolling(async () => {
      calls += 1;
      await pending;
    }, 10);
    await sleep(30);
    stop();
    release();
    await sleep(30);
    expect(calls).toBe(1);
  });
});
