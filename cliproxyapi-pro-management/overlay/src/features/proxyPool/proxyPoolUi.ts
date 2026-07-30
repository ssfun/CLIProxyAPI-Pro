import type { ProxyPoolHealthState, ProxyPoolNodeConfig } from '@/services/api/proxyPool';

export type ProxyPoolView = 'nodes' | 'diagnostics' | 'settings';
export type ProxyPoolStatusFilter = 'all' | ProxyPoolHealthState;
export type ProxyPoolDurationUnit = 's' | 'm';

export const proxyNodeKey = (node: ProxyPoolNodeConfig, index: number): string =>
  node.id.trim() || `draft-${index + 1}`;

export const maskProxyCredentials = (value: string): string => {
  const schemeEnd = value.indexOf('//');
  const credentialsEnd = value.lastIndexOf('@');
  if (schemeEnd < 0 || credentialsEnd <= schemeEnd + 2) return value;
  return `${value.slice(0, schemeEnd + 2)}***@${value.slice(credentialsEnd + 1)}`;
};

export const formatProxyPoolTime = (value: string, language: string): string => {
  if (!value || value.startsWith('0001-')) return '-';
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return '-';
  return new Intl.DateTimeFormat(language, {
    month: '2-digit',
    day: '2-digit',
    hour: '2-digit',
    minute: '2-digit',
    second: '2-digit',
  }).format(date);
};

export const formatProxyPoolSuccessRate = (success: number, total: number): string => {
  if (!Number.isFinite(total) || total <= 0) return '-';
  const normalizedSuccess = Math.min(Math.max(Number.isFinite(success) ? success : 0, 0), total);
  return `${Math.round((normalizedSuccess / total) * 1000) / 10}%`;
};

const durationUnitMilliseconds: Record<string, number> = {
  ns: 0.000001,
  us: 0.001,
  µs: 0.001,
  μs: 0.001,
  ms: 1,
  s: 1000,
  m: 60_000,
  h: 3_600_000,
};

export const proxyPoolDurationValue = (
  value: string,
  targetUnit: ProxyPoolDurationUnit
): number | null => {
  const source = value.trim();
  if (!source) return null;
  const pattern = /(-?\d+(?:\.\d+)?)(ns|us|µs|μs|ms|s|m|h)/g;
  let milliseconds = 0;
  let cursor = 0;
  let matched = false;
  for (const match of source.matchAll(pattern)) {
    if (match.index !== cursor) return null;
    milliseconds += Number(match[1]) * durationUnitMilliseconds[match[2]];
    cursor = match.index + match[0].length;
    matched = true;
  }
  if (!matched || cursor !== source.length || !Number.isFinite(milliseconds) || milliseconds <= 0) {
    return null;
  }
  return milliseconds / durationUnitMilliseconds[targetUnit];
};

export const serializeProxyPoolDuration = (value: number, unit: ProxyPoolDurationUnit): string =>
  `${Math.round(value * 1000) / 1000}${unit}`;

export const proxyPoolStateLabel = (state: ProxyPoolHealthState): string => {
  if (state === 'healthy') return 'Healthy';
  if (state === 'degraded') return 'Degraded';
  if (state === 'isolated') return 'Isolated';
  if (state === 'disabled') return 'Disabled';
  return 'Unknown';
};

export const createProxyPoolNode = (index: number): ProxyPoolNodeConfig => ({
  id:
    typeof crypto !== 'undefined' && typeof crypto.randomUUID === 'function'
      ? `proxy-${crypto.randomUUID().slice(0, 8)}`
      : `proxy-${Date.now().toString(36)}-${index + 1}`,
  label: '',
  url: '',
  enabled: true,
  weight: 1,
  order: (index + 1) * 10,
});
