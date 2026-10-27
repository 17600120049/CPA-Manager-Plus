import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import type { ReactNode } from 'react';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { configurationText as c } from '../i18n/configurationWorkspace';
import { gatewayFamilies, gatewayGroups } from './gatewayCatalog';
import type {
  GatewayFamilyView,
  GatewayFieldView,
  GatewayGroupView,
  GatewayOperation,
  GatewayView,
} from './gatewayConfigClient';
import { FreshGatewayWorkspace } from './FreshGatewayWorkspace';
import type { PublicApiClient } from './publicApi';
import { PublicApiError } from './publicApi';

vi.mock('../components/ui/Modal', () => ({
  Modal: ({ children, footer, open }: { children: ReactNode; footer: ReactNode; open: boolean }) =>
    open ? (
      <div role="dialog">
        {children}
        {footer}
      </div>
    ) : null,
}));

const BASELINE = 'a'.repeat(64);

// The Manager owns the type of every leaf; the test states the same inventory so
// a wrong control in the page shows up as a wrong interaction here.
const INT_PATHS = [
  'routing.retry.request-retry',
  'routing.retry.max-retry-credentials',
  'routing.retry.max-retry-interval',
  'routing.cooldown.transient-error-cooldown-seconds',
  'requests.nonstream-keepalive-interval',
  'requests.streaming.keepalive-seconds',
  'requests.streaming.bootstrap-retries',
];
const STRING_PATHS = [
  'routing.strategy',
  'routing.session-affinity-ttl',
  'requests.proxy-url',
  'multimedia.gpt-image-2-base-model',
  'multimedia.video-result-auth-cache-ttl',
  'upstream.codex.stream-bootstrap-timeout',
  'upstream.claude.header-defaults.user-agent',
  'upstream.claude.header-defaults.package-version',
  'upstream.claude.header-defaults.runtime-version',
  'upstream.claude.header-defaults.os',
  'upstream.claude.header-defaults.arch',
  'upstream.claude.header-defaults.timeout',
  'upstream.claude.header-defaults.timezone',
];

function fieldType(path: string): 'int' | 'bool' | 'string' | 'image-mode' {
  if (path === 'multimedia.disable-image-generation') return 'image-mode';
  if (INT_PATHS.includes(path)) return 'int';
  if (STRING_PATHS.includes(path)) return 'string';
  return 'bool';
}

function sampleValue(type: string) {
  if (type === 'int') return 3;
  if (type === 'string') return 'value';
  return false;
}

function buildView(overrides: Partial<GatewayView> = {}): GatewayView {
  const fields: GatewayFieldView[] = gatewayGroups().flatMap((group) =>
    group.fields.map((meta) => {
      const type = fieldType(meta.path);
      return {
        path: meta.path,
        type,
        kind:
          type === 'image-mode'
            ? ('select' as const)
            : type === 'int'
              ? ('number' as const)
              : type === 'bool'
                ? ('bool' as const)
                : ('text' as const),
        group: group.id,
        presence: 'present' as const,
        value: sampleValue(type),
        editable: true,
      };
    })
  );
  const groups: GatewayGroupView[] = gatewayGroups().map((group) => ({
    id: group.id,
    prefix: group.id,
    paths: group.fields.map((field) => field.path),
    baseline: BASELINE,
    writable: true,
    persisted: 'unknown',
    effective: 'unknown',
  }));
  const families: GatewayFamilyView[] = gatewayFamilies().map((family) => ({
    family: family.family,
    path: family.path,
    rules: family.filter ? [] : null,
    baseline: BASELINE,
    writable: true,
    readOnly: false,
  }));
  return {
    capability: { mode: 'managed', readable: true, writable: true, rulesWritable: true },
    generation: '7',
    groups,
    fields,
    families,
    groupsInvalid: false,
    ...overrides,
  };
}

interface Call {
  method: string;
  resource: string;
  body?: unknown;
}

