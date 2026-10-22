import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshConsolePage } from './FreshConsolePage';
import { FreshManagedRuntime } from './FreshManagedRuntime';
import { createPublicApiClient } from './publicApi';
import type { FreshOperation } from './bootstrapTransport';
import { fullWebText as t } from '../i18n/fullweb';
import { LegacyConsolePage } from './LegacyConsolePage';
import { LoginPage } from '../features/login/LoginPage';
import { useAuthStore } from '../stores/useAuthStore';
import { obfuscatedStorage } from '../services/storage/secureStorage';
import { STORAGE_KEY_AUTH } from '../utils/constants';
import { usageServiceApi } from '../services/api/usageService';
const context = { basePath: '/console', apiBase: '/console/api/v2', revision: 1 };
const operation: FreshOperation = {
  identity: {
    admissionId: 'admission',
    installationId: 'installation',
    generation: 'generation',
    databasePath: '/data/usage.sqlite',
    operationId: 'fresh-console-operation',
  },
  revision: 6,
  adminInitialized: true,
  gatewayPending: true,
  productComplete: true,
  routing: {
    operationId: 'fresh-console-operation',
    target: '/console',
    revision: 1,
    stage: 'committed',
    committed: true,
  },
};
const key = 'Cpamp_Test_Only_Console_Admin_123456';
describe('Fresh installation and Console authority boundary', () => {
  let renderer: ReactTestRenderer;
  let location: URL;
  let read: ReturnType<typeof vi.fn<typeof fetch>>;
  const persist = vi.fn((_key: string, _value: string) => undefined);
  const storage = new Map<string, string>();
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    location = new URL('https://manager.example/console');
    vi.stubGlobal('window', {
      location,
      history: {
        pushState: (_: unknown, _title: string, path: string) => {
          location.href = new URL(path, location).href;
        },
      },
      addEventListener: (event: string, handler: () => void) => {
        void event;
        void handler;
      },
      removeEventListener: vi.fn(),
      setTimeout: globalThis.setTimeout,
      clearTimeout: globalThis.clearTimeout,
      matchMedia: () => ({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    });
    vi.stubGlobal('document', {
      documentElement: {
        classList: { toggle: vi.fn() },
        setAttribute: vi.fn(),
        style: { setProperty: vi.fn(), removeProperty: vi.fn() },
      },
      body: { style: { overflow: '' } },
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });
    storage.clear();
    vi.stubGlobal('localStorage', {
      getItem: vi.fn((name: string) => storage.get(name) ?? null),
      setItem: (name: string, value: string) => {
        persist(name, value);
        storage.set(name, value);
      },
      removeItem: (name: string) => storage.delete(name),
    });
    vi.stubGlobal('sessionStorage', {
      getItem: vi.fn(() => null),
      setItem: persist,
      removeItem: vi.fn(),
    });
    useAuthStore.getState().logout();
    useAuthStore.setState({ rememberPassword: false });
    persist.mockClear();
    read = vi
      .fn<typeof fetch>()
      .mockImplementation(() => Promise.resolve(Response.json(operation)));
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    useAuthStore.getState().logout();
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
  });
  const find = (id: string) => renderer.root.findByProps({ 'data-testid': id });
  const has = (id: string) => renderer.root.findAllByProps({ 'data-testid': id }).length > 0;
  const hasLogin = () => renderer.root.findAllByType(LoginPage).length > 0;
  const input = () => renderer.root.findAllByType('input').find((node) => node.props.id === 'console-admin-key')!;
  const logout = () =>
    renderer.root
      .findAllByType('button')
      .find((button) =>
        [t('logout'), 'Logout', '登出', 'Выйти'].includes(button.props['aria-label'])
      )!;
  async function mount() {
    await act(async () => {
      renderer = create(
        <FreshConsolePage
          context={context}
          client={createPublicApiClient(
            () => context,
            async (url, init) => {
              const target = String(url);
              if (target.endsWith('/api-keys'))
                return Response.json({
                  keys: [],
                  gatewayPending: true,
                  revocationAvailable: false,
                });
              if (target.endsWith('/settings/update-channel'))
                return Response.json({ channel_preference: 'auto' });
              if (target.endsWith('/installation/runtime'))
                return Response.json({
                  runtimeIdentity: 'runtime',
                  runtimeGeneration: '17486582467214426535',
                  activeGatewayArtifact: { artifactId: 'sha256:' + 'a'.repeat(64) },
                  state: 'ready',
                });
              if (target.includes('/gateway/retry/')) return new Response('', { status: 503 });
              return read(url, init);
            }
          )}
        />
      );
    });
    await act(async () => {
      await vi.dynamicImportSettled();
    });
  }
  async function login(remember = false) {
    await act(async () => input().props.onChange({ target: { value: key } }));
    await act(async () =>
      renderer.root.findByProps({ type: 'checkbox' }).props.onChange({ target: { checked: remember } })
    );
    await act(async () => find('console-login').props.onSubmit({ preventDefault: vi.fn() }));
  }
  async function navigate(route: string) {
    if (!renderer.root.findAllByType('a').some((link) => link.props.href === '/console' + route))
      await act(async () =>
        renderer.root
          .findAllByType('button')
          .find((button) => button.props['aria-controls'] === 'nav-system')!
          .props.onClick({ currentTarget: null })
      );
    await act(async () =>
      renderer.root
        .findAllByType('a')
        .find((link) => link.props.href === '/console' + route)!
        .props.onClick({
          button: 0,
          preventDefault: vi.fn(),
          currentTarget: null,
          target: null,
          metaKey: false,
          altKey: false,
          ctrlKey: false,
          shiftKey: false,
        })
    );
  }
  it('uses the original login page and persists no credential without remember-login', async () => {
    await mount();
    expect(hasLogin()).toBe(true);
    await login();
    expect(has('legacy-console')).toBe(true);
    expect(renderer.root.findByType(LegacyConsolePage)).toBeTruthy();
    expect(read.mock.calls[0][0]).toBe('https://manager.example/console/api/v2/operation');
    expect(new Headers(read.mock.calls[0][1]?.headers).get('Authorization')).toBe('Bearer ' + key);
    await act(async () => logout().props.onClick());
    expect(input().props.value).toBe('');
    await act(async () => renderer.unmount());
    await mount();
    expect(hasLogin()).toBe(true);
    // The legacy layout clears unrelated cached stores on logout. Verify the
    // Fresh administrator credential itself never appears in a persistence
    // payload while allowing those normal cache writes.
    expect(persist.mock.calls.flat().some((value) => String(value).includes(key))).toBe(false);
    expect(obfuscatedStorage.getItem<{ state: { managementKey?: string } }>(STORAGE_KEY_AUTH)?.state.managementKey).toBeUndefined();
  });
  it('never calls legacy discovery or legacy login/restore before the business Console is admitted', async () => {
    const discovery = vi.spyOn(usageServiceApi, 'getInfo').mockRejectedValue(new Error('Legacy discovery is forbidden'));
    const legacyLogin = vi.spyOn(useAuthStore.getState(), 'login').mockRejectedValue(new Error('Legacy login is forbidden'));
    const legacyRestore = vi.spyOn(useAuthStore.getState(), 'restoreSession').mockResolvedValue(false);
    read.mockImplementation(() => Promise.resolve(Response.json({ ...operation, productComplete: false })));
    await mount();
    await login(true);
    expect(has('legacy-console')).toBe(false);
    expect(renderer.root.findByType(FreshManagedRuntime).props.guided).toBe(true);
    expect(discovery).not.toHaveBeenCalled();
    expect(legacyLogin).not.toHaveBeenCalled();
    expect(legacyRestore).not.toHaveBeenCalled();
    expect(read.mock.calls[0][0]).toBe('https://manager.example/console/api/v2/operation');
  });
  it('restores a remembered session through canonical authentication after refresh and clears it on logout', async () => {
    await mount();
    await login(true);
    expect(useAuthStore.getState().rememberPassword).toBe(true);
    expect(obfuscatedStorage.getItem<{ state: { managementKey?: string } }>(STORAGE_KEY_AUTH)?.state.managementKey).toBe(key);
    await act(async () => renderer.unmount());
    // Rehydrate persisted state as a new document would; no authenticated flag
    // is persisted and no management page may bypass the operation check.
    useAuthStore.setState({ isAuthenticated: false });
    await useAuthStore.persist.rehydrate();
    read.mockClear();
    await mount();
    expect(read).toHaveBeenCalledTimes(1);
    expect(has('legacy-console')).toBe(true);
    await act(async () => logout().props.onClick());
    await act(async () => renderer.unmount());
    read.mockClear();
    await mount();
    expect(hasLogin()).toBe(true);
    expect(read).not.toHaveBeenCalled();
    expect(useAuthStore.getState().managementKey).toBe('');
  });
  it('rejects and removes a remembered credential when the server revokes it', async () => {
    await mount();
    await login(true);
    await act(async () => renderer.unmount());
    read.mockImplementation(() => Promise.resolve(new Response('', { status: 401 })));
    await mount();
    expect(hasLogin()).toBe(true);
    expect(has('legacy-console')).toBe(false);
    expect(useAuthStore.getState().managementKey).toBe('');
  });
  it('retains a remembered credential across a temporary server failure without mounting management pages', async () => {
    await mount();
    await login(true);
    await act(async () => renderer.unmount());
    read.mockImplementation(() => Promise.resolve(new Response('', { status: 503 })));
    await mount();
    expect(hasLogin()).toBe(true);
    expect(has('legacy-console')).toBe(false);
    expect(useAuthStore.getState().managementKey).toBe(key);
    await act(async () => renderer.unmount());
    read.mockImplementation(() => Promise.resolve(Response.json(operation)));
    await mount();
    expect(has('legacy-console')).toBe(true);
  });
  it('does not send credentials remembered for a different deployment prefix', async () => {
    useAuthStore.setState({
      rememberPassword: true,
      managementKey: key,
      apiBase: 'https://manager.example/foreign/api/v2/cpamp',
      sessionPanelBase: 'https://manager.example/foreign/api/v2/cpamp',
      sessionMode: 'manager_embedded',
    });
    await mount();
    expect(hasLogin()).toBe(true);
    expect(read).not.toHaveBeenCalled();
  });
  it('can sign in and revoke in-memory authority when browser storage is full', async () => {
    await mount();
    const original = localStorage.setItem;
    localStorage.setItem = () => { throw new DOMException('Storage full', 'QuotaExceededError'); };
    try {
      await login(true);
      expect(has('legacy-console')).toBe(true);
      expect(useAuthStore.getState().isAuthenticated).toBe(true);
      expect(useAuthStore.getState().rememberPassword).toBe(false);
      await act(async () => renderer.root.findByType(LegacyConsolePage).props.onUnauthorized());
      expect(hasLogin()).toBe(true);
      expect(has('legacy-console')).toBe(false);
      expect(useAuthStore.getState().managementKey).toBe('');
      expect(useAuthStore.getState().isAuthenticated).toBe(false);
    } finally {
      localStorage.setItem = original;
    }
  });
  it('continues incomplete Fresh in its guided journey without mounting management pages', async () => {
    read.mockImplementation(() =>
      Promise.resolve(Response.json({ ...operation, productComplete: false }))
    );
    await mount();
    await login();
    expect(renderer.root.findByType(FreshManagedRuntime).props.guided).toBe(true);
    expect(has('legacy-console')).toBe(false);
    expect(read.mock.calls.every(([, init]) => init?.method === 'GET')).toBe(true);
  });
  it.each([401, 403, 409, 503])('refuses authentication on HTTP %s', async (status) => {
    read.mockImplementation(() => Promise.resolve(new Response('', { status })));
    await mount();
    await login();
    expect(has('legacy-console')).toBe(false);
  });
  it.each([
    { adminInitialized: false },
    { revision: 0 },
    { identity: { ...operation.identity, installationId: '' } },
    { routing: { ...operation.routing, target: '/foreign' } },
    { routing: { ...operation.routing, revision: 2 } },
    { routing: { ...operation.routing, committed: false } },
    { routing: { ...operation.routing, operationId: 'foreign' } },
  ])('refuses malformed or mismatched operation %j', async (patch) => {
    read.mockImplementation(() => Promise.resolve(Response.json({ ...operation, ...patch })));
    await mount();
    await login();
    expect(has('legacy-console')).toBe(false);
  });
  it('keeps the legacy CPAMP pages mounted across real routes', async () => {
    await mount();
    await login();
    await navigate('/ai-providers');
    expect(has('legacy-console')).toBe(true);
    expect(
      renderer.root
        .findAllByType('a')
        .some(
          (link) =>
            link.props.href === '/console/ai-providers' &&
            String(link.props.className).includes('active')
        )
    ).toBe(true);
    await navigate('/system');
    expect(has('legacy-console')).toBe(true);
    expect(
      renderer.root
        .findAllByType('a')
        .some(
          (link) =>
            link.props.href === '/console/system' &&
            String(link.props.className).includes('active')
        )
    ).toBe(true);
  });
  it('retires rejected authority from an already mounted editor', async () => {
    await mount();
    await login();
    await act(async () => renderer.root.findByType(LegacyConsolePage).props.onUnauthorized());
    expect(has('legacy-console')).toBe(false);
    expect(input().props.value).toBe('');
  });
});
