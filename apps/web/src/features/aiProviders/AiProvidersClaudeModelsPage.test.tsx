import { createElement, type ReactNode } from 'react';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProviderFormState } from '@/components/providers/types';
import type { ClaudeEditOutletContext } from './AiProvidersClaudeEditLayout';

type ModelsContext = Pick<
  ClaudeEditOutletContext,
  'form' | 'disableControls' | 'loading' | 'saving' | 'mergeDiscoveredModels'
>;
const mocks = vi.hoisted(() => ({
  context: null as ModelsContext | null,
  t: (key: string) => key,
  request: vi.fn(),
  navigate: vi.fn(),
  merge: vi.fn(),
}));

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('react-router-dom', () => ({
  useNavigate: () => mocks.navigate,
  useOutletContext: () => mocks.context,
}));
vi.mock('@/hooks/useEdgeSwipeBack', () => ({ useEdgeSwipeBack: () => ({ current: null }) }));
vi.mock('@/components/common/SecondaryScreenShell', () => ({
  SecondaryScreenShell: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
}));
vi.mock('@/services/api', async () => {
  const { modelsApi } = await import('@/services/api/models');
  return { modelsApi };
});
vi.mock('@/services/api/apiCall', async (importOriginal) => ({
  ...(await importOriginal<typeof import('@/services/api/apiCall')>()),
  apiCallApi: { request: mocks.request },
}));

import { AiProvidersClaudeModelsPage } from './AiProvidersClaudeModelsPage';

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
const renderers: ReactTestRenderer[] = [];
const success = () => ({ statusCode: 200, body: { data: [{ id: 'claude-discovered' }] } });
const emptyForm = (): ProviderFormState => ({
  apiKey: '',
  authIndex: '',
  baseUrl: 'https://api.anthropic.com',
  proxyUrl: '',
  disableCooling: 'inherit',
  headers: [],
  models: [],
  excludedModels: [],
  modelEntries: [],
  excludedText: '',
});
const flush = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
    await Promise.resolve();
  });
};
const renderPage = async (form: Partial<ProviderFormState> = {}, loading = false) => {
  mocks.context = {
    form: { ...emptyForm(), ...form },
    disableControls: false,
    loading,
    saving: false,
    mergeDiscoveredModels: mocks.merge,
  };
  let renderer: ReactTestRenderer | undefined;
  await act(async () => {
    renderer = create(createElement(AiProvidersClaudeModelsPage));
  });
  renderers.push(renderer!);
  await flush();
  return renderer!;
};
const refresh = async (renderer: ReactTestRenderer) => {
  const button = renderer.root.find(
    (node) =>
      node.props.onClick && node.props.children === 'ai_providers.claude_models_fetch_refresh'
  );
  await act(async () => {
    button.props.onClick();
  });
  await flush();
};
const update = async (renderer: ReactTestRenderer, form: Partial<ProviderFormState>) => {
  mocks.context = { ...mocks.context!, form: { ...mocks.context!.form, ...form } };
  await act(async () => {
    renderer.update(createElement(AiProvidersClaudeModelsPage));
  });
  await flush();
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('window', { addEventListener: vi.fn(), removeEventListener: vi.fn() });
  mocks.request.mockResolvedValue(success());
});
afterEach(async () => {
  for (const renderer of renderers.splice(0)) await act(async () => renderer.unmount());
  vi.unstubAllGlobals();
});

