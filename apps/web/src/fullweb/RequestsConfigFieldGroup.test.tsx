import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import i18n from '../i18n';
import { configurationText as c } from '../i18n/configurationWorkspace';
import { gatewaySections } from './gatewayCatalog';
import { RequestsConfigFieldGroup } from './RequestsConfigFieldGroup';
import { PublicApiError, type FullWebCredential, type PublicApiClient } from './publicApi';

const KEEPALIVE = 'requests.streaming.keepalive-seconds';
const BOOTSTRAP = 'requests.streaming.bootstrap-retries';
const HEADERS = 'requests.passthrough-headers';
const credential: FullWebCredential = { kind: 'admin', value: 'test-credential' };
const CATALOG_PATHS = [KEEPALIVE, BOOTSTRAP, HEADERS, 'requests.nonstream-keepalive-interval'];

type Operation = { state: string; requestId?: string; code?: string };

// gatewayFieldServer models the Manager surface for one requests group: a value
// and one CAS revision per field, plus the original operation of the last write.
function gatewayFieldServer(paths: string[], initial: Record<string, unknown>) {
  const values: Record<string, unknown> = { ...initial };
  const revision: Record<string, number> = Object.fromEntries(paths.map((path) => [path, 2]));
  const state: Record<string, Operation> = {};
  const reason: Record<string, string> = {};
  const gets: Record<string, number> = {};
  const puts: Record<string, number> = {};
  const posts: Record<string, number> = {};
  // The global record revision an older Manager sent back to every field.
  let recordRevision = 2;
  let conflicts = 0;
  const pathOf = (resource: string) => resource.split('/').slice(2).join('.');
  const fieldRevision = (path: string) => revision[path];
  const view = (path: string) => ({
    field: path,
    presence: values[path] === undefined ? 'missing' : 'present',
    value: values[path],
    source: 'isolated-test',
    authority: 'host-runtime',
    editable: true,
    reason: reason[path] ?? '',
    persisted: 'persisted',
    effective: 'unknown',
    expected: { sourceId: 'install-1', generation: '9', revision: fieldRevision(path), sha256: '' },
    ...(state[path] ? { operation: state[path] } : {}),
  });
  let gate: Promise<void> = Promise.resolve();
  let openGate: () => void = () => undefined;
  const client: PublicApiClient = {
    get: async <T,>(resource: string, _credential?: FullWebCredential) => {
      const path = pathOf(resource);
      gets[path] = (gets[path] ?? 0) + 1;
      return view(path) as T;
    },
    put: async <T,>(resource: string, body: unknown, _credential: FullWebCredential) => {
      const path = pathOf(resource);
      const payload = body as { requestId: string; value: unknown; expected: { revision: number } };
      puts[path] = (puts[path] ?? 0) + 1;
      await gate;
      if (payload.expected.revision !== fieldRevision(path)) {
        conflicts++;
        throw new PublicApiError('http', 409, undefined, 'source_or_operation_conflict');
      }
      values[path] = payload.value;
      recordRevision++;
      revision[path] = recordRevision;
      void recordRevision;
      state[path] = { state: 'persisted', requestId: payload.requestId };
      return { operation: state[path] } as T;
    },
    post: async <T,>(resource: string, _body: unknown, _credential: FullWebCredential) => {
      const request = resource.split('/')[2];
      const path = Object.keys(state).find((key) => state[key].requestId === request) ?? KEEPALIVE;
      posts[path] = (posts[path] ?? 0) + 1;
      state[path] = { state: 'persisted', requestId: request };
      return {} as T;
    },
  };
  return {
    client,
    values,
    gets,
    puts,
    posts,
    state,
    reason,
    view,
    // stale re-reads the field without changing it: another writer moved its
    // revision, so the expectation one page load captured no longer holds.
    stale(path: string) {
      recordRevision++;
      revision[path] = recordRevision;
    },
    conflicts: () => conflicts,
    // The project's TS lib predates Promise.withResolvers, so the gate uses the
    // executor form.
    holdWrites() {
      let open: (() => void) | null = null;
      gate = new Promise<void>((resolve) => {
        open = () => resolve();
      });
      openGate = () => open?.();
    },
    releaseWrites() {
      openGate();
    },
  };
}

