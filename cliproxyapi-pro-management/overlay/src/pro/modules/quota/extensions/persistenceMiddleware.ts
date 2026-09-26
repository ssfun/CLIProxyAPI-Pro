/**
 * Zustand persistence middleware for quota data.
 * Automatically syncs quota state to SQLite quota cache.
 */

import {
  captureQuotaCacheGeneration,
  commitIfQuotaCacheCurrent,
  useQuotaStore,
} from '@/stores/useQuotaStore';
import {
  getQuotaProviderMapName,
  getQuotaProviderSetterName,
  isProQuotaProviderType,
  PRO_QUOTA_PROVIDER_TYPES,
  type ProQuotaProviderType,
} from '../quotaStoreMetadata';
import { sqliteQuotaCache, type QuotaCacheEntry } from './sqliteQuotaCache';
import {
  isAuthCardQuotaCacheDataCompatible,
  normalizePersistedQuotaState,
  selectPreferredQuotaCacheEntries,
} from './normalizedQuotaSnapshot';

interface QuotaStatusState {
  status: 'idle' | 'loading' | 'success' | 'error';
  cachedAt?: number;
  quotaProviderSnapshot?: boolean;
}

type QuotaStoreState = ReturnType<typeof useQuotaStore.getState>;
type QuotaMapUpdater = (
  previous: Record<string, QuotaStatusState>
) => Record<string, QuotaStatusState>;

class QuotaPersistenceMiddleware {
  private unsubscribe: (() => void) | null = null;
  private isHydrating = false;
  private epoch = 0;
  private syncQueue = new Set<string>();
  private isFlushing = false;
  private retryTimer: ReturnType<typeof setTimeout> | null = null;
  private retryDelayMs = 1_000;
  private syncedVersions = new Map<string, string>();
  private loadedGeneration = 0;
  private reloadRequested = false;
  private ensureFreshPromise: Promise<void> | null = null;
  private lastQuotaMaps = new Map<ProQuotaProviderType, Record<string, QuotaStatusState>>();
  private hydratedStates = new Map<ProQuotaProviderType, Record<string, QuotaStatusState>>();

  /**
   * Start the middleware
   */
  start() {
    if (this.unsubscribe) {
      console.warn('QuotaPersistenceMiddleware already started');
      return;
    }

    // Check if upstream store structure is compatible
    if (!this.checkCompatibility()) {
      console.warn(
        'QuotaPersistenceMiddleware: Upstream store structure changed, persistence disabled'
      );
      return;
    }

    this.unsubscribe = useQuotaStore.subscribe((state, previous) => {
      if (state.cacheGeneration !== previous.cacheGeneration) this.reset();
      if (this.isHydrating) return;

      PRO_QUOTA_PROVIDER_TYPES.forEach((provider) => {
        const quotaMap = this.getQuotaMap(state, provider);
        if (!quotaMap || this.lastQuotaMaps.get(provider) === quotaMap) return;
        this.lastQuotaMaps.set(provider, quotaMap);
        this.syncProvider(provider, quotaMap);
      });
    });
    void this.ensureFresh();
  }

  stop() {
    this.unsubscribe?.();
    this.unsubscribe = null;
    this.reset();
  }

  private reset() {
    this.epoch++;
    this.lastQuotaMaps.clear();
    this.syncedVersions.clear();
    this.hydratedStates.clear();
    this.syncQueue.clear();
    this.loadedGeneration = 0;
    this.reloadRequested = true;
    this.ensureFreshPromise = null;
    this.isFlushing = false;
    this.retryDelayMs = 1_000;
    if (this.retryTimer) clearTimeout(this.retryTimer);
    this.retryTimer = null;
  }

  /**
   * Check if upstream store structure is compatible
   */
  private checkCompatibility(): boolean {
    const state = useQuotaStore.getState();
    const requiredFields = [
      ...PRO_QUOTA_PROVIDER_TYPES.map(getQuotaProviderMapName),
      ...PRO_QUOTA_PROVIDER_TYPES.map(getQuotaProviderSetterName),
      'clearQuotaCache',
    ];

    const missing = requiredFields.filter((field) => !(field in state));
    if (missing.length > 0) {
      console.error(`QuotaPersistenceMiddleware: Missing fields: ${missing.join(', ')}`);
      return false;
    }

    return true;
  }

