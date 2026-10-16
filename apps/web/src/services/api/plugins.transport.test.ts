import { afterEach, describe, expect, it, vi } from 'vitest';
import type { AxiosAdapter } from 'axios';

const { adapter } = vi.hoisted(() => ({ adapter: vi.fn<AxiosAdapter>() }));

// Keep real Axios and ApiClient interceptors; replace only network delivery.
vi.mock('axios', async (importOriginal) => {
  const actual = await importOriginal<typeof import('axios')>();
  return {
    ...actual,
    default: {
      ...actual.default,
      create: (config: Parameters<typeof actual.default.create>[0]) =>
        actual.default.create({ ...config, adapter }),
    },
  };
});

import { apiClient } from './client';
import { pluginStoreApi } from './plugins';

afterEach(() => {
  vi.unstubAllGlobals();
  adapter.mockReset();
});

describe('Panel plugin store through the real ApiClient interceptor', () => {
  it('delivers an encoded slash in a plugin ID beneath CPA Management', async () => {
    vi.stubGlobal('window', { dispatchEvent: vi.fn(), location: new URL('http://panel.local') });
    apiClient.setConfig({ apiBase: 'http://cpa.local:8317', managementKey: 'panel-cpa-key' });
    adapter.mockImplementation(async (config) => ({
      data: { status: 'installed', id: 'demo/plugin' },
      status: 200,
      statusText: 'OK',
      headers: {},
      config,
    }));

    await expect(
      pluginStoreApi.install('demo/plugin', { version: 'v1.2.3' })
    ).resolves.toMatchObject({
      status: 'installed',
      id: 'demo/plugin',
    });
    expect(adapter).toHaveBeenCalledTimes(1);
    expect(adapter.mock.calls[0][0]).toMatchObject({
      baseURL: 'http://cpa.local:8317/v0/management',
      url: '/plugin-store/demo%2Fplugin/install',
      method: 'post',
      headers: { Authorization: 'Bearer panel-cpa-key' },
    });
  });
});
