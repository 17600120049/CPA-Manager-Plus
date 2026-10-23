// The legacy CPA client remains the Panel transport. Its relative endpoint
// paths must stay beneath the configured CPA Management namespace.
export function assertPanelManagementTarget(base: string, resource: string): void {
  const path = resource.split('?')[0];
  const normalizedBase = base.replace(/\/+$/, '');
  const fullWebLegacyBridge = /\/api\/v2\/cpamp\/v0\/management$/i.test(normalizedBase);
  if (
    !normalizedBase.endsWith('/v0/management') ||
    (/\/api\/v2(?:\/|$)/.test(normalizedBase) && !fullWebLegacyBridge) ||
    !path.startsWith('/') ||
    path.startsWith('//') ||
    /[\\#]/.test(resource) ||
    /%(?:2e|5c)/i.test(path) ||
    path.split('/').some((segment) => segment === '.' || segment === '..') ||
    path === '/api/v2' ||
    path.startsWith('/api/v2/')
  ) {
    throw new Error('Panel Lite requests require a CPA Management target');
  }
}
