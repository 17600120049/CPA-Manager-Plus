import { execFileSync } from 'node:child_process';
import { describe, it } from 'vitest';

describe('R1-1 local-only OCI candidate contracts', () => {
  it('requires immutable platform manifests and rejects bad source, platform and byte identities', () => {
    execFileSync('python3', ['tests/embedded_candidate_contract_test.py'], {
      cwd: new URL('..', import.meta.url),
      stdio: 'pipe',
      timeout: 30_000,
    });
  });
});
