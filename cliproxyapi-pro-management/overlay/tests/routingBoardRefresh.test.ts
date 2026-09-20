import { describe, expect, test } from 'bun:test';
import { readFileSync } from 'node:fs';
import { createLatestRequestGate } from '../src/pro/modules/routing/latestRequestGate';

// Exercise the page's actual request callback without mocking React modules globally.
const source = readFileSync(
  new URL('../src/pro/modules/routing/RoutingPolicyPage.tsx', import.meta.url),
  'utf8'
);
const callback = source.match(
  /const loadBoard = useCallback\(\s*(async [\s\S]*?),\s*\[applyResponse/
)![1];
function harness() {
  const state = {
    loading: false,
    data: null as unknown,
    error: '',
    notifications: 0,
  };
  const pending: {
    resolve: (value: unknown) => void;
    reject: (error: Error) => void;
  }[] = [];
  const load = new Function(
    'connectionStatus',
    'setData',
    'setLoading',
    'requestGate',
    'routingPolicyApi',
    'applyResponse',
    'showNotification',
    't',
    'setRuntimeError',
    `return (${callback})`
  )(
    'connected',
    (value: unknown) => {
      state.data = value;
    },
    (value: boolean) => {
      state.loading = value;
    },
    { current: createLatestRequestGate() },
    {
      get: () => new Promise((resolve, reject) => pending.push({ resolve, reject })),
    },
    (value: unknown) => {
      state.data = value;
      state.error = '';
    },
    () => {
      state.notifications++;
    },
    (key: string) => key,
    (value: string) => {
      state.error = value;
    }
  ) as (options?: { showLoading?: boolean; notify?: boolean }) => Promise<void>;
  return { state, pending, load };
}

describe('scheduling board request lifecycle', () => {
  test('poll taking over a foreground refresh clears loading and ignores stale success', async () => {
    const { state, pending, load } = harness();
    const foreground = load({ showLoading: true, notify: true });
    const poll = load();
    pending[1].resolve('new snapshot');
    await poll;
    expect(state.loading).toBe(false);
    pending[0].resolve('old snapshot');
    await foreground;
    expect(state.data).toBe('new snapshot');
    expect(state.notifications).toBe(0);
  });

  test('failed replacement request clears loading; stale errors cannot overwrite it', async () => {
    const { state, pending, load } = harness();
    const foreground = load({ showLoading: true });
    const poll = load();
    pending[1].reject(new Error('current failure'));
    await poll;
    pending[0].reject(new Error('stale failure'));
    await foreground;
    expect(state.loading).toBe(false);
    expect(state.error).toBe('current failure');
    expect(state.data).toBeNull();
  });

  test('manual refresh supersedes polling without losing its loading indicator', async () => {
    const { state, pending, load } = harness();
    const poll = load();
    const foreground = load({ showLoading: true, notify: true });
    pending[0].resolve('old snapshot');
    await poll;
    expect(state.loading).toBe(true);
    pending[1].resolve('new snapshot');
    await foreground;
    expect(state.loading).toBe(false);
    expect(state.data).toBe('new snapshot');
    expect(state.notifications).toBe(1);
  });
});
