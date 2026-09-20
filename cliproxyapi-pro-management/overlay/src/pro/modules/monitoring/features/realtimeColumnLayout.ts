import type { RealtimeLogColumnKey } from './realtimeLogPreferences';

export interface RealtimeColumnSize {
  key: RealtimeLogColumnKey;
  preferred: number;
  header: number;
  manual?: number;
}

// Text columns absorb changes in available space; short metrics keep their
// measured width. These limits apply only to automatic sizing.
const flexible = {
  type: { min: 160, ideal: 208, max: 280, grow: 1, shrink: 0 },
  apiKey: { min: 136, ideal: 160, max: 192, grow: 0, shrink: 0 },
  model: { min: 220, ideal: 300, max: 520, grow: 3, shrink: 2 },
  time: { min: 120, ideal: 164, max: 220, grow: 0, shrink: 1 },
} satisfies Partial<Record<RealtimeLogColumnKey, object>>;

const metricMinimums: Partial<Record<RealtimeLogColumnKey, number>> = {
  reasoningEffort: 96, stream: 108, status: 96, latency: 116, tokens: 164, cacheRead: 108, cost: 112, recent: 86,
};

export function allocateRealtimeColumnWidths(columns: RealtimeColumnSize[], available: number) {
  const sizes = columns.map((column) => {
    const policy = flexible[column.key as keyof typeof flexible];
    const locked = column.manual !== undefined;
    const min = locked ? column.manual! : Math.max(column.header, policy?.min ?? Math.max(column.preferred, metricMinimums[column.key] ?? 68));
    const max = locked ? min : Math.max(min, policy?.max ?? column.preferred);
    const width = locked ? min : Math.min(max, Math.max(min, column.preferred, policy?.ideal ?? 0));
    return { key: column.key, width, min, max, grow: locked ? 0 : policy?.grow ?? 0, shrink: locked ? -1 : policy?.shrink ?? -1 };
  });
  let spare = Math.floor(available) - sizes.reduce((sum, size) => sum + size.width, 0);
  for (const priority of [0, 1, 2]) {
    const candidates = sizes.filter((size) => size.shrink === priority);
    for (const size of candidates) {
      const reduction = Math.min(Math.max(0, -spare), size.width - size.min);
      size.width -= reduction;
      spare += reduction;
    }
  }
  while (spare >= 1) {
    const candidates = sizes.filter((size) => size.grow > 0 && size.width < size.max);
    if (!candidates.length) break;
    const weight = candidates.reduce((sum, size) => sum + size.grow, 0);
    const budget = spare;
    for (const size of candidates) {
      const increase = Math.min(spare, size.max - size.width, Math.max(1, Math.floor(budget * size.grow / weight)));
      size.width += increase;
      spare -= increase;
    }
  }
  return sizes.map(({ key, width }) => ({ key, width }));
}

export function retainRealtimeColumnSample<T>(previous: { epoch: string; ready: boolean; value: T } | null, epoch: string, ready: boolean, measure: () => T) {
  return previous?.epoch === epoch && previous.ready ? previous : { epoch, ready, value: measure() };
}
