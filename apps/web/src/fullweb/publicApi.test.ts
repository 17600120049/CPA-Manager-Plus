import { afterEach, describe, expect, it, vi } from 'vitest';
import { createPublicApiClient, type FullWebCredential } from './publicApi';
import { resolveRoutingContext, type RoutingContext } from './routingContext';

const context = resolveRoutingContext({
  state: 'verified_effective',
  verifiedEffectiveControlBasePath: '/phase4-test',
  revision: 1,
})!;
const credential: FullWebCredential = { kind: 'admin', value: 'full-web-admin-secret' };

function browserOrigin(origin = 'https://manager.example:18317') {
  vi.stubGlobal('window', { location: new URL(origin) });
  vi.stubGlobal('localStorage', {
    getItem: () => {
      throw new Error('Full Web cannot read persisted CPA state');
    },
  });
}

afterEach(() => vi.unstubAllGlobals());

describe('canonical Full Web Public API transport', () => {
  it('uses the same canonical non-root base and explicit in-memory credential', async () => {
    browserOrigin();
    const transport = vi.fn<typeof fetch>().mockResolvedValue(Response.json({ technical: true }));
    const client = createPublicApiClient(() => context, transport);
    expect(await client.get('technical', credential)).toEqual({ technical: true });
    expect(transport).toHaveBeenCalledWith(
      'https://manager.example:18317/phase4-test/api/v2/technical',
      {
        method: 'GET',
        headers: { Accept: 'application/json', Authorization: 'Bearer full-web-admin-secret' },
        mode: 'same-origin',
        credentials: 'omit',
        redirect: 'error',
      }
    );
  });

  it('does not inherit CPA keys or connection settings for unauthenticated requests', async () => {
    browserOrigin();
    const transport = vi.fn<typeof fetch>().mockResolvedValue(Response.json({}));
    await createPublicApiClient(() => context, transport).get('technical');
    expect(transport.mock.calls[0][1]?.headers).toEqual({ Accept: 'application/json' });
  });

  it.each([
    '',
    '/',
    '/v1/models',
    '/v0/management',
    '/api/v2',
    '/favicon.ico',
    '/apple-touch-icon.png',
    '//external.example/path',
    'https://external.example/path',
    'http://cpa:8317/v0/management',
    'http://cpa:9081',
    '../models',
    'v0/../management',
    'technical?target=/v0/management',
    'technical#x',
    'technical%2f..',
    'technical\\x',
  ])('rejects a non-resource destination %j before adding credentials', async (resource) => {
    browserOrigin();
    const transport = vi.fn<typeof fetch>();
    await expect(
      createPublicApiClient(() => context, transport).get(resource, credential)
    ).rejects.toMatchObject({ kind: 'routing_unavailable' });
    expect(transport).not.toHaveBeenCalled();
  });

  it.each([
    null,
    { ...context, apiBase: '/v0/management' },
    { ...context, apiBase: 'https://evil/api/v2' },
    { ...context, basePath: '//external.example' },
    { ...context, revision: 0 },
  ])('fails closed for unavailable or inconsistent context %j', async (value) => {
    browserOrigin();
    const transport = vi.fn<typeof fetch>();
    await expect(
      createPublicApiClient(() => value as RoutingContext | null, transport).get(
        'technical',
        credential
      )
    ).rejects.toMatchObject({ kind: 'routing_unavailable' });
    expect(transport).not.toHaveBeenCalled();
  });

  it.each(['http://localhost:8317', 'http://localhost:9081', 'file:///tmp/index.html'])(
    'never sends Full Web credentials to CPA/internal/non-HTTP contexts %s',
    async (origin) => {
      browserOrigin(origin);
      const transport = vi.fn<typeof fetch>();
      await expect(
        createPublicApiClient(() => context, transport).get('technical', credential)
      ).rejects.toMatchObject({ kind: 'routing_unavailable' });
      expect(transport).not.toHaveBeenCalled();
    }
  );

  it.each([
    [401, 'authentication'],
    [403, 'authentication'],
    [404, 'http'],
    [500, 'http'],
  ])('classifies HTTP %i as %s', async (status, kind) => {
    browserOrigin();
    const transport = vi
      .fn<typeof fetch>()
      .mockResolvedValue(new Response(null, { status: status as number }));
    await expect(
      createPublicApiClient(() => context, transport).get('technical')
    ).rejects.toMatchObject({ kind, status });
  });

  it('classifies network failure without exposing credential or transport error details', async () => {
    browserOrigin();
    const transport = vi.fn<typeof fetch>().mockRejectedValue(new Error(credential.value));
    await expect(
      createPublicApiClient(() => context, transport).get('technical', credential)
    ).rejects.toMatchObject({ kind: 'network', message: 'Public API network' });
  });

  it('does not accept an accidental HTML SPA response as API success', async () => {
    browserOrigin();
    const transport = vi.fn<typeof fetch>().mockResolvedValue(new Response('<html>carrier</html>'));
    await expect(
      createPublicApiClient(() => context, transport).get('technical')
    ).rejects.toMatchObject({ kind: 'network' });
  });
});
