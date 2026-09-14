import { expect, test } from 'bun:test';
import { Transpiler } from 'bun';
import { readFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { buildMonitoringApiKeyNames } from '../src/pro/modules/monitoring/features/apiKeyIdentity';

// Execute the page's real effect and refresh callback with controlled HTTP responses.
const source = readFileSync(resolve(import.meta.dir, '../src/pro/modules/monitoring/MonitoringCenterPage.tsx'), 'utf8');
const start = source.indexOf('  useEffect(() => {\n    let cancelled = false;');
const effect = source.slice(start, source.indexOf('\n\n  useEffect(', start));
const refreshStart = source.indexOf('  const refreshAll =');
const refresh = source.slice(refreshStart, source.indexOf('\n\n', refreshStart));
const compile = (code: string) => new Transpiler({ loader: 'ts' }).transformSync(code);
type Catalog = { loaded: boolean; names: Map<string, string>; apiKeyNames?: ReadonlyMap<string, string> };
function harness() {
  let state: Catalog = { loaded: false, names: new Map() };
  let failed = false;
  let cleanup = () => {};
  const pending: Array<{ resolve: (value: unknown) => void; reject: (reason: Error) => void }> = [];
  const context = {
    useEffect: (fn: () => () => void) => { cleanup = fn(); },
    useCallback: (fn: unknown) => fn,
    profileCatalogRequestRef: { current: null as Promise<void> | null },
    refreshProfileCatalogRef: { current: (_force?: boolean) => Promise.resolve() },
    profileCatalogFetchedAtRef: { current: 0 }, profileCatalogGenerationRef: { current: null },
    setCurrentProfileCatalog: (value: Catalog | ((current: Catalog) => Catalog)) => { state = typeof value === 'function' ? value(state) : value; },
    setProfileCatalogRefreshFailed: (value: boolean) => { failed = value; },
    connectionStatus: 'connected', apiBase: 'server-a', managementKey: 'fixture',
    apiKeyPolicyApi: { profileCatalog: () => new Promise((resolve, reject) => pending.push({ resolve, reject })) },
    buildMonitoringApiKeyNames, PROFILE_CATALOG_REFRESH_MS: 30_000,
    window: { setInterval: () => 1, clearInterval: () => {}, addEventListener: () => {}, removeEventListener: () => {} },
    document: { visibilityState: 'visible', addEventListener: () => {}, removeEventListener: () => {} },
    refreshUsage: async () => {}, refreshMeta: async () => {}, refreshRealtimeLogs: async () => {}, refreshAggregates: async () => {},
  };
  const run = () => new Function(...Object.keys(context), compile(effect))(...Object.values(context));
  const manual = () => new Function(...Object.keys(context), compile(refresh) + '\nreturn refreshAll();')(...Object.values(context)) as Promise<void>;
  const reply = (index: number, name: string, generation = 1) => pending[index].resolve({ items: [], apiKeys: [{ apiKeyHash: 'hash', displayName: name }], policyGeneration: generation });
  run();
  return { context, pending, manual, reply, run, cleanup: () => cleanup(), state: () => state, failed: () => failed };
}
const tick = async () => { await Promise.resolve(); await Promise.resolve(); await Promise.resolve(); };

test('manual page refresh bypasses name TTL and recovers errors even at the same generation', async () => {
  const h = harness(); h.reply(0, 'Old'); await tick();
  await h.context.refreshProfileCatalogRef.current(); expect(h.pending).toHaveLength(1);
  const renamed = h.manual(); expect(h.pending).toHaveLength(2);
  h.reply(1, 'New', 2); await renamed; expect(h.state().apiKeyNames?.get('hash')).toBe('New');
  const failed = h.manual(); h.pending[2].reject(new Error('offline')); await failed;
  expect(h.failed()).toBe(true); expect(h.state().apiKeyNames?.get('hash')).toBe('New');
  const retry = h.manual(); h.reply(3, 'New', 2); await retry; expect(h.failed()).toBe(false);
  h.cleanup();
});

test('manual refresh supersedes older pending responses and errors', async () => {
  const h = harness(); const manual = h.manual();
  h.reply(1, 'New', 2); await manual;
  h.reply(0, 'Old'); await tick(); expect(h.state().apiKeyNames?.get('hash')).toBe('New');
  const old = h.manual(); const newer = h.manual(); h.reply(3, 'Newest', 3); await newer;
  h.pending[2].reject(new Error('old failure')); await old;
  expect(h.failed()).toBe(false); expect(h.state().apiKeyNames?.get('hash')).toBe('Newest'); h.cleanup();
});

test('connection replacement clears names and rejects old responses after cleanup', async () => {
  const h = harness(); h.reply(0, 'Server A'); await tick();
  const old = h.manual(); h.cleanup(); h.context.apiBase = 'server-b'; h.run();
  expect(h.state().loaded).toBe(false); expect(h.failed()).toBe(false);
  h.reply(2, 'Server B'); await tick(); h.pending[1].reject(new Error('server A failure')); await old;
  expect(h.state().apiKeyNames?.get('hash')).toBe('Server B'); expect(h.failed()).toBe(false);
  h.cleanup(); const count = h.pending.length; await h.manual(); expect(h.pending).toHaveLength(count);
});
