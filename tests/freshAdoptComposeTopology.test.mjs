import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { parse as parseYaml } from 'yaml';
import { describe, expect, it } from 'vitest';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const composePath = path.join(repoRoot, 'docker-compose.yml');

// A Fresh claim session is the whole trust anchor of an adoption, so the
// operator holding the `initialize` handoff alone decides to adopt. That anchor
// is only meaningful while the Fresh record it authorizes is unreadable to every
// other container: the Manager data volume holds the Fresh operation, its
// admin credential and the protected adoption snapshot.
const MANAGER_DATA_VOLUME = 'cpamp-manager-data';
const MANAGER_DATA_TARGET = '/data';
const MANAGER_SERVICE = 'cpamp-manager';

// Compose accepts both the short `source:target[:mode]` string and the long
// object form. Normalizing both keeps this assertion independent of formatting.
const normalizeVolume = (volume) => {
  if (typeof volume === 'string') {
    const [source, target, mode] = volume.split(':');
    return { source, target, type: undefined, readOnly: /^(?:ro|rw)$/.test(mode) ? mode === 'ro' : false };
  }
  return {
    source: volume?.source,
    target: volume?.target,
    type: volume?.type,
    readOnly: volume?.read_only === true,
  };
};

const serviceVolumes = (document) =>
  Object.entries(document?.services ?? {}).flatMap(([service, config]) =>
    (config?.volumes ?? []).map((volume) => ({ service, ...normalizeVolume(volume) })),
  );

describe('Fresh adoption compose topology', () => {
  const document = parseYaml(readFileSync(composePath, 'utf8'));
  const volumes = serviceVolumes(document);

  it('mounts the Manager data volume only in the Manager service', () => {
    const mounts = volumes.filter((volume) => volume.source === MANAGER_DATA_VOLUME);

    expect(mounts.map(({ service, target }) => ({ service, target }))).toEqual([
      { service: MANAGER_SERVICE, target: MANAGER_DATA_TARGET },
    ]);
  });

  it('never exposes the Manager data path to another service', () => {
    // A different volume at the same path would still let a sibling container
    // outlive this record, so the target path is reserved as well as the source.
    const offenders = volumes
      .filter((volume) => volume.service !== MANAGER_SERVICE)
      .filter((volume) => volume.target === MANAGER_DATA_TARGET)
      .map((volume) => `${volume.service}:${volume.target}`);

    expect(offenders).toEqual([]);
  });

  it('declares the Manager data volume as a named volume, not a host bind', () => {
    expect(Object.keys(document?.volumes ?? {})).toContain(MANAGER_DATA_VOLUME);
    const managerData = volumes.filter((volume) => volume.source === MANAGER_DATA_VOLUME);
    expect(managerData.every((volume) => volume.type !== 'bind')).toBe(true);
  });
});
