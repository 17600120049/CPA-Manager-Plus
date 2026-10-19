import { afterEach, expect, it, vi } from 'vitest';

const { render } = vi.hoisted(() => ({ render: vi.fn() }));
vi.mock('react-dom/client', () => ({ createRoot: () => ({ render }) }));
vi.mock('react-router-dom', () => ({ createBrowserRouter: vi.fn(), RouterProvider: () => null }));
afterEach(() => {
  vi.unstubAllGlobals();
  vi.resetModules();
  render.mockClear();
});

it('consumes a new host Handoff in an already-open Bootstrap tab and retires its previous in-memory transport', async () => {
  const location = new URL('https://manager.example/.well-known/cpamp-bootstrap/ui');
  let hashChanged!: () => void;
  vi.stubGlobal('window', {
    location,
    history: {
      replaceState: (_: unknown, _title: string, path: string) => {
        location.href = new URL(path, location.origin).href;
      },
    },
    addEventListener: (name: string, handler: () => void) => {
      if (name === 'hashchange') hashChanged = handler;
    },
  });
  vi.stubGlobal('document', { getElementById: () => null });
  await import('./main');
  const first = render.mock.calls[0][0];
  expect(first.props.handoff).toBeNull();
  const clear = vi.spyOn(first.props.transport, 'clear');
  const handoff = {
    identity: {
      admissionId: 'admission',
      installationId: 'installation',
      generation: 'generation',
      operationId: 'fresh-op',
      databasePath: '/data/usage.sqlite',
    },
    claimGeneration: 2,
    token: 'x'.repeat(43),
    resume: true,
  };
  location.hash =
    '#handoff=' +
    btoa(JSON.stringify(handoff)).replace(/=/g, '').replace(/\+/g, '-').replace(/\//g, '_');
  hashChanged();
  const second = render.mock.calls[1][0];
  expect(clear).toHaveBeenCalledOnce();
  expect(location.hash).toBe('');
  expect(second.key).not.toBe(first.key);
  expect(second.props.transport).not.toBe(first.props.transport);
  expect(second.props.handoff).toEqual(handoff);
});
