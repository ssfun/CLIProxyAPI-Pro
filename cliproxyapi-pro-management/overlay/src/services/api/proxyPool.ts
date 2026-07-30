import { parseDocument } from 'yaml';
import { authFilesApi } from './authFiles';
import { apiClient } from './client';
import { configFileApi } from './configFile';
import { pluginsApi } from './plugins';

export const PROXY_POOL_PLUGIN_ID = 'proxy-pool';
export const DEFAULT_PROXY_POOL_LISTEN = '127.0.0.1:8318';

export type ProxyPoolStrategy = 'round-robin' | 'weighted' | 'least-connections';
export type ProxyPoolHealthState = 'unknown' | 'healthy' | 'degraded' | 'isolated' | 'disabled';

export interface ProxyPoolNodeConfig {
  id: string;
  label: string;
  url: string;
  enabled: boolean;
  weight: number;
  order: number;
}

export interface ProxyPoolConfig {
  enabled: boolean;
  priority: number;
  listen: string;
  strategy: ProxyPoolStrategy;
  dialTimeout: string;
  maxFailoverAttempts: number;
  failOpen: boolean;
  restoreProxyUrl: string;
  healthCheck: {
    enabled: boolean;
    interval: string;
    timeout: string;
    isolationThreshold: number;
    isolationDuration: string;
    probeAddress: string;
    testUrl: string;
  };
  nodes: ProxyPoolNodeConfig[];
}

export interface ProxyPoolNodeStatus extends Omit<ProxyPoolNodeConfig, 'url'> {
  displayUrl: string;
  state: ProxyPoolHealthState;
  isolationUntil: string;
  lastCheck: string;
  lastSuccess: string;
  lastFailure: string;
  lastError: string;
  latencyMs: number;
  exitIp: string;
  location: string;
  consecutiveFailures: number;
  activeTunnels: number;
  totalConnects: number;
  successConnects: number;
  failedConnects: number;
}

export interface ProxyPoolStatus {
  ready: boolean;
  listen: string;
  proxyUrl: string;
  strategy: ProxyPoolStrategy;
  generation: number;
  activeTunnels: number;
  totalNodes: number;
  healthyNodes: number;
  isolatedNodes: number;
  lastError: string;
  startedAt: string;
  lastAppliedAt: string;
  lastHealthAt: string;
  nodes: ProxyPoolNodeStatus[];
}

export interface ProxyPoolImportResult {
  nodes: ProxyPoolNodeConfig[];
  duplicateCount: number;
  errors: Array<{ line: number; message: string }>;
}

export interface ProxyPoolProbeResult {
  success: boolean;
  nodeId: string;
  latencyMs: number;
  exitIp: string;
  location: string;
  error: string;
  checkedAt: string;
}

export interface ProxyPoolBypassCredential {
  name: string;
  provider: string;
  proxyUrl: string;
}

export interface ProxyPoolSnapshot {
  pluginsEnabled: boolean;
  pluginDiscovered: boolean;
  pluginEnabled: boolean;
  pluginRegistered: boolean;
  config: ProxyPoolConfig;
  status: ProxyPoolStatus | null;
  globalProxyUrl: string;
  takeoverActive: boolean;
  bypassCredentials: ProxyPoolBypassCredential[];
}

const asRecord = (value: unknown): Record<string, unknown> =>
  value && typeof value === 'object' && !Array.isArray(value)
    ? (value as Record<string, unknown>)
    : {};
const asString = (value: unknown, fallback = ''): string =>
  typeof value === 'string' ? value : value == null ? fallback : String(value);
const asBoolean = (value: unknown, fallback = false): boolean =>
  typeof value === 'boolean' ? value : fallback;
const asNumber = (value: unknown, fallback = 0): number => {
  const result = Number(value);
  return Number.isFinite(result) ? result : fallback;
};

export const defaultProxyPoolConfig = (): ProxyPoolConfig => ({
  enabled: true,
  priority: 100,
  listen: DEFAULT_PROXY_POOL_LISTEN,
  strategy: 'round-robin',
  dialTimeout: '8s',
  maxFailoverAttempts: 3,
  failOpen: false,
  restoreProxyUrl: '',
  healthCheck: {
    enabled: true,
    interval: '30s',
    timeout: '8s',
    isolationThreshold: 3,
    isolationDuration: '5m',
    probeAddress: 'www.gstatic.com:443',
    testUrl: 'https://ipwho.is/',
  },
  nodes: [],
});

