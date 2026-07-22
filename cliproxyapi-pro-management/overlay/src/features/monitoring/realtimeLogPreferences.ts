const REALTIME_LOG_COLUMNS_STORAGE_KEY = 'cli-proxy-realtime-log-columns-v2';
const REALTIME_LOG_FOLLOW_STORAGE_KEY = 'cli-proxy-realtime-log-follow-v1';

export const REALTIME_LOG_COLUMN_KEYS = [
  'type',
  'model',
  'reasoningEffort',
  'stream',
  'apiKey',
  'recent',
  'status',
  'successRate',
  'calls',
  'ttft',
  'latency',
  'tokens',
  'cacheRead',
  'cost',
  'time',
] as const;

export type RealtimeLogColumnKey = typeof REALTIME_LOG_COLUMN_KEYS[number];

export type RealtimeLogColumnPreference = {
  key: RealtimeLogColumnKey;
  visible: boolean;
  width?: number;
};

export const REALTIME_LOG_COLUMN_DEFAULT_WIDTHS: Record<RealtimeLogColumnKey, number> = {
  type: 170,
  model: 230,
  reasoningEffort: 116,
  stream: 108,
  apiKey: 145,
  recent: 86,
  status: 180,
  successRate: 86,
  calls: 76,
  ttft: 92,
  latency: 96,
  tokens: 196,
  cacheRead: 126,
  cost: 132,
  time: 164,
};

const REALTIME_LOG_COLUMN_MIN_WIDTHS: Record<RealtimeLogColumnKey, number> = {
  type: 96,
  model: 132,
  reasoningEffort: 96,
  stream: 92,
  apiKey: 104,
  recent: 76,
  status: 120,
  successRate: 76,
  calls: 68,
  ttft: 76,
  latency: 76,
  tokens: 164,
  cacheRead: 108,
  cost: 112,
  time: 116,
};

const REALTIME_LOG_COLUMN_MAX_WIDTH = 420;
const REALTIME_LOG_COLUMN_MAX_WIDTHS: Partial<Record<RealtimeLogColumnKey, number>> = {
  type: 240,
};
const REALTIME_LOG_COLUMN_KEY_SET = new Set<RealtimeLogColumnKey>(REALTIME_LOG_COLUMN_KEYS);

export const createDefaultRealtimeLogColumns = (): RealtimeLogColumnPreference[] => (
  REALTIME_LOG_COLUMN_KEYS.map((key) => ({ key, visible: true }))
);

const REALTIME_LOG_DEFAULT_COLUMNS = createDefaultRealtimeLogColumns();

export const isRealtimeLogColumnKey = (value: unknown): value is RealtimeLogColumnKey => (
  typeof value === 'string' && REALTIME_LOG_COLUMN_KEY_SET.has(value as RealtimeLogColumnKey)
);

export const clampRealtimeLogColumnWidth = (key: RealtimeLogColumnKey, width: unknown) => {
  const numericWidth = typeof width === 'number' && Number.isFinite(width)
    ? width
    : REALTIME_LOG_COLUMN_DEFAULT_WIDTHS[key];
  const maxWidth = REALTIME_LOG_COLUMN_MAX_WIDTHS[key] ?? REALTIME_LOG_COLUMN_MAX_WIDTH;
  return Math.min(maxWidth, Math.max(REALTIME_LOG_COLUMN_MIN_WIDTHS[key], Math.round(numericWidth)));
};

const normalizeRealtimeLogColumnWidth = (key: RealtimeLogColumnKey, width: unknown) => (
  typeof width === 'number' && Number.isFinite(width)
    ? clampRealtimeLogColumnWidth(key, width)
    : undefined
);

export const normalizeRealtimeLogColumns = (value: unknown): RealtimeLogColumnPreference[] => {
  const next: RealtimeLogColumnPreference[] = [];
  const seen = new Set<RealtimeLogColumnKey>();

  if (Array.isArray(value)) {
    value.forEach((item) => {
      if (!item || typeof item !== 'object') return;
      const key = (item as { key?: unknown }).key;
      if (key === 'usage') {
        const visible = (item as { visible?: unknown }).visible !== false;
        (['tokens', 'cacheRead'] as const).forEach((replacementKey) => {
          if (seen.has(replacementKey)) return;
          next.push({ key: replacementKey, visible });
          seen.add(replacementKey);
        });
        return;
      }
      if (!isRealtimeLogColumnKey(key) || seen.has(key)) return;
      next.push({
        key,
        visible: (item as { visible?: unknown }).visible !== false,
        width: normalizeRealtimeLogColumnWidth(key, (item as { width?: unknown }).width),
      });
      seen.add(key);
    });
  }

  const shouldMigrateReasoningEffort = next.length > 0 && !seen.has('reasoningEffort');
  const shouldMigrateStream = next.length > 0 && !seen.has('stream');

  REALTIME_LOG_DEFAULT_COLUMNS.forEach((item) => {
    if (!seen.has(item.key)) next.push({ ...item });
  });

  if (shouldMigrateReasoningEffort) {
    const reasoningEffortIndex = next.findIndex((item) => item.key === 'reasoningEffort');
    const modelIndex = next.findIndex((item) => item.key === 'model');
    if (reasoningEffortIndex >= 0 && modelIndex >= 0) {
      const [reasoningEffortColumn] = next.splice(reasoningEffortIndex, 1);
      const migratedModelIndex = next.findIndex((item) => item.key === 'model');
      next.splice(migratedModelIndex + 1, 0, reasoningEffortColumn);
    }
  }

  if (shouldMigrateStream) {
    const streamIndex = next.findIndex((item) => item.key === 'stream');
    const reasoningEffortIndex = next.findIndex((item) => item.key === 'reasoningEffort');
    if (streamIndex >= 0 && reasoningEffortIndex >= 0) {
      const [streamColumn] = next.splice(streamIndex, 1);
      const migratedReasoningEffortIndex = next.findIndex((item) => item.key === 'reasoningEffort');
      next.splice(migratedReasoningEffortIndex + 1, 0, streamColumn);
    }
  }

  const timeColumn = next.find((item) => item.key === 'time');
  const ordered = timeColumn ? [...next.filter((item) => item.key !== 'time'), timeColumn] : next;
  return ordered.some((item) => item.visible) ? ordered : createDefaultRealtimeLogColumns();
};

export const loadRealtimeLogColumns = () => {
  if (typeof window === 'undefined') return createDefaultRealtimeLogColumns();
  try {
    return normalizeRealtimeLogColumns(JSON.parse(window.localStorage.getItem(REALTIME_LOG_COLUMNS_STORAGE_KEY) || 'null'));
  } catch {
    return createDefaultRealtimeLogColumns();
  }
};

export const saveRealtimeLogColumns = (columns: RealtimeLogColumnPreference[]) => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(REALTIME_LOG_COLUMNS_STORAGE_KEY, JSON.stringify(columns));
  } catch {
    // Column preferences are convenience state; storage failures should not break logs.
  }
};

export const loadRealtimeLogFollowEnabled = () => {
  if (typeof window === 'undefined') return true;
  try {
    return window.localStorage.getItem(REALTIME_LOG_FOLLOW_STORAGE_KEY) !== 'false';
  } catch {
    return true;
  }
};

export const saveRealtimeLogFollowEnabled = (enabled: boolean) => {
  if (typeof window === 'undefined') return;
  try {
    window.localStorage.setItem(REALTIME_LOG_FOLLOW_STORAGE_KEY, String(enabled));
  } catch {
    // Live-follow preference is optional; storage failures should not break logs.
  }
};
