/** Real HTTP regression: import the generated frontend, without replacing its transport. */
import assert from 'node:assert/strict';
import { writeFile } from 'node:fs/promises';
import { pathToFileURL } from 'node:url';

const [frontend, apiBase, phase, output] = process.argv.slice(2);
assert(frontend && apiBase && phase && output, 'Expected frontend, API base, phase, output');
assert(['write', 'read', 'hydrate'].includes(phase), `Unknown phase: ${phase}`);

// Only browser event delivery is shimmed. Axios, API modules, SQLite adapter and
// persistence middleware are the actual generated code and make real HTTP calls.
const browserEvents = new EventTarget();
Object.assign(globalThis, {
  window: Object.assign(browserEvents, {
    setTimeout,
    clearTimeout,
    location: new URL(apiBase),
  }),
});
const versions: unknown[] = [];
browserEvents.addEventListener('server-version-update', (event) => {
  versions.push((event as CustomEvent).detail);
});

const load = (path: string) => import(pathToFileURL(`${frontend}/src/${path}`).href);
const fixture = {
  provider: 'codex',
  fileName: 'pro-route-contract-synthetic.json',
  cachedAt: 1_790_741_234_567,
  data: {
    status: 'success',
    windows: [{ id: 'primary', label: 'Synthetic', usedPercent: 42, resetLabel: '1h' }],
    planType: 'plus',
    cachedAt: 1_790_741_234_567,
  },
};
const pluginFixture = {
  provider: 'plugin',
  fileName: 'pro-route-contract-plugin.json',
  cachedAt: fixture.cachedAt,
  data: {
    status: 'success',
    groups: [{ id: 'requests', label: 'Requests', buckets: [] }],
    subscription: { plan: 'Synthetic Plugin', tierName: null, tierId: null },
    summary: [{ key: 'balance', label: 'Balance', value: 12.5, format: 'currency', currency: 'USD' }],
    cachedAt: fixture.cachedAt,
  },
};
const invalidPluginFile = 'pro-route-contract-plugin-missing-summary.json';
const receipt: Record<string, unknown> = { phase, passed: false, checks: [] };
const checks = receipt.checks as string[];
let middleware: { stop(): void } | undefined;

