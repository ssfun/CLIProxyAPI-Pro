import type { QuotaCacheEntry } from './sqliteQuotaCache';

const isRecord = (value: unknown): value is Record<string, unknown> =>
  value !== null && typeof value === 'object' && !Array.isArray(value);

const isNormalizedGeminiQuotaSnapshot = (data: unknown): boolean =>
  isRecord(data) && data.status === undefined && Array.isArray(data.items);

const isMetaQuotaData = (value: unknown): value is Record<string, unknown> => {
  return isRecord(value) && Array.isArray(value.windows);
};

export const isAuthCardQuotaCacheDataCompatible = (provider: string, data: unknown): boolean => {
  if (provider === 'gemini-cli' && isNormalizedGeminiQuotaSnapshot(data)) return true;
  if (!isRecord(data)) return false;

  const status = data.status;
  if (!['idle', 'loading', 'success', 'error'].includes(String(status))) return false;
  if (status !== 'success') return true;

  switch (provider) {
    case 'plugin':
      return Array.isArray(data.summary) && isAuthCardQuotaCacheDataCompatible('antigravity', data);
    case 'antigravity':
      return (
        Array.isArray(data.groups) &&
        data.groups.every((group) => isRecord(group) && Array.isArray(group.buckets))
      );
    case 'claude':
    case 'codex':
      return Array.isArray(data.windows);
    case 'devin':
      return Array.isArray(data.windows);
    case 'gemini-cli':
      return Array.isArray(data.buckets);
    case 'kimi':
      return Array.isArray(data.rows);
    case 'meta':
      return isMetaQuotaData(data.data);
    case 'xai':
      return isRecord(data.billing);
    default:
      return false;
  }
};

const quotaCacheFreshness = (entry: QuotaCacheEntry): number[] => [
  entry.observedAt || 0,
  entry.cachedAt || 0,
  entry.storedAt || 0,
  entry.revision || 0,
];

const isFresherQuotaCacheEntry = (
  candidate: QuotaCacheEntry,
  current: QuotaCacheEntry
): boolean => {
  const candidateFreshness = quotaCacheFreshness(candidate);
  const currentFreshness = quotaCacheFreshness(current);
  for (let index = 0; index < candidateFreshness.length; index += 1) {
    if (candidateFreshness[index] === currentFreshness[index]) continue;
    return candidateFreshness[index] > currentFreshness[index];
  }
  return false;
};

export const selectPreferredQuotaCacheEntries = (
  provider: string,
  entries: QuotaCacheEntry[]
): Map<string, QuotaCacheEntry> => {
  const selected = new Map<string, QuotaCacheEntry>();

  entries.forEach((entry) => {
    if (provider === 'xai' && !entry.identityFingerprint?.trim()) return;
    // Account policy consumes exactly the cache representations that the auth
    // card can hydrate. Other plugin rows remain persisted but cannot displace
    // a valid inspection snapshot for the same file.
    if (!isAuthCardQuotaCacheDataCompatible(provider, entry.data)) return;

    const current = selected.get(entry.fileName);
    if (!current) {
      selected.set(entry.fileName, entry);
      return;
    }

    if (provider === 'gemini-cli') {
      // Core-owned snapshots are authoritative over the legacy UI mirror for the same auth file.
      const candidateIsSnapshot = isNormalizedGeminiQuotaSnapshot(entry.data);
      const currentIsSnapshot = isNormalizedGeminiQuotaSnapshot(current.data);
      if (candidateIsSnapshot !== currentIsSnapshot) {
        if (candidateIsSnapshot) selected.set(entry.fileName, entry);
        return;
      }
    }

    if (isFresherQuotaCacheEntry(entry, current)) {
      selected.set(entry.fileName, entry);
    }
  });

  return selected;
};

export const normalizePersistedQuotaState = (
  provider: string,
  data: unknown,
  cachedAt: number,
  identityFingerprint?: string
): unknown => {
  if (provider === 'xai' && isRecord(data)) {
    // The persisted row identifies the observation; never rebind it to auth-list data.
    return { ...data, quotaIdentityFingerprint: identityFingerprint?.trim() || undefined };
  }
  if (provider !== 'gemini-cli' || !isRecord(data)) return data;
  if (data.status !== undefined || !Array.isArray(data.items)) return data;

  const plan = isRecord(data.plan) ? data.plan : null;
  const metadata = isRecord(data.metadata) ? data.metadata : null;
  return {
    status: 'success',
    buckets: data.items.filter(isRecord).map((item) => ({
      id: String(item.id ?? ''),
      label: String(item.label ?? item.id ?? ''),
      remainingFraction:
        typeof item.remaining_fraction === 'number' ? item.remaining_fraction : null,
      remainingAmount: typeof item.remaining_amount === 'number' ? item.remaining_amount : null,
      resetTime: typeof item.reset_at === 'string' ? item.reset_at : undefined,
      tokenType:
        isRecord(item.metadata) && typeof item.metadata.token_type === 'string'
          ? item.metadata.token_type
          : null,
      modelIds: Array.isArray(item.model_ids) ? item.model_ids : [],
    })),
    projectId: typeof metadata?.project_id === 'string' ? metadata.project_id : '',
    tierLabel: typeof plan?.label === 'string' ? plan.label : null,
    tierId: typeof plan?.id === 'string' ? plan.id : null,
    creditBalance: typeof plan?.credit_balance === 'number' ? plan.credit_balance : null,
    cachedAt: typeof data.observed_at_ms === 'number' ? data.observed_at_ms : cachedAt,
    quotaProviderSnapshot: true,
  };
};
