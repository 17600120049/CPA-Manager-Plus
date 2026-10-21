import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { fullWebText as t } from '../i18n/fullweb';
import { FreshRetryConfiguration } from './FreshRetryConfiguration';
import { createPublicApiClient } from './publicApi';

const context = { basePath: '/console', apiBase: '/console/api/v2', revision: 1 };
const expected = {
  sourceId: 'isolated-source-1234',
  generation: '7',
  revision: 2,
  sha256: 'a'.repeat(64),
};
const credential = 'admin-only-in-memory';

function retryView(overrides: Record<string, unknown> = {}) {
  return {
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
    expected,
    ...overrides,
  };
}

function operation(
  state: 'unknown' | 'persisted' | 'recovered',
  value = 3,
  requestId = 'ab'.repeat(16)
) {
  return {
    requestId,
    value,
    sourceId: expected.sourceId,
    generation: expected.generation,
    revision: expected.revision,
    sha256: expected.sha256,
    state,
  };
}

function operationResponse(next: ReturnType<typeof operation>) {
  return Response.json({ operation: next, effective: 'unknown' });
}

describe('Full Web request retry editor', () => {
  let renderer: ReactTestRenderer;
  let pendingEvents: boolean[];

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    pendingEvents = [];
    vi.stubGlobal('window', { location: new URL('https://manager.example/console') });
    vi.stubGlobal('crypto', {
      getRandomValues: (bytes: Uint8Array) => bytes.fill(0xab),
    });
  });

  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    vi.unstubAllGlobals();
  });

  async function mount(transport: ReturnType<typeof vi.fn<typeof fetch>>) {
    await act(async () => {
      renderer = create(
        <FreshRetryConfiguration
          client={createPublicApiClient(() => context, transport)}
          credential={credential}
          onUnauthorized={vi.fn()}
          onPending={(value) => pendingEvents.push(value)}
        />
      );
    });
  }

  function input() {
    return renderer.root.findByProps({ 'data-testid': 'retry-value' });
  }

  function button(testId: string) {
    return renderer.root.findByProps({ 'data-testid': testId });
  }

  async function change(value: string) {
    await act(async () => input().props.onChange({ target: { value } }));
  }

  async function click(testId: string) {
    await act(async () => {
      button(testId).props.onClick();
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
    });
  }

  it.each([
    ['missing', null, '', 'retryPresenceMissing'],
    ['null', null, '', 'retryPresenceNull'],
    ['present', 0, '0', 'retryPresencePresent'],
    ['present', 8, '8', 'retryPresencePresent'],
  ] as const)(
    'preserves %s/null/zero/present display semantics',
    async (presence, value, draft, label) => {
      const transport = vi
        .fn<typeof fetch>()
        .mockResolvedValue(Response.json(retryView({ presence, value })));
      await mount(transport);
      expect(input().props.value).toBe(draft);
      expect(renderer.root.findAllByProps({ className: 'info-val' })[1].props.children).toBe(
        presence === 'present'
          ? String(value)
          : t(presence === 'missing' ? 'retryPresenceMissing' : 'retryPresenceNull')
      );
      expect(JSON.stringify(renderer.toJSON())).toContain(t(label));
    }
  );

  it('shows the writer-isolation lock and prevents saving when Administrator is read-only', async () => {
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        Response.json(
          retryView({ editable: false, reason: 'config_key_writer_not_isolated', value: 0 })
        )
      );
    await mount(transport);
    expect(input().props.value).toBe('0');
    expect(input().props.disabled).toBe(true);
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retryWriterNotIsolated'));
    expect(button('retry-save').props.disabled).toBe(true);
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(0);
  });

  it('shows the private-source lock and prevents saving when the source is unavailable', async () => {
    const transport = vi.fn<typeof fetch>().mockResolvedValue(
      Response.json(
        retryView({
          editable: false,
          reason: 'private_source_unavailable',
          value: null,
          presence: 'unknown',
        })
      )
    );
    await mount(transport);
    expect(input().props.value).toBe('');
    expect(input().props.disabled).toBe(true);
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retrySourceUnavailable'));
    expect(button('retry-save').props.disabled).toBe(true);
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(0);
  });

  it('keeps an unknown field state read-only even if an inconsistent response says editable', async () => {
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValue(
        Response.json(
          retryView({ editable: true, presence: 'unknown', persisted: 'unknown', value: null })
        )
      );
    await mount(transport);
    expect(input().props.value).toBe('');
    expect(input().props.disabled).toBe(true);
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retryPresenceUnknown'));
    expect(button('retry-save').props.disabled).toBe(true);
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(0);
  });

  it('rejects blank, decimal, negative, exponent, string-like and out-of-range drafts before PUT', async () => {
    const transport = vi.fn<typeof fetch>().mockResolvedValue(Response.json(retryView()));
    await mount(transport);
    for (const value of ['', '-1', '1.5', '1e2', '11', '0.0', ' 1', '+1', 'text']) {
      await change(value);
      expect(button('retry-save').props.disabled).toBe(true);
    }
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(0);
  });

  it('saves zero and the complete latest expected source object exactly once', async () => {
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(retryView({ value: 2 })))
      .mockResolvedValueOnce(operationResponse(operation('persisted', 0)))
      .mockResolvedValueOnce(
        Response.json(retryView({ value: 0, operation: operation('persisted', 0) }))
      );
    await mount(transport);
    await change('0');
    await click('retry-save');
    const writes = transport.mock.calls.filter(([, init]) => init?.method === 'PUT');
    expect(writes).toHaveLength(1);
    expect(JSON.parse(String(writes[0][1]?.body))).toEqual({
      requestId: 'ab'.repeat(16),
      value: 0,
      expected,
    });
    expect(input().props.value).toBe('0');
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retryEffectiveUnknown'));
  });

  it.each([409, 503])('retains the original ID and never repeats PUT after %s', async (status) => {
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(retryView()))
      .mockResolvedValueOnce(new Response('', { status }));
    await mount(transport);
    await change('3');
    await click('retry-save');
    expect(renderer.root.findByProps({ 'data-testid': 'retry-operation-id' }).props.children).toBe(
      'ab'.repeat(16)
    );
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
    expect(pendingEvents[pendingEvents.length - 1]).toBe(true);
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retryOperationNoPutReplay'));
  });

  it('retains the original ID and never repeats PUT after a network failure', async () => {
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(retryView()))
      .mockRejectedValueOnce(new Error('connection lost'));
    await mount(transport);
    await change('3');
    await click('retry-save');
    expect(renderer.root.findByProps({ 'data-testid': 'retry-operation-id' }).props.children).toBe(
      'ab'.repeat(16)
    );
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retryOperationNoPutReplay'));
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
  });

  it('ignores a duplicate save click while the original PUT is in flight', async () => {
    let resolvePut!: (response: Response) => void;
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(retryView()))
      .mockReturnValueOnce(new Promise<Response>((resolve) => (resolvePut = resolve)));
    await mount(transport);
    await change('3');
    await act(async () => {
      button('retry-save').props.onClick();
      button('retry-save').props.onClick();
      await Promise.resolve();
    });
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
    resolvePut(new Response('', { status: 503 }));
    await act(async () => {
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
      await new Promise<void>((resolve) => setTimeout(resolve, 0));
    });
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retryOperationNoPutReplay'));
  });

  it('does not adopt a mismatched operation identity from a failed response', async () => {
    const foreign = operation('unknown', 3, 'cd'.repeat(16));
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(retryView()))
      .mockResolvedValueOnce(
        Response.json(
          {
            code: 'source_or_operation_conflict',
            result: { operation: foreign, effective: 'unknown' },
          },
          { status: 409 }
        )
      );
    await mount(transport);
    await change('3');
    await click('retry-save');
    expect(renderer.root.findByProps({ 'data-testid': 'retry-operation-id' }).props.children).toBe(
      'ab'.repeat(16)
    );
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retryOperationNoPutReplay'));
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
  });

  it('checks and reconciles the original unknown operation, then reloads the actual recovered value', async () => {
    const recovered = operation('recovered', 3);
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValueOnce(Response.json(retryView({ value: 1 })))
      .mockResolvedValueOnce(operationResponse(operation('unknown', 3)))
      .mockResolvedValueOnce(operationResponse(operation('unknown', 3)))
      .mockResolvedValueOnce(operationResponse(recovered))
      .mockResolvedValueOnce(Response.json(retryView({ value: 7, operation: recovered })));
    await mount(transport);
    await change('3');
    await click('retry-save');
    await click('retry-check-operation');
    await click('retry-reconcile');
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'PUT')).toHaveLength(1);
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1);
    expect(input().props.value).toBe('7');
    expect(JSON.stringify(renderer.toJSON())).toContain(t('retryRecovered'));
    expect(pendingEvents[pendingEvents.length - 1]).toBe(false);
  });
});
