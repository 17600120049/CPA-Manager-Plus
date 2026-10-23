import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { MemoryRouter } from 'react-router-dom';
import { afterEach, beforeEach, describe, expect, it } from 'vitest';
import { FreshSystemWorkspace } from './FreshSystemWorkspace';

describe('FreshSystemWorkspace component', () => {
  let renderer: ReactTestRenderer;

  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
  });

  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
  });

  async function mount(basePath = '/console') {
    await act(async () => {
      renderer = create(
        <MemoryRouter initialEntries={['/console/system']}>
          <FreshSystemWorkspace basePath={basePath} />
        </MemoryRouter>
      );
    });
  }

  it('renders system workspace cards with base path and security status', async () => {
    await mount('/custom-console');

    // Title and cards
    expect(
      renderer.root.findByProps({ 'data-testid': 'fresh-system-workspace' })
    ).toBeDefined();
    expect(
      renderer.root.findByProps({ 'data-testid': 'system-entry-card' })
    ).toBeDefined();
    expect(
      renderer.root.findByProps({ 'data-testid': 'system-startup-card' })
    ).toBeDefined();
    expect(
      renderer.root.findByProps({ 'data-testid': 'system-security-card' })
    ).toBeDefined();
    expect(
      renderer.root.findByProps({ 'data-testid': 'system-ownership-card' })
    ).toBeDefined();

    // Entry path
    const pathCode = renderer.root.findByProps({
      'data-testid': 'system-entry-path',
    });
    expect(pathCode.findByType('code').props.children).toBe('/custom-console');
  });

  it('opens and closes the offline recovery modal', async () => {
    await mount();

    const viewModalBtn = renderer.root.findByProps({
      'data-testid': 'view-offline-recovery-btn',
    });
    await act(async () => viewModalBtn.props.onClick());

    const recoveryModal = renderer.root.findByProps({
      'data-testid': 'recovery-modal',
    });
    expect(recoveryModal).toBeDefined();

    // Close modal
    const closeBtn = recoveryModal.findByProps({ children: '已知悉' });
    await act(async () => closeBtn.props.onClick());

    expect(
      renderer.root.findAllByProps({ 'data-testid': 'recovery-modal' })
    ).toHaveLength(0);
  });
});
