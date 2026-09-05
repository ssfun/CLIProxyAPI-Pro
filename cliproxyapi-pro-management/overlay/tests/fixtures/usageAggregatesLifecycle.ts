/* eslint-disable @typescript-eslint/no-explicit-any -- Minimal hook runner models heterogeneous React slots. */
import { mock } from 'bun:test';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
const root = fileURLToPath(new URL('../../', import.meta.url)).replace(/\/$/, '');
const scenario = process.argv[2];
let connected = true,
  generation = 1;
let now = Date.UTC(2026, 8, 5, 8),
  serverCount = 0,
  latestId = 0,
  dirty = true,
  index = 0;
const slots: any[] = [];
let effects: any[] = [];
const timers = new Map<number, { at: number; fn: () => void }>();
let timerId = 0;
let result: any;
const unchanged = (a: any[], b: any[]) =>
  a && b && a.length === b.length && a.every((x, i) => Object.is(x, b[i]));
const react = {
  useRef(value: any) {
    const n = index++;
    return slots[n] ?? (slots[n] = { current: value });
  },
  useState(value: any) {
    const n = index++;
    if (!slots[n]) slots[n] = { value: typeof value === 'function' ? value() : value };
    return [
      slots[n].value,
      (next: any) => {
        const v = typeof next === 'function' ? next(slots[n].value) : next;
        if (!Object.is(v, slots[n].value)) {
          slots[n].value = v;
          dirty = true;
        }
      },
    ];
  },
  useCallback(fn: any, deps: any[]) {
    const n = index++;
    if (!slots[n] || !unchanged(slots[n].deps, deps)) slots[n] = { fn, deps };
    return slots[n].fn;
  },
  useEffect(fn: any, deps: any[]) {
    const n = index++;
    if (!slots[n] || !unchanged(slots[n].deps, deps)) {
      effects.push(() => {
        slots[n]?.cleanup?.();
        slots[n] = { deps, cleanup: fn() };
      });
    }
  },
};
mock.module('react', () => react);
mock.module(root + '/src/stores/useAuthStore.ts', () => ({
  useAuthStore: (select: any) =>
    select({
      apiBase: 'http://test',
      managementKey: 'test',
      connectionStatus: connected ? 'connected' : 'disconnected',
    }),
}));
const calls: any[] = [];
let holdRequests = false;
let failRequests = false;
const releases: Array<() => void> = [];
mock.module(root + '/src/services/api/client.ts', () => ({
  apiClient: {
    get: async (_: string, { params }: any) => {
      calls.push({ at: now, params });
      if (holdRequests) await new Promise<void>((resolve) => releases.push(resolve));
      if (failRequests) throw new Error('offline');
      return { items: [{ totalRequests: serverCount }], latest_id: latestId, snapshot_at_ms: now };
    },
  },
}));
mock.module(root + '/src/pro/modules/monitoring/features/timeRange/index.ts', () => ({
  getTimeRangeKey: () => 'today',
  getLocalTimeZone: () => 'UTC',
  resolveTimeRange: (_: any, time: number) => ({ fromMs: 0, toMs: time, interval: 'hour' }),
}));
const RealDate = Date;
// @ts-expect-error -- The deterministic clock implements the subset used by this hook.
globalThis.Date = class extends RealDate {
  constructor(...args: any[]) {
    super(...((args.length ? args : [now]) as [any]));
  }
  static now() {
    return now;
  }
};
// @ts-expect-error -- The deterministic clock implements the subset used by this hook.
globalThis.setTimeout = (fn: any, ms = 0) => {
  const id = ++timerId;
  timers.set(id, { at: now + ms, fn });
  return id;
};
// @ts-expect-error -- The deterministic clock implements the subset used by this hook.
globalThis.clearTimeout = (id: number) => timers.delete(id);
// @ts-expect-error -- The deterministic clock implements the subset used by this hook.
globalThis.window = { setTimeout: globalThis.setTimeout, clearTimeout: globalThis.clearTimeout };
const { useUsageAggregates } = await import(
  root + '/src/pro/modules/monitoring/features/hooks/useUsageAggregates.ts'
);
const range = 'today';
async function settle() {
  for (let n = 0; n < 30; n++) {
    if (dirty) {
      dirty = false;
      index = 0;
      effects = [];
      // eslint-disable-next-line react-hooks/rules-of-hooks -- Exercise the production hook through the slot runner.
      result = useUsageAggregates({
        latestId,
        generation,
        timeRange: range,
        apiKeyHash: 'all',
      } as any);
      effects.forEach((fn) => fn());
    }
    await Promise.resolve();
  }
}
async function advance(ms: number) {
  const target = now + ms;
  await settle();
  for (;;) {
    const next = [...timers.entries()]
      .filter(([, x]) => x.at <= target)
      .sort((a, b) => a[1].at - b[1].at)[0];
    if (!next) break;
    now = next[1].at;
    timers.delete(next[0]);
    next[1].fn();
    await settle();
  }
  now = target;
  await settle();
}

