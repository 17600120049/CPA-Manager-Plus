import { useState } from 'react';
import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { describe, expect, it, vi } from 'vitest';
import { FreshExternalConnection } from './FreshExternalConnection';
import type { FreshOperation } from './bootstrapTransport';
import { PublicApiError, type createPublicApiClient } from './publicApi';

const identity = {
  admissionId: 'a', installationId: 'i', generation: 'g',
  databasePath: '/data/usage.sqlite', operationId: 'o',
};
const initial: FreshOperation = {
  identity, revision: 4, adminInitialized: true, productComplete: false,
  gatewayPending: true,
  routing: { operationId: 'o', revision: 1, target: '/console', stage: 'committed', committed: true },
};
const checkpoint = {
  strategy: 'connect_external',
  targetIdentity: 'external:sha256:fd4c35afa405c2cb257987151885823ab76cc177741c7c8dab7679ab53c1cbb2',
  targetGeneration: 1, stage: 'committed',
};
const committed: FreshOperation = { ...initial, revision: 6, strategy: checkpoint };

async function mount(
  get: ReturnType<typeof vi.fn>,
  post: ReturnType<typeof vi.fn>,
  initialOperation: FreshOperation = initial
) {
  const pending = vi.fn(), unauthorized = vi.fn();
  const client = { get, post } as unknown as ReturnType<typeof createPublicApiClient>;
  function Harness() {
    const [current, update] = useState(initialOperation);
    return (
      <FreshExternalConnection client={client} credential="private-admin" operation={current}
        onOperation={update} onPending={pending} onUnauthorized={unauthorized} />
    );
  }
  let renderer!: ReactTestRenderer;
  await act(async () => { renderer = create(<Harness />); });
  const node = (id: string) => renderer.root.findByProps({ 'data-testid': id });
  const field = async (id: string, value: string) =>
    act(async () => node(id).props.onChange({ target: { value } }));
  const prepare = async () => {
    await field('external-base-url', 'http://cpa.private:8317');
    await field('external-management-key', 'secret');
  };
  const click = async (id: string) => act(async () => node(id).props.onClick());
  return { renderer, node, field, prepare, click, pending, unauthorized };
}

