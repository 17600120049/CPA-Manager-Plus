/**
 * 模型列表状态管理（带缓存）
 */

import { create } from 'zustand';
import { modelsApi } from '@/services/api/models';
import { CACHE_EXPIRY_MS } from '@/utils/constants';
import type { ModelInfo } from '@/utils/models';

interface ModelsCache {
  data: ModelInfo[];
  timestamp: number;
  apiBase: string;
  apiKey: string;
  managementKey: string;
}

interface ModelsState {
  models: ModelInfo[];
  loading: boolean;
  error: string | null;
  cache: ModelsCache | null;

  fetchModels: (
    apiBase: string,
    apiKey?: string,
    forceRefresh?: boolean,
    managementKey?: string
  ) => Promise<ModelInfo[]>;
  clearCache: () => void;
  isCacheValid: (apiBase: string, apiKey?: string, managementKey?: string) => boolean;
}

export const useModelsStore = create<ModelsState>((set, get) => ({
  models: [],
  loading: false,
  error: null,
  cache: null,

  fetchModels: async (apiBase, apiKey, forceRefresh = false, managementKey) => {
    const { cache, isCacheValid } = get();
    const apiKeyScope = apiKey?.trim() || '';
    const managementKeyScope = managementKey?.trim() || '';

    // 检查缓存
    if (!forceRefresh && isCacheValid(apiBase, apiKeyScope, managementKeyScope) && cache) {
      set({ models: cache.data, error: null });
      return cache.data;
    }

    set({ loading: true, error: null });

    try {
      // The Fresh Console bridge is a same-origin Manager API. Its canonical
      // boundary requires the verified Manager admin credential; a CPA client
      // key alone is intentionally insufficient when the saved CPA connection
      // has no configured key yet. Standalone Panel callers keep their
      // existing CPA-key behavior by leaving this header empty.
      const requestHeaders: Record<string, string> = managementKeyScope
        ? { Authorization: `Bearer ${managementKeyScope}` }
        : {};
      const list = await modelsApi.fetchModels(apiBase, apiKeyScope || undefined, requestHeaders);
      const now = Date.now();

      set({
        models: list,
        loading: false,
        cache: {
          data: list,
          timestamp: now,
          apiBase,
          apiKey: apiKeyScope,
          managementKey: managementKeyScope,
        },
      });

      return list;
    } catch (error: unknown) {
      const message =
        error instanceof Error
          ? error.message
          : typeof error === 'string'
            ? error
            : 'Failed to fetch models';
      set({
        error: message,
        loading: false,
        models: [],
      });
      throw error;
    }
  },

  clearCache: () => {
    set({ cache: null, models: [] });
  },

  isCacheValid: (apiBase, apiKey, managementKey) => {
    const { cache } = get();
    if (!cache) return false;
    if (cache.apiBase !== apiBase) return false;
    const apiKeyScope = apiKey?.trim() || '';
    if ((cache.apiKey || '') !== apiKeyScope) return false;
    const managementKeyScope = managementKey?.trim() || '';
    if ((cache.managementKey || '') !== managementKeyScope) return false;
    return Date.now() - cache.timestamp < CACHE_EXPIRY_MS;
  },
}));
