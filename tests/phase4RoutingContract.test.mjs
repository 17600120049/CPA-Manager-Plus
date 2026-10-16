import { readFileSync } from 'node:fs';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import { describe, expect, it } from 'vitest';

const repoRoot = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const fixturePath = path.join(repoRoot, 'tests/fixtures/phase4-routing/route-owners.json');
const schemaPath = path.join(repoRoot, 'tests/fixtures/phase4-routing/route-owners.schema.json');
const ingressClassifierPath = path.join(repoRoot, 'apps/ingress/internal/ingress/classifier.go');
const managerRoutingPath = path.join(repoRoot, 'apps/manager-server/internal/domain/routing/path.go');

const readJSON = (filePath) => JSON.parse(readFileSync(filePath, 'utf8'));

describe('Phase4-01 routing ownership contract', () => {
  it('keeps one exact discovery path and one canonical Public API v2 suffix', () => {
    const fixture = readJSON(fixturePath);
    const schema = readJSON(schemaPath);
    const ingress = readFileSync(ingressClassifierPath, 'utf8');
    const manager = readFileSync(managerRoutingPath, 'utf8');

    expect(fixture.fixedBootstrapDiscoveryPath).toBe('/.well-known/cpamp-bootstrap');
    expect(fixture.canonicalPublicApiV2Suffix).toBe('/api/v2');
    expect(schema.properties.fixedBootstrapDiscoveryPath.const).toBe(fixture.fixedBootstrapDiscoveryPath);
    expect(schema.properties.canonicalPublicApiV2Suffix.const).toBe(fixture.canonicalPublicApiV2Suffix);
    expect(ingress).toContain(`FixedBootstrapDiscoveryPath = "${fixture.fixedBootstrapDiscoveryPath}"`);
    expect(manager).toContain(`BootstrapDiscoveryPath = "${fixture.fixedBootstrapDiscoveryPath}"`);
    expect(manager).toContain(`PublicAPIV2Suffix = "${fixture.canonicalPublicApiV2Suffix}"`);
    expect(fixture.routes.filter((route) => route.id === 'bootstrap_discovery')).toHaveLength(1);
    expect(fixture.routes.filter((route) => route.id === 'public_api_v2_namespace')).toHaveLength(1);
    expect(fixture.routes.filter((route) => route.id === 'retired_control_namespace')).toHaveLength(1);
  });

  it('keeps routing authority and credential decisions separate', () => {
    const fixture = readJSON(fixturePath);
    const discovery = fixture.routes.find((route) => route.id === 'bootstrap_discovery');
    const publicAPI = fixture.routes.find((route) => route.id === 'public_api_v2_namespace');
    const retired = fixture.routes.find((route) => route.id === 'retired_control_namespace');
    const gateway = fixture.routes.find((route) => route.id === 'unknown_non_control_path');

    expect(discovery.activeMethods).toEqual(['GET', 'OPTIONS']);
    expect(discovery.unsupportedMethods).toBe('manager_local_rejection');
    expect(discovery.completedBehavior).toBe('generic_404');
    expect(publicAPI.authority).toBe('verified_effective_snapshot_only');
    expect(publicAPI.noAuthorityBehavior).toBe('inactive_fail_closed');
    expect(retired.behavior).toBe('deny_only');
    expect(retired.gatewayFallback).toBe('deny');
    expect(gateway.owner).toBe('gateway');
    expect(gateway.credentialHeaders).toBe('do_not_classify');
    expect(fixture.normalization.credentialRouting).toBe('route_ownership_precedes_credentials');
  });
});