describe('Claude model page actual discovery requests', () => {
  it.each([
    {
      name: 'first-party key',
      form: { apiKey: 'plain-key' },
      header: { 'x-api-key': 'plain-key' },
      absent: 'Authorization',
    },
    {
      name: 'custom key',
      form: { apiKey: 'plain-key', baseUrl: 'https://gateway.example.com' },
      header: { Authorization: 'Bearer plain-key' },
      absent: 'x-api-key',
    },
    {
      name: 'OAuth',
      form: { apiKey: 'sk-ant-oat-test-token' },
      header: {
        Authorization: 'Bearer sk-ant-oat-test-token',
        'anthropic-beta': 'oauth-2025-04-20',
      },
      absent: 'x-api-key',
    },
    {
      name: 'header-only',
      form: {
        authIndex: 'auth-1',
        headers: [{ key: 'Authorization', value: 'Custom header-token' }],
      },
      header: { Authorization: 'Custom header-token' },
      absent: 'x-api-key',
    },
    {
      name: 'custom auth index',
      form: { authIndex: 'auth-1', baseUrl: 'https://gateway.example.com' },
      header: { Authorization: 'Bearer $TOKEN$' },
      absent: 'x-api-key',
    },
  ])(
    'auto-loads $name through the real model service and preserves manual refresh',
    async ({ form, header, absent }) => {
      const renderer = await renderPage({
        ...form,
        proxyUrl: '  socks5://provider-proxy.example:1080  ',
      });
      expect(mocks.request).toHaveBeenCalledTimes(1);
      expect(mocks.request).toHaveBeenCalledWith(
        expect.objectContaining({
          method: 'GET',
          url: `${form.baseUrl ?? 'https://api.anthropic.com'}/v1/models`,
          proxyUrl: 'socks5://provider-proxy.example:1080',
          header: expect.objectContaining(header),
        })
      );
      expect(mocks.request.mock.calls[0][0].header).not.toHaveProperty(absent);
      expect(JSON.stringify(renderer.toJSON())).toContain('claude-discovered');
      await refresh(renderer);
      expect(mocks.request).toHaveBeenCalledTimes(2);
    }
  );

  it.each([
    { headers: [] },
    { headers: [{ key: 'Authorization', value: '  ' }] },
    { headers: [{ key: 'x-api-key', value: '' }] },
  ])('does not auto-load without effective credentials ($headers)', async ({ headers }) => {
    const renderer = await renderPage({ headers });
    expect(mocks.request).not.toHaveBeenCalled();
    await update(renderer, { headers: [{ key: 'Authorization', value: 'Custom ready-token' }] });
    expect(mocks.request).toHaveBeenCalledTimes(1);
    expect(mocks.request.mock.calls[0][0].header.Authorization).toBe('Custom ready-token');
  });

  it('waits for the parent form to finish loading before automatic discovery', async () => {
    const renderer = await renderPage({ apiKey: 'plain-key' }, true);
    expect(mocks.request).not.toHaveBeenCalled();
    mocks.context = { ...mocks.context!, loading: false };
    await act(async () => {
      renderer.update(createElement(AiProvidersClaudeModelsPage));
    });
    await flush();
    expect(mocks.request).toHaveBeenCalledTimes(1);
  });

  it('reloads after a proxy change while an identical form rerender does not duplicate discovery', async () => {
    const renderer = await renderPage({ apiKey: 'plain-key', proxyUrl: 'direct' });
    await update(renderer, {});
    expect(mocks.request).toHaveBeenCalledTimes(1);
    await update(renderer, { proxyUrl: 'socks5://new-proxy.example:1080' });
    expect(mocks.request).toHaveBeenCalledTimes(2);
    expect(mocks.request.mock.calls[1][0].proxyUrl).toBe('socks5://new-proxy.example:1080');
  });

  it('reports effective authentication presence on failure without adding credential values to diagnostics', async () => {
    mocks.request.mockRejectedValueOnce(new Error('401 upstream rejected request'));
    const renderer = await renderPage({
      apiKey: 'private-key-for-test',
      authIndex: 'auth-1',
      baseUrl: 'https://gateway.example.com',
    });
    const text = JSON.stringify(renderer.toJSON());
    expect(text).toContain(
      '[diag: apiKeyField=yes, authIndex=yes, baseType=custom, effectiveXApiKey=no, effectiveAuthorization=yes]'
    );
    expect(text).not.toContain('private-key-for-test');
    await refresh(renderer);
    expect(mocks.request).toHaveBeenCalledTimes(2);
    expect(JSON.stringify(renderer.toJSON())).not.toContain('[diag:');
  });
});
