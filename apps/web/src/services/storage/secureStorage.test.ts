import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';

describe('legacy plaintext storage migration', () => {
  let values: Map<string, string>;
  let write: ReturnType<typeof vi.fn>;

  beforeEach(() => {
    vi.resetModules();
    values = new Map();
    write = vi.fn((key: string, value: string) => values.set(key, value));
    vi.stubGlobal('localStorage', {
      getItem: (key: string) => values.get(key) ?? null,
      setItem: write,
      removeItem: (key: string) => values.delete(key),
      clear: () => values.clear(),
    });
    vi.stubGlobal('window', { location: { host: 'cpa.local:8317' } });
    vi.stubGlobal('navigator', {
      userAgent: 'Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) Chrome/120.0.0.0',
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    vi.restoreAllMocks();
  });

  it('preserves historical v1 bytes and decoded values without writes', async () => {
    const ciphertext =
      'enc::v1::GE4IXRkwDgscD1tSAVkDFVhaRllKEksPGhEEQUlMXENWRUleDhEPTwsKDgQCTnNWSBVGbxsfGhhBAEtYR14NQygYTlFWTEIfB1VHZA0GAAhFIxUKQSNxXQ==';
    values.set('legacy', ciphertext);
    const { obfuscatedStorage } = await import('./secureStorage');
    obfuscatedStorage.migratePlaintextKeys(['legacy', 'legacy']);
    obfuscatedStorage.migratePlaintextKeys(['legacy']);
    expect(values.get('legacy')).toBe(ciphertext);
    expect(write).not.toHaveBeenCalled();
    expect(obfuscatedStorage.getItem('legacy')).toEqual({
      apiBase: 'http://cpa.local:8317',
      managementKey: 'test-admin-key-888-long-credential',
    });
  });

  it('preserves all v2 session keys through repeated migration and module reload with another UA', async () => {
    const { obfuscateData } = await import('@/utils/encryption');
    const decoded = {
      apiBase: 'http://cpa.local:8317',
      apiUrl: 'http://fallback.local:8317',
      managementKey: 'FAKE-management-key',
    };
    for (const [key, value] of Object.entries(decoded)) {
      values.set(key, obfuscateData(JSON.stringify(value)));
    }
    const originals = new Map(values);
    const { obfuscatedStorage } = await import('./secureStorage');
    obfuscatedStorage.migratePlaintextKeys([...Object.keys(decoded), 'managementKey']);
    vi.resetModules();
    vi.stubGlobal('navigator', { userAgent: 'Firefox/130.0' });
    const { obfuscatedStorage: reloaded } = await import('./secureStorage');
    reloaded.migratePlaintextKeys(Object.keys(decoded));
    expect(values).toEqual(originals);
    expect(write).not.toHaveBeenCalled();
    for (const [key, value] of Object.entries(decoded)) {
      expect(reloaded.getItem(key)).toBe(value);
    }
  });

  it.each([
    ['bare string', 'FAKE-plain-key', 'FAKE-plain-key'],
    ['JSON string', '"http://cpa.local:8317"', 'http://cpa.local:8317'],
    ['object', '{"enabled":true,"count":2}', { enabled: true, count: 2 }],
    ['false', 'false', false],
    ['zero', '0', 0],
  ])('migrates %s once and remains idempotent', async (_name, raw, expected) => {
    values.set('legacy', raw as string);
    const { obfuscatedStorage } = await import('./secureStorage');
    obfuscatedStorage.migratePlaintextKeys(['legacy', 'legacy']);
    const migrated = values.get('legacy');
    expect(migrated).toMatch(/^enc::v2::/);
    expect(obfuscatedStorage.getItem('legacy')).toEqual(expected);
    obfuscatedStorage.migratePlaintextKeys(['legacy']);
    expect(values.get('legacy')).toBe(migrated);
    expect(write).toHaveBeenCalledTimes(1);
  });

  it('does not write absent, empty, unrelated, or recognized malformed values', async () => {
    values.set('empty', '');
    values.set('unrelated', 'keep-me');
    values.set('bad-v1', 'enc::v1::%%%');
    values.set('bad-v2', 'enc::v2::%%%');
    const originals = new Map(values);
    const { obfuscatedStorage } = await import('./secureStorage');
    obfuscatedStorage.migratePlaintextKeys(['absent', 'empty', 'bad-v1', 'bad-v2']);
    expect(values).toEqual(originals);
    expect(write).not.toHaveBeenCalled();
    // Format recognition does not establish that malformed data is usable or authenticated.
  });

  it('retains the storage alias and the existing option semantics', async () => {
    const { obfuscatedStorage, secureStorage } = await import('./secureStorage');
    expect(secureStorage).toBe(obfuscatedStorage);
    secureStorage.setItem('plain', false, { encrypt: false });
    expect(values.get('plain')).toBe('false');
    expect(secureStorage.getItem('plain', { encrypt: false })).toBe(false);
    secureStorage.setItem('encoded', 'FAKE-key', { encrypt: true });
    expect(values.get('encoded')).toMatch(/^enc::v2::/);
    expect(secureStorage.getItem('encoded', { encrypt: true })).toBe('FAKE-key');
  });
});
