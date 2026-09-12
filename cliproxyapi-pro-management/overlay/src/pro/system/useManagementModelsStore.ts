import { create } from 'zustand';
import { apiClient } from '@/services/api/client';
import { normalizeModelList, type ModelInfo } from '@/utils/models';
import { CACHE_EXPIRY_MS } from '@/utils/constants';

interface ManagementModelsState {
  models: ModelInfo[];
  loading: boolean;
  error: string | null;
  fetchModels: (apiBase: string, forceRefresh?: boolean) => Promise<ModelInfo[]>;
  clearCache: () => void;
}

const fetchManagementModels = async () => {
  const response = await apiClient.get<{ data: unknown[] }>('/models');
  return normalizeModelList(response.data, { dedupe: true });
};

// Keep management catalogs separate from business-key discovery caches.
export function createManagementModelsStore(fetchList = fetchManagementModels) {
  let generation = 0;
  let scope = '';
  let cache: { data: ModelInfo[]; timestamp: number } | null = null;
  let pending: Promise<ModelInfo[]> | null = null;

  return create<ManagementModelsState>((set) => ({
    models: [],
    loading: false,
    error: null,
    clearCache: () => {
      generation += 1;
      scope = '';
      cache = null;
      pending = null;
      set({ models: [], loading: false, error: null });
    },
    fetchModels: async (apiBase, forceRefresh = false) => {
      if (scope !== apiBase) {
        generation += 1;
        scope = apiBase;
        cache = null;
        pending = null;
        set({ models: [], error: null });
      }
      if (!forceRefresh && pending) return pending;
      if (!forceRefresh && cache && Date.now() - cache.timestamp < CACHE_EXPIRY_MS) {
        return cache.data;
      }
      const requestGeneration = ++generation;
      set({ loading: true, error: null });
      const request = (async () => {
        try {
          const models = await fetchList();
          if (generation === requestGeneration) {
            cache = { data: models, timestamp: Date.now() };
            set({ models, loading: false, error: null });
          }
          return models;
        } catch (error) {
          if (generation === requestGeneration) {
            cache = null;
            set({
              models: [],
              loading: false,
              error: error instanceof Error ? error.message : String(error),
            });
          }
          throw error;
        } finally {
          if (generation === requestGeneration) pending = null;
        }
      })();
      pending = request;
      return request;
    },
  }));
}

export const useManagementModelsStore = createManagementModelsStore();
