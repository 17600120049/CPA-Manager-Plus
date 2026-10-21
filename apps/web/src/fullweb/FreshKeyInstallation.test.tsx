import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshKeyInstallation } from './FreshKeyInstallation';
import { createPublicApiClient } from './publicApi';
import { fullWebText as t } from '../i18n/fullweb';

const id = 'a'.repeat(32);
const context = { basePath: '/control', apiBase: '/control/api/v2', revision: 1 };
const pending = {
  keyId: id,
  managerStaged: true,
  cpaConfigured: false,
  runtimeState: 'effect_unknown',
  runtimeGeneration: '7',
  observedFromMs: 0,
  observedUntilMs: 0,
  expiresAtMs: 0,
  ingressAuthorized: false,
  installAvailable: true,
  attempted: false,
};
describe('private S1 installation', () => {
  let renderer: ReactTestRenderer;
  const unauthorized = vi.fn();
  const onPending = vi.fn();
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.stubGlobal('window', { location: new URL('https://manager.example/control') });
    unauthorized.mockClear();
    onPending.mockClear();
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
  async function mount(fetchMock: ReturnType<typeof vi.fn<typeof fetch>>) {
    await act(async () => {
      renderer = create(
        <FreshKeyInstallation
          id={id}
          client={createPublicApiClient(() => context, fetchMock)}
          credential="admin-only"
          onUnauthorized={unauthorized}
          onPending={onPending}
          canInstall={() => true}
          disabled={false}
        />
      );
    });
  }
  async function click(label: string) {
    await act(async () =>
      renderer.root
        .findAllByType('button')
        .find((b) => b.props.children === label)!
        .props.onClick()
    );
  }
  const view = () => JSON.stringify(renderer.toJSON());

  it('sends only canonical ID and empty JSON, never retries an unknown write', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending))
      .mockRejectedValueOnce(new Error('lost'))
      .mockResolvedValueOnce(
        Response.json({ ...pending, attempted: true, installAvailable: false })
      );
    await mount(fetchMock);
    await click(t('keysCheckInstallation'));
    await click(t('keysInstallOne'));
    expect(fetchMock.mock.calls[1][0]).toBe(
      'https://manager.example/control/api/v2/api-keys/' + id + '/initial-install'
    );
    expect(fetchMock.mock.calls[1][1]).toMatchObject({
      method: 'POST',
      body: '{}',
      cache: 'no-store',
      headers: { Authorization: 'Bearer admin-only' },
    });
    expect(view()).toContain(t('keysInstallUnknown'));
    expect(onPending).toHaveBeenLastCalledWith(true);
    expect(view()).not.toContain(t('keysInstallOne'));
    await click(t('keysCheckInstallation'));
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
    expect(view()).toContain(t('keysIngressPending'));
    expect(onPending).toHaveBeenLastCalledWith(false);
  });


  it('holds the exit lock after a lost install until a durable attempt is read, without a second POST', async () => {
    let rejectInstall!: (reason: Error) => void;
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending))
      .mockReturnValueOnce(
        new Promise<Response>((_resolve, reject) => {
          rejectInstall = reject;
        })
      )
      .mockResolvedValueOnce(Response.json(pending))
      .mockResolvedValueOnce(Response.json({ ...pending, attempted: true, installAvailable: false }));
    await mount(fetchMock);
    await click(t('keysCheckInstallation'));
    await act(async () => {
      const install = renderer.root.findAllByType('button').find((b) => b.props.children === t('keysInstallOne'))!;
      install.props.onClick();
      install.props.onClick();
    });
    expect(onPending).toHaveBeenLastCalledWith(true);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
    await act(async () => rejectInstall(new Error('reply lost')));
    await click(t('keysCheckInstallation'));
    expect(onPending).toHaveBeenLastCalledWith(true);
    await click(t('keysCheckInstallation'));
    expect(onPending).toHaveBeenLastCalledWith(false);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it('retains the original UNKNOWN warning if a recovery GET fails and later observes the S1 journal', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending))
      .mockRejectedValueOnce(new Error('install response lost'))
      .mockResolvedValueOnce(new Response('', { status: 503 }))
      .mockResolvedValueOnce(Response.json(pending))
      .mockResolvedValueOnce(
        Response.json({ ...pending, attempted: true, installAvailable: false })
      );
    await mount(fetchMock);
    await click(t('keysCheckInstallation'));
    await click(t('keysInstallOne'));
    expect(view()).toContain(t('keysInstallUnknown'));
    await click(t('keysCheckInstallation'));
    expect(view()).toContain(t('keysInstallUnknown'));
    expect(view()).toContain(t('keysInstallUnavailable'));
    expect(onPending).toHaveBeenLastCalledWith(true);
    await click(t('keysCheckInstallation'));
    expect(view()).toContain(t('keysInstallUnknown'));
    expect(onPending).toHaveBeenLastCalledWith(true);
    await click(t('keysCheckInstallation'));
    expect(view()).not.toContain(t('keysInstallUnknown'));
    expect(onPending).toHaveBeenLastCalledWith(false);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it('holds an unresolved Key install when a different S1 journal consumed the global slot', async () => {
    const foreignJournal = { ...pending, attempted: false, installAvailable: false };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(pending))
      .mockRejectedValueOnce(new Error('unknown POST reply'))
      .mockResolvedValueOnce(Response.json(foreignJournal));
    await mount(fetchMock);
    await click(t('keysCheckInstallation'));
    await click(t('keysInstallOne'));
    await click(t('keysCheckInstallation'));
    expect(view()).toContain(t('keysInstallUnknown'));
    expect(view()).not.toContain(t('keysInstallOne'));
    expect(onPending).toHaveBeenLastCalledWith(true);
    expect(onPending).not.toHaveBeenCalledWith(false);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it('expires a live observation without a write or reload', async () => {
    vi.useFakeTimers();
    const now = Date.now();
    await mount(
      vi.fn<typeof fetch>().mockResolvedValue(
        Response.json({
          ...pending,
          cpaConfigured: true,
          attempted: true,
          installAvailable: false,
          runtimeState: 'effective_observed',
          observedFromMs: now - 1000,
          observedUntilMs: now,
          expiresAtMs: now + 5000,
        })
      )
    );
    await click(t('keysCheckInstallation'));
    expect(view()).toContain(t('keysLiveObserved'));
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5001);
    });
    expect(view()).not.toContain(t('keysLiveObserved'));
    expect(view()).toContain(t('keysLiveUnknown'));
  });

  it('rejects an Ingress grant or unbounded effective evidence', async () => {
    await mount(
      vi
        .fn<typeof fetch>()
        .mockResolvedValue(Response.json({ ...pending, ingressAuthorized: true }))
    );
    await click(t('keysCheckInstallation'));
    expect(view()).toContain(t('keysInstallUnavailable'));
    expect(view()).not.toContain(t('keysInstallOne'));
  });

  it('keeps the snapshot during a deferred GET while its live claim expires', async () => {
    vi.useFakeTimers();
    const now = Date.now();
    let finish!: (response: Response) => void;
    const deferred = new Promise<Response>((resolve) => {
      finish = resolve;
    });
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        Response.json({
          ...pending,
          cpaConfigured: true,
          attempted: true,
          installAvailable: false,
          runtimeState: 'effective_observed',
          observedFromMs: now - 1000,
          observedUntilMs: now,
          expiresAtMs: now + 5000,
        })
      )
      .mockReturnValueOnce(deferred);
    await mount(fetchMock);
    await click(t('keysCheckInstallation'));
    await click(t('keysCheckInstallation'));
    expect(renderer.root.findByType('section').props['aria-busy']).toBe(true);
    for (const label of [
      'keysLoading',
      'keysManagerStaged',
      'keysCPAConfigured',
      'keysLiveObserved',
      'keysIngressPending',
      'keysObservation',
    ] as const) {
      expect(view()).toContain(t(label));
    }
    expect(view()).toContain(new Date(now).toISOString());
    await act(async () => {
      await vi.advanceTimersByTimeAsync(5001);
    });
    expect(view()).not.toContain(t('keysLiveObserved'));
    expect(view()).toContain(t('keysLiveUnknown'));
    expect(view()).toContain(t('keysCPAConfigured'));
    expect(view()).toContain(new Date(now).toISOString());
    await act(async () => {
      finish(Response.json({ ...pending, attempted: true, installAvailable: false }));
    });
    expect(renderer.root.findByType('section').props['aria-busy']).toBe(false);
    expect(view()).not.toContain(t('keysLiveObserved'));
    expect(view()).not.toContain(t('keysObservation'));
    expect(view()).not.toContain(t('keysInstallOne'));
    expect(fetchMock.mock.calls.every(([, init]) => init?.method === 'GET')).toBe(true);
  });

  it('retires Admin authentication after denial', async () => {
    await mount(vi.fn<typeof fetch>().mockResolvedValue(new Response('', { status: 401 })));
    await click(t('keysCheckInstallation'));
    expect(unauthorized).toHaveBeenCalledOnce();
  });
});
