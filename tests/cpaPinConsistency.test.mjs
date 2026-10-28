import { execFileSync } from 'node:child_process';
import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

// Dockerfile.runtime is the only authority for the embedded CPA pin. Every other
// location (Go constants, CI scripts, workflow, manifests, contract docs) is a
// consumer and MUST agree with it exactly; a partial bump is a defect.
const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const read = (relativePath) => readFileSync(path.join(repoRoot, relativePath), 'utf8');

const HEX64 = '[0-9a-f]{64}';

const runtimePin = () => {
  const dockerfile = read('Dockerfile.runtime');
  const asset = /asset_name="CLIProxyAPI_(\d+\.\d+\.\d+)_linux_\$\{asset_arch\}\.tar\.gz"/.exec(
    dockerfile
  );
  expect(asset, 'Dockerfile.runtime must name the versioned release asset').not.toBeNull();
  const version = asset[1];

  // The release download URL, the asset name and the artifact manifest must all
  // carry the same version, so a half-edited Dockerfile fails here first.
  expect(dockerfile).toContain(`releases/download/v${version}/\${asset_name}`);
  expect(dockerfile).toContain(`"version":"${version}"`);

  const archPin = (arch, assetArch) => {
    const block = new RegExp(
      `${arch}\\) asset_arch="${assetArch}"; \\\\\n\\s*archive_sha256="(${HEX64})"; \\\\\n\\s*executable_sha256="(${HEX64})"`
    ).exec(dockerfile);
    expect(block, `Dockerfile.runtime must pin linux/${arch}`).not.toBeNull();
    return { archive: block[1], executable: block[2] };
  };

  return { version, amd64: archPin('amd64', 'amd64'), arm64: archPin('arm64', 'aarch64') };
};

const pin = runtimePin();
const artifactId = `sha256:${pin.amd64.executable}`;

// The tag commit is declared by the qualification fixture and must match every
// "tag <sha>" claim that sits next to the pinned version.
const fixture = JSON.parse(read('tests/fixtures/cpa-v8-qualification/manifest.json'));
const sourceCommit = fixture.candidate.sourceCommit;
expect(sourceCommit).toMatch(/^[0-9a-f]{40}$/);

const assignedValue = (text, name) => {
  const match = new RegExp(`(?:^|[\\s(,{])${name}\\s*=\\s*['"]([^'"]*)['"]`, 'm').exec(text);
  expect(match, `${name} must be declared`).not.toBeNull();
  return match[1];
};

const dictValue = (text, base, key) => {
  const block = new RegExp(`${base}\\s*=\\s*\\{([\\s\\S]*?)\\}`).exec(text);
  expect(block, `${base} must be declared`).not.toBeNull();
  const match = new RegExp(`["']?${key}["']?\\s*:\\s*["']([^"']*)["']`).exec(block[1]);
  expect(match, `${base}.${key} must be declared`).not.toBeNull();
  return match[1];
};

const jsonValue = (relativePath, selector) => selector(JSON.parse(read(relativePath)));