const normalizeStrategy = (value: unknown): ProxyPoolStrategy => {
  const strategy = asString(value);
  return strategy === 'weighted' || strategy === 'least-connections'
    ? strategy
    : 'round-robin';
};

export const normalizeProxyPoolConfig = (value: unknown): ProxyPoolConfig => {
  const source = asRecord(value);
  const health = asRecord(source['health-check']);
  const defaults = defaultProxyPoolConfig();
  const nodes = Array.isArray(source.nodes)
    ? source.nodes.map((item, index): ProxyPoolNodeConfig => {
        const node = asRecord(item);
        return {
          id: asString(node.id).trim() || `proxy-${index + 1}`,
          label: asString(node.label).trim(),
          url: asString(node.url).trim(),
          enabled: asBoolean(node.enabled, true),
          weight: Math.max(1, Math.trunc(asNumber(node.weight, 1))),
          order: Math.trunc(asNumber(node.order, (index + 1) * 10)),
        };
      })
    : [];
  return {
    enabled: asBoolean(source.enabled, true),
    priority: Math.trunc(asNumber(source.priority, defaults.priority)),
    listen: asString(source.listen, defaults.listen).trim() || defaults.listen,
    strategy: normalizeStrategy(source.strategy),
    dialTimeout: asString(source['dial-timeout'], defaults.dialTimeout).trim(),
    maxFailoverAttempts: Math.max(
      1,
      Math.trunc(asNumber(source['max-failover-attempts'], defaults.maxFailoverAttempts))
    ),
    failOpen: asBoolean(source['fail-open'], defaults.failOpen),
    restoreProxyUrl: asString(source['restore-proxy-url']).trim(),
    healthCheck: {
      enabled: asBoolean(health.enabled, defaults.healthCheck.enabled),
      interval: asString(health.interval, defaults.healthCheck.interval).trim(),
      timeout: asString(health.timeout, defaults.healthCheck.timeout).trim(),
      isolationThreshold: Math.max(
        1,
        Math.trunc(
          asNumber(health['isolation-threshold'], defaults.healthCheck.isolationThreshold)
        )
      ),
      isolationDuration: asString(
        health['isolation-duration'],
        defaults.healthCheck.isolationDuration
      ).trim(),
      probeAddress: asString(
        health['probe-address'],
        defaults.healthCheck.probeAddress
      ).trim(),
      testUrl: asString(health['test-url'], defaults.healthCheck.testUrl).trim(),
    },
    nodes,
  };
};

export const serializeProxyPoolConfig = (config: ProxyPoolConfig): Record<string, unknown> => ({
  enabled: true,
  priority: config.priority,
  listen: config.listen.trim(),
  strategy: config.strategy,
  'dial-timeout': config.dialTimeout.trim(),
  'max-failover-attempts': config.maxFailoverAttempts,
  'fail-open': config.failOpen,
  'restore-proxy-url': config.restoreProxyUrl.trim(),
  'health-check': {
    enabled: config.healthCheck.enabled,
    interval: config.healthCheck.interval.trim(),
    timeout: config.healthCheck.timeout.trim(),
    'isolation-threshold': config.healthCheck.isolationThreshold,
    'isolation-duration': config.healthCheck.isolationDuration.trim(),
    'probe-address': config.healthCheck.probeAddress.trim(),
    'test-url': config.healthCheck.testUrl.trim(),
  },
  nodes: config.nodes.map((node, index) => ({
    id: node.id.trim(),
    label: node.label.trim(),
    url: node.url.trim(),
    enabled: node.enabled,
    weight: Math.max(1, Math.trunc(node.weight)),
    order: Math.trunc(node.order || (index + 1) * 10),
  })),
});

const proxyUrlPattern = /^(?:https?|socks5h?):\/\//i;

const proxyUrlKey = (value: string): string => {
  try {
    return new URL(value.trim()).toString();
  } catch {
    return value.trim();
  }
};

const proxyIDPart = (value: string): string =>
  value
    .trim()
    .toLowerCase()
    .replace(/[^a-z0-9]+/g, '-')
    .replace(/^-+|-+$/g, '')
    .slice(0, 28);

