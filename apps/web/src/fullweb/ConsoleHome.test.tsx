import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { ConsoleHome } from './ConsoleHome';
import { PublicApiError, type createPublicApiClient } from './publicApi';
import { setFullWebLanguage } from '../i18n/fullweb';
import type { FreshOperation } from './bootstrapTransport';

describe('Console home observations', () => {
  let renderer: ReactTestRenderer;
  const get = vi.fn();
  const unauthorized = vi.fn();
  const navigate = vi.fn();
  const target = {
    runtimeIdentity: 'embedded',
    runtimeGeneration: '17486582467214426535',
    activeGatewayArtifact: { artifactId: 'sha256:' + 'a'.repeat(64) },
    state: 'ready',
  };
  const operation = {
    productComplete: true,
    gatewayPending: true,
    strategy: {
      targetIdentity: target.runtimeIdentity,
      targetGeneration: target.runtimeGeneration,
      targetArtifactId: target.activeGatewayArtifact.artifactId,
    },
  } as FreshOperation;
  const props = () => ({
    client: { get } as unknown as ReturnType<typeof createPublicApiClient>,
    credential: 'test-administrator',
    operation,
    onUnauthorized: unauthorized,
    onNavigate: navigate,
    basePath: '/console',
    blocked: false,
    refreshEpoch: 0,
  });
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    setFullWebLanguage('en');
    get.mockReset();
    unauthorized.mockClear();
    navigate.mockClear();
  });
  afterEach(async () => {
    if (renderer) await act(async () => renderer.unmount());
    setFullWebLanguage('zh-CN');
  });
  const text = () => JSON.stringify(renderer.toJSON());
  it('uses the authenticated observation rather than installation completion to report runtime health', async () => {
    get.mockResolvedValue({ ...target, state: 'offline' });
    await act(async () => {
      renderer = create(<ConsoleHome {...props()} />);
    });
    expect(get).toHaveBeenCalledWith('installation/runtime', {
      kind: 'admin',
      value: 'test-administrator',
    });
    expect(
      renderer.root.findByProps({ 'data-testid': 'console-runtime-state' }).props.children
    ).toBe('Stopped');
    expect(text()).toContain('Installed');
    expect(text()).toContain('Not enabled');
    expect(text()).not.toContain('identity has changed');
  });
  it('does not present an unreadable or changed Runtime as a healthy saved destination', async () => {
    get
      .mockRejectedValueOnce(new Error('offline'))
      .mockResolvedValueOnce({ ...target, runtimeGeneration: '17486582467214426536' });
    await act(async () => {
      renderer = create(<ConsoleHome {...props()} />);
    });
    expect(text()).toContain('could not be read');
    expect(
      renderer.root.findByProps({ 'data-testid': 'console-runtime-state' }).props.children
    ).toBe('Unknown');
    await act(async () => renderer.update(<ConsoleHome {...props()} refreshEpoch={1} />));
    expect(text()).toContain('identity has changed');
    expect(unauthorized).not.toHaveBeenCalled();
  });
  it('ignores a late authentication failure after leaving the current session', async () => {
    let reject!: (error: unknown) => void;
    get.mockImplementation(
      () =>
        new Promise((_resolve, fail) => {
          reject = fail;
        })
    );
    await act(async () => {
      renderer = create(<ConsoleHome {...props()} />);
    });
    await act(async () => renderer.unmount());
    await act(async () => reject(new PublicApiError('authentication', 401)));
    expect(unauthorized).not.toHaveBeenCalled();
  });
  it('compares a safe numeric generation with the same checkpoint without a false identity conflict', async () => {
    get.mockResolvedValue({ ...target, runtimeGeneration: 3 });
    const same = { ...operation, strategy: { ...operation.strategy, targetGeneration: 3 } };
    await act(async () => {
      renderer = create(<ConsoleHome {...props()} operation={same} />);
    });
    expect(text()).not.toContain('identity has changed');
    expect(
      renderer.root.findByProps({ 'data-testid': 'console-runtime-state' }).props.children
    ).toBe('Running');
  });
});
