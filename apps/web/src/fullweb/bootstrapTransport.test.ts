import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import {
  BOOTSTRAP_UI,
  BootstrapTransportError,
  consumeHandoff,
  createBootstrapTransport,
  validateFreshAdminKey,
} from './bootstrapTransport';

const identity = {
  admissionId: 'admission-a',
  installationId: 'installation-a',
  generation: 'generation-a',
  databasePath: '/data/manager.sqlite',
  operationId: 'fresh-operation-a',
};
const operation = {
  identity,
  revision: 1,
  adminInitialized: false,
  gatewayPending: true,
  routing: { committed: false },
};
const handoff = {
  identity,
  claimGeneration: 1,
  token: 'claim-secret-012345678901234567890123456789',
  resume: false,
};

describe('bounded Bootstrap handoff and transport', () => {
  beforeEach(() => {
    const location = new URL('https://manager.example' + BOOTSTRAP_UI);
    vi.stubGlobal('window', {
      location,
      history: {
        replaceState: (_: unknown, _title: string, path: string) => {
          location.href = new URL(path, location.origin).href;
        },
      },
    });
    vi.stubGlobal('localStorage', {
      setItem: vi.fn(() => {
        throw new Error('secret persistence forbidden');
      }),
    });
    vi.stubGlobal('sessionStorage', {
      setItem: vi.fn(() => {
        throw new Error('secret persistence forbidden');
      }),
    });
  });
  afterEach(() => vi.unstubAllGlobals());

  it('removes even malformed fragments before decoding or transport', () => {
    const replaceState = vi.fn();
    expect(
      consumeHandoff(
        { pathname: BOOTSTRAP_UI, search: '', hash: '#handoff=broken' },
        { replaceState }
      )
    ).toBeNull();
    expect(replaceState).toHaveBeenCalledWith(null, '', BOOTSTRAP_UI);
    const encoded = btoa(JSON.stringify(handoff))
      .replace(/=/g, '')
      .replace(/\+/g, '-')
      .replace(/\//g, '_');
    expect(
      consumeHandoff(
        { pathname: BOOTSTRAP_UI, search: '', hash: '#handoff=' + encoded },
        { replaceState }
      )
    ).toEqual(handoff);
  });

  it('keeps claim out of request URL and session out of storage; uses only Fresh scheme', async () => {
    const setLocal = vi.spyOn(localStorage, 'setItem');
    const transport = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: 'session-secret', operation })))
      .mockResolvedValueOnce(new Response(JSON.stringify(operation)));
    const client = createBootstrapTransport(transport);
    expect(await client.start(handoff)).toEqual(operation);
    await client.request('operation');
    expect(transport.mock.calls[0][0]).toBe(
      window.location.origin + '/.well-known/cpamp-bootstrap/api/v2/claim/exchange'
    );
    expect(transport.mock.calls[0][0]).not.toContain(handoff.token);
    expect(transport.mock.calls[1][1]).toMatchObject({
      headers: { Authorization: 'Fresh session-secret' },
      credentials: 'omit',
      redirect: 'error',
      cache: 'no-store',
    });
    expect(setLocal).not.toHaveBeenCalled();
    expect(sessionStorage.setItem).not.toHaveBeenCalled();
    setLocal.mockRestore();
    client.clear();
    await expect(client.request('operation')).rejects.toThrow('authority unavailable');
  });

  it('resume uses only same-operation continuation, without exchanging a claim', async () => {
    const resumed = { ...operation, adminInitialized: true };
    const transport = vi.fn().mockResolvedValue(new Response(JSON.stringify(resumed)));
    const client = createBootstrapTransport(transport);
    expect(await client.start({ ...handoff, resume: true })).toEqual(resumed);
    expect(transport).toHaveBeenCalledTimes(1);
    expect(transport.mock.calls[0][0]).toContain('/operation');
  });

  it('rejects operation identity mismatch, API aliases and non-shell destinations before network', async () => {
    const transport = vi.fn().mockResolvedValue(
      new Response(
        JSON.stringify({
          token: 'session-secret',
          operation: { ...operation, identity: { ...identity, generation: 'other' } },
        })
      )
    );
    const client = createBootstrapTransport(transport);
    await expect(client.start(handoff)).rejects.toThrow('authority mismatch');
    await expect(client.request('../v1/models' as 'operation')).rejects.toThrow();
    window.history.replaceState(null, '', '/v1/models');
    await expect(client.request('operation')).rejects.toThrow();
    expect(transport).toHaveBeenCalledTimes(1);
  });

  it('does not reflect server errors or secrets and classifies HTTP status codes', async () => {
    const client403 = createBootstrapTransport(
      vi.fn().mockResolvedValue(new Response(handoff.token, { status: 403 }))
    );
    await expect(client403.start(handoff)).rejects.toMatchObject({
      message: 'Bootstrap request unavailable',
      kind: 'rejected',
      status: 403,
    });

    const client400 = createBootstrapTransport(
      vi
        .fn()
        .mockResolvedValueOnce(new Response(JSON.stringify({ token: 'session-secret', operation })))
        .mockResolvedValueOnce(new Response('bad key', { status: 400 }))
    );
    await client400.start(handoff);
    await expect(client400.commitAdminKey('commit-1', 'weak')).rejects.toMatchObject({
      name: 'BootstrapTransportError',
      kind: 'bad_request',
      status: 400,
    });
  });

  it('validates admin key complexity matching Manager security contract', () => {
    expect(validateFreshAdminKey('').valid).toBe(false);
    expect(validateFreshAdminKey('short-key-123').valid).toBe(false);
    expect(validateFreshAdminKey('cpamp_0123456789abcdefABCDEFG withspace').valid).toBe(false);
    expect(validateFreshAdminKey('aaaaaaaaaaaaaaaaaaaaaaaa').valid).toBe(false);
    expect(validateFreshAdminKey('cpamp_0123456789abcdefABCDEFGH99')).toMatchObject({
      valid: true,
      hasForbiddenChar: false,
    });
  });

  it('generates and commits admin key over authenticated Fresh session', async () => {
    const generatedKey = 'cpamp_0123456789abcdefABCDEFGH99';
    const committedOp = { ...operation, revision: 2, adminInitialized: true };
    const transport = vi
      .fn()
      .mockResolvedValueOnce(new Response(JSON.stringify({ token: 'session-secret', operation })))
      .mockResolvedValueOnce(new Response(JSON.stringify({ key: generatedKey })))
      .mockResolvedValueOnce(new Response(JSON.stringify(committedOp)))
      .mockResolvedValueOnce(new Response(JSON.stringify(committedOp)));

    const client = createBootstrapTransport(transport);
    await client.start(handoff);
    expect(await client.generateAdminKey()).toBe(generatedKey);

    const afterCommit = await client.commitAdminKey('commit-abc', generatedKey);
    expect(afterCommit).toEqual(committedOp);
    expect(JSON.parse(transport.mock.calls[2][1].body as string)).toEqual({
      identity,
      commitId: 'commit-abc',
      key: generatedKey,
    });

    expect(await client.fetchOperation()).toEqual(committedOp);
    expect(transport).toHaveBeenCalledTimes(4);
    expect(new BootstrapTransportError('test', 'transport')).toBeInstanceOf(Error);
  });
});

