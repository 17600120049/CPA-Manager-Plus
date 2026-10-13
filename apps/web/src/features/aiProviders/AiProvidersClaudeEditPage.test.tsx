import {
  createContext,
  createElement,
  useContext,
  useEffect,
  useState,
  type ReactNode,
} from 'react';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import type { ProviderFormState } from '@/components/providers/types';
import type { ClaudeEditOutletContext } from './AiProvidersClaudeEditLayout';

const mocks = vi.hoisted(() => ({
  context: null as ClaudeEditOutletContext | null,
  t: (key: string) => key,
  request: vi.fn(),
  notify: vi.fn(),
  navigate: vi.fn(),
}));

vi.mock('react-i18next', () => ({ useTranslation: () => ({ t: mocks.t }) }));
vi.mock('react-router-dom', () => ({
  useNavigate: () => mocks.navigate,
  useOutletContext: () => useContext(TestOutletContext),
}));
vi.mock('@/stores', () => ({
  useNotificationStore: () => ({ showNotification: mocks.notify }),
}));
vi.mock('@/hooks/useEdgeSwipeBack', () => ({
  useEdgeSwipeBack: () => ({ current: null }),
}));
vi.mock('@/components/common/SecondaryScreenShell', () => ({
  SecondaryScreenShell: ({ children }: { children?: ReactNode }) => <div>{children}</div>,
}));
vi.mock('@/components/providers/CoolingPolicySelect', () => ({
  CoolingPolicySelect: () => null,
}));
vi.mock('@/services/api', () => ({
  apiCallApi: { request: mocks.request },
  getApiCallErrorMessage: () => '401 upstream rejected request',
}));

import { AiProvidersClaudeEditPage } from './AiProvidersClaudeEditPage';

const TestOutletContext = createContext<ClaudeEditOutletContext | null>(null);

