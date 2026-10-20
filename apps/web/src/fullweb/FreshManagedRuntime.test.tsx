import { useState } from 'react';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { FreshManagedRuntime } from './FreshManagedRuntime';
import type { FreshOperation } from './bootstrapTransport';
import { PublicApiError, type createPublicApiClient } from './publicApi';

const identity = {
  admissionId: 'a',
  installationId: 'i',
  generation: 'g',
  databasePath: '/data/usage.sqlite',
  operationId: 'o',
};
const initial: FreshOperation = {
  identity,
  revision: 4,
  adminInitialized: true,
  gatewayPending: true,
  routing: {
    operationId: 'o',
    revision: 1,
    target: '/console',
    stage: 'committed',
    committed: true,
  },
};
const status = {
  runtimeIdentity: 'owned-runtime',
  runtimeGeneration: 3,
  activeGatewayArtifact: { artifactId: 'sha256:' + 'a'.repeat(64) },
};
const staged: FreshOperation = {
  ...initial,
  revision: 5,
  strategy: {
    strategy: 'new_managed',
    targetIdentity: status.runtimeIdentity,
    targetGeneration: 3,
    targetArtifactId: status.activeGatewayArtifact.artifactId,
    stage: 'staged',
  },
};
const committed: FreshOperation = {
  ...staged,
  revision: 6,
  strategy: { ...staged.strategy, stage: 'committed' },
};

async function mount(
  get: ReturnType<typeof vi.fn>,
  post: ReturnType<typeof vi.fn>,
  operation = initial
) {
  const pending = vi.fn(),
    unauthorized = vi.fn();
  const client = { get, post } as unknown as ReturnType<typeof createPublicApiClient>;
  function Harness() {
    const [current, update] = useState(operation);
    return (
      <FreshManagedRuntime
        client={client}
        credential="admin-secret"
        operation={current}
        onOperation={update}
        onPending={pending}
        onUnauthorized={unauthorized}
      />
    );
  }
  let renderer!: ReactTestRenderer;
  await act(async () => {
    renderer = create(<Harness />);
  });
  const node = (id: string) => renderer.root.findByProps({ 'data-testid': id });
  const click = async (id: string) => {
    await act(async () => {
      await node(id).props.onClick();
    });
  };
  const observe = async () => {
    await act(async () => {
      node('runtime-observation').props.onChange({ target: { value: JSON.stringify(status) } });
    });
  };
  return { node, click, observe, pending, unauthorized, renderer };
}