// Accept one URL per line, or "label | URL | weight". Comma, tab, and
// whitespace-separated label/URL pairs are accepted as a convenience, while
// the pipe form remains unambiguous for labels containing spaces.
export const parseProxyPoolImport = (
  input: string,
  existing: ProxyPoolNodeConfig[] = []
): ProxyPoolImportResult => {
  const result: ProxyPoolImportResult = { nodes: [], duplicateCount: 0, errors: [] };
  const seenURLs = new Set(existing.map((node) => proxyUrlKey(node.url)));
  const seenIDs = new Set(existing.map((node) => node.id.trim()));
  let nextOrder = existing.reduce((maximum, node) => Math.max(maximum, node.order), 0) + 10;

  input.split(/\r?\n/).forEach((rawLine, index) => {
    const line = rawLine.trim();
    if (!line || line.startsWith('#')) return;
    const parts = line.includes('|')
      ? line.split('|').map((part) => part.trim()).filter(Boolean)
      : line.includes(',')
        ? line.split(',').map((part) => part.trim()).filter(Boolean)
        : line.split(/\s+/).filter(Boolean);
    const urlIndex = parts.findIndex((part) => proxyUrlPattern.test(part));
    if (urlIndex < 0) {
      result.errors.push({ line: index + 1, message: 'missing supported proxy URL' });
      return;
    }
    const url = parts[urlIndex];
    try {
      const parsed = new URL(url);
      if (!parsed.hostname || !proxyUrlPattern.test(url)) throw new Error('invalid proxy URL');
    } catch {
      result.errors.push({ line: index + 1, message: 'invalid proxy URL' });
      return;
    }
    const key = proxyUrlKey(url);
    if (seenURLs.has(key)) {
      result.duplicateCount += 1;
      return;
    }
    const label = urlIndex > 0 ? parts.slice(0, urlIndex).join(' ') : '';
    const rawWeight = parts[urlIndex + 1];
    const parsedWeight = rawWeight && /^\d+$/.test(rawWeight) ? Number(rawWeight) : 1;
    let base = proxyIDPart(label);
    if (!base) {
      try {
        base = proxyIDPart(new URL(url).hostname);
      } catch {
        base = 'node';
      }
    }
    let id = `proxy-${base || 'node'}`;
    let suffix = 2;
    while (seenIDs.has(id)) {
      id = `proxy-${base || 'node'}-${suffix}`;
      suffix += 1;
    }
    result.nodes.push({
      id,
      label,
      url,
      enabled: true,
      weight: Math.max(1, Math.trunc(parsedWeight)),
      order: nextOrder,
    });
    nextOrder += 10;
    seenIDs.add(id);
    seenURLs.add(key);
  });
  return result;
};

const normalizeNodeStatus = (value: unknown): ProxyPoolNodeStatus => {
  const source = asRecord(value);
  return {
    id: asString(source.id),
    label: asString(source.label),
    displayUrl: asString(source.display_url),
    enabled: asBoolean(source.enabled),
    weight: asNumber(source.weight, 1),
    order: asNumber(source.order),
    state: asString(source.state, 'unknown') as ProxyPoolHealthState,
    isolationUntil: asString(source.isolation_until),
    lastCheck: asString(source.last_check),
    lastSuccess: asString(source.last_success),
    lastFailure: asString(source.last_failure),
    lastError: asString(source.last_error),
    latencyMs: asNumber(source.latency_ms),
    exitIp: asString(source.exit_ip),
    location: asString(source.location),
    consecutiveFailures: asNumber(source.consecutive_failures),
    activeTunnels: asNumber(source.active_tunnels),
    totalConnects: asNumber(source.total_connects),
    successConnects: asNumber(source.success_connects),
    failedConnects: asNumber(source.failed_connects),
  };
};

const normalizeStatus = (value: unknown): ProxyPoolStatus => {
  const source = asRecord(value);
  return {
    ready: asBoolean(source.ready),
    listen: asString(source.listen),
    proxyUrl: asString(source.proxy_url),
    strategy: normalizeStrategy(source.strategy),
    generation: asNumber(source.generation),
    activeTunnels: asNumber(source.active_tunnels),
    totalNodes: asNumber(source.total_nodes),
    healthyNodes: asNumber(source.healthy_nodes),
    isolatedNodes: asNumber(source.isolated_nodes),
    lastError: asString(source.last_error),
    startedAt: asString(source.started_at),
    lastAppliedAt: asString(source.last_applied_at),
    lastHealthAt: asString(source.last_health_at),
    nodes: Array.isArray(source.nodes) ? source.nodes.map(normalizeNodeStatus) : [],
  };
};

