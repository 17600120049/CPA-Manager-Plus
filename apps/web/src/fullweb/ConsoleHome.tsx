import { useEffect, useState } from 'react';
import { workspaceText as t } from '../i18n/consoleWorkspace';
import type { FreshOperation } from './bootstrapTransport';
import { PublicApiError, type createPublicApiClient } from './publicApi';
import { validRuntimeGeneration, type RuntimeGeneration } from './runtimeGeneration';
import { FullWebIcon } from './FullWebIcon';
import './consoleWorkspace.css';

type Observation = {
  runtimeIdentity: string;
  runtimeGeneration: RuntimeGeneration;
  state: 'unknown' | 'offline' | 'starting' | 'ready';
  activeGatewayArtifact: { artifactId: string };
};
export function ConsoleHome({
  client,
  credential,
  operation,
  onUnauthorized,
  onNavigate,
  basePath,
  blocked,
  refreshEpoch,
}: {
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  operation: FreshOperation;
  onUnauthorized: () => void;
  onNavigate: (path: string) => void;
  basePath: string;
  blocked: boolean;
  refreshEpoch: number;
}) {
  const [runtime, setRuntime] = useState<Observation | null>(null);
  const [unavailable, setUnavailable] = useState(false);
  useEffect(() => {
    let active = true;
    void client
      .get<Observation>('installation/runtime', { kind: 'admin', value: credential })
      .then((value) => {
        if (!active) return;
        if (
          !value ||
          typeof value.runtimeIdentity !== 'string' ||
          !value.runtimeIdentity ||
          !validRuntimeGeneration(value.runtimeGeneration) ||
          !['ready', 'offline', 'starting', 'unknown'].includes(value.state) ||
          !/^sha256:[a-f0-9]{64}$/.test(value.activeGatewayArtifact?.artifactId || '')
        )
          throw new Error('Invalid runtime observation');
        setRuntime(value);
        setUnavailable(false);
      })
      .catch((error) => {
        if (!active) return;
        setRuntime(null);
        setUnavailable(true);
        if (error instanceof PublicApiError && error.kind === 'authentication') onUnauthorized();
      });
    return () => {
      active = false;
    };
  }, [client, credential, refreshEpoch, onUnauthorized]);
  const changed =
    runtime &&
    operation.strategy?.targetIdentity &&
    (runtime.runtimeIdentity !== operation.strategy.targetIdentity ||
      String(runtime.runtimeGeneration) !== String(operation.strategy.targetGeneration) ||
      runtime.activeGatewayArtifact.artifactId !== operation.strategy.targetArtifactId);
  const links = [
    { icon: 'key', label: 'keys', help: 'keysHelp', route: '/api-keys' },
    {
      icon: 'gateway',
      label: 'configuration',
      help: 'configurationHelp',
      route: '/system/gateway',
    },
    { icon: 'settings', label: 'settings', help: 'settingsHelp', route: '/system/settings' },
    {
      icon: 'installation',
      label: 'summary',
      help: 'installationHelp',
      route: '/system/installation',
    },
  ] as const;
  return (
    <div className="console-home" data-testid="console-home">
      <section className="card workspace-welcome">
        <div>
          <span className="workspace-eyebrow">CPAMP CONSOLE</span>
          <h2>{t('welcome')}</h2>
          <p>{t('welcomeHelp')}</p>
        </div>
        <span className="workspace-deployment">
          <FullWebIcon name="installation" />
          {t('embedded')}
        </span>
      </section>
      <div className="workspace-metrics">
        <section className="card workspace-metric">
          <span className="workspace-metric-icon">
            <FullWebIcon name="check" />
          </span>
          <div>
            <h3>{t('installation')}</h3>
            <strong>{t(operation.productComplete ? 'installed' : 'pending')}</strong>
            <small>{t('version')}</small>
          </div>
        </section>
        <section className="card workspace-metric">
          <span className="workspace-metric-icon">
            <FullWebIcon name="gateway" />
          </span>
          <div>
            <h3>{t('runtime')}</h3>
            <strong data-testid="console-runtime-state">
              {t(unavailable ? 'unknown' : runtime?.state || 'loading')}
            </strong>
            <small>{t('live')}</small>
          </div>
        </section>
        <section className="card workspace-metric">
          <span className="workspace-metric-icon is-amber">
            <FullWebIcon name="alert" />
          </span>
          <div>
            <h3>{t('gateway')}</h3>
            <strong>{t('gatewayPending')}</strong>
            <small>{t('deployment')}</small>
          </div>
        </section>
      </div>
      {unavailable && (
        <p className="alert alert-warn" role="status">
          {t('unavailable')}
        </p>
      )}
      {changed && (
        <p className="alert alert-warn" role="status">
          {t('identityChanged')}
        </p>
      )}
      <section className="card workspace-shortcuts">
        <h2>{t('shortcuts')}</h2>
        <div className="workspace-link-grid">
          {links.map((link) => (
            <a
              key={link.route}
              href={basePath + link.route}
              aria-disabled={blocked || undefined}
              onClick={(event) => {
                if (blocked) {
                  event.preventDefault();
                  return;
                }
                if (event.metaKey || event.ctrlKey || event.shiftKey || event.altKey) return;
                event.preventDefault();
                onNavigate(basePath + link.route);
              }}
            >
              <span className="workspace-link-icon">
                <FullWebIcon name={link.icon} />
              </span>
              <div>
                <h3>{t(link.label)}</h3>
                <p>{t(link.help)}</p>
              </div>
              <FullWebIcon name="arrowRight" />
            </a>
          ))}
        </div>
      </section>
      <section className="workspace-notice">
        <FullWebIcon name="alert" />
        <div>
          <h3>{t('gateway')}</h3>
          <p>{t('gatewayHelp')}</p>
        </div>
      </section>
    </div>
  );
}
