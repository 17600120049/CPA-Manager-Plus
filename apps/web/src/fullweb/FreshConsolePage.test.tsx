/// <reference types="node" />
import { webcrypto } from 'node:crypto';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshConsolePage } from './FreshConsolePage';
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
const retryExpected = {
  sourceId: 'isolated-source-1234',
  generation: '7',
  revision: 2,
  sha256: 'a'.repeat(64),
};
const retryView = {
  field: 'routing.retry.request-retry',
  presence: 'present',
  value: 2,
  source: 'private-embedded-cpa-v8.0.13',
  authority: 'host-runtime',
  editable: true,
  reason: '',
  persisted: 'persisted',
  effective: 'unknown',
  concurrentProtection: 'host-exclusive-runtime-reservation',
  expected: retryExpected,
};

describe('Fresh Console Admin Key login and read-only operation', () => {
  let renderer: ReactTestRenderer;
  let focusRetry: ReturnType<typeof vi.fn>;
  let scrollRetry: ReturnType<typeof vi.fn>;
  beforeEach(() => {
    focusRetry = vi.fn();
    scrollRetry = vi.fn();
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.stubGlobal('window', { location: new URL('https://manager.example/console') });
    vi.stubGlobal('crypto', { getRandomValues: (bytes: Uint8Array) => bytes.fill(0xbb), subtle: webcrypto.subtle });
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
  async function mount(
    fetchMock: ReturnType<typeof vi.fn<typeof fetch>>,
    keyFetch: typeof fetch = async () =>
      Response.json({ keys: [], gatewayPending: true, revocationAvailable: false }),
    settingFetch: typeof fetch = async () => Response.json({ channel_preference: 'auto' })
  ) {
    await act(async () => {
      renderer = create(
        <FreshConsolePage
          context={context}
          client={createPublicApiClient(
            () => context,
            (url, init) =>
              String(url).includes('/api-keys')
                ? keyFetch(url, init)
                : String(url).includes('/settings/update-channel')
                  ? settingFetch(url, init)
                  : fetchMock(url, init)
          )}
        />,
        {
          createNodeMock: (element) => {
            const props = element.props as { id?: string };
            if (props.id === 'gateway-retry-title') return { focus: focusRetry };
            if (props.id === 'gateway-retry') return { scrollIntoView: scrollRetry };
            return null;
          },
        }
      );
    });
  }
  async function login(value = key) {
    await act(async () => input().props.onChange({ target: { value } }));
    await act(async () => {
      await renderer.root.findByType('form').props.onSubmit({ preventDefault: vi.fn() });
    });
  }

  it('scrolls and focuses the editor on first and repeated navigation without remounting', async () => {
    const retryFetch = vi.fn<typeof fetch>().mockResolvedValue(Response.json(retryView));
    await mount(
      vi
        .fn<typeof fetch>()
        .mockImplementation((url, init) =>
          String(url).includes('/gateway/retry/')
            ? retryFetch(url, init)
            : Promise.resolve(Response.json(operation))
        )
    );
    await login();
    for (let activation = 1; activation <= 2; activation++) {
      await act(async () =>
        renderer.root
          .findByProps({ 'data-testid': 'gateway-retry-navigation' })
          .props.onClick({ preventDefault: vi.fn() })
      );
      expect(focusRetry).toHaveBeenCalledTimes(activation);
      expect(focusRetry).toHaveBeenLastCalledWith({ preventScroll: true });
      expect(scrollRetry).toHaveBeenCalledTimes(activation);
      expect(scrollRetry).toHaveBeenLastCalledWith({ block: 'start' });
      expect(renderer.root.findByProps({ id: 'gateway-retry-title' }).props.tabIndex).toBe(-1);
    }
    expect(retryFetch).toHaveBeenCalledTimes(1);
  });

  it.each(['persisted', 'recovered', 'rejected'] as const)(
    'blocks same-turn logout during PUT and releases only after %s confirmation',
    async (confirmation) => {
      let resolvePut!: (value: Response) => void;
      let rejectPut!: (reason: Error) => void;
      const pendingPut = new Promise<Response>((resolve, reject) => {
        resolvePut = resolve;
        rejectPut = reject;
      });
      const requestId = 'bb'.repeat(16);
      const savedOperation = { ...retryExpected, requestId, value: 3, state: confirmation };
      const response = (state: string) =>
        Response.json({ operation: { ...savedOperation, state }, effective: 'unknown' });
      const retryFetch = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(Response.json(retryView))
        .mockReturnValueOnce(pendingPut);
      if (confirmation === 'recovered') {
        retryFetch
          .mockResolvedValueOnce(response('unknown'))
          .mockResolvedValueOnce(response('recovered'));
      }
      if (confirmation !== 'rejected') {
        retryFetch.mockResolvedValueOnce(
          Response.json({
            ...retryView,
            value: confirmation === 'recovered' ? 7 : 3,
            operation: savedOperation,
          })
        );
      }
      await mount(
        vi
          .fn<typeof fetch>()
          .mockImplementation((url, init) =>
            String(url).includes('/gateway/retry/')
              ? retryFetch(url, init)
              : Promise.resolve(Response.json(operation))
          )
      );
      await login();
      await act(async () =>
        renderer.root
          .findByProps({ 'data-testid': 'gateway-retry-navigation' })
          .props.onClick({ preventDefault: vi.fn() })
      );
      await act(async () =>
        renderer.root
          .findByProps({ 'data-testid': 'retry-value' })
          .props.onChange({ target: { value: '3' } })
      );
      const logout = () => buttons().find((button) => button.props.children === t('logout'))!;
      await act(async () => {
        renderer.root.findByProps({ 'data-testid': 'retry-save' }).props.onClick();
        logout().props.onClick();
      });
      expect(logout().props.disabled).toBe(true);
      expect(renderer.root.findAllByProps({ 'data-testid': 'retry-editor' })).toHaveLength(1);
      if (confirmation === 'recovered') {
        await act(async () => rejectPut(new Error('lost original response')));
        await act(async () => logout().props.onClick());
        expect(logout().props.disabled).toBe(true);
        expect(
          renderer.root.findByProps({ 'data-testid': 'retry-operation-id' }).props.children
        ).toBe(requestId);
        await act(async () =>
          renderer.root.findByProps({ 'data-testid': 'retry-check-operation' }).props.onClick()
        );
        expect(logout().props.disabled).toBe(true);
        await act(async () =>
          renderer.root.findByProps({ 'data-testid': 'retry-reconcile' }).props.onClick()
        );
        expect(renderer.root.findByProps({ 'data-testid': 'retry-value' }).props.value).toBe('7');
        expect(retryFetch.mock.calls[2][0]).toBe(
          'https://manager.example/console/api/v2/gateway/retry/operations/' + requestId
        );
        expect(retryFetch.mock.calls[3][0]).toBe(
          'https://manager.example/console/api/v2/gateway/retry/operations/' +
            requestId +
            '/reconcile'
        );
      } else {
        await act(async () =>
          resolvePut(
            confirmation === 'rejected'
              ? Response.json({ code: 'invalid_retry_request' }, { status: 400 })
              : response('persisted')
          )
        );
      }
      expect(logout().props.disabled).toBe(false);
      expect(retryFetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
      await act(async () => logout().props.onClick());
      expect(renderer.root.findAllByProps({ 'data-testid': 'retry-editor' })).toHaveLength(0);
      expect(input().props.value).toBe('');
    }
  );

  it('retires Console Admin authority when the settings read rejects it', async () => {
    await mount(
      vi.fn<typeof fetch>().mockImplementation(async () => Response.json(operation)),
      undefined,
      async () => new Response('', { status: 401 })
    );
    await login();
    expect(renderer.root.findAllByProps({ 'data-testid': 'console-operation' })).toHaveLength(0);
    expect(input().props.value).toBe('');
    expect(view()).toContain(t('authError'));
  });

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

  it('ignores a stale Console refresh after a newer Runtime stage is observed', async () => {
    let resolveRefresh!: (response: Response) => void;
    const delayedRefresh = new Promise<Response>((done) => {
      resolveRefresh = done;
    });
    const host = {
      runtimeIdentity: 'owned-runtime',
      runtimeGeneration: 3,
      activeGatewayArtifact: { artifactId: 'sha256:' + 'a'.repeat(64) },
    };
    const staged: FreshOperation = {
      ...operation,
      revision: 7,
      strategy: {
        strategy: 'new_managed',
        targetIdentity: host.runtimeIdentity,
        targetGeneration: host.runtimeGeneration,
        targetArtifactId: host.activeGatewayArtifact.artifactId,
        stage: 'staged',
      },
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(operation))
      .mockReturnValueOnce(delayedRefresh)
      .mockResolvedValueOnce(Response.json({}))
      .mockResolvedValueOnce(Response.json(staged));
    await mount(fetchMock);
    await login();
    await act(async () => buttons()[1].props.onClick());
    await act(async () => renderer.root.findByProps({ 'data-testid': 'fresh-select-new' }).props.onClick());
    await act(async () => {
      renderer.root
        .findByProps({ 'data-testid': 'runtime-observation' })
        .props.onChange({ target: { value: JSON.stringify(host) } });
    });
    await act(async () => {
      renderer.root.findByProps({ 'data-testid': 'install-runtime' }).props.onClick();
    });
    const revision = () =>
      renderer.root.findByProps({ 'data-testid': 'console-operation' }).findAllByType('dd')[1].props
        .children;
    const stage = () =>
      renderer.root.findByProps({ 'data-testid': 'install-stage' }).props.children;
    expect(revision()).toBe(7);
    expect(stage()).toBe('staged');
    await act(async () => resolveRefresh(Response.json(operation)));
    expect(revision()).toBe(7);
    expect(stage()).toBe('staged');
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it('uses one strategy owner and blocks mode switching during an External POST', async () => {
    let finish!: (value: Response) => void;
    const pending = new Promise<Response>((resolve) => { finish = resolve; });
    const external = {
      ...operation,
      revision: operation.revision + 2,
      strategy: {
        strategy: 'connect_external',
        targetIdentity: 'external:sha256:fd4c35afa405c2cb257987151885823ab76cc177741c7c8dab7679ab53c1cbb2',
        targetGeneration: 1,
        stage: 'committed',
      },
    };
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(operation))
      .mockReturnValueOnce(pending)
      .mockResolvedValueOnce(Response.json(external));
    await mount(fetchMock);
    await login();
    await act(async () =>
      renderer.root.findByProps({ 'data-testid': 'fresh-select-external' }).props.onClick()
    );
    expect(renderer.root.findAllByProps({ 'data-testid': 'runtime-observation' })).toHaveLength(0);
    await act(async () =>
      renderer.root.findByProps({ 'data-testid': 'external-base-url' })
        .props.onChange({ target: { value: 'http://cpa.private:8317' } })
    );
    await act(async () =>
      renderer.root.findByProps({ 'data-testid': 'external-management-key' })
        .props.onChange({ target: { value: 'secret' } })
    );
    await act(async () => renderer.root.findByProps({ 'data-testid': 'external-connect' }).props.onClick());
    expect(renderer.root.findByProps({ 'data-testid': 'fresh-select-new' }).props.disabled).toBe(true);
    expect(buttons().find((button) => button.props.children === t('logout'))?.props.disabled).toBe(true);
    await act(async () => renderer.root.findByProps({ 'data-testid': 'fresh-select-new' }).props.onClick());
    expect(renderer.root.findAllByProps({ 'data-testid': 'runtime-observation' })).toHaveLength(0);
    await act(async () => finish(Response.json(external)));
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
    expect(renderer.root.findAllByProps({ 'data-testid': 'fresh-select-new' })).toHaveLength(0);
    expect(renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
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

  it('blocks logout during an unknown rename and unlocks only after reading current persisted details', async () => {
    const record = {
      id: 'a'.repeat(32),
      requestId: 'b'.repeat(32),
      maskedPreview: 'cpamp_sk_…cccc',
      name: '',
      nameRevision: 1,
      createdAtMs: 1234,
      managerState: 'persisted',
      runtimeState: 'not_applied',
    };
    let reject!: (error: Error) => void;
    const keyFetch = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        Response.json({ keys: [record], gatewayPending: true, revocationAvailable: false })
      )
      .mockResolvedValueOnce(Response.json(record))
      .mockReturnValueOnce(
        new Promise((_resolve, fail) => {
          reject = fail;
        })
      )
      .mockResolvedValueOnce(Response.json({ ...record, name: 'saved', nameRevision: 2 }));
    await mount(vi.fn<typeof fetch>().mockResolvedValue(Response.json(operation)), keyFetch);
    await login();
    const detail = renderer.root
      .findAllByType('button')
      .find(
        (item) =>
          Array.isArray(item.props.children) && item.props.children.includes(t('keysDetails'))
      )!;
    await act(async () => detail.props.onClick());
    await act(async () =>
      renderer.root.findByProps({ id: 'key-name' }).props.onChange({ target: { value: 'saved' } })
    );
    const logout = renderer.root
      .findAllByType('button')
      .find((item) => item.props.children === t('logout'))!;
    await act(async () => {
      renderer.root.findByType('form').props.onSubmit({ preventDefault: vi.fn() });
      logout.props.onClick();
    });
    expect(view()).toContain(operation.identity.operationId);
    expect(
      renderer.root.findAllByType('button').find((item) => item.props.children === t('logout'))!
        .props.disabled
    ).toBe(true);
    await act(async () => reject(new Error('lost rename response')));
    expect(view()).toContain(t('keysNameUnknown'));
    await act(async () =>
      renderer.root
        .findAllByType('button')
        .find((item) => item.props.children === t('keysReadDetails'))!
        .props.onClick()
    );
    expect(view()).toContain(t('keysNameChecked'));
    expect(
      renderer.root.findAllByType('button').find((item) => item.props.children === t('logout'))!
        .props.disabled
    ).toBe(false);
    expect(keyFetch.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
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

  it.each(['creation reply', 'reconciliation'])(
    'blocks ordinary logout until %s confirms the pending key operation',
    async (confirmation) => {
      let resolve!: (value: Response) => void;
      let reject!: (reason: Error) => void;
      const pending = new Promise<Response>((done, fail) => {
        resolve = done;
        reject = fail;
      });
      let resolveCheck!: (value: Response) => void;
      const pendingCheck = new Promise<Response>((done) => {
        resolveCheck = done;
      });
      const savedKey = {
        id: 'a'.repeat(32),
        requestId: 'b'.repeat(32),
        maskedPreview: 'cpamp_sk_…cccc',
        name: '',
        nameRevision: 1,
        createdAtMs: 1234,
        managerState: 'persisted',
        runtimeState: 'not_applied',
      };
      const secret = 'cpamp_sk_' + 'c'.repeat(64);
      const keyFetch = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(
          Response.json({ keys: [], gatewayPending: true, revocationAvailable: false })
        )
        .mockReturnValueOnce(pending)
        .mockReturnValueOnce(pendingCheck);
      await mount(vi.fn<typeof fetch>().mockResolvedValue(Response.json(operation)), keyFetch);
      await login();
      const logout = buttons()[2];
      await act(async () => {
        buttons()[3].props.onClick();
        // A same-turn invocation must also respect the guard before React rerenders.
        logout.props.onClick();
      });
      expect(buttons()[2].props.disabled).toBe(true);
      expect(view()).toContain(t('keysExitBlocked'));
      expect(view()).toContain(operation.identity.operationId);
      if (confirmation === 'creation reply') {
        await act(async () => resolve(Response.json({ key: savedKey, createdSecret: secret })));
        expect(view()).toContain(secret);
      } else {
        await act(async () => reject(new Error('lost reply')));
        await act(async () => buttons()[2].props.onClick());
        expect(view()).toContain(t('keysUnknown'));
        expect(buttons()[2].props.disabled).toBe(true);
        await act(async () =>
          buttons()
            .find((button) => button.props.children === t('keysCheck'))!
            .props.onClick()
        );
        expect(buttons()[2].props.disabled).toBe(true);
        await act(async () => resolveCheck(Response.json(savedKey)));
        expect(view()).toContain(t('keysConfirmed'));
        expect(view()).not.toContain(secret);
      }
      expect(buttons()[2].props.disabled).toBe(false);
      expect(view()).not.toContain(t('keysExitBlocked'));
      expect(keyFetch.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
      await act(async () => buttons()[2].props.onClick());
      expect(input().props.value).toBe('');
      expect(view()).not.toContain(secret);
      expect(localStorage.setItem).not.toHaveBeenCalled();
      expect(sessionStorage.setItem).not.toHaveBeenCalled();
    }
  );

  it.each([401, 403])(
    'still retires rejected Admin authority during creation (%s)',
    async (status) => {
      const keyFetch = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(
          Response.json({ keys: [], gatewayPending: true, revocationAvailable: false })
        )
        .mockResolvedValueOnce(new Response('', { status }))
        .mockResolvedValueOnce(
          Response.json({ keys: [], gatewayPending: true, revocationAvailable: false })
        );
      await mount(
        vi.fn<typeof fetch>().mockImplementation(async () => Response.json(operation)),
        keyFetch
      );
      await login();
      await act(async () => buttons()[3].props.onClick());
      expect(input().props.value).toBe('');
      expect(view()).toContain(t('authError'));
      await login();
      expect(buttons()[2].props.disabled).toBe(false);
      expect(view()).not.toContain(t('keysExitBlocked'));
    }
  );
});
