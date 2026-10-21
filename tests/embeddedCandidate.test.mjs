import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { generateReleaseInfo } from '../bin/release/generate-release-info.mjs';
import { verifyCandidate, verifyImage } from '../bin/release/publish-update-index.mjs';
import { expectedReleaseAssetNames } from '../bin/release/verify-published-release.mjs';
import { repository, validateInfo } from '../bin/release/update-contract.mjs';

describe('R1-1 local-only OCI candidate contracts', () => {
  it('requires immutable platform manifests and rejects bad source, platform and byte identities', () => {
    execFileSync('python3', ['tests/embedded_candidate_contract_test.py'], {
      cwd: new URL('..', import.meta.url),
      stdio: 'pipe',
      timeout: 30_000,
    });
  });

  it('replays the strict published 1.x consumer without R1 fields or single-platform relaxation', () => {
    const tag = 'v1.14.7';
    // Captured from the real published update-channel catalog.
    const info = JSON.parse(readFileSync(new URL('./fixtures/release-v1.14.7-info.json', import.meta.url), 'utf8'));
    const sha = info.release.source_commit;
    expect(sha).toBe('64ce94e6aa1af691555429a3211e67c59c0480ad');
    expect(validateInfo(info, tag)).toBe(info);
    expect(info.schema_version).toBe(1);
    expect(info.distribution.native.assets).toHaveLength(6);
    expect(info.distribution.embedded).toBeUndefined();
    const names = expectedReleaseAssetNames(tag);
    expect(names).toHaveLength(9);
    const published = {
      tag_name: tag, draft: false, immutable: true, published_at: 'test',
      prerelease: false,
      assets: names.map((name) => ({
        name, state: 'uploaded', size: 1, digest: 'sha256:' + 'c'.repeat(64),
      })),
    };
    expect(() => verifyCandidate(published, info, sha)).not.toThrow();
    expect(() => verifyCandidate({ ...published, tag_name: 'v2.0.0' }, { ...info, release: { ...info.release, version: 'v2.0.0' } }, sha)).toThrow(/v2 Embedded/);
    const labels = {
      'org.opencontainers.image.revision': sha,
      'org.opencontainers.image.version': tag.slice(1),
      'org.opencontainers.image.source': repository,
    };
    const manifest = { digest: 'sha256:' + 'b'.repeat(64), manifests: [
      { platform: { os: 'linux', architecture: 'amd64' } },
      { platform: { os: 'linux', architecture: 'arm64' } },
    ] };
    const inspected = { manifest, image: {
      amd64: { config: { Labels: labels } },
      arm64: { config: { Labels: labels } },
    } };
    expect(verifyImage(inspected, info)).toBe(manifest.digest);
    expect(() => verifyImage({
      ...inspected, manifest: { ...manifest, manifests: [manifest.manifests[0]] },
    }, info)).toThrow();
  });
});
