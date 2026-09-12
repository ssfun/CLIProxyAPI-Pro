export const formatTokenCount = (value: number) =>
  Math.max(0, Math.round(Number(value) || 0)).toLocaleString();

export const getCacheHitRate = (
  row: { cacheInputTokens: number; cachedTokens: number }
): number | null =>
  row.cacheInputTokens > 0
    ? Math.min(Math.max(row.cachedTokens / row.cacheInputTokens, 0), 1)
    : null;