describe('RequestsConfigFieldGroup', () => {
  let renderer: ReactTestRenderer;
  let catalog: ReturnType<typeof gatewaySections>[number]['fields'];
  let all: Set<string>;
  const onUnauthorized = vi.fn();
  const onPending = vi.fn();
  const onDraftChange = vi.fn();

  beforeEach(async () => {
    await i18n.changeLanguage('en');
    vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
    vi.stubGlobal('crypto', { getRandomValues: (bytes: Uint8Array) => bytes.fill(0xcc) });
    onUnauthorized.mockClear();
    onPending.mockClear();
    onDraftChange.mockClear();
    // One stable catalog, exactly like the memoized section in the workspace.
    catalog = gatewaySections().find((section) => section.id === 'requests')?.fields ?? [];
    all = new Set(catalog.map((field) => field.path));
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    vi.unstubAllGlobals();
  });

  const element = (client: PublicApiClient, visible: ReadonlySet<string> = all) => (
    <RequestsConfigFieldGroup
      fields={catalog}
      visiblePaths={visible}
      client={client}
      credential={credential.value}
      onUnauthorized={onUnauthorized}
      onPending={onPending}
      onDraftChange={onDraftChange}
    />
  );
  const mount = async (client: PublicApiClient, visible: ReadonlySet<string> = all) => {
    await act(async () => {
      renderer = create(element(client, visible));
    });
  };
  const node = (id: string) => renderer.root.findByProps({ 'data-testid': id });
  const has = (id: string) => renderer.root.findAllByProps({ 'data-testid': id }).length > 0;
  const text = () => JSON.stringify(renderer.toJSON());
  const change = async (id: string, value: string) => {
    await act(async () => node(id).props.onChange({ target: { value } }));
  };
  const click = async (id: string) => {
    await act(async () => node(id).props.onClick());
  };
  const diff = (path: string) =>
    [...onDraftChange.mock.calls].reverse().flatMap(([diffs]) => diffs).find((item) => item.path === path);

  it('renders only the visible fields and reports every editable draft once', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    await mount(server.client);
    expect(has(`config-field-${KEEPALIVE}`)).toBe(true);
    expect(has(`config-field-${BOOTSTRAP}`)).toBe(true);
    expect(diff(KEEPALIVE)).toMatchObject({ dirty: false, newVal: '2' });
    // Visibility is a render concern: a hidden field keeps its own read.
    await act(async () => renderer.update(element(server.client, new Set([KEEPALIVE]))));
    expect(has(`config-field-${KEEPALIVE}`)).toBe(true);
    expect(has(`config-field-${BOOTSTRAP}`)).toBe(false);
    expect(server.gets[BOOTSTRAP]).toBe(1);
  });

  it('keeps a draft the user edited and never re-reads it on a catalog change', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    await mount(server.client);
    const reads = server.gets[KEEPALIVE];
    await change(`config-input-${KEEPALIVE}`, '45');
    expect(node(`config-input-${KEEPALIVE}`).props.value).toBe('45');
    expect(diff(KEEPALIVE)).toMatchObject({ dirty: true, oldVal: '2', newVal: '45' });
    // A re-render with a new visibility set is not a reload of the drafts.
    await act(async () => renderer.update(element(server.client)));
    expect(node(`config-input-${KEEPALIVE}`).props.value).toBe('45');
    expect(server.gets[KEEPALIVE]).toBe(reads);
  });

  it('saves, re-reads the field and reports the saved draft as unchanged', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    await mount(server.client);
    await change(`config-input-${KEEPALIVE}`, '20');
    await click(`config-save-${KEEPALIVE}`);
    expect(server.puts[KEEPALIVE]).toBe(1);
    expect(server.values[KEEPALIVE]).toBe(20);
    expect(server.gets[KEEPALIVE]).toBeGreaterThan(1);
    expect(diff(KEEPALIVE)).toMatchObject({ dirty: false, oldVal: '20', newVal: '20' });
    expect(onPending.mock.calls).toContainEqual([true]);
    expect(onPending).toHaveBeenLastCalledWith(false);
  });

  it('treats the saved number as the same value as its string draft', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 45, [BOOTSTRAP]: 0, [HEADERS]: false });
    await mount(server.client);
    await change(`config-input-${KEEPALIVE}`, '45');
    expect(diff(KEEPALIVE)).toMatchObject({ dirty: false, newVal: '45', oldVal: '45' });
    // Every dirty report reached the workspace once, and none of them was dirty.
    expect([...onDraftChange.mock.calls].flatMap(([diffs]) => diffs).filter((d) => d.dirty)).toEqual([]);
  });

  it('locks an unknown operation, offers reconcile, and unlocks after it is resolved', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    server.state[KEEPALIVE] = { state: 'unknown', requestId: 'a'.repeat(32) };
    await mount(server.client);
    expect(node(`config-input-${KEEPALIVE}`).props.disabled).toBe(true);
    expect(node(`config-save-${KEEPALIVE}`).props.disabled).toBe(true);
    expect(has(`config-reconcile-${KEEPALIVE}`)).toBe(true);
    expect(text()).toContain(c('fieldOperationUnknown'));
    await click(`config-reconcile-${KEEPALIVE}`);
    expect(server.posts[KEEPALIVE]).toBe(1);
    expect(node(`config-input-${KEEPALIVE}`).props.disabled).toBe(false);
    expect(has(`config-reconcile-${KEEPALIVE}`)).toBe(false);
  });

  it('shows a terminal failure as a definite outcome instead of an unknown', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    server.state[KEEPALIVE] = { state: 'failed', requestId: 'b'.repeat(32), code: 'write_not_applied' };
    await mount(server.client);
    expect(text()).toContain(c('fieldWriteNotApplied'));
    expect(text()).not.toContain('write_not_applied');
    expect(node(`config-input-${KEEPALIVE}`).props.disabled).toBe(false);
  });

  it('disables the control while a save is pending and re-reads the answer', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    await mount(server.client);
    await change(`config-input-${KEEPALIVE}`, '30');
    server.holdWrites();
    let saving: Promise<void> = Promise.resolve();
    await act(async () => {
      saving = node(`config-save-${KEEPALIVE}`).props.onClick();
    });
    expect(onPending).toHaveBeenLastCalledWith(true);
    expect(node(`config-save-${KEEPALIVE}`).props.disabled).toBe(true);
    await act(async () => {
      server.releaseWrites();
      await saving;
    });
    expect(onPending).toHaveBeenLastCalledWith(false);
    expect(server.values[KEEPALIVE]).toBe(30);
    expect(diff(KEEPALIVE)).toMatchObject({ dirty: false, newVal: '30' });
  });

  it('saves two fields observed by one page load without a conflict', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    await mount(server.client);
    await change(`config-input-${KEEPALIVE}`, '20');
    await click(`config-save-${KEEPALIVE}`);
    await change(`config-input-${BOOTSTRAP}`, '3');
    await click(`config-save-${BOOTSTRAP}`);
    expect(server.conflicts()).toBe(0);
    expect(server.values[BOOTSTRAP]).toBe(3);
    expect(server.puts[BOOTSTRAP]).toBe(1);
  });

  it('repairs stale expectations for the whole group after a conflict', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    await mount(server.client);
    // Another writer moves this field's revision, so the page-load expectation
    // the group captured is stale before the save.
    server.stale(BOOTSTRAP);
    await change(`config-input-${BOOTSTRAP}`, '4');
    const stale = [...onDraftChange.mock.calls];
    expect(stale.length).toBeGreaterThan(0);
    await click(`config-save-${BOOTSTRAP}`);
    expect(server.conflicts()).toBe(1);
    // The 409 re-read every field, so the same draft saves without a reload.
    expect(server.gets[KEEPALIVE]).toBeGreaterThan(1);
    expect(server.gets[HEADERS]).toBeGreaterThan(1);
    expect(node(`config-input-${BOOTSTRAP}`).props.value).toBe('4');
    await click(`config-save-${BOOTSTRAP}`);
    expect(server.conflicts()).toBe(1);
    expect(server.values[BOOTSTRAP]).toBe(4);
  });

  it('renders a translated reason instead of the raw server code', async () => {
    const server = gatewayFieldServer(CATALOG_PATHS, { [KEEPALIVE]: 2, [BOOTSTRAP]: 0, [HEADERS]: false });
    server.reason[KEEPALIVE] = 'original_operation_or_source_requires_reconciliation';
    await mount(server.client);
    expect(text()).toContain(c('fieldReasonReconcile'));
    expect(text()).not.toContain('original_operation_or_source_requires_reconciliation');
  });
});