describe(`CPA pin consistency (v${pin.version})`, () => {
  it('derives one version and both platform digests from Dockerfile.runtime', () => {
    expect(pin.amd64.archive).not.toEqual(pin.arm64.archive);
    expect(pin.amd64.executable).not.toEqual(pin.arm64.executable);
  });

  it('keeps the qualification fixture on the pinned version, tag and archive', () => {
    expect(fixture.candidate.version).toBe(`v${pin.version}`);
    expect(fixture.candidate.id).toBe(`CPA8-${pin.version}-linux-amd64-plugin`);
    expect(fixture.candidate.assetName).toBe(`CLIProxyAPI_${pin.version}_linux_amd64.tar.gz`);
    expect(fixture.candidate.declaredSha256).toBe(pin.amd64.archive);
  });

  it('binds the dependent consumer record to the pinned artifact', () => {
    const consumer =
      'apps/manager-server/internal/service/freshstrategy/qualification/consumer.json';
    expect(jsonValue(consumer, (value) => value.archiveSha256)).toBe(pin.amd64.archive);
    expect(jsonValue(consumer, (value) => value.binarySha256)).toBe(pin.amd64.executable);
    expect(jsonValue(consumer, (value) => value.artifactId)).toBe(artifactId);
  });

  it('binds every Go consumer constant to the pinned artifact', () => {
    const adoption = read(
      'apps/manager-server/internal/service/freshstrategy/adoption_cpa_test.go'
    );
    expect(assignedValue(adoption, 'realCPA8BinarySHA256')).toBe(pin.amd64.executable);
    expect(assignedValue(adoption, 'realCPA8ArchiveSHA256')).toBe(pin.amd64.archive);
    expect(assignedValue(adoption, 'realCPA8Version')).toBe(`v${pin.version}`);

    expect(
      assignedValue(
        read('apps/manager-server/internal/service/cpa/private_initial_key.go'),
        'privateCPAArtifact'
      )
    ).toBe(artifactId);

    expect(
      assignedValue(read('apps/runtime-supervisor/internal/privateconfig/authority.go'), 'Artifact')
    ).toBe(artifactId);
  });

  it('binds every Go version literal that gates the private runtime', () => {
    expect(read('apps/manager-server/internal/service/freshkeyapply/service.go')).toContain(
      `v.ActiveGatewayArtifact.Version != "${pin.version}"`
    );
    expect(
      read('apps/runtime-supervisor/cmd/cpamp-runtime-supervisor/private_runtime.go')
    ).toContain(`o.Version != "${pin.version}"`);
  });

  it('binds the python and node pin scripts to the same artifact', () => {
    const defaultSmoke = read('bin/ci/runtime-v8-default-smoke.py');
    expect(dictValue(defaultSmoke, 'PINS', 'amd64')).toBe(pin.amd64.executable);
    expect(dictValue(defaultSmoke, 'PINS', 'arm64')).toBe(pin.arm64.executable);

    const candidate = read('bin/ci/check-v2-private-candidate.py');
    expect(dictValue(candidate, 'CPA_EXECUTABLE_SHA256', 'amd64')).toBe(pin.amd64.executable);
    expect(dictValue(candidate, 'CPA_EXECUTABLE_SHA256', 'arm64')).toBe(pin.arm64.executable);
    expect(assignedValue(candidate, 'CPA_VERSION')).toBe(pin.version);

    const prepare = read('bin/ci/prepare-cpa-v8-artifact.py');
    expect(assignedValue(prepare, 'ASSET')).toBe(`CLIProxyAPI_${pin.version}_linux_amd64.tar.gz`);
    expect(assignedValue(prepare, 'SHA256')).toBe(pin.amd64.archive);

    expect(assignedValue(read('bin/ci/fresh-s1-docker-proof.py'), 'PIN')).toBe(
      pin.amd64.executable
    );
    expect(assignedValue(read('bin/ci/runtime-v8-bundle-smoke.py'), 'PIN')).toBe(
      pin.amd64.executable
    );
    expect(assignedValue(read('bin/ci/fresh-new-installation.mjs'), 'pin')).toBe(
      pin.amd64.executable
    );

    const embedded = read('bin/release/verify-embedded-candidate.py');
    expect(assignedValue(embedded, 'CPA_SHA')).toBe(pin.amd64.executable);
    expect(assignedValue(embedded, 'CPA_ARCHIVE_SHA')).toBe(pin.amd64.archive);
  });

  it('binds the evidence contract candidate to the pinned version and tag', () => {
    const contract = read('bin/ci/validate-cpa-v8-evidence.mjs');
    expect(contract).toContain(`version: 'v${pin.version}'`);
    expect(contract).toContain(`sourceCommit: '${sourceCommit}'`);
    expect(contract).toContain(`assetName: 'CLIProxyAPI_${pin.version}_linux_amd64.tar.gz'`);
    expect(contract).toContain(`declaredSha256: '${pin.amd64.archive}'`);
    expect(contract).toContain(`id: 'CPA8-${pin.version}-linux-amd64-plugin'`);
  });

  it('binds the pr-check workflow download and digest gates to the pinned artifact', () => {
    const workflow = read('.github/workflows/pr-check.yml');
    expect(workflow).toContain(
      `releases/download/v${pin.version}/CLIProxyAPI_${pin.version}_linux_amd64.tar.gz`
    );
    expect(workflow).toContain(`printf '%s  %s\\n' ${pin.amd64.archive} `);
    expect(workflow).toContain(`printf '%s  %s\\n' ${pin.amd64.executable} `);
    expect(workflow).toContain(`target_archive = '${pin.amd64.archive}'`);
    expect(workflow).toContain(`target_binary = '${pin.amd64.executable}'`);
  });

  it('binds the current runtime contract doc to the pinned version, tag and digests', () => {
    const contractDoc = read('bin/ci/runtime-v8-default-smoke.md');
    expect(contractDoc).toContain(`CLIProxyAPI v${pin.version} pin`);
    expect(contractDoc).toContain(sourceCommit);
    for (const digest of [
      pin.amd64.archive,
      pin.amd64.executable,
      pin.arm64.archive,
      pin.arm64.executable,
    ]) {
      expect(contractDoc).toContain(digest);
    }
  });

  it('keeps every "tag <sha>" claim equal to the declared source commit', () => {
    const claims = execFileSync('git', ['grep', '-n', '-E', 'tag [0-9a-f]{7,40}', '--', '.'], {
      cwd: repoRoot,
      encoding: 'utf8',
    })
      .split('\n')
      .filter(Boolean)
      .filter((line) => !/^docs\/(plan|architecture)\//.test(line))
      .filter(
        (line) =>
          !line.startsWith(
            'apps/manager-server/internal/service/freshstrategy/qualification/evidence/'
          )
      );

    expect(claims.length).toBeGreaterThan(0);
    for (const claim of claims) {
      const [, sha] = /tag ([0-9a-f]{7,40})/.exec(claim);
      expect(sourceCommit.startsWith(sha), `${claim} must name the declared tag commit`).toBe(true);
    }
  });

  it('keeps the shared CPA LICENSE digest equal across both candidate readers', () => {
    expect(assignedValue(read('bin/ci/check-v2-private-candidate.py'), 'CPA_LICENSE_SHA256')).toBe(
      assignedValue(read('bin/release/verify-embedded-candidate.py'), 'CPA_LICENSE_SHA')
    );
  });

  it('keeps superseded pin digests out of every live location', () => {
    // Retained historical observations (the previous v8.0.13 pin) stay readable
    // only where they describe that run: plan history, architecture evidence,
    // the superseded AQ evidence and the retained bundle-smoke record.
    const historical = [
      'docs/plan/',
      'docs/architecture/',
      'apps/manager-server/internal/service/freshstrategy/qualification/evidence/',
      'bin/ci/runtime-v8-bundle-smoke.md',
      'tests/fixtures/cpa-v8-qualification/README.md',
    ];
    const superseded = ['50ecffb4', 'b682e9e4', 'f7ff98a1', '9dfe80b7'];
    let matches = [];
    try {
      matches = execFileSync('git', ['grep', '-l', '-E', superseded.join('|'), '--', '.'], {
        cwd: repoRoot,
        encoding: 'utf8',
      })
        .split('\n')
        .filter(Boolean);
    } catch (error) {
      if (error.status !== 1) throw error; // 1 means "no match", which passes.
    }

    expect(matches.filter((file) => !historical.some((prefix) => file.startsWith(prefix)))).toEqual(
      []
    );
  });
});
