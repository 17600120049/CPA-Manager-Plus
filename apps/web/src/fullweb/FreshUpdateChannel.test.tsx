import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshUpdateChannel } from './FreshUpdateChannel';
import { createPublicApiClient } from './publicApi';
import { fullWebText as t } from '../i18n/fullweb';

const context = { basePath: '/console', apiBase: '/console/api/v2', revision: 1 };
const result = (channel = 'auto') => Response.json({ channel_preference: channel });
const deferred = () => {
  let resolve!: (value: Response) => void;
  const promise = new Promise<Response>((r) => {
    resolve = r;
  });
  return { promise, resolve };
};

describe('Fresh persisted Manager update preference', () => {
  let renderer: ReactTestRenderer;
  const unauthorized = vi.fn();
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    unauthorized.mockClear();
    vi.stubGlobal('window', { location: new URL('https://manager.example/console') });
    for (const storage of ['localStorage', 'sessionStorage'])
      vi.stubGlobal(storage, {
        getItem: () => {
          throw new Error('browser persistence forbidden');
        },
        setItem: () => {
          throw new Error('browser persistence forbidden');
        },
      });
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    vi.useRealTimers();
    vi.unstubAllGlobals();
  });
  const select = () => renderer.root.findByType('select');
  const view = () => JSON.stringify(renderer.toJSON());
  const button = (label: string) =>
    renderer.root.findAllByType('button').find((b) => b.props.children === label)!;
  async function mount(transport: typeof fetch, onUnauthorized = unauthorized) {
    await act(async () => {
      renderer = create(
        <FreshUpdateChannel
          client={createPublicApiClient(() => context, transport)}
          credential="test-admin-only"
          onUnauthorized={onUnauthorized}
        />
      );
    });
  }
  async function choose(channel: string) {
    await act(async () => select().props.onChange({ target: { value: channel } }));
  }
  async function click(label: string) {
    await act(async () => button(label).props.onClick());
  }
  const writes = (transport: ReturnType<typeof vi.fn<typeof fetch>>) =>
    transport.mock.calls.filter(([, init]) => init?.method === 'PUT');

  it('reads, explicitly saves, refreshes without clearing the value, and restores on remount', async () => {
    const pending = deferred();
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(result())
      .mockResolvedValueOnce(result('stable'))
      .mockReturnValueOnce(pending.promise)
      .mockResolvedValueOnce(result('stable'));
    await mount(transport);
    expect(select().props.value).toBe('auto');
    await choose('stable');
    expect(writes(transport)).toHaveLength(0);
    await click(t('channelSave'));
    expect(view()).toContain(t('channelSaved'));
    expect(writes(transport)[0]).toEqual([
      'https://manager.example/console/api/v2/settings/update-channel',
      expect.objectContaining({
        method: 'PUT',
        body: '{"channel_preference":"stable"}',
        headers: {
          Accept: 'application/json',
          Authorization: 'Bearer test-admin-only',
          'Content-Type': 'application/json',
        },
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
        signal: expect.any(AbortSignal),
      }),
    ]);
    await click(t('channelReload'));
    expect(select().props.value).toBe('stable');
    expect(select().props.disabled).toBe(true);
    await act(async () => pending.resolve(result('stable')));
    await act(async () => renderer.unmount());
    await mount(transport);
    expect(select().props.value).toBe('stable');
    expect(writes(transport)).toHaveLength(1);
  });

  it.each(['stable', 'auto', 'beta'])(
    'reconciles a lost save response to actual %s without another write',
    async (actual) => {
      const transport = vi
        .fn<typeof fetch>()
        .mockResolvedValueOnce(result())
        .mockRejectedValueOnce(new Error('private network detail'))
        .mockResolvedValueOnce(result(actual));
      await mount(transport);
      await choose('stable');
      await click(t('channelSave'));
      expect(select().props.value).toBe(actual);
      expect(view()).toContain(t('channelReconciled'));
      expect(view()).not.toContain(t('channelSaved'));
      expect(view()).not.toContain('private network detail');
      expect(button(t('channelSave')).props.disabled).toBe(true);
      expect(writes(transport)).toHaveLength(1);
      expect(transport.mock.calls.map(([, init]) => init?.method)).toEqual(['GET', 'PUT', 'GET']);
    }
  );

  it('keeps unknown state locked until an explicit authoritative read succeeds', async () => {
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(result())
      .mockResolvedValueOnce(new Response('', { status: 503 }))
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce(result('stable'));
    await mount(transport);
    await choose('stable');
    await click(t('channelSave'));
    expect(view()).toContain(t('channelUnknown'));
    expect(select().props.disabled).toBe(true);
    await click(t('channelSave'));
    expect(writes(transport)).toHaveLength(1);
    await click(t('channelReload'));
    expect(select().props.value).toBe('stable');
    expect(view()).not.toContain(t('channelUnknown'));
    expect(writes(transport)).toHaveLength(1);
  });

  it('blocks same-turn duplicate submissions and a refresh racing with a save', async () => {
    const pending = deferred();
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(result())
      .mockReturnValueOnce(pending.promise);
    await mount(transport);
    await choose('stable');
    const save = button(t('channelSave')).props.onClick;
    const reload = button(t('channelReload')).props.onClick;
    await act(async () => {
      save();
      save();
      reload();
    });
    expect(writes(transport)).toHaveLength(1);
    expect(transport).toHaveBeenCalledTimes(2);
    await act(async () => pending.resolve(result('stable')));
    expect(select().props.value).toBe('stable');
  });

  it.each([401, 403])(
    'clears authentication on rejected read/save/reconciliation (%s)',
    async (status) => {
      for (const phase of ['read', 'save', 'reconcile']) {
        const transport = vi.fn<typeof fetch>();
        if (phase !== 'read') transport.mockResolvedValueOnce(result());
        if (phase === 'reconcile') transport.mockRejectedValueOnce(new Error('lost response'));
        transport.mockResolvedValueOnce(new Response('secret', { status }));
        await mount(transport);
        if (phase !== 'read') {
          await choose('stable');
          await click(t('channelSave'));
        }
        expect(unauthorized).toHaveBeenCalledTimes(1);
        expect(view()).not.toContain('secret');
        await act(async () => renderer.unmount());
        unauthorized.mockClear();
      }
    }
  );

  it.each(['read', 'save'])('ignores late %s responses after logout/remount', async (phase) => {
    const pending = deferred();
    const transport = vi.fn<typeof fetch>();
    if (phase === 'save') transport.mockResolvedValueOnce(result());
    transport.mockReturnValueOnce(pending.promise);
    await mount(transport);
    if (phase === 'save') {
      await choose('stable');
      await click(t('channelSave'));
    }
    await act(async () => renderer.unmount());
    await mount(async () => result('beta'));
    await act(async () => pending.resolve(result('stable')));
    expect(select().props.value).toBe('beta');
    expect(unauthorized).not.toHaveBeenCalled();
  });

  it.each([null, {}, { channel_preference: 'unknown' }])(
    'rejects malformed authoritative reads %j',
    async (bad) => {
      await mount(async () => Response.json(bad));
      expect(select().props.disabled).toBe(true);
      expect(view()).toContain(t('channelUnavailable'));
    }
  );

  it('treats malformed/mismatched success as unknown and reconciles', async () => {
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(result())
      .mockResolvedValueOnce(result('beta'))
      .mockResolvedValueOnce(result('stable'));
    await mount(transport);
    await choose('stable');
    await click(t('channelSave'));
    expect(view()).toContain(t('channelReconciled'));
    expect(view()).not.toContain(t('channelSaved'));
    expect(writes(transport)).toHaveLength(1);
  });
});
