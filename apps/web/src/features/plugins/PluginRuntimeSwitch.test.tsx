import i18n from '@/i18n';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi, type Mock } from 'vitest';
import { PublicApiError, type PublicApiClient } from '@/fullweb/publicApi';
import { PluginRuntimeSwitch } from './PluginRuntimeSwitch';

const reactGlobal = globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean };
reactGlobal.IS_REACT_ACT_ENVIRONMENT = true;

const BASELINE = 'a'.repeat(64);

const managedView = (options: {
  enabled?: boolean;
  writable?: boolean;
  operation?: { requestId: string; state: string; http200?: boolean };
} = {}) => ({
  capability: { mode: 'managed', readable: true, writable: true, rulesWritable: true },
  generation: '7',
  groups: [
    {
      id: 'plugins',
      prefix: 'plugins',
      paths: ['plugins.enabled'],
      baseline: BASELINE,
      writable: options.writable !== false,
      persisted: options.operation?.state ?? 'unknown',
      effective: 'unknown',
      ...(options.operation ? { operation: { group: 'plugins', target: 'group', ...options.operation } } : {}),
    },
  ],
  fields: [
    {
      path: 'plugins.enabled',
      type: 'bool',
      kind: 'bool',
      group: 'plugins',
      presence: 'present',
      value: options.enabled === true,
      editable: true,
    },
  ],
  families: [],
  groupsInvalid: false,
});

const savedReply = (requestId: string) => ({
  operation: { requestId, group: 'plugins', target: 'group', state: 'persisted', http200: true },
  effective: 'unknown',
});

let renderer: ReactTestRenderer | undefined;

const text = (node: ReactTestInstance): string =>
  node.children.map((child) => (typeof child === 'string' ? child : text(child))).join('');

const switchInput = () => renderer!.root.findByType('input');

const client = (get: Mock, post: Mock): PublicApiClient =>
  ({ get, post, put: vi.fn() }) as unknown as PublicApiClient;

const mount = async (api: PublicApiClient, onApplied = vi.fn()) => {
  await act(async () => {
    renderer = create(
      <PluginRuntimeSwitch client={api} credential="admin-test" onApplied={onApplied} />
    );
  });
  return onApplied;
};

afterEach(async () => {
  if (renderer)
    await act(async () => {
      renderer?.unmount();
    });
  renderer = undefined;
  vi.unstubAllGlobals();
});

beforeEach(async () => {
  // The host locale decides the default language; the copy asserted below must not
  // depend on the machine running these tests.
  await i18n.changeLanguage('zh-CN');
});

describe('Plugin runtime switch', () => {
  it('shows the persisted value and leaves the effective value unclaimed', async () => {
    const api = client(vi.fn().mockResolvedValue(managedView()), vi.fn());
    await mount(api);
    const content = text(renderer!.root);
    expect(content).toContain('插件运行开关');
    expect(content).toContain('已关闭');
    expect(content).toContain('未知，直到 CPA 重载');
    expect(switchInput().props.checked).toBe(false);
    expect(switchInput().props.disabled).toBe(false);
  });

  it('is absent when the installation does not own the plugin root', async () => {
    const external = {
      ...managedView(),
      capability: {
        mode: 'external',
        readable: false,
        writable: false,
        rulesWritable: false,
        reason: 'plugin_switch_requires_managed_install',
      },
    };
    await mount(client(vi.fn().mockResolvedValue(external), vi.fn()));
    expect(renderer!.toJSON()).toBeNull();
  });

  it('is absent where the Manager has no plugin runtime route at all', async () => {
    const get = vi.fn().mockRejectedValue(new PublicApiError('routing_unavailable'));
    await mount(client(get, vi.fn()));
    expect(renderer!.toJSON()).toBeNull();
  });

  it('writes the switch against the observed baseline and re-reads it', async () => {
    const get = vi
      .fn()
      .mockResolvedValueOnce(managedView())
      .mockResolvedValueOnce(managedView({ enabled: true }));
    const post = vi.fn().mockResolvedValue(savedReply('request-write'));
    const onApplied = await mount(client(get, post));

    await act(async () => {
      switchInput().props.onChange({ target: { checked: true } });
    });

    expect(post).toHaveBeenCalledTimes(1);
    const [resource, body] = post.mock.calls[0];
    expect(resource).toBe('plugins/runtime/groups/plugins');
    expect(body).toMatchObject({
      expected: { generation: '7', baseline: BASELINE },
      edits: [{ path: 'plugins.enabled', action: 'set', value: true }],
    });
    expect(typeof body.requestId).toBe('string');
    expect(body.requestId).toHaveLength(32);
    expect(get).toHaveBeenCalledTimes(2);
    const content = text(renderer!.root);
    expect(content).toContain('已启用');
    expect(content).toContain('已保存。CPA 重载配置后生效。');
    expect(onApplied).toHaveBeenCalledTimes(1);
  });

  it('settles an unknown result by reading back and never resends the write', async () => {
    const get = vi
      .fn()
      .mockResolvedValueOnce(managedView())
      .mockResolvedValueOnce(
        managedView({
          writable: false,
          operation: { requestId: 'request-unknown', state: 'unknown' },
        })
      )
      .mockResolvedValueOnce(managedView({ enabled: true }));
    const post = vi
      .fn()
      .mockRejectedValueOnce(new PublicApiError('network'))
      .mockResolvedValueOnce(savedReply('request-unknown'));
    const onApplied = await mount(client(get, post));

    await act(async () => {
      switchInput().props.onChange({ target: { checked: true } });
    });

    expect(post).toHaveBeenCalledTimes(2);
    expect(post.mock.calls[0][0]).toBe('plugins/runtime/groups/plugins');
    expect(post.mock.calls[1][0]).toBe('plugins/runtime/operations/request-unknown/reconcile');
    expect(post.mock.calls[1][1]).toEqual({});
    const content = text(renderer!.root);
    expect(content).toContain('已读回：新值已保存。');
    expect(onApplied).toHaveBeenCalledTimes(1);
  });

  it('offers the read-back control for a journaled unknown write after a reload', async () => {
    const pending = managedView({
      enabled: true,
      writable: false,
      operation: { requestId: 'request-journaled', state: 'unknown' },
    });
    const get = vi.fn().mockResolvedValueOnce(pending).mockResolvedValueOnce(managedView({ enabled: true }));
    const post = vi.fn().mockResolvedValue(savedReply('request-journaled'));
    await mount(client(get, post));

    expect(switchInput().props.disabled).toBe(true);
    const reconcile = renderer!.root
      .findAll((node) => node.type === 'button')
      .find((item) => text(item) === '读回结果');
    expect(reconcile).toBeDefined();

    await act(async () => {
      reconcile!.props.onClick();
    });

    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0][0]).toBe('plugins/runtime/operations/request-journaled/reconcile');
    expect(text(renderer!.root)).toContain('已读回：新值已保存。');
  });

  it('keeps the draft and reports a moved baseline without writing again', async () => {
    const get = vi.fn().mockResolvedValue(managedView());
    const post = vi
      .fn()
      .mockRejectedValue(new PublicApiError('http', 409, undefined, 'source_or_operation_conflict'));
    const onApplied = await mount(client(get, post));

    await act(async () => {
      switchInput().props.onChange({ target: { checked: true } });
    });

    expect(post).toHaveBeenCalledTimes(1);
    expect(text(renderer!.root)).toContain('读取后配置已被改动，请重新读取后再保存。');
    expect(onApplied).not.toHaveBeenCalled();
  });
});
