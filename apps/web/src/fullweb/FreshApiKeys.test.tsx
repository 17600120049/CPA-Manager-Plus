import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshApiKeys } from './FreshApiKeys';
import { createPublicApiClient } from './publicApi';
import { fullWebText as t } from '../i18n/fullweb';

const context = { basePath: '/console', apiBase: '/console/api/v2', revision: 1 };
const key = {
  id: 'a'.repeat(32),
  requestId: 'b'.repeat(32),
  maskedPreview: 'cpamp_sk_…cccc',
  createdAtMs: 1234,
  managerState: 'persisted',
  runtimeState: 'not_applied',
  name: '',
  nameRevision: 1,
};
const secret = 'cpamp_sk_' + 'c'.repeat(64);
const empty = () => Response.json({ keys: [], gatewayPending: true, revocationAvailable: false });

describe('Fresh client key persistence UI', () => {
  let renderer: ReactTestRenderer;
  const unauthorized = vi.fn();
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    unauthorized.mockClear();
    vi.stubGlobal('window', { location: new URL('https://manager.example/console') });
    vi.stubGlobal('crypto', { getRandomValues: (bytes: Uint8Array) => bytes.fill(0xbb) });
    vi.stubGlobal('navigator', {
      clipboard: { writeText: vi.fn().mockResolvedValue(undefined) },
    });
    vi.stubGlobal('localStorage', {
      setItem: vi.fn(() => {
        throw new Error('forbidden');
      }),
    });
    vi.stubGlobal('sessionStorage', {
      setItem: vi.fn(() => {
        throw new Error('forbidden');
      }),
    });
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
  const view = () => JSON.stringify(renderer.toJSON());
  const button = (text: string) =>
    renderer.root.findAllByType('button').find((item) => item.props.children === text)!;
  async function mount(
    fetchMock: ReturnType<typeof vi.fn<typeof fetch>>,
    onCreationPending = vi.fn()
  ) {
    await act(async () => {
      renderer = create(
        <FreshApiKeys
          client={createPublicApiClient(() => context, fetchMock)}
          credential="admin-test-only"
          onUnauthorized={unauthorized}
          onCreationPending={onCreationPending}
        />
      );
    });
  }
  async function click(text: string) {
    await act(async () => button(text).props.onClick());
  }

  it('keeps the Console exit lock when a revocation read finishes during an unknown S1 install', async () => {
    const onCreationPending = vi.fn();
    let finishOperation!: (response: Response) => void;
    let rejectInstall!: (reason: Error) => void;
    let initialReads = 0;
    const initial = {
      keyId: key.id,
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
    const fetchMock = vi.fn<typeof fetch>((url, options) => {
      const address = String(url);
      if (address.endsWith('/api-keys') && options?.method === 'GET')
        return Promise.resolve(
          Response.json({ keys: [key], gatewayPending: true, revocationAvailable: true })
        );
      if (address.endsWith('/api-keys/operations/latest'))
        return new Promise<Response>((resolve) => { finishOperation = resolve; });
      if (address.endsWith('/api-keys/' + key.id) && options?.method === 'GET')
        return Promise.resolve(Response.json(key));
      if (address.endsWith('/initial-install') && options?.method === 'GET')
        return Promise.resolve(
          Response.json(
            initialReads++ === 0 ? initial : { ...initial, attempted: true, installAvailable: false }
          )
        );
      if (address.endsWith('/initial-install') && options?.method === 'POST')
        return new Promise<Response>((_resolve, reject) => { rejectInstall = reject; });
      return Promise.reject(new Error('unexpected Key API: ' + address));
    });
    await mount(fetchMock, onCreationPending);
    await act(async () => renderer.root.findAllByType('li')[0].findByType('button').props.onClick());
    await click(t('keysCheckInstallation'));
    await act(async () => button(t('keysInstallOne')).props.onClick());
    expect(onCreationPending).toHaveBeenLastCalledWith(true);

    // The separate Revocation component remains mounted while Key Details is open.
    await act(async () =>
      finishOperation(
        Response.json({
          operation: null,
          nextStep: '',
          runtimeState: 'effect_unknown',
          expiresAtMs: 0,
        })
      )
    );
    expect(onCreationPending).toHaveBeenLastCalledWith(true);
    await act(async () => rejectInstall(new Error('install response lost')));
    expect(onCreationPending).toHaveBeenLastCalledWith(true);
    await click(t('keysCheckInstallation'));
    expect(onCreationPending).toHaveBeenLastCalledWith(false);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it('keeps a pending revocation lock when an unrelated Key detail is read', async () => {
    const onCreationPending = vi.fn();
    const pendingOperation = {
      id: 'd'.repeat(32),
      targetId: key.id,
      keeperId: 'e'.repeat(32),
      mode: 'replace',
      outcome: 'pending',
      revision: 1,
      steps: [
        { name: 'add', state: 'unattempted', returned: false },
        { name: 'revoke', state: 'unattempted', returned: false },
      ],
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(
        Response.json({ keys: [key], gatewayPending: true, revocationAvailable: true })
      )
      .mockResolvedValueOnce(
        Response.json({
          operation: pendingOperation,
          nextStep: 'add',
          runtimeState: 'effect_unknown',
          expiresAtMs: 0,
        })
      )
      .mockResolvedValueOnce(Response.json(key));
    await mount(fetchMock, onCreationPending);
    expect(onCreationPending).toHaveBeenLastCalledWith(true);
    await act(async () => renderer.root.findAllByType('li')[0].findByType('button').props.onClick());
    expect(onCreationPending).toHaveBeenLastCalledWith(true);
    expect(fetchMock.mock.calls.every(([, init]) => init?.method === 'GET')).toBe(true);
  });

  it('uses real Public API, shows creation secret once, preserves identity and never persists browser secrets', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(empty())
      .mockResolvedValueOnce(Response.json({ key, createdSecret: secret }, { status: 201 }));
    await mount(fetchMock);
    expect(view()).toContain(t('keysEmpty'));
    await click(t('keysCreate'));
    expect(view()).toContain(secret);
    expect(view()).toContain(key.id);
    expect(view()).toContain(t('keysStaged'));
    expect(button(t('keysCreate')).props.disabled).toBe(true);
    await click(t('keysCopy'));
    expect(navigator.clipboard.writeText).toHaveBeenCalledExactlyOnceWith(secret);
    expect(view()).toContain(t('keysCopied'));
    const [url, init] = fetchMock.mock.calls[1];
    expect(url).toBe('https://manager.example/console/api/v2/api-keys');
    expect(init).toMatchObject({
      method: 'POST',
      body: JSON.stringify({ requestId: key.requestId }),
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
      headers: { Authorization: 'Bearer admin-test-only', 'Content-Type': 'application/json' },
    });
    expect(String(url)).not.toContain('admin-test-only');
    await click(t('keysDismiss'));
    expect(view()).not.toContain(secret);
    expect(view()).not.toContain(t('keysCopied'));
    expect(view()).toContain(key.maskedPreview);
    expect(localStorage.setItem).not.toHaveBeenCalled();
    expect(sessionStorage.setItem).not.toHaveBeenCalled();
  });

  it('keeps the one-time key visible and reports failure when clipboard permission is denied', async () => {
    const writeText = vi.fn().mockRejectedValue(new Error('permission denied'));
    vi.stubGlobal('navigator', { clipboard: { writeText } });
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(empty())
      .mockResolvedValueOnce(Response.json({ key, createdSecret: secret }, { status: 201 }));
    await mount(fetchMock);
    await click(t('keysCreate'));
    await click(t('keysCopy'));
    expect(writeText).toHaveBeenCalledExactlyOnceWith(secret);
    expect(view()).toContain(t('keysCopyFailed'));
    expect(view()).toContain(secret);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await click(t('keysDismiss'));
    expect(view()).not.toContain(secret);
    expect(view()).not.toContain(t('keysCopyFailed'));
  });


  it.each(['success', 'failure'] as const)(
    'ignores a delayed clipboard %s after the one-time secret is dismissed',
    async (outcome) => {
      let resolveCopy!: () => void;
      let rejectCopy!: (reason: Error) => void;
      const writeText = vi.fn(
        () =>
          new Promise<void>((resolve, reject) => {
            resolveCopy = resolve;
            rejectCopy = reject;
          })
      );
      vi.stubGlobal('navigator', { clipboard: { writeText } });
      const fetchMock = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(empty())
        .mockResolvedValueOnce(Response.json({ key, createdSecret: secret }, { status: 201 }));
      await mount(fetchMock);
      await click(t('keysCreate'));
      await click(t('keysCopy'));
      expect(writeText).toHaveBeenCalledExactlyOnceWith(secret);
      expect(view()).toContain(secret);

      await click(t('keysDismiss'));
      expect(view()).not.toContain(secret);
      await act(async () => {
        if (outcome === 'success') resolveCopy();
        else rejectCopy(new Error('permission denied'));
      });
      expect(view()).not.toContain(secret);
      expect(view()).not.toContain(t('keysCopied'));
      expect(view()).not.toContain(t('keysCopyFailed'));
      expect(fetchMock).toHaveBeenCalledTimes(2);
    }
  );

  it('keeps the one-time key available for manual copy when Clipboard API is unavailable', async () => {
    vi.stubGlobal('navigator', {});
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(empty())
      .mockResolvedValueOnce(Response.json({ key, createdSecret: secret }, { status: 201 }));
    await mount(fetchMock);
    await click(t('keysCreate'));
    await click(t('keysCopy'));
    expect(view()).toContain(t('keysCopyFailed'));
    expect(view()).toContain(secret);
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await click(t('keysDismiss'));
    expect(view()).not.toContain(secret);
    expect(view()).not.toContain(t('keysCopyFailed'));
  });

  it('blocks double submission, reconciles lost reply with GET and never resends creation', async () => {
    let reject!: (reason: Error) => void;
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(empty())
      .mockReturnValueOnce(
        new Promise<Response>((_resolve, fail) => {
          reject = fail;
        })
      )
      .mockResolvedValueOnce(Response.json(key));
    await mount(fetchMock);
    await act(async () => {
      button(t('keysCreate')).props.onClick();
      button(t('keysCreate')).props.onClick();
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    await act(async () => reject(new Error('private transport error')));
    expect(view()).toContain(t('keysUnknown'));
    expect(view()).not.toContain('private transport error');
    expect(button(t('keysCreate')).props.disabled).toBe(true);
    await click(t('keysCheck'));
    expect(fetchMock.mock.calls[2][0]).toContain('/api-keys/requests/' + key.requestId);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
    expect(view()).toContain(t('keysConfirmed'));
    expect(view()).not.toContain(secret);
  });

  it('retries only the same request after a not-found reconciliation and does not report unknown as success', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(empty())
      .mockResolvedValueOnce(new Response('', { status: 503 }))
      .mockResolvedValueOnce(new Response('', { status: 404 }))
      .mockResolvedValueOnce(Response.json({ key }));
    await mount(fetchMock);
    await click(t('keysCreate'));
    expect(view()).not.toContain(t('keysConfirmed'));
    await click(t('keysCheck'));
    expect(view()).toContain(t('keysUnknown'));
    await click(t('keysRetry'));
    expect(fetchMock.mock.calls[1][1]?.body).toBe(fetchMock.mock.calls[3][1]?.body);
    expect(view()).toContain(t('keysConfirmed'));
  });

  it('releases a hanging result check after its timeout and allows another check without a POST', async () => {
    vi.useFakeTimers();
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(empty())
      .mockRejectedValueOnce(new Error('lost creation reply'))
      .mockImplementationOnce(
        (_url, options) =>
          new Promise((_resolve, reject) => {
            options?.signal?.addEventListener('abort', () => reject(new Error('timed out')));
          })
      )
      .mockResolvedValueOnce(Response.json(key));
    await mount(fetchMock);
    await click(t('keysCreate'));
    await click(t('keysCheck'));
    expect(button(t('keysCheck')).props.disabled).toBe(true);
    await act(async () => {
      await vi.advanceTimersByTimeAsync(15000);
    });
    expect(fetchMock.mock.calls[2][1]?.signal?.aborted).toBe(true);
    expect(button(t('keysCheck')).props.disabled).toBe(false);
    expect(view()).toContain(t('keysUnknown'));
    expect(view()).not.toContain(t('keysConfirmed'));
    await click(t('keysCheck'));
    expect(view()).toContain(t('keysConfirmed'));
    expect(fetchMock.mock.calls[2][0]).toBe(fetchMock.mock.calls[3][0]);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });

  it.each([401, 403])('retires Admin authority after %s', async (status) => {
    await mount(vi.fn<typeof fetch>().mockResolvedValue(new Response('', { status })));
    expect(unauthorized).toHaveBeenCalledTimes(1);
    expect(button(t('keysCreate')).props.disabled).toBe(true);
  });

  it('does not treat malformed or Gateway-effective state as a valid list', async () => {
    await mount(
      vi
        .fn<typeof fetch>()
        .mockResolvedValue(
          Response.json({ keys: [key], gatewayPending: false, revocationAvailable: true })
        )
    );
    expect(view()).not.toContain(key.id);
    expect(button(t('keysCreate')).props.disabled).toBe(true);
    expect(view()).toContain(t('keysUnavailable'));
  });
});
