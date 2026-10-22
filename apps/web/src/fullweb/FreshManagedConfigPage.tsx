import { useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { workspaceText as w } from '../i18n/consoleWorkspace';
import { FreshApiKeys } from './FreshApiKeys';
import { FreshRetryConfiguration } from './FreshRetryConfiguration';
import { FreshUpdateChannel } from './FreshUpdateChannel';
import { FullWebIcon } from './FullWebIcon';
import type { createPublicApiClient } from './publicApi';

/**
 * Manager-owned resources embedded in the established CPAMP layout.
 *
 * Embedded Fresh New deliberately has no CPA Management Key. Keeping these
 * resources on the canonical Fresh Public API lets the existing MainLayout
 * remain the product shell while avoiding a fake /v0/management connection.
 */
export function FreshManagedConfigPage({
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
  const [retryPending, setRetryPending] = useState(false);
  const [keysPending, setKeysPending] = useState(false);
  const pending = retryPending || keysPending;

  return (
    <div className="fresh-managed-config" data-testid="fresh-managed-config">
      <header className="page-header">
        <div>
          <p className="page-eyebrow">CPAMP</p>
          <h1>{w('configuration')}</h1>
          <p className="page-description">{w('configurationHelp')}</p>
        </div>
        <span className="badge badge-emerald">{w('stored')}</span>
      </header>

      <section className="card fresh-managed-resource" aria-labelledby="fresh-managed-keys-title">
        <div className="fresh-managed-resource-heading">
          <div className="fresh-managed-resource-icon">
            <FullWebIcon name="key" />
          </div>
          <div>
            <h2 id="fresh-managed-keys-title">{t('keysTitle')}</h2>
            <p>{w('keysHelp')}</p>
          </div>
        </div>
        <FreshApiKeys
          client={client}
          credential={credential}
          onUnauthorized={onUnauthorized}
          onCreationPending={(value) => {
            setKeysPending(value);
            onPending(value || retryPending);
          }}
        />
      </section>

      <section className="card fresh-managed-resource" aria-labelledby="fresh-managed-retry-title">
        <div className="fresh-managed-resource-heading">
          <div className="fresh-managed-resource-icon">
            <FullWebIcon name="gateway" />
          </div>
          <div>
            <h2 id="fresh-managed-retry-title">{w('connection')}</h2>
            <p>{w('connectionHelp')}</p>
          </div>
        </div>
        <FreshRetryConfiguration
          client={client}
          credential={credential}
          onUnauthorized={onUnauthorized}
          onPending={(value) => {
            setRetryPending(value);
            onPending(value || keysPending);
          }}
        />
      </section>

      <section className="card fresh-managed-resource" aria-labelledby="fresh-managed-updates-title">
        <div className="fresh-managed-resource-heading">
          <div className="fresh-managed-resource-icon">
            <FullWebIcon name="settings" />
          </div>
          <div>
            <h2 id="fresh-managed-updates-title">{w('preferences')}</h2>
            <p>{w('settingsHelp')}</p>
          </div>
        </div>
        <FreshUpdateChannel client={client} credential={credential} onUnauthorized={onUnauthorized} />
      </section>

      {pending && (
        <p className="alert alert-warn" role="status">
          {t('keysExitBlocked')}
        </p>
      )}
    </div>
  );
}

export function FreshManagedUnavailablePage({
  title,
  detail,
}: {
  title: string;
  detail: string;
}) {
  return (
    <section className="card fresh-managed-unavailable" data-testid="fresh-managed-unavailable">
      <div className="fresh-managed-resource-icon" aria-hidden="true">
        <FullWebIcon name="alert" />
      </div>
      <div>
        <h1>{title}</h1>
        <p>{detail}</p>
        <p className="fresh-managed-unavailable-note">{w('gatewayHelp')}</p>
      </div>
    </section>
  );
}