const normalizeProbeResult = (value: unknown): ProxyPoolProbeResult => {
  const source = asRecord(value);
  return {
    success: asBoolean(source.success),
    nodeId: asString(source.node_id),
    latencyMs: asNumber(source.latency_ms),
    exitIp: asString(source.exit_ip),
    location: asString(source.location),
    error: asString(source.error),
    checkedAt: asString(source.checked_at),
  };
};

const readGlobalProxyUrl = async (): Promise<string> => {
  const response = asRecord(await apiClient.get('/proxy-url'));
  return asString(response['proxy-url']).trim();
};

const localProxyUrl = (listen: string): string => `socks5://${listen.trim()}`;

export const isProxyPoolListenerUrl = (proxyUrl: string, listen: string): boolean => {
  try {
    const parsed = new URL(proxyUrl.trim());
    const protocol = parsed.protocol.toLowerCase();
    return (
      (protocol === 'socks5:' || protocol === 'socks5h:') &&
      parsed.host.toLowerCase() === listen.trim().toLowerCase()
    );
  } catch {
    return false;
  }
};

const ensureGlobalPluginSwitch = async (): Promise<void> => {
  const raw = await configFileApi.fetchConfigYaml();
  const document = parseDocument(raw || '{}');
  if (document.errors.length > 0) throw document.errors[0];
  document.setIn(['plugins', 'enabled'], true);
  await configFileApi.saveConfigYaml(document.toString());
};

const waitForStatus = async (
  listen: string,
  minimumGeneration = 0,
  attempts = 30
): Promise<ProxyPoolStatus> => {
  let lastError: unknown = null;
  for (let attempt = 0; attempt < attempts; attempt += 1) {
    try {
      const status = normalizeStatus(await apiClient.get('/pro/proxy-pool/status'));
      if (
        status.ready &&
        status.listen === listen.trim() &&
        status.generation >= minimumGeneration
      ) {
        return status;
      }
    } catch (error) {
      lastError = error;
    }
    await new Promise((resolve) => window.setTimeout(resolve, 250));
  }
  throw lastError instanceof Error
    ? lastError
    : new Error('Proxy pool plugin did not become ready in time');
};

