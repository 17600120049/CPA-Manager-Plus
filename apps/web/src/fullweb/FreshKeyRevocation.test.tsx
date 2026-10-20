import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshKeyRevocation } from './FreshKeyRevocation';
import { createPublicApiClient } from './publicApi';
import { fullWebText as t } from '../i18n/fullweb';

const id = 'b'.repeat(32);
const target = 'a'.repeat(32);
const keeper = 'c'.repeat(32);
const operation = {
  id,
  targetId: target,
  keeperId: keeper,
  mode: 'replace',
  outcome: 'pending',
  revision: 1,
  steps: [
    { name: 'add', state: 'unattempted', returned: false },
    { name: 'revoke', state: 'unattempted', returned: false },
  ],
};
const result = (o: unknown = operation, nextStep = 'add') =>
  Response.json({ operation: o, nextStep, runtimeState: 'effect_unknown', expiresAtMs: 0 });
const keys = [target, keeper].map((id) => ({
  id,
  requestId: id,
  maskedPreview: 'cpamp_sk_…test',
  createdAtMs: 1,
  managerState: 'persisted' as const,
  runtimeState: 'not_applied' as const,
  name: '',
  nameRevision: 1,
}));

describe('Canonical revocation recovery UI', () => {
  let view: ReactTestRenderer;
  const pending = vi.fn();
  const changed = vi.fn();
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    pending.mockClear();
    changed.mockClear();
    vi.stubGlobal('window', {
      location: new URL('https://manager.example/control'),
      confirm: () => true,
    });
    vi.stubGlobal('crypto', { getRandomValues: (bytes: Uint8Array) => bytes.fill(0xbb) });
  });
  afterEach(async () => {
    if (view) await act(async () => view.unmount());
    vi.unstubAllGlobals();
  });
  const button = (name: Parameters<typeof t>[0]) =>
    view.root.findAllByType('button').find((b) => b.props.children === t(name))!;
  const click = async (name: Parameters<typeof t>[0]) => {
    await act(async () => button(name).props.onClick());
  };
  async function mount(fetchMock: ReturnType<typeof vi.fn<typeof fetch>>) {
    await act(async () => {
      view = create(
        <FreshKeyRevocation
          keys={keys}
          client={createPublicApiClient(
            () => ({ basePath: '/control', apiBase: '/control/api/v2', revision: 1 }),
            fetchMock
          )}
          credential="test-admin"
          onUnauthorized={vi.fn()}
          onPending={pending}
          onChanged={changed}
          disabled={false}
        />
      );
    });
  }
  async function select() {
    await act(async () =>
      view.root.findAllByType('select')[0].props.onChange({ target: { value: target } })
    );
    await act(async () =>
      view.root.findAllByType('select')[1].props.onChange({ target: { value: keeper } })
    );
    await act(async () =>
      view.root.findAllByType('select')[2].props.onChange({ target: { value: 'replace' } })
    );
  }
  it('rejects a prepare reply with another canonical selection and recovers only the original request', async () => {
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(result(null, ''))
      .mockResolvedValueOnce(result({ ...operation, targetId: 'd'.repeat(32) }))
      .mockResolvedValueOnce(result());
    await mount(fetchMock);
    await select();
    await click('keysOperationPrepare');
    expect(JSON.stringify(view.toJSON())).toContain(t('keysOperationUnknown'));
    expect(pending).toHaveBeenLastCalledWith(true);
    await click('keysOperationCheck');
    expect(String(fetchMock.mock.calls[2][0])).toContain('/operations/' + id);
    expect(button('keysOperationContinue').props.disabled).toBe(false);
    expect(fetchMock.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
  });
  it('never unlocks a durable operation on a null or regressed response', async () => {
    const known = {
      ...operation,
      revision: 3,
      steps: [{ name: 'add', state: 'observed', returned: true }, operation.steps[1]],
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(result(known, 'revoke'))
      .mockResolvedValueOnce(result(null, ''))
      .mockResolvedValueOnce(result());
    await mount(fetchMock);
    await click('keysOperationCheck');
    expect(JSON.stringify(view.toJSON())).toContain(t('keysOperationUnknown'));
    expect(button('keysOperationContinue').props.disabled).toBe(true);
    await click('keysOperationCheck');
    expect(button('keysOperationContinue').props.disabled).toBe(true);
    expect(pending).not.toHaveBeenCalledWith(false);
    expect(fetchMock.mock.calls.every(([, init]) => init?.method === 'GET')).toBe(true);
  });
  it('recovers latest after reload, continues once, then refreshes retained canonical history', async () => {
    const committed = {
      ...operation,
      revision: 4,
      outcome: 'committed',
      steps: operation.steps.map((s) => ({ ...s, state: 'observed', returned: true })),
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(result(operation, 'add'))
      .mockResolvedValueOnce(result(committed, ''));
    await mount(fetchMock);
    expect(String(fetchMock.mock.calls[0][0])).toContain('/operations/latest');
    await click('keysOperationContinue');
    expect(String(fetchMock.mock.calls[1][0])).toContain('/operations/' + id + '/continue');
    expect(changed).toHaveBeenCalledTimes(1);
    expect(pending).toHaveBeenLastCalledWith(false);
    expect(JSON.stringify(view.toJSON())).toContain(t('keysOperationCommitted'));
  });
  it('allows correcting a rejected new selection after the previous operation committed', async () => {
    const committed = {
      ...operation,
      id: 'd'.repeat(32),
      revision: 4,
      outcome: 'committed',
      steps: operation.steps.map((s) => ({ ...s, state: 'observed', returned: true })),
    };
    const fetchMock = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(result(committed, ''))
      .mockResolvedValueOnce(new Response('', { status: 409 }))
      .mockResolvedValueOnce(result(null, ''));
    await mount(fetchMock);
    await select();
    await click('keysOperationPrepare');
    expect(pending).toHaveBeenLastCalledWith(true);
    await click('keysOperationCheck');
    expect(String(fetchMock.mock.calls[2][0])).toContain('/operations/' + id);
    expect(pending).toHaveBeenLastCalledWith(false);
    expect(view.root.findByType('fieldset').props.disabled).toBe(false);
  });
});