describe('Fresh New existing Runtime strategy integration', () => {
  it('requires a host target then stages, starts and completes with authoritative reads', async () => {
    const get = vi
      .fn()
      .mockResolvedValueOnce(staged)
      .mockResolvedValueOnce(committed)
      .mockResolvedValueOnce({ ...committed, revision: 7, productComplete: true });
    const post = vi.fn().mockResolvedValue({});
    const ui = await mount(get, post);
    await ui.click('install-runtime');
    expect(post).not.toHaveBeenCalled();
    await ui.observe();
    await ui.click('install-runtime');
    expect(post.mock.calls[0]).toEqual([
      'strategy',
      {
        identity,
        expectedRevision: 4,
        strategy: 'new_managed',
        expectedRuntimeIdentity: status.runtimeIdentity,
        expectedRuntimeGeneration: 3,
        expectedArtifactId: status.activeGatewayArtifact.artifactId,
      },
      { kind: 'admin', value: 'admin-secret' },
    ]);
    await ui.click('install-runtime');
    expect(post.mock.calls[1][1].expectedRevision).toBe(5);
    expect(post.mock.calls[1][1].expectedRuntimeGeneration).toBe(3);
    await ui.click('install-runtime');
    expect(post.mock.calls[2][0]).toBe('complete');
    expect(get).toHaveBeenCalledTimes(3);
    expect(ui.renderer.root.findAllByProps({ 'data-testid': 'install-runtime' })).toHaveLength(0);
    expect(ui.pending).toHaveBeenLastCalledWith(false);
  });

  it('keeps installation and logout unlocked after a failed read-only refresh', async () => {
    const get = vi.fn().mockRejectedValue(new PublicApiError('network'));
    const post = vi.fn();
    const ui = await mount(get, post, staged);
    await ui.click('refresh-installation');
    expect(get).toHaveBeenCalledTimes(1);
    expect(post).not.toHaveBeenCalled();
    expect(ui.node('install-runtime').props.disabled).toBe(false);
    expect(ui.pending).toHaveBeenLastCalledWith(false);
    expect(ui.renderer.root.findAllByProps({ role: 'status' })).toHaveLength(0);
  });

  it('retains a prior ambiguous-write lock when a read-only refresh also fails', async () => {
    const get = vi.fn().mockRejectedValue(new PublicApiError('network'));
    const post = vi.fn().mockRejectedValue(new PublicApiError('network'));
    const ui = await mount(get, post, staged);
    await ui.click('install-runtime');
    expect(ui.node('install-runtime').props.disabled).toBe(true);
    await ui.click('refresh-installation');
    expect(get).toHaveBeenCalledTimes(2);
    expect(ui.node('install-runtime').props.disabled).toBe(true);
    expect(ui.pending).toHaveBeenLastCalledWith(true);
  });

  it('reconciles a lost start response without another write', async () => {
    const get = vi.fn().mockResolvedValue(committed),
      post = vi.fn().mockRejectedValue(new PublicApiError('network'));
    const ui = await mount(get, post, staged);
    await ui.click('install-runtime');
    expect(post).toHaveBeenCalledTimes(1);
    expect(get).toHaveBeenCalledTimes(1);
    expect(ui.node('install-runtime').props.disabled).toBe(false);
    expect(ui.pending).toHaveBeenLastCalledWith(false);
  });

  it('keeps unknown results locked until a valid read and rejects changed identities', async () => {
    const get = vi
      .fn()
      .mockRejectedValueOnce(new PublicApiError('network'))
      .mockResolvedValueOnce({ ...committed, identity: { ...identity, generation: 'foreign' } })
      .mockResolvedValueOnce(committed);
    const post = vi.fn().mockRejectedValue(new PublicApiError('network'));
    const ui = await mount(get, post, staged);
    await ui.click('install-runtime');
    expect(ui.node('install-runtime').props.disabled).toBe(true);
    await ui.click('install-runtime');
    expect(post).toHaveBeenCalledTimes(1);
    await ui.click('refresh-installation');
    expect(ui.node('install-runtime').props.disabled).toBe(true);
    await ui.click('refresh-installation');
    expect(ui.node('install-runtime').props.disabled).toBe(false);
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('never bypasses a 409 target mismatch, even after a successful read', async () => {
    const get = vi.fn().mockResolvedValue(staged),
      post = vi.fn().mockRejectedValue(new PublicApiError('http', 409));
    const ui = await mount(get, post, staged);
    await ui.click('install-runtime');
    await ui.click('refresh-installation');
    expect(ui.node('install-runtime').props.disabled).toBe(true);
    await ui.click('install-runtime');
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('rejects a server checkpoint that differs from the host-observed target', async () => {
    const get = vi
      .fn()
      .mockResolvedValue({ ...staged, strategy: { ...staged.strategy, targetGeneration: 99 } });
    const ui = await mount(get, vi.fn().mockResolvedValue({}));
    await ui.observe();
    await ui.click('install-runtime');
    expect(ui.node('install-runtime').props.disabled).toBe(true);
    expect(ui.pending).toHaveBeenLastCalledWith(true);
  });

  it('retires admin authority on authentication failure', async () => {
    const ui = await mount(
      vi.fn(),
      vi.fn().mockRejectedValue(new PublicApiError('authentication', 401)),
      staged
    );
    await ui.click('install-runtime');
    expect(ui.unauthorized).toHaveBeenCalledTimes(1);
  });

  it('blocks writes for an incomplete persisted Runtime checkpoint', async () => {
    const post = vi.fn();
    const ui = await mount(vi.fn(), post, {
      ...staged,
      strategy: { ...staged.strategy, targetGeneration: undefined },
    });
    expect(ui.node('install-runtime').props.disabled).toBe(true);
    await ui.click('install-runtime');
    expect(post).not.toHaveBeenCalled();
  });

  it('sends only one Start while the first request is still in flight', async () => {
    let finish!: (value: unknown) => void;
    const post = vi.fn().mockImplementation(
      () =>
        new Promise((resolve) => {
          finish = resolve;
        })
    );
    const ui = await mount(vi.fn().mockResolvedValue(committed), post, staged);
    await ui.click('install-runtime');
    await ui.click('install-runtime');
    expect(post).toHaveBeenCalledTimes(1);
    expect(ui.pending).toHaveBeenLastCalledWith(true);
    await act(async () => {
      finish(committed);
    });
    expect(ui.pending).toHaveBeenLastCalledWith(false);
  });
});
