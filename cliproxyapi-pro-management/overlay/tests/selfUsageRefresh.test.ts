import { describe, expect, test } from 'bun:test';
import {
  createSelfUsageRefresh,
  parseSelfUsageNotification,
} from '../src/pro/pages/selfUsageRefresh';

function deferred() {
  let resolve!: () => void;
  const promise = new Promise<void>((done) => {
    resolve = done;
  });
  return { promise, resolve };
}

describe('public usage refresh queue', () => {
  test('coalesces changes during a slow request into one immediate follow-up', async () => {
    const first = deferred();
    let calls = 0;
    let concurrent = 0;
    let maxConcurrent = 0;
    const queue = createSelfUsageRefresh(async () => {
      calls += 1;
      concurrent += 1;
      maxConcurrent = Math.max(concurrent, maxConcurrent);
      if (calls === 1) await first.promise;
      concurrent -= 1;
    });
    const completed = queue.refresh();
    for (let i = 0; i < 5; i++) void queue.refresh();
    expect(calls).toBe(1);
    first.resolve();
    await completed;
    expect(calls).toBe(2);
    expect(maxConcurrent).toBe(1);
    queue.dispose();
  });

  test('reset aborts the old request and fences a late result that ignores abort', async () => {
    const oldRequest = deferred();
    let calls = 0;
    const committed: string[] = [];
    let oldSignal: AbortSignal | undefined;
    const queue = createSelfUsageRefresh(async (signal, isCurrent) => {
      calls += 1;
      if (calls === 1) {
        oldSignal = signal;
        await oldRequest.promise;
        if (isCurrent()) committed.push('old dataset');
      } else if (isCurrent()) committed.push('new dataset');
    });
    const old = queue.refresh();
    void queue.refresh();
    queue.invalidate();
    expect(oldSignal?.aborted).toBe(true);
    await queue.refresh(true);
    expect(committed).toEqual(['new dataset']);
    oldRequest.resolve();
    await old;
    expect(committed).toEqual(['new dataset']);
    expect(calls).toBe(2);
    queue.dispose();
  });

  test('hidden pages defer work and disposal cancels queued refreshes', async () => {
    let visible = false;
    let calls = 0;
    const pending = deferred();
    let activeSignal: AbortSignal | undefined;
    const queue = createSelfUsageRefresh(
      async (signal) => {
        activeSignal = signal;
        calls += 1;
        await pending.promise;
      },
      () => visible
    );
    await queue.refresh();
    expect(calls).toBe(0);
    visible = true;
    const completed = queue.refresh();
    void queue.refresh();
    queue.dispose();
    expect(activeSignal?.aborted).toBe(true);
    pending.resolve();
    await completed;
    await queue.refresh(true);
    expect(calls).toBe(1);
  });

  test('parses reset and reconnect generations without treating payload text as an event', () => {
    expect(parseSelfUsageNotification('event: change\ndata: {"generation":2}')).toEqual({
      event: 'change',
      generation: 2,
    });
    expect(parseSelfUsageNotification('event: reset\r\ndata: {"generation":3}\r')).toEqual({
      event: 'reset',
      generation: 3,
    });
    expect(parseSelfUsageNotification(': keepalive')).toBeNull();
    expect(
      parseSelfUsageNotification('event: irrelevant\ndata: {"text":"event: change"}')
    ).toBeNull();
    expect(parseSelfUsageNotification('event: change\ndata: {}')).toEqual({
      event: 'change',
      generation: undefined,
    });
  });
});
