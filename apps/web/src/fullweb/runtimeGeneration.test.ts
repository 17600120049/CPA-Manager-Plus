import { describe, expect, it, vi, afterEach } from 'vitest';
import { parseRuntimeJSON, runtimeRequestJSON, validRuntimeGeneration } from './runtimeGeneration';
import { createPublicApiClient } from './publicApi';

describe('exact Supervisor uint64 fence transport', () => {
  afterEach(() => vi.unstubAllGlobals());
  it('preserves large numeric fences without changing embedded strings or other numbers', () => {
    expect(
      parseRuntimeJSON(
        '{"runtimeGeneration":18446744073709551615,"targetGeneration":9007199254740993,"other":3,"note":' +
          JSON.stringify('"runtimeGeneration":18446744073709551615') +
          '}'
      )
    ).toEqual({
      runtimeGeneration: '18446744073709551615',
      targetGeneration: '9007199254740993',
      other: 3,
      note: '"runtimeGeneration":18446744073709551615',
    });
    expect(parseRuntimeJSON('{"targetGeneration":3}')).toEqual({ targetGeneration: 3 });
  });
  it.each([0, -1, 1.5, Number.MAX_SAFE_INTEGER + 1, '', '01', '-1', '1e3', '18446744073709551616'])(
    'rejects invalid or already rounded generation %s',
    (value) => {
      expect(validRuntimeGeneration(value)).toBe(false);
      expect(() => runtimeRequestJSON({ expectedRuntimeGeneration: value })).toThrow();
    }
  );
  it('sends the exact integer through the existing same-origin strategy request and preserves readback', async () => {
    vi.stubGlobal('window', { location: new URL('http://localhost/console') });
    const transport = vi
      .fn()
      .mockResolvedValue(new Response('{"strategy":{"targetGeneration":9007199254740993}}'));
    const client = createPublicApiClient(
      () => ({ basePath: '/console', apiBase: '/console/api/v2', revision: 1 }),
      transport
    );
    expect(
      await client.post(
        'strategy',
        { expectedRuntimeGeneration: '9007199254740993' },
        { kind: 'admin', value: 'secret' }
      )
    ).toEqual({ strategy: { targetGeneration: '9007199254740993' } });
    expect(transport.mock.calls[0][1].body).toBe('{"expectedRuntimeGeneration":9007199254740993}');
  });
});