const loadBypassCredentials = async (): Promise<ProxyPoolBypassCredential[]> => {
  const result: ProxyPoolBypassCredential[] = [];
  try {
    const response = await authFilesApi.list();
    response.files.forEach((file) => {
      const proxyUrl = asString(file.proxy_url ?? file['proxy-url']).trim();
      if (!proxyUrl) return;
      result.push({
        name: asString(file.name),
        provider: asString(file.provider ?? file.type),
        proxyUrl,
      });
    });
  } catch {
    // The proxy-pool page remains usable when auth-file inventory is unavailable.
  }
  try {
    const rawConfig = await apiClient.get('/config');
    const walk = (value: unknown, path: string[]) => {
      if (Array.isArray(value)) {
        value.forEach((item, index) => walk(item, [...path, String(index)]));
        return;
      }
      const record = asRecord(value);
      Object.entries(record).forEach(([key, child]) => {
        if (key === 'proxy-url' && path.length > 0) {
          const proxyUrl = asString(child).trim();
          if (proxyUrl) {
            result.push({
              name: path.join('.'),
              provider: path[0] || 'config',
              proxyUrl,
            });
          }
          return;
        }
        walk(child, [...path, key]);
      });
    };
    walk(rawConfig, []);
  } catch {
    // Config inventory is an advisory warning and must not block pool control.
  }
  const seen = new Set<string>();
  return result.filter((item) => {
    const key = `${item.name}\u0000${item.proxyUrl}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
};

export const proxyPoolApi = {
  async load(): Promise<ProxyPoolSnapshot> {
    const [pluginList, rawConfig, globalProxyUrl, bypassCredentials] = await Promise.all([
      pluginsApi.list(),
      pluginsApi.getConfig(PROXY_POOL_PLUGIN_ID),
      readGlobalProxyUrl(),
      loadBypassCredentials(),
    ]);
    const plugin = pluginList.plugins.find((entry) => entry.id === PROXY_POOL_PLUGIN_ID);
    let status: ProxyPoolStatus | null = null;
    if (plugin?.registered) {
      try {
        status = normalizeStatus(await apiClient.get('/pro/proxy-pool/status'));
      } catch {
        status = null;
      }
    }
    const config = normalizeProxyPoolConfig(rawConfig);
    const effectiveBypassCredentials = bypassCredentials.filter(
      (item) => !isProxyPoolListenerUrl(item.proxyUrl, config.listen)
    );
    return {
      pluginsEnabled: pluginList.pluginsEnabled,
      pluginDiscovered: Boolean(plugin),
      pluginEnabled: Boolean(plugin?.enabled),
      pluginRegistered: Boolean(plugin?.registered),
      config,
      status,
      globalProxyUrl,
      takeoverActive: globalProxyUrl === localProxyUrl(config.listen),
      bypassCredentials: effectiveBypassCredentials,
    };
  },

  async save(config: ProxyPoolConfig, preserveTakeover = false): Promise<ProxyPoolStatus> {
    const pluginList = await pluginsApi.list();
    const plugin = pluginList.plugins.find((entry) => entry.id === PROXY_POOL_PLUGIN_ID);
    if (!plugin) throw new Error('Bundled proxy-pool plugin was not found');
    let minimumGeneration = 1;
    if (plugin.registered) {
      try {
        minimumGeneration = (await this.status()).generation + 1;
      } catch {
        minimumGeneration = 1;
      }
    }
    await pluginsApi.patchConfig(PROXY_POOL_PLUGIN_ID, serializeProxyPoolConfig(config));
    if (!plugin.enabled) await pluginsApi.updateEnabled(PROXY_POOL_PLUGIN_ID, true);
    if (!pluginList.pluginsEnabled) await ensureGlobalPluginSwitch();
    const status = await waitForStatus(config.listen, minimumGeneration);
    if (preserveTakeover) {
      await apiClient.put('/proxy-url', { value: status.proxyUrl });
    }
    return status;
  },

  async activate(config: ProxyPoolConfig): Promise<ProxyPoolStatus> {
    if (!config.nodes.some((node) => node.enabled && node.url.trim())) {
      throw new Error('At least one enabled proxy node is required');
    }
    const currentProxyUrl = await readGlobalProxyUrl();
    const nextLocalProxyUrl = localProxyUrl(config.listen);
    const nextConfig = {
      ...config,
      restoreProxyUrl:
        currentProxyUrl && currentProxyUrl !== nextLocalProxyUrl
          ? currentProxyUrl
          : config.restoreProxyUrl,
    };
    const status = await this.save(nextConfig);
    await apiClient.put('/proxy-url', { value: status.proxyUrl });
    return status;
  },

  async deactivate(config: ProxyPoolConfig): Promise<void> {
    await apiClient.put('/proxy-url', { value: config.restoreProxyUrl.trim() });
  },

  async status(): Promise<ProxyPoolStatus> {
    return normalizeStatus(await apiClient.get('/pro/proxy-pool/status'));
  },

  async testNode(
    nodeId: string,
    proxyUrl = '',
    testUrl = ''
  ): Promise<ProxyPoolProbeResult> {
    try {
      return normalizeProbeResult(
        await apiClient.post('/pro/proxy-pool/test', {
          node_id: nodeId,
          proxy_url: proxyUrl.trim(),
          url: testUrl.trim(),
        })
      );
    } catch (error) {
      const details = asRecord((error as { details?: unknown })?.details);
      if (details.node_id) return normalizeProbeResult(details);
      throw error;
    }
  },

  async testAll(): Promise<ProxyPoolProbeResult[]> {
    const response = asRecord(
      await apiClient.post('/pro/proxy-pool/test-all', { concurrency: 4 })
    );
    return Array.isArray(response.results) ? response.results.map(normalizeProbeResult) : [];
  },

  resetStats: () => apiClient.post('/pro/proxy-pool/reset-stats'),

  recoverNode: (nodeId: string) =>
    apiClient.post('/pro/proxy-pool/recover', { node_id: nodeId }),
};