const summary = () => result.data?.allSummary[0]?.totalRequests;
const unmount = () => slots.forEach((slot) => slot?.cleanup?.());
await advance(0);
assert.equal(summary(), 0);
assert.equal(calls.length, 5);
assert.equal(timers.size, 0);
await advance(10000);
serverCount = 1;
latestId = 1;
dirty = true;
await advance(1000);
assert.equal(summary(), 0);
assert.equal(result.data.trend[0].totalRequests, 1);
assert.equal(calls.length, 8);
assert.equal(timers.size, 1);

if (scenario === 'idle' || scenario === 'coalesce') {
  if (scenario === 'coalesce') {
    await advance(30000);
    serverCount = 2;
    latestId = 2;
    dirty = true;
    await advance(1000);
    assert.equal(timers.size, 1);
  }
  // Expiry stays at t=60s even when more events arrive; no sliding deadline.
  await advance(Date.UTC(2026, 8, 5, 8) + 59999 - now);
  assert.equal(summary(), 0);
  await advance(1);
  assert.equal(summary(), serverCount);
  assert.equal(timers.size, 0);
  const completedCalls = calls.length;
  await advance(120000);
  assert.equal(calls.length, completedCalls);
} else if (scenario === 'manual') {
  await result.refresh();
  await settle();
  assert.equal(summary(), 1);
  assert.equal(timers.size, 0);
  const completedCalls = calls.length;
  await advance(120000);
  assert.equal(calls.length, completedCalls);
} else if (scenario === 'disconnect') {
  connected = false;
  dirty = true;
  await settle();
  assert.equal(result.data, null);
  assert.equal(timers.size, 0);
  const completedCalls = calls.length;
  await advance(120000);
  assert.equal(calls.length, completedCalls);
} else if (scenario === 'generation') {
  generation++;
  serverCount = 0;
  latestId = 0;
  dirty = true;
  await advance(0);
  assert.equal(summary(), 0);
  assert.equal(calls.length, 13);
  assert.equal(timers.size, 0);
  await advance(120000);
  assert.equal(calls.length, 13);
} else if (scenario === 'failure') {
  failRequests = true;
  await advance(49000);
  assert.equal(result.error, 'offline');
  assert.equal(summary(), 0);
  assert.equal(timers.size, 0);
  const completedCalls = calls.length;
  await advance(120000);
  assert.equal(calls.length, completedCalls);
} else if (scenario === 'unmount-pending') {
  holdRequests = true;
  latestId = 2;
  dirty = true;
  await advance(1000);
  unmount();
  releases.forEach((resolve) => resolve());
  await settle();
  assert.equal(timers.size, 0);
  assert.equal(summary(), 0);
} else if (scenario === 'unmount') {
  unmount();
  assert.equal(timers.size, 0);
  const completedCalls = calls.length;
  await advance(120000);
  assert.equal(calls.length, completedCalls);
} else {
  throw new Error('Unknown scenario: ' + scenario);
}
unmount();
