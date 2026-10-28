// Canonical Client Keys have two origins. The workspace creates its own; the
// default installation mints exactly one so CPA never starts with client
// authentication disabled. Both are the same canonical record with the same
// masked-only projection.
export type KeyOrigin = 'workspace' | 'installer';

export interface Key {
  id: string;
  requestId: string;
  maskedPreview: string;
  createdAtMs: number;
  managerState: 'persisted';
  runtimeState: 'not_applied' | 'effect_unknown';
  name: string;
  nameRevision: number;
  lifecycle?: 'active' | 'superseded';
  origin: KeyOrigin;
}

const workspaceSecret = /^cpamp_sk_[a-f0-9]{64}$/;
const installSecret = /^sk-[A-Za-z0-9]{64}$/;

/** maskedPreviewOf is the only rendering a saved secret may produce. */
export function maskedPreviewOf(secret: string): string {
  if (workspaceSecret.test(secret)) return 'cpamp_sk_…' + secret.slice(-4);
  if (installSecret.test(secret)) return 'sk-…' + secret.slice(-4);
  return '';
}

/** validSecretShape accepts exactly the two canonical Client Key shapes. */
export function validSecretShape(secret: string): boolean {
  return workspaceSecret.test(secret) || installSecret.test(secret);
}

export function validKey(value: Key): boolean {
  return (
    !!value &&
    /^[a-f0-9]{32}$/.test(value.id) &&
    /^[a-f0-9]{32}$/.test(value.requestId) &&
    /^(?:cpamp_sk_…[a-f0-9]{4}|sk-…[A-Za-z0-9]{4})$/.test(value.maskedPreview) &&
    Number.isSafeInteger(value.createdAtMs) &&
    value.createdAtMs > 0 &&
    value.managerState === 'persisted' &&
    ['not_applied', 'effect_unknown'].includes(value.runtimeState) &&
    typeof value.name === 'string' &&
    [...value.name].length <= 120 &&
    Number.isSafeInteger(value.nameRevision) &&
    value.nameRevision > 0 &&
    (value.lifecycle === undefined || ['active', 'superseded'].includes(value.lifecycle)) &&
    (value.origin === 'workspace' || value.origin === 'installer') &&
    (value.origin === 'installer') === value.maskedPreview.startsWith('sk-…')
  );
}

/**
 * keyDisplayName keeps a renamed key authoritative. The installation's Key has
 * a stored default name that every locale translates instead of displaying.
 */
export function keyDisplayName(
  key: Key,
  installerLabel: string,
  unnamedLabel: string
): string {
  if (key.origin === 'installer' && key.nameRevision <= 1) return installerLabel;
  return key.name || unnamedLabel;
}