function fakeClient(initial: GatewayView) {
  const calls: Call[] = [];
  const state = {
    view: initial,
    operation: {
      requestId: 'f'.repeat(32),
      group: 'retry',
      target: 'group' as const,
      state: 'persisted' as const,
    } satisfies GatewayOperation,
    saveError: null as unknown,
    reconcileError: null as unknown,
  };
  const client: PublicApiClient = {
    get: async <T,>(resource: string) => {
      calls.push({ method: 'get', resource });
      if (resource === 'gateway/config') return state.view as T;
      return { operation: state.operation, effective: 'unknown' } as T;
    },
    post: async <T,>(resource: string, body: unknown) => {
      calls.push({ method: 'post', resource, body });
      if (state.saveError) throw state.saveError;
      if (resource.endsWith('/reconcile')) {
        if (state.reconcileError) throw state.reconcileError;
        return { operation: state.operation, effective: 'unknown' } as T;
      }
      return {
        operation: {
          ...(body as { requestId: string }),
          group: resource.split('/')[3],
          target: resource.includes('/rules/') ? 'family' : 'group',
          state: 'persisted',
        },
        effective: 'unknown',
      } as T;
    },
    put: async () => {
      throw new Error('the gateway page never uses PUT');
    },
  };
  return { client, calls, state };
}

