import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshConsolePage } from './FreshConsolePage';
import { createPublicApiClient } from './publicApi';
import type { FreshOperation } from './bootstrapTransport';

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
  productComplete: false,
  routing: {
    operationId: 'fresh-console-operation',
    target: '/console',
    revision: 1,
    stage: 'committed',
    committed: true,
  },
};
const key = 'Cpamp_Test_Only_Console_Admin_123456';

describe('Fresh Console Admin Key login and read-only operation', () => {
  let renderer: ReactTestRenderer;
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.stubGlobal('window', { location: new URL('https://manager.example/console') });
    vi.stubGlobal('document', {
      documentElement: { classList: { toggle: vi.fn<typeof fetch>() } },
    });
    vi.stubGlobal('localStorage', {
      setItem: vi.fn(() => {
        throw new Error('persistence forbidden');
      }),
    });
    vi.stubGlobal('sessionStorage', {
      setItem: vi.fn(() => {
        throw new Error('persistence forbidden');
      }),
    });
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    vi.unstubAllGlobals();
  });
  const input = () => renderer.root.findByType('input');
  const buttons = () => renderer.root.findAllByType('button');
  const view = () => JSON.stringify(renderer.toJSON());
  async function mount(fetchMock: ReturnType<typeof vi.fn<typeof fetch>>) {
    await act(async () => {
      renderer = create(
        <FreshConsolePage
          context={context}
          client={createPublicApiClient(() => context, fetchMock)}
        />
      );
    });
  }
  async function login(value = key) {
    await act(async () => input().props.onChange({ target: { value } }));
    await act(async () => {
      await renderer.root.findByType('form').props.onSubmit({ preventDefault: vi.fn() });
    });
  }

  it('authenticates with the protected canonical GET, refreshes real state, logs out and requires login on remount', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json(operation))
      .mockResolvedValueOnce(Response.json({ ...operation, revision: 7 }));
    await mount(fetchMock);
    expect(fetchMock).not.toHaveBeenCalled();
    expect(document.title).toContain('CPAMP');
    expect(input().parent?.props.className).toBe('input-wrap');
    await login();
    expect(view()).toContain(operation.identity.operationId);
    expect(view()).toContain('Pending');
    expect(view()).not.toContain(key);
    expect(fetchMock.mock.calls[0]).toEqual([
      'https://manager.example/console/api/v2/operation',
      {
        method: 'GET',
        headers: { Accept: 'application/json', Authorization: 'Bearer ' + key },
        mode: 'same-origin',
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
      },
    ]);
    await act(async () => buttons()[1].props.onClick());
    expect(view()).toContain('7');
    await act(async () => buttons()[2].props.onClick());
    expect(input().props.value).toBe('');
    expect(view()).not.toContain(operation.identity.operationId);
    expect(localStorage.setItem).not.toHaveBeenCalled();
    expect(sessionStorage.setItem).not.toHaveBeenCalled();
    await act(async () => renderer.unmount());
    await mount(fetchMock);
    expect(input().props.value).toBe('');
    expect(fetchMock).toHaveBeenCalledTimes(2);
  });

  it.each([401, 403, 503, 409])(
    'does not display operation for failed login (%s), or reflect server errors',
    async (status) => {
      await mount(
        vi.fn<typeof fetch>().mockResolvedValue(new Response('secret server details', { status }))
      );
      await login();
      expect(renderer.root.findAllByProps({ 'data-testid': 'console-operation' })).toHaveLength(0);
      expect(input().props.value).toBe('');
      expect(view()).not.toContain('secret server details');
      expect(renderer.root.findAllByProps({ role: 'alert' })).toHaveLength(1);
    }
  );

  it.each([
    { ...operation, adminInitialized: false },
    { ...operation, routing: { ...operation.routing, target: '/foreign' } },
    { ...operation, routing: { ...operation.routing, committed: false } },
    { ...operation, identity: { operationId: operation.identity.operationId } },
  ])('rejects uninitialized, foreign or malformed server state', async (value) => {
    await mount(vi.fn<typeof fetch>().mockResolvedValue(Response.json(value)));
    await login();
    expect(renderer.root.findAllByProps({ 'data-testid': 'console-operation' })).toHaveLength(0);
    expect(view()).not.toContain(key);
  });

  it('retains explicitly stale state on network interruption, then clears it when the key is rejected', async () => {
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json(operation))
      .mockRejectedValueOnce(new Error('private transport detail'))
      .mockResolvedValueOnce(new Response('', { status: 401 }));
    await mount(fetchMock);
    await login();
    await act(async () => buttons()[1].props.onClick());
    expect(view()).toContain(operation.identity.operationId);
    expect(renderer.root.findAllByProps({ role: 'alert' })).toHaveLength(1);
    expect(view()).not.toContain('private transport detail');
    await act(async () => buttons()[1].props.onClick());
    expect(input().props.value).toBe('');
    expect(view()).not.toContain(operation.identity.operationId);
  });

  it('does not restore an authenticated view after logout during a pending refresh', async () => {
    let resolve!: (value: Response) => void;
    const pending = new Promise<Response>((done) => {
      resolve = done;
    });
    await mount(
      vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(Response.json(operation))
        .mockReturnValueOnce(pending)
    );
    await login();
    await act(async () => buttons()[1].props.onClick());
    await act(async () => buttons()[2].props.onClick());
    await act(async () => {
      resolve(Response.json(operation));
    });
    expect(input().props.value).toBe('');
    expect(view()).not.toContain(operation.identity.operationId);
  });
});