describe('Fresh External one-operation connection', () => {
  it('acknowledges only one POST plus a matching later GET, with no complete', async () => {
    const get = vi.fn().mockResolvedValue(committed);
    const post = vi.fn().mockResolvedValue(committed);
    const ui = await mount(get, post);
    await ui.prepare();
    await ui.click('external-connect');
    expect(post).toHaveBeenCalledTimes(1);
    expect(post.mock.calls[0]).toEqual([
      'strategy',
      { identity, expectedRevision: 4, strategy: 'connect_external',
        baseUrl: 'http://cpa.private:8317', managementKey: 'secret' },
      { kind: 'admin', value: 'private-admin' },
    ]);
    expect(get).toHaveBeenCalledTimes(1);
    expect(ui.pending).toHaveBeenLastCalledWith(false);
    expect(ui.renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
    expect(JSON.stringify(ui.renderer.toJSON())).not.toContain('secret');
    expect(JSON.stringify(ui.renderer.toJSON())).toContain('Pending');
  });

  it('never confirms or retries an ambiguous POST even when GET shows same hash', async () => {
    const post = vi.fn().mockRejectedValue(new PublicApiError('network'));
    const get = vi.fn().mockResolvedValue(committed);
    const ui = await mount(get, post);
    await ui.prepare();
    await ui.click('external-connect');
    await ui.click('external-refresh');
    expect(get).toHaveBeenCalledTimes(2);
    expect(post).toHaveBeenCalledTimes(1);
    expect(ui.pending).toHaveBeenLastCalledWith(true);
    expect(ui.renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
  });


  it('never confirms a matching POST and GET for the wrong submitted target', async () => {
    const wrongTarget = { ...committed, strategy: {
      ...checkpoint, targetIdentity: 'external:sha256:' + 'c'.repeat(64),
    } };
    const ui = await mount(vi.fn().mockResolvedValue(wrongTarget),
      vi.fn().mockResolvedValue(wrongTarget));
    await ui.prepare();
    await ui.click('external-connect');
    expect(ui.pending).toHaveBeenLastCalledWith(true);
    expect(ui.renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
  });

  it('rejects a later revision even with matching target and authenticated GET', async () => {
    const later = { ...committed, revision: initial.revision + 3 };
    const ui = await mount(vi.fn().mockResolvedValue(later), vi.fn().mockResolvedValue(later));
    await ui.prepare();
    await ui.click('external-connect');
    expect(ui.pending).toHaveBeenLastCalledWith(true);
    expect(ui.renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
  });

  it('does not mistake a stale revision or a different target for confirmed persistence', async () => {
    const post = vi.fn().mockResolvedValue(committed);
    const mismatch = { ...committed, strategy: {
      ...checkpoint, targetIdentity: 'external:sha256:' + 'b'.repeat(64),
    } };
    const ui = await mount(vi.fn().mockResolvedValue(mismatch), post);
    await ui.prepare();
    await ui.click('external-connect');
    expect(ui.pending).toHaveBeenLastCalledWith(true);
    expect(post).toHaveBeenCalledTimes(1);
    expect(ui.renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
  });

  it.each([401, 403])('retires Admin credential on %s without an authenticated GET', async (status) => {
    const post = vi.fn().mockRejectedValue(new PublicApiError('authentication', status));
    const get = vi.fn();
    const ui = await mount(get, post);
    await ui.prepare();
    await ui.click('external-connect');
    expect(ui.unauthorized).toHaveBeenCalledTimes(1);
    expect(get).not.toHaveBeenCalled();
  });


  it('distinguishes CPA Management Key refusal from a strategy CAS conflict', async () => {
    const post = vi.fn().mockRejectedValue(
      new PublicApiError('http', 409, undefined, 'management_authentication_rejected'));
    const ui = await mount(vi.fn().mockResolvedValue(initial), post);
    await ui.prepare();
    await ui.click('external-connect');
    expect(JSON.stringify(ui.renderer.toJSON())).toContain('rejected');
    expect(ui.pending).toHaveBeenLastCalledWith(true);
    expect(post).toHaveBeenCalledTimes(1);
  });

  it('keeps 409 and source/permit failures STOP without resending', async () => {
    for (const err of [new PublicApiError('http', 409), new PublicApiError('http', 503)]) {
      const post = vi.fn().mockRejectedValue(err);
      const ui = await mount(vi.fn().mockResolvedValue(initial), post);
      await ui.prepare();
      await ui.click('external-connect');
      expect(ui.pending).toHaveBeenLastCalledWith(true);
      expect(post).toHaveBeenCalledTimes(1);
      expect(ui.renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
      await act(async () => ui.renderer.unmount());
    }
  });

  it('re-entry into a durable pending fence cannot submit another POST', async () => {
    const staged = { ...committed, revision: 5,
      strategy: { ...checkpoint, stage: 'validated' } };
    const post = vi.fn();
    const ui = await mount(vi.fn().mockResolvedValue(staged), post, staged);
    expect(ui.renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
    expect(ui.node('external-stage').props.children).toBe('validated');
    expect(ui.pending).toHaveBeenCalledWith(true);
    await ui.click('external-refresh');
    expect(post).not.toHaveBeenCalled();
  });

  it('does not allow a second POST while the first is in flight', async () => {
    let resolve!: (value: FreshOperation) => void;
    const post = vi.fn().mockImplementation(() => new Promise<FreshOperation>((r) => { resolve = r; }));
    const ui = await mount(vi.fn().mockResolvedValue(committed), post);
    await ui.prepare();
    await act(async () => { ui.node('external-connect').props.onClick(); });
    expect(post).toHaveBeenCalledTimes(1);
    expect(ui.renderer.root.findAllByProps({ 'data-testid': 'external-connect' })).toHaveLength(0);
    await act(async () => resolve(committed));
    expect(post).toHaveBeenCalledTimes(1);
  });
});