try {
  const { apiClient } = await load('services/api/client.ts');
  const { configApi } = await load('services/api/config.ts');
  const { authFilesApi } = await load('services/api/authFiles.ts');
  const { authFileConnectionApi } = await load('pro/authFiles/connectionTestApi.ts');
  const { sqliteQuotaCache } = await load('pro/modules/quota/extensions/sqliteQuotaCache.ts');
  const { dataManagementApi } = await load('pro/modules/dataManagement/dataManagement.ts');
  const { proxyPoolApi } = await load('pro/modules/proxyPool/proxyPool.ts');
  apiClient.setConfig({ apiBase, managementKey: 'pro-route-contract-synthetic-key' });

  const rawConfig = await apiClient.get('/config');
  assert.equal(rawConfig['config-version'], 8, 'Native configuration must keep v8 layout');
  assert.equal(rawConfig.server.host, '127.0.0.1');
  assert.equal(typeof (await configApi.getConfig()), 'object');
  checks.push('native-v8-config');

  const credentials = await authFilesApi.list();
  assert.deepEqual(credentials.files, [], 'Fixture must contain no provider credentials');
  checks.push('native-v8-credentials');

  // Reject invalid input before any provider request. This is a Pro operation
  // despite living in the upstream authFilesApi domain module.
  await assert.rejects(authFileConnectionApi.testConnection({ name: '', model: '' }), (error: unknown) => {
    assert.equal(
      (error as { status?: number }).status,
      400,
      'Connection-test endpoint should validate input, not return route 404'
    );
    return true;
  });
  checks.push('pro-auth-files-test-validates-input');

  const overview = await dataManagementApi.overview();
  assert.equal(overview.service, 'pro-data-management');
  assert(overview.domains.some((domain: { id: string }) => domain.id === 'quota-cache'));
  checks.push('pro-data-overview');

  const pool = await proxyPoolApi.status();
  assert.equal(pool.ready, false);
  assert.equal(pool.totalNodes, 0);
  assert.deepEqual(pool.nodes, []);
  checks.push('pro-proxy-pool-status');

  if (phase === 'write') {
    assert.deepEqual(await sqliteQuotaCache.getAll(), [], 'Quota cache must start empty');
    assert.equal(
      await sqliteQuotaCache.set(
        fixture.provider,
        fixture.fileName,
        fixture.data,
        fixture.cachedAt
      ),
      true,
      'Actual SQLite client write must succeed'
    );
    checks.push('pro-quota-write');

    const { useQuotaStore } = await load('stores/useQuotaStore.ts');
    const { quotaPersistenceMiddleware } = await load('pro/modules/quota/extensions/persistenceMiddleware.ts');
    middleware = quotaPersistenceMiddleware;
    quotaPersistenceMiddleware.start();
    await quotaPersistenceMiddleware.ensureFresh();
    useQuotaStore.getState().setPluginQuota({ [pluginFixture.fileName]: pluginFixture.data });
    const deadline = Date.now() + 5_000;
    while (!(await sqliteQuotaCache.getAll()).some((row: { fileName: string }) => row.fileName === pluginFixture.fileName)) {
      assert(Date.now() < deadline, 'Plugin store update did not reach SQLite through persistence');
      await new Promise((resolve) => setTimeout(resolve, 20));
    }
    middleware.stop();
    assert.equal(await sqliteQuotaCache.set('plugin', invalidPluginFile, {
      status: 'success', groups: [], cachedAt: fixture.cachedAt,
    }, fixture.cachedAt), true);
    checks.push('plugin-store-persists-through-real-http');
  }

  const rows = await sqliteQuotaCache.getAll();
  assert.equal(rows.length, 3);
  const row = rows.find((item: { provider: string }) => item.provider === fixture.provider);
  assert(row, 'Codex cache row is missing');
  assert.equal(row.provider, fixture.provider);
  assert.equal(row.fileName, fixture.fileName);
  assert.deepEqual(row.data, fixture.data);
  assert.equal(row.cachedAt, fixture.cachedAt);
  assert.equal(row.observedAt, fixture.cachedAt);
  assert(row.revision > 0);
  const stats = await sqliteQuotaCache.getStats();
  assert.equal(stats.totalEntries, 3);
  assert(stats.generation > 0);
  receipt.quota = { revision: row.revision, generation: stats.generation, data: row.data };
  const pluginRow = rows.find((item: { fileName: string }) => item.fileName === pluginFixture.fileName);
  assert(pluginRow, 'Plugin cache row is missing');
  assert.equal(pluginRow.provider, pluginFixture.provider);
  assert.deepEqual(pluginRow.data, pluginFixture.data);
  assert.equal(pluginRow.cachedAt, pluginFixture.cachedAt);
  assert.equal(pluginRow.observedAt, pluginFixture.cachedAt);
  assert(pluginRow.revision > 0);
  receipt.pluginQuota = { revision: pluginRow.revision, data: pluginRow.data };
  checks.push('pro-quota-read-and-stats');

  if (phase === 'hydrate') {
    const { useQuotaStore } = await load('stores/useQuotaStore.ts');
    const { quotaPersistenceMiddleware } = await load(
      'pro/modules/quota/extensions/persistenceMiddleware.ts'
    );
    middleware = quotaPersistenceMiddleware;
    assert.deepEqual(useQuotaStore.getState().pluginQuota, {}, 'Fresh plugin map must be empty');
    assert.deepEqual(
      useQuotaStore.getState().codexQuota,
      {},
      'Fresh process must have empty memory'
    );
    quotaPersistenceMiddleware.start();
    await quotaPersistenceMiddleware.ensureFresh();
    assert.deepEqual(
      useQuotaStore.getState().codexQuota[fixture.fileName],
      fixture.data,
      'Restarted Core must hydrate a fresh real frontend store from SQLite'
    );
    checks.push('restart-hydrates-fresh-zustand-store');
    assert.deepEqual(
      useQuotaStore.getState().pluginQuota[pluginFixture.fileName],
      pluginFixture.data,
      'Plugin quota must survive Core restart and hydrate the upstream store'
    );
    assert.equal(useQuotaStore.getState().pluginQuota[invalidPluginFile], undefined,
      'Malformed plugin state without summary must not hydrate');
    checks.push('restart-hydrates-plugin-and-rejects-malformed-state');
  }

  assert(versions.length > 0, 'Shared client must still dispatch server version events');
  receipt.serverVersion = versions.at(-1);
  checks.push('shared-client-response-events');
  receipt.passed = true;
} catch (error) {
  receipt.error = error instanceof Error ? error.stack || error.message : String(error);
  process.exitCode = 1;
} finally {
  middleware?.stop();
  await writeFile(output, `${JSON.stringify(receipt, null, 2)}\n`);
  console.log(JSON.stringify({ phase, passed: receipt.passed, output }));
}
