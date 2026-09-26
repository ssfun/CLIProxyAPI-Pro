import { apiClient } from '@/services/api/client';

export interface QuotaCacheEntry<T = unknown> {
  id: string;
  provider: string;
  fileName: string;
  data: T;
  cachedAt: number;
  accessedAt: number;
  observedAt: number;
  storedAt: number;
  version: number;
  revision: number;
  authIndex?: string;
  identityFingerprint?: string;
}

interface QuotaCacheListResponse<T = unknown> {
  items?: QuotaCacheEntry<T>[];
}

interface QuotaCacheStatsResponse {
  totalEntries?: number;
  updatedAt?: number;
  generation?: number;
}

class SqliteQuotaCache {
  async set(
    provider: string,
    fileName: string,
    data: unknown,
    cachedAt = Date.now()
  ): Promise<boolean> {
    try {
      await apiClient.put('/usage/quota-cache', {
        provider,
        fileName,
        data,
        cachedAt,
        observedAt: cachedAt,
        accessedAt: Date.now(),
        version: Number((data as { schemaVersion?: unknown } | null)?.schemaVersion) || 1,
      });
      return true;
    } catch (err) {
      console.error('SQLite quota cache set error:', err);
      return false;
    }
  }

  async getAll<T = unknown>(): Promise<QuotaCacheEntry<T>[]> {
    const response = await apiClient.get<QuotaCacheListResponse<T>>('/usage/quota-cache');
    return response.items ?? [];
  }

  async getStats(): Promise<{
    totalEntries: number;
    updatedAt: number;
    generation: number;
  }> {
    const stats = await apiClient.get<QuotaCacheStatsResponse>('/usage/quota-cache', {
      params: { stats: '1' },
    });
    return {
      totalEntries: stats.totalEntries ?? 0,
      updatedAt: stats.updatedAt ?? 0,
      generation: stats.generation ?? 0,
    };
  }
}

export const sqliteQuotaCache = new SqliteQuotaCache();