  /**
   * Sync provider quota to SQLite quota cache.
   */
  private syncProvider(provider: ProQuotaProviderType, quotaMap: Record<string, QuotaStatusState>) {
    let changed = false;
    const activeKeys = new Set<string>();
    Object.entries(quotaMap).forEach(([fileName, state]) => {
      const key = `${provider}:${fileName}`;
      activeKeys.add(key);
      if (state.status !== 'success') return;
      if (provider === 'gemini-cli' && state.quotaProviderSnapshot) return;

      const version = this.getSyncVersion(state);
      if (this.syncedVersions.get(key) === version) return;
      this.syncQueue.add(key);
      changed = true;
    });

    this.pruneSyncedVersions(provider, activeKeys);
    if (changed) void this.flushSyncQueue();
  }

  private getSyncVersion(state: unknown) {
    return JSON.stringify(state);
  }

  private pruneSyncedVersions(provider: ProQuotaProviderType, activeKeys: Set<string>) {
    const prefix = `${provider}:`;
    Array.from(this.syncedVersions.keys()).forEach((key) => {
      if (key.startsWith(prefix) && !activeKeys.has(key)) {
        this.syncedVersions.delete(key);
      }
    });
  }

  /**
   * Flush sync queue to SQLite quota cache
   */
  private async flushSyncQueue() {
    if (this.isFlushing || !this.unsubscribe || this.retryTimer) return;
    const epoch = this.epoch;
    this.isFlushing = true;

    try {
      while (this.syncQueue.size > 0 && epoch === this.epoch) {
        const key = this.syncQueue.values().next().value as string | undefined;
        if (!key) break;
        this.syncQueue.delete(key);

        const separatorIndex = key.indexOf(':');
        if (separatorIndex <= 0) continue;

        const provider = key.slice(0, separatorIndex) as ProQuotaProviderType;
        const fileName = key.slice(separatorIndex + 1);
        const state = useQuotaStore.getState();
        const quotaMap = this.getQuotaMap(state, provider);
        const quotaState = quotaMap?.[fileName];

        if (quotaState?.status !== 'success') continue;
        if (provider === 'gemini-cli' && quotaState.quotaProviderSnapshot) continue;

        const version = this.getSyncVersion(quotaState);
        if (this.syncedVersions.get(key) === version) continue;
        const cachedAt = quotaState.cachedAt ?? Date.now();
        const synced = await sqliteQuotaCache.set(
          provider,
          fileName,
          { ...quotaState, cachedAt },
          cachedAt
        );
        if (epoch !== this.epoch) return;
        if (synced) {
          this.syncedVersions.set(key, version);
          this.retryDelayMs = 1_000;
        } else {
          this.syncQueue.add(key);
          this.scheduleRetry();
          break;
        }
      }
    } catch (err) {
      console.error('QuotaPersistenceMiddleware: Failed to sync to SQLite quota cache:', err);
    } finally {
      if (epoch === this.epoch) this.isFlushing = false;
    }
  }

  private scheduleRetry() {
    if (this.retryTimer) return;
    const delay = this.retryDelayMs;
    this.retryDelayMs = Math.min(this.retryDelayMs * 2, 30_000);
    this.retryTimer = setTimeout(() => {
      this.retryTimer = null;
      void this.flushSyncQueue();
    }, delay);
  }

