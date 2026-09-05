// Per mounted connection/dataset; no shared cache of authenticated data.
export function createSummaryCache<T>(ttlMs = 60_000) {
  let entry: { key: string; value: T; fetchedAt: number } | undefined;
  let revision = 0;
  return {
    remainingTtlMs() {
      return entry ? Math.max(0, ttlMs - (Date.now() - entry.fetchedAt)) : 0;
    },
    clear() {
      entry = undefined;
      revision += 1;
    },
    async load(key: string, fetch: () => Promise<T>, force = false): Promise<T> {
      if (!force && entry?.key === key && Date.now() - entry.fetchedAt < ttlMs) return entry.value;
      const request = ++revision;
      const value = await fetch();
      if (request === revision) entry = { key, value, fetchedAt: Date.now() };
      return value;
    },
  };
}
