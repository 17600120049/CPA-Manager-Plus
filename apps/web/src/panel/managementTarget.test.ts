import { describe, expect, it } from 'vitest';
import { assertPanelManagementTarget } from './managementTarget';
import {
  usageServiceApi,
  dashboardApi,
  monitoringAnalyticsApi,
  accountQuotaSnapshotApi,
} from './unsupportedManagerApi';

describe('Panel Lite CPA transport boundary', () => {
  it.each([
    '/config',
    '/auth-files/a.json',
    '/request-log-by-id/log%201?download=true',
    '/plugin-store/demo%2Fplugin/install',
  ])('preserves CPA Management resources %s', (resource) => {
    expect(() =>
      assertPanelManagementTarget('http://cpa:8317/v0/management', resource)
    ).not.toThrow();
  });

  it.each([
    '/api/v2',
    '/api/v2/status',
    'https://manager.example/control/api/v2/status',
    '//manager.example/control/api/v2/status',
    '/../../control/api/v2/status',
    '/%2e%2e/control/api/v2/status',
    '/.%2e/control/api/v2/status',
    '/%5cmanager.example/path',
    '/\\manager.example/path',
  ])('rejects CPAMP or escaping destinations %s', (resource) => {
    expect(() => assertPanelManagementTarget('http://cpa:8317/v0/management', resource)).toThrow();
  });

  it('rejects a non-CPA base before a CPA key can be sent', () => {
    expect(() =>
      assertPanelManagementTarget('https://manager.example/control/api/v2', '/status')
    ).toThrow();
    expect(() =>
      assertPanelManagementTarget('https://manager.example/control/api/v2/v0/management', '/status')
    ).toThrow();
  });

  it('legacy Manager compatibility calls cannot reach the network', async () => {
    await expect(usageServiceApi.getInfo('https://manager.example')).rejects.toThrow(
      'unavailable in Panel Lite'
    );
    for (const adapter of [dashboardApi, monitoringAnalyticsApi, accountQuotaSnapshotApi]) {
      const operation = (
        adapter as unknown as Record<string, (...args: unknown[]) => Promise<never>>
      ).anyOperation;
      await expect(operation('https://manager.example', 'cpa-key')).rejects.toThrow(
        'unavailable in Panel Lite'
      );
    }
  });
});