  async ensureFresh() {
    if (this.ensureFreshPromise) return this.ensureFreshPromise;
    const epoch = this.epoch;
    const initialState = useQuotaStore.getState();
    const generation = captureQuotaCacheGeneration();

    this.ensureFreshPromise = (async () => {
      try {
        const stats = await sqliteQuotaCache.getStats();
        if (epoch !== this.epoch) return;
        if (
          !this.reloadRequested &&
          stats.generation > 0 &&
          stats.generation === this.loadedGeneration
        )
          return;
        // Consume only the current request; markStale during the read must survive.
        this.reloadRequested = false;
        const cachedEntries = await sqliteQuotaCache.getAll();
        if (epoch !== this.epoch) return;
        const applied = commitIfQuotaCacheCurrent(generation, () => {
          const entriesByProvider = new Map<ProQuotaProviderType, QuotaCacheEntry[]>();
          cachedEntries.forEach((entry) => {
            if (!isProQuotaProviderType(entry.provider)) return;
            const entries = entriesByProvider.get(entry.provider) ?? [];
            entries.push(entry);
            entriesByProvider.set(entry.provider, entries);
          });
          // Suppress only our synchronous store writes, never network-time updates.
          this.isHydrating = true;
          try {
            PRO_QUOTA_PROVIDER_TYPES.forEach((provider) => {
              this.preloadProvider(provider, entriesByProvider.get(provider) ?? [], initialState);
            });
            this.loadedGeneration = stats.generation;
          } finally {
            this.isHydrating = false;
          }
        });
        if (!applied) this.reloadRequested = true;
      } catch (err) {
        if (epoch === this.epoch) this.reloadRequested = true;
        console.error('QuotaPersistenceMiddleware: Failed to preload cache:', err);
      }
    })().finally(() => {
      if (epoch === this.epoch) this.ensureFreshPromise = null;
    });
    return this.ensureFreshPromise;
  }

  markStale() {
    this.reloadRequested = true;
  }

  /**
   * Preload single provider from SQLite quota cache
   */
  private preloadProvider(
    provider: ProQuotaProviderType,
    cachedEntries: QuotaCacheEntry[],
    initialState: QuotaStoreState
  ) {
    const cached = selectPreferredQuotaCacheEntries(provider, cachedEntries);
    const previouslyHydrated = this.hydratedStates.get(provider) ?? {};
    const hydrated: Record<string, QuotaStatusState> = {};
    const initial = this.getQuotaMap(initialState, provider);

    const setterName = getQuotaProviderSetterName(provider);
    const storeState = useQuotaStore.getState();
    const setter = storeState[setterName] as unknown as (updater: QuotaMapUpdater) => void;

    if (typeof setter === 'function') {
      setter((prev) => {
        let changed = false;
        const next = { ...prev };
        Object.entries(previouslyHydrated).forEach(([fileName, state]) => {
          if (cached.has(fileName) || next[fileName] !== state) return;
          delete next[fileName];
          this.syncedVersions.delete(`${provider}:${fileName}`);
          changed = true;
        });
        cached.forEach((entry, fileName) => {
          const data = normalizePersistedQuotaState(provider, entry.data, entry.cachedAt);
          if (!isAuthCardQuotaCacheDataCompatible(provider, data)) return;
          const quotaState = data as QuotaStatusState;
          const current = next[fileName];
          if (current !== initial?.[fileName]) return;
          if (
            current &&
            (current.status === 'loading' ||
              (current.cachedAt ?? 0) > (quotaState.cachedAt ?? entry.cachedAt))
          )
            return;
          hydrated[fileName] = quotaState;
          this.syncedVersions.set(`${provider}:${fileName}`, this.getSyncVersion(quotaState));
          if (next[fileName] === quotaState) return;
          next[fileName] = quotaState;
          changed = true;
        });
        return changed ? next : prev;
      });

      this.hydratedStates.set(provider, hydrated);
      const quotaMap = this.getQuotaMap(useQuotaStore.getState(), provider);
      if (quotaMap) this.lastQuotaMaps.set(provider, quotaMap);
    }
  }

  /**
   * Get quota map from state by provider
   */
  private getQuotaMap(
    state: QuotaStoreState,
    provider: ProQuotaProviderType
  ): Record<string, QuotaStatusState> | null {
    const mapName = getQuotaProviderMapName(provider);
    return state[mapName] || null;
  }
}

export const quotaPersistenceMiddleware = new QuotaPersistenceMiddleware();
