import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { FreshGatewayWorkspace } from './FreshGatewayWorkspace';
import { createPublicApiClient } from './publicApi';

const context = { basePath: '/console', apiBase: '/console/api/v2', revision: 1 };

describe('FreshGatewayWorkspace component', () => {
  let renderer: ReactTestRenderer;
  const onUnauthorized = vi.fn();
  const onPending = vi.fn();

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    vi.stubGlobal('window', { location: new URL('https://manager.example/console') });
    vi.stubGlobal('crypto', { getRandomValues: (bytes: Uint8Array) => bytes.fill(0xcc) });
    onUnauthorized.mockClear();
    onPending.mockClear();
  });

  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    vi.unstubAllGlobals();
  });

  async function mount(transport: ReturnType<typeof vi.fn<typeof fetch>>) {
    await act(async () => {
      renderer = create(
        <MemoryRouter initialEntries={['/console/gateway']}>
          <FreshGatewayWorkspace
            client={createPublicApiClient(() => context, transport)}
            credential="test-credential"
            onUnauthorized={onUnauthorized}
            onPending={onPending}
          />
        </MemoryRouter>
      );
    });
  }

  function mockDefaultTransport() {
    return vi.fn<typeof fetch>().mockImplementation((url) => {
      const isInterval = String(url).includes('max-retry-interval');
      return Promise.resolve(
        Response.json({
          field: isInterval
            ? 'routing.retry.max-retry-interval'
            : 'routing.retry.request-retry',
          presence: 'present',
          value: isInterval ? 30 : 2,
          source: 'private-embedded-cpa-v8.0.13',
          authority: 'host-runtime',
          editable: true,
          reason: '',
          persisted: 'persisted',
          effective: 'unknown',
          concurrentProtection: 'host-exclusive-runtime-reservation',
          expected: {
            sourceId: 'isolated-source-1234',
            generation: '7',
            revision: 2,
            sha256: 'a'.repeat(64),
          },
        })
      );
    });
  }

  it('renders overview tab with catalog stats, search bar and owner cards', async () => {
    const transport = mockDefaultTransport();
    await mount(transport);

    expect(
      renderer.root.findByProps({ 'data-testid': 'gateway-workspace-title' })
    ).toBeDefined();
    expect(
      renderer.root.findByProps({ 'data-testid': 'gateway-overview-content' })
    ).toBeDefined();

    // Toolbar
    const searchInput = renderer.root.findByProps({
      'data-testid': 'gateway-search-input',
    });
    expect(searchInput.props.value).toBe('');

    // Owner card interaction
    const viewOwnerBtn = renderer.root.findByProps({
      'data-testid': 'view-owner-0',
    });
    await act(async () => viewOwnerBtn.props.onClick());

    const modal = renderer.root.findByProps({
      'data-testid': 'owner-detail-modal',
    });
    expect(modal).toBeDefined();

    // Close owner modal
    const closeBtn = modal.findByProps({ children: '关闭' });
    await act(async () => closeBtn.props.onClick());
    expect(
      renderer.root.findAllByProps({ 'data-testid': 'owner-detail-modal' })
    ).toHaveLength(0);
  });

  it('switches between tabs and displays matching sections', async () => {
    const transport = mockDefaultTransport();
    await mount(transport);

    // Switch to routing tab
    const routingTab = renderer.root.findByProps({
      'data-testid': 'gateway-tab-routing',
    });
    await act(async () => routingTab.props.onClick());

    expect(
      renderer.root.findByProps({ 'data-testid': 'section-routing' })
    ).toBeDefined();

    // Switch to retry tab
    const retryTab = renderer.root.findByProps({
      'data-testid': 'gateway-tab-retry',
    });
    await act(async () => retryTab.props.onClick());

    expect(
      renderer.root.findByProps({ 'data-testid': 'fresh-managed-retry' })
    ).toBeDefined();
    expect(
      renderer.root.findByProps({ 'data-testid': 'retry-editor' })
    ).toBeDefined();
    expect(
      renderer.root.findByProps({ 'data-testid': 'retry-interval-editor' })
    ).toBeDefined();
  });

  it('filters fields using search input and handles diff modal and discard', async () => {
    const transport = mockDefaultTransport();
    await mount(transport);
    await act(async () => {});

    // Switch to retry tab
    const retryTab = renderer.root.findByProps({
      'data-testid': 'gateway-tab-retry',
    });
    await act(async () => retryTab.props.onClick());
    await act(async () => {});

    // Modify a retry draft
    const input = renderer.root.findByProps({ 'data-testid': 'retry-value' });
    await act(async () => input.props.onChange({ target: { value: '4' } }));

    // Dirty count should be updated
    const dirtyBadge = renderer.root.findByProps({
      'data-testid': 'gateway-dirty-count',
    });
    expect(dirtyBadge.props.children).toBe('1 项未保存');

    // View changes (Diff modal)
    const viewChangesBtn = renderer.root.findByProps({
      'data-testid': 'view-changes-btn',
    });
    expect(viewChangesBtn.props.disabled).toBe(false);

    await act(async () => viewChangesBtn.props.onClick());
    const diffModal = renderer.root.findByProps({
      'data-testid': 'diff-modal',
    });
    expect(diffModal).toBeDefined();

    // Close diff modal
    const closeDiffBtn = diffModal.findByProps({ children: '返回编辑' });
    await act(async () => closeDiffBtn.props.onClick());
    expect(
      renderer.root.findAllByProps({ 'data-testid': 'diff-modal' })
    ).toHaveLength(0);

    // Search for a keyword
    const searchInput = renderer.root.findByProps({
      'data-testid': 'gateway-search-input',
    });
    await act(async () =>
      searchInput.props.onChange({ target: { value: 'proxy' } })
    );

    // Discard drafts
    const discardBtn = renderer.root.findByProps({
      'data-testid': 'discard-drafts-btn',
    });
    await act(async () => discardBtn.props.onClick());

    const discardModal = renderer.root.findByProps({
      'data-testid': 'discard-modal',
    });
    expect(discardModal).toBeDefined();

    const confirmDiscardBtn = discardModal.findByProps({
      'data-testid': 'confirm-discard-btn',
    });
    await act(async () => confirmDiscardBtn.props.onClick());

    // Input should be reset
    expect(input.props.value).toBe('2');
  });
});