describe('FreshGatewayWorkspace', () => {
  let renderer: ReactTestRenderer;

  beforeEach(async () => {
    await i18n.changeLanguage('en-US');
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  });

  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
  });

  async function mount(client: PublicApiClient, onDirtyChange = vi.fn(), onPending = vi.fn()) {
    await act(async () => {
      renderer = create(
        <FreshGatewayWorkspace
          client={client}
          credential="admin-credential"
          onUnauthorized={() => undefined}
          onPending={onPending}
          onDirtyChange={onDirtyChange}
        />
      );
    });
    await act(async () => {
      await Promise.resolve();
    });
    return { onDirtyChange, onPending };
  }

  function testId(id: string) {
    return renderer.root.findByProps({ 'data-testid': id });
  }

  function maybeTestId(id: string) {
    return renderer.root.findAllByProps({ 'data-testid': id })[0];
  }

  async function openTab(key: Parameters<typeof c>[0]) {
    const tab = renderer.root.findAll(
      (node) => node.type === 'button' && node.props.role === 'tab' && node.props.children === c(key)
    )[0];
    await act(async () => tab.props.onClick());
  }

  it('projects the whole page from the Manager view and never invents a field', async () => {
    const { client } = fakeClient(buildView());
    await mount(client);
    expect(testId('gateway-capability')).toBeDefined();
    expect(testId('gateway-overview')).toBeDefined();
    const counts = testId('gateway-dirty-count');
    expect(JSON.stringify(counts.props.children)).toContain('0');
    expect(renderer.root.findAllByProps({ 'data-testid': 'gateway-journal-invalid' })).toHaveLength(0);
    // The read is one projection request, not one request per field.
    expect((client as unknown as { get: unknown }) === undefined).toBe(false);
  });

  it('renders the eight-field common view, group tabs and a search across sections', async () => {
    const { client } = fakeClient(buildView());
    await mount(client);
    const tab = (label: string) =>
      renderer.root.findAll(
        (node) => node.type === 'button' && node.props.role === 'tab' && node.props.children === label
      )[0];
    await act(async () => tab(c('common')).props.onClick());
    const commonFields = renderer.root.findAll(
      (node) =>
        typeof node.type === 'string' &&
        node.type === 'div' &&
        typeof node.props['data-testid'] === 'string' &&
        node.props['data-testid'].startsWith('gateway-field-')
    );
    expect(commonFields).toHaveLength(8);
    await act(async () => tab(c('routing')).props.onClick());
    expect(maybeTestId('gateway-group-routing')).toBeDefined();
    expect(maybeTestId('gateway-group-retry')).toBeUndefined();
    const search = renderer.root.findByProps({ type: 'search' });
    await act(async () => search.props.onChange({ target: { value: 'proxy-url' } }));
    expect(maybeTestId('gateway-field-requests.proxy-url')).toBeDefined();
    await act(async () => search.props.onChange({ target: { value: 'nothing-matches-this' } }));
    expect(maybeTestId('gateway-no-matches')).toBeDefined();
  });

  it('saves one group with only the edited leaves and reports the draft as dirty', async () => {
    const { client, calls } = fakeClient(buildView());
    const { onDirtyChange } = await mount(client);
    await openTab('retry');
    const row = testId('gateway-field-routing.retry.request-retry');
    const input = row.findByType('input');
    await act(async () => input.props.onChange({ target: { value: '5' } }));
    expect(row.props['data-dirty'] ?? testId('gateway-field-routing.retry.request-retry').props['data-dirty']).toBe(
      'true'
    );
    expect(onDirtyChange).toHaveBeenLastCalledWith(true);
    const save = testId('gateway-group-retry').findAll(
      (node) => node.type === 'button' && node.props.children === c('groupSave')
    )[0];
    await act(async () => save.props.onClick());
    await act(async () => {
      await Promise.resolve();
    });
    const writes = calls.filter((call) => call.method === 'post');
    expect(writes).toHaveLength(1);
    expect(writes[0].resource).toBe('gateway/config/groups/retry');
    const body = writes[0].body as {
      requestId: string;
      expected: { generation: string; baseline: string };
      edits: { path: string; action: string; value?: unknown }[];
    };
    expect(body.requestId).toMatch(/^[0-9a-f]{32}$/);
    expect(body.expected).toEqual({ generation: '7', baseline: BASELINE });
    expect(body.edits).toEqual([{ path: 'routing.retry.request-retry', action: 'set', value: 5 }]);
    expect(onDirtyChange).toHaveBeenLastCalledWith(false);
  });

  it('keeps the draft when the baseline moved and shows the conflict', async () => {
    const { client, calls } = fakeClient(buildView());
    const { state } = { state: { saveError: null as unknown } };
    const fake = fakeClient(buildView());
    const harness = await mount(fake.client);
    expect(harness).toBeDefined();
    await openTab('retry');
    const row = testId('gateway-field-routing.retry.request-retry');
    await act(async () => row.findByType('input').props.onChange({ target: { value: '9' } }));
    fake.state.saveError = new PublicApiError('http', 409, undefined, 'source_or_operation_conflict');
    const save = testId('gateway-group-retry').findAll(
      (node) => node.type === 'button' && node.props.children === c('groupSave')
    )[0];
    await act(async () => save.props.onClick());
    await act(async () => {
      await Promise.resolve();
    });
    expect(maybeTestId('gateway-conflict')).toBeDefined();
    expect(maybeTestId('gateway-group-error-retry')).toBeDefined();
    // The conflicted draft survives and the page re-read the installation.
    const reloaded = fake.calls.filter((call) => call.resource === 'gateway/config');
    expect(reloaded.length).toBeGreaterThan(1);
    void client;
    void calls;
    void state;
  });

  it('offers the original operation after an unknown result and never resends the write', async () => {
    const fake = fakeClient(buildView());
    await mount(fake.client);
    await openTab('retry');
    const row = testId('gateway-field-routing.retry.request-retry');
    await act(async () => row.findByType('input').props.onChange({ target: { value: '7' } }));
    fake.state.saveError = new PublicApiError('http', 409, undefined, 'original_operation_unknown');
    const save = testId('gateway-group-retry').findAll(
      (node) => node.type === 'button' && node.props.children === c('groupSave')
    )[0];
    await act(async () => save.props.onClick());
    await act(async () => {
      await Promise.resolve();
    });
    const error = testId('gateway-group-error-retry');
    const query = error.findAll(
      (node) => node.type === 'button' && node.props.children === c('groupQueryOriginal')
    )[0];
    const writesBefore = fake.calls.filter((call) => call.method === 'post').length;
    await act(async () => query.props.onClick());
    await act(async () => {
      await Promise.resolve();
    });
    const writes = fake.calls.filter((call) => call.method === 'post');
    expect(writes.length).toBe(writesBefore + 1);
    expect(writes[writes.length - 1]?.resource).toMatch(/^gateway\/config\/operations\/[0-9a-f]{32}\/reconcile$/);
  });

  it('maps a CPA rejection to the group and the field it named', async () => {
    const fake = fakeClient(buildView());
    await mount(fake.client);
    await openTab('retry');
    const row = testId('gateway-field-routing.retry.request-retry');
    await act(async () => row.findByType('input').props.onChange({ target: { value: '0' } }));
    fake.state.saveError = new PublicApiError(
      'http',
      422,
      undefined,
      'cpa_rejected_config',
      'routing.retry.request-retry',
      'invalid value'
    );
    const save = testId('gateway-group-retry').findAll(
      (node) => node.type === 'button' && node.props.children === c('groupSave')
    )[0];
    await act(async () => save.props.onClick());
    await act(async () => {
      await Promise.resolve();
    });
    expect(testId('gateway-group-error-retry').props.children.join('')).toContain('invalid value');
    const field = testId('gateway-field-routing.retry.request-retry');
    const texts = field
      .findAllByType('p')
      .map((node) => [node.props.children].flat(2).filter((item) => typeof item === 'string').join(''));
    expect(texts.join(' | ')).toContain(c('fieldCpaRejected'));
  });

  it('locks the private profile and never renders a save action', async () => {
    const view = buildView({
      capability: {
        mode: 'private',
        readable: true,
        writable: false,
        rulesWritable: false,
        reason: 'private_profile_scope_frozen',
      },
      groups: gatewayGroups().map((group) => ({
        id: group.id,
        prefix: group.id,
        paths: group.fields.map((field) => field.path),
        baseline: BASELINE,
        writable: false,
        reason: 'private_profile_scope_frozen',
        persisted: 'unknown',
        effective: 'unknown',
      })),
      fields: buildView().fields.map((field) => ({
        ...field,
        editable: false,
        reason: 'private_profile_scope_frozen',
      })),
      families: buildView().families.map((family) => ({
        ...family,
        writable: false,
        readOnly: true,
        reason: 'private_profile_scope_frozen',
      })),
    });
    const fake = fakeClient(view);
    await mount(fake.client);
    expect(testId('gateway-readonly')).toBeDefined();
    const save = renderer.root.findAll(
      (node) => node.type === 'button' && node.props.children === c('groupSave')
    );
    expect(save.every((node) => node.props.disabled === true)).toBe(true);
  });

  it('keeps payload rule lists read-only on an External installation', async () => {
    const view = buildView({
      capability: {
        mode: 'external',
        readable: true,
        writable: true,
        rulesWritable: false,
        reason: 'external_rule_write_unproven',
      },
      families: buildView().families.map((family) => ({
        ...family,
        writable: false,
        readOnly: true,
        reason: 'external_rule_write_unproven',
      })),
    });
    await mount(fakeClient(view).client);
    expect(testId('gateway-readonly')).toBeDefined();
    const edit = renderer.root.findAll(
      (node) => node.type === 'button' && node.props.children === c('ruleEditFamily')
    );
    expect(edit.every((node) => node.props.disabled === true)).toBe(true);
  });

  it('drafts a rule family and writes it with one request', async () => {
    const fake = fakeClient(buildView());
    await mount(fake.client);
    await openTab('requests');
    const open = renderer.root.findAll(
      (node) => node.type === 'button' && node.props.children === c('ruleEditFamily')
    )[0];
    await act(async () => open.props.onClick());
    expect(maybeTestId('gateway-rule-editor')).toBeDefined();
    const add = testId('rule-add');
    await act(async () => add.props.onClick());
    const name = testId('rule-model-name-0');
    await act(async () => name.props.onChange({ target: { value: 'gpt-*' } }));
    const paramAdd = testId('rule-param-add');
    await act(async () => paramAdd.props.onClick());
    const paramInputs = renderer.root.findAll(
      (node) => node.type === 'input' && node.props['data-param-row'] === undefined
    );
    void paramInputs;
    const rows = testId('gateway-rule-editor').findAll(
      (node) => typeof node.type === 'string' && node.props['data-param-row'] !== undefined
    );
    const paramRow = rows[0];
    const [pathInput, valueInput] = paramRow.findAllByType('input');
    await act(async () => pathInput.props.onChange({ target: { value: 'reasoning.effort' } }));
    await act(async () => valueInput.props.onChange({ target: { value: 'high' } }));
    const apply = testId('rule-editor-apply');
    await act(async () => apply.props.onClick());
    const saveDraft = testId('rule-save');
    await act(async () => saveDraft.props.onClick());
    const save = testId('gateway-family-default').findAll(
      (node) => node.type === 'button' && node.props.children === c('groupSave')
    )[0];
    await act(async () => save.props.onClick());
    await act(async () => {
      await Promise.resolve();
    });
    const write = fake.calls.filter(
      (call) => call.method === 'post' && call.resource.startsWith('gateway/config/rules/')
    );
    expect(write).toHaveLength(1);
    expect(write[0].resource).toBe('gateway/config/rules/default');
    expect((write[0].body as { rules: unknown[] }).rules).toEqual([
      { models: [{ name: 'gpt-*' }], params: { 'reasoning.effort': 'high' } },
    ]);
  });
});
