import { fullWebText as t } from '../i18n/fullweb';
import { workspaceText as w } from '../i18n/consoleWorkspace';
import { FreshApiKeys } from './FreshApiKeys';
import type { createPublicApiClient } from './publicApi';

/**
 * Manager-owned keys embedded in the established CPAMP layout.
 *
 * Embedded Fresh New deliberately has no CPA Management Key. Keeping these
 * resources on the canonical Fresh Public API lets the existing MainLayout
 * remain the product shell while avoiding a fake /v0/management connection.
 */
export function FreshManagedKeysPage({
  client,
  credential,
  onUnauthorized,
  onPending,
}: {
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  onUnauthorized: () => void;
  onPending: (pending: boolean) => void;
}) {
  return (
    <div className="fresh-managed-config" data-testid="fresh-managed-keys">
      <header className="page-header">
        <div>
          <h1>{t('keysTitle')}</h1>
          <p className="page-description">{w('keysHelp')}</p>
        </div>
        <span className="badge badge-emerald">{w('stored')}</span>
      </header>

      <section className="card" aria-label={t('keysTitle')}>
        <FreshApiKeys
          client={client}
          credential={credential}
          onUnauthorized={onUnauthorized}
          onCreationPending={onPending}
        />
      </section>
    </div>
  );
}
