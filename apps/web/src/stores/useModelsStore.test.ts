import { beforeEach, describe, expect, it, vi } from 'vitest';
import { modelsApi } from '@/services/api/models';
import { useModelsStore } from './useModelsStore';

vi.mock('@/services/api/models', () => ({
  modelsApi: {
    fetchModels: vi.fn(),
  },
}));

describe('useModelsStore', () => {
  beforeEach(() => {
    useModelsStore.getState().clearCache();
    vi.mocked(modelsApi.fetchModels).mockReset();
    vi.mocked(modelsApi.fetchModels).mockResolvedValue([{ name: 'gpt-test' }]);
  });

  it('passes the Fresh admin credential to the embedded model bridge and scopes its cache', async () => {
    const store = useModelsStore.getState();

    await store.fetchModels('/console/api/v2/cpamp', undefined, false, 'admin-one');
    expect(modelsApi.fetchModels).toHaveBeenCalledWith('/console/api/v2/cpamp', undefined, {
      Authorization: 'Bearer admin-one',
    });

    await useModelsStore
      .getState()
      .fetchModels('/console/api/v2/cpamp', undefined, false, 'admin-one');
    expect(modelsApi.fetchModels).toHaveBeenCalledTimes(1);

    await useModelsStore
      .getState()
      .fetchModels('/console/api/v2/cpamp', undefined, false, 'admin-two');
    expect(modelsApi.fetchModels).toHaveBeenCalledTimes(2);
  });
});