(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;

const emptyForm = (): ProviderFormState => ({
  apiKey: '',
  authIndex: '',
  baseUrl: 'https://api.anthropic.com',
  proxyUrl: '',
  disableCooling: 'inherit',
  headers: [],
  models: [],
  excludedModels: [],
  modelEntries: [{ name: 'claude-test', alias: '' }],
  excludedText: '',
});
const renderers: ReactTestRenderer[] = [];
const flush = async () => {
  await act(async () => {
    await Promise.resolve();
    await Promise.resolve();
  });
};
const renderPage = async (overrides: Partial<ProviderFormState> = {}) => {
  function Wrapper() {
    const [form, setForm] = useState({ ...emptyForm(), ...overrides });
    const [testModel, setTestModel] = useState('claude-test');
    const [testStatus, setTestStatus] = useState<ClaudeEditOutletContext['testStatus']>('idle');
    const [testMessage, setTestMessage] = useState('');
    const context: ClaudeEditOutletContext = {
      hasIndexParam: true,
      editIndex: 0,
      invalidIndexParam: false,
      invalidIndex: false,
      disableControls: false,
      loading: false,
      saving: false,
      form,
      setForm,
      testModel,
      setTestModel,
      testStatus,
      setTestStatus,
      testMessage,
      setTestMessage,
      availableModels: form.modelEntries.map((entry) => entry.name).filter(Boolean),
      handleBack: mocks.navigate,
      handleSave: async () => undefined,
      mergeDiscoveredModels: () => undefined,
    };
    useEffect(() => {
      mocks.context = context;
    });
    return (
      <TestOutletContext.Provider value={context}>
        <AiProvidersClaudeEditPage />
      </TestOutletContext.Provider>
    );
  }
  let renderer: ReactTestRenderer | undefined;
  await act(async () => {
    renderer = create(createElement(Wrapper));
  });
  renderers.push(renderer!);
  await flush();
  return renderer!;
};
const testConnection = async (renderer: ReactTestRenderer) => {
  const button = renderer.root.find(
    (node) => node.props.onClick && node.props.children === 'ai_providers.claude_test_action'
  );
  await act(async () => {
    await button.props.onClick();
  });
  await flush();
};

beforeEach(() => {
  vi.clearAllMocks();
  vi.stubGlobal('window', { addEventListener: vi.fn(), removeEventListener: vi.fn() });
  mocks.request.mockResolvedValue({ statusCode: 200 });
});
afterEach(async () => {
  for (const renderer of renderers.splice(0)) await act(async () => renderer.unmount());
  vi.unstubAllGlobals();
});

describe('Claude edit page connectivity authentication', () => {
  it.each([
    {
      name: 'Anthropic key',
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
        headers: [{ key: 'Authorization', value: 'Custom header-token' }],
        authIndex: 'auth-1',
      },
      header: { Authorization: 'Custom header-token' },
      absent: 'x-api-key',
    },
    {
      name: 'Anthropic auth index',
      form: { authIndex: 'auth-1' },
      header: { 'x-api-key': '$TOKEN$' },
      absent: 'Authorization',
    },
    {
      name: 'custom auth index',
      form: { authIndex: 'auth-1', baseUrl: 'https://gateway.example.com' },
      header: { Authorization: 'Bearer $TOKEN$' },
      absent: 'x-api-key',
    },
  ])(
    'sends effective $name headers on the actual test button',
    async ({ form, header, absent }) => {
      const renderer = await renderPage({
        ...form,
        proxyUrl: 'socks5://provider-proxy.example:1080',
      });
      await testConnection(renderer);
      expect(mocks.request).toHaveBeenCalledWith(
        expect.objectContaining({
          method: 'POST',
          url: `${form.baseUrl ?? 'https://api.anthropic.com'}/v1/messages`,
          proxyUrl: 'socks5://provider-proxy.example:1080',
          header: expect.objectContaining({ ...header, 'Content-Type': 'application/json' }),
          data: JSON.stringify({
            model: 'claude-test',
            max_tokens: 8,
            messages: [{ role: 'user', content: 'Hi' }],
          }),
        }),
        { timeout: 30_000 }
      );
      const headers = mocks.request.mock.calls[0][0].header as Record<string, string>;
      expect(headers).not.toHaveProperty(absent);
      const names = Object.keys(headers).map((name) => name.toLowerCase());
      expect(new Set(names).size).toBe(names.length);
      expect(mocks.context?.testStatus).toBe('success');
    }
  );

  it.each([{ headers: [] }, { headers: [{ key: 'Authorization', value: '  ' }] }])(
    'does not send a credential-free request with headers $headers',
    async ({ headers }) => {
      const renderer = await renderPage({ headers });
      await testConnection(renderer);
      expect(mocks.request).not.toHaveBeenCalled();
      expect(mocks.context?.testStatus).toBe('error');
      expect(mocks.context?.testMessage).toBe('ai_providers.claude_test_key_required');
    }
  );

  it('invalidates a successful test when the provider proxy changes', async () => {
    const renderer = await renderPage({ apiKey: 'plain-key', proxyUrl: 'direct' });
    await testConnection(renderer);
    expect(mocks.context?.testStatus).toBe('success');
    await act(async () =>
      mocks.context?.setForm((form) => ({ ...form, proxyUrl: 'socks5://new-proxy.example:1080' }))
    );
    expect(mocks.context?.testStatus).toBe('idle');
    expect(mocks.context?.testMessage).toBe('');
    await testConnection(renderer);
    expect(mocks.request.mock.calls[1][0].proxyUrl).toBe('socks5://new-proxy.example:1080');
  });

  it.each([
    {
      failure: Object.assign(new Error('request timeout'), { code: 'ECONNABORTED' }),
      message: 'ai_providers.claude_test_timeout',
    },
    {
      failure: new Error('upstream unavailable'),
      message: 'ai_providers.claude_test_failed: upstream unavailable',
    },
  ])('keeps the actual failed probe result ($message)', async ({ failure, message }) => {
    mocks.request.mockRejectedValueOnce(failure);
    const renderer = await renderPage({ apiKey: 'plain-key' });
    await testConnection(renderer);
    expect(mocks.context?.testStatus).toBe('error');
    expect(mocks.context?.testMessage).toBe(message);
  });
});
