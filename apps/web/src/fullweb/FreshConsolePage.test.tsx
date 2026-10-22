import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshConsolePage } from './FreshConsolePage';
import { FreshApiKeys } from './FreshApiKeys';
import { FreshRetryConfiguration } from './FreshRetryConfiguration';
import { FreshManagedRuntime } from './FreshManagedRuntime';
import { createPublicApiClient } from './publicApi';
import type { FreshOperation } from './bootstrapTransport';
import { fullWebText as t } from '../i18n/fullweb';
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
  let pop: () => void;
  let read: ReturnType<typeof vi.fn<typeof fetch>>;
  const persist = vi.fn(() => {
    throw new Error('secret persistence forbidden');
  });
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
        if (event === 'popstate') pop = handler;
      },
      removeEventListener: vi.fn(),
      matchMedia: () => ({
        matches: false,
        addEventListener: vi.fn(),
        removeEventListener: vi.fn(),
      }),
    });
    vi.stubGlobal('document', {
      documentElement: { classList: { toggle: vi.fn() }, setAttribute: vi.fn() },
      body: { style: { overflow: '' } },
      addEventListener: vi.fn(),
      removeEventListener: vi.fn(),
    });
    vi.stubGlobal('localStorage', { setItem: persist });
    vi.stubGlobal('sessionStorage', { setItem: persist });
    persist.mockClear();
    read = vi
      .fn<typeof fetch>()
      .mockImplementation(() => Promise.resolve(Response.json(operation)));
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    vi.unstubAllGlobals();
  });
  const find = (id: string) => renderer.root.findByProps({ 'data-testid': id });
  const has = (id: string) => renderer.root.findAllByProps({ 'data-testid': id }).length > 0;
  const input = () => renderer.root.findByProps({ id: 'console-admin-key' });
  const logout = () =>
    renderer.root
      .findAllByType('button')
      .find((button) => button.props['aria-label'] === t('logout'))!;
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
  }
  async function login() {
    await act(async () => input().props.onChange({ target: { value: key } }));
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
        .props.onClick({ preventDefault: vi.fn() })
    );
  }
  it('uses only canonical authenticated reads and requires login after remount', async () => {
    await mount();
    await login();
    expect(has('console-shell')).toBe(true);
    expect(has('console-home')).toBe(true);
    expect(read.mock.calls[0][0]).toBe('https://manager.example/console/api/v2/operation');
    expect(new Headers(read.mock.calls[0][1]?.headers).get('Authorization')).toBe('Bearer ' + key);
    await act(async () => logout().props.onClick());
    expect(input().props.value).toBe('');
    await act(async () => renderer.unmount());
    await mount();
    expect(has('console-login')).toBe(true);
    expect(persist).not.toHaveBeenCalled();
  });
  it('continues incomplete Fresh in its guided journey without mounting management pages', async () => {
    read.mockImplementation(() =>
      Promise.resolve(Response.json({ ...operation, productComplete: false }))
    );
    await mount();
    await login();
    expect(renderer.root.findByType(FreshManagedRuntime).props.guided).toBe(true);
    expect(has('console-shell')).toBe(false);
    expect(renderer.root.findAllByType(FreshApiKeys)).toHaveLength(0);
    expect(read.mock.calls.every(([, init]) => init?.method === 'GET')).toBe(true);
  });
  it.each([401, 403, 409, 503])('refuses authentication on HTTP %s', async (status) => {
    read.mockImplementation(() => Promise.resolve(new Response('', { status })));
    await mount();
    await login();
    expect(has('console-shell')).toBe(false);
    expect(input().props.value).toBe('');
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
    expect(has('console-shell')).toBe(false);
  });
  it('keeps delivered editors mounted across real routes and browser Back', async () => {
    await mount();
    await login();
    await navigate('/api-keys');
    const keys = renderer.root.findByType(FreshApiKeys);
    await navigate('/system/gateway');
    expect(location.pathname).toBe('/console/system/gateway');
    expect(renderer.root.findByType(FreshApiKeys)).toBe(keys);
    const retry = renderer.root.findByType(FreshRetryConfiguration);
    await navigate('/system/settings');
    expect(renderer.root.findByType(FreshRetryConfiguration)).toBe(retry);
    location.pathname = '/console/system/gateway';
    await act(async () => pop());
    expect(renderer.root.findByType(FreshRetryConfiguration)).toBe(retry);
    expect(
      renderer.root.findAllByType('a').find((link) => link.props.href.endsWith('/system/gateway'))!
        .props['aria-current']
    ).toBe('page');
  });
  it.each(['keys', 'gateway'] as const)(
    'same-turn pending %s operation blocks logout and navigation',
    async (kind) => {
      await mount();
      await login();
      if (kind === 'keys') await navigate('/api-keys');
      if (kind === 'gateway') await navigate('/system/gateway');
      const report =
        kind === 'keys'
          ? renderer.root.findByType(FreshApiKeys).props.onCreationPending
          : renderer.root.findByType(FreshRetryConfiguration).props.onPending;
      await act(async () => {
        report(true);
        logout().props.onClick();
      });
      expect(has('console-shell')).toBe(true);
      expect(logout().props.disabled).toBe(true);
      const before = location.pathname;
      await navigate('/system/settings');
      expect(location.pathname).toBe(before);
      await act(async () => report(false));
      await act(async () => logout().props.onClick());
      expect(has('console-login')).toBe(true);
    }
  );
  it('retires rejected authority from an already mounted editor', async () => {
    await mount();
    await login();
    await navigate('/api-keys');
    await act(async () => renderer.root.findByType(FreshApiKeys).props.onUnauthorized());
    expect(has('console-shell')).toBe(false);
    expect(input().props.value).toBe('');
  });
  it('ignores an in-flight refresh after logout', async () => {
    await mount();
    await login();
    let resolve!: (value: Response) => void;
    read.mockImplementation(
      () =>
        new Promise<Response>((done) => {
          resolve = done;
        })
    );
    await act(async () =>
      renderer.root
        .findAllByType('button')
        .find((button) => button.props['aria-label'] === t('refresh'))!
        .props.onClick()
    );
    await act(async () => logout().props.onClick());
    await act(async () => resolve(Response.json(operation)));
    expect(has('console-login')).toBe(true);
  });
});
