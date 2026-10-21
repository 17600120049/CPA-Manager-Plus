import { useEffect, useRef, useState, type FormEvent } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { sameIdentity, type FreshOperation } from './bootstrapTransport';
import { createPublicApiClient, PublicApiError } from './publicApi';
import type { RoutingContext } from './routingContext';
import { FreshApiKeys } from './FreshApiKeys';
import { FreshUpdateChannel } from './FreshUpdateChannel';
import { FreshManagedRuntime } from './FreshManagedRuntime';
import { FreshExternalConnection } from './FreshExternalConnection';
import { FreshRetryConfiguration } from './FreshRetryConfiguration';

const defaultClient = createPublicApiClient();

export function FreshConsolePage({
  context,
  client = defaultClient,
}: {
  context: RoutingContext;
  client?: ReturnType<typeof createPublicApiClient>;
}) {
  const [key, setKey] = useState('');
  const [credential, setCredential] = useState('');
  const [operation, setOperation] = useState<FreshOperation | null>(null);
  const [strategyChoice, setStrategyChoice] = useState<'new_managed' | 'connect_external' | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [light, setLight] = useState(false);
  const [creationPending, setCreationPending] = useState(false);
  const creationPendingRef = useRef(false);
  const [installationPending, setInstallationPending] = useState(false);
  const installationPendingRef = useRef(false);
  const [retryNavigation, setRetryNavigation] = useState(0);
  const [retryPending, setRetryPending] = useState(false);
  const retryPendingRef = useRef(false);
  const requestId = useRef(0);
  useEffect(() => {
    document.title = t('consoleTitle');
  }, []);
  useEffect(
    () => () => {
      requestId.current++;
    },
    []
  );
  useEffect(() => {
    document.documentElement.classList.toggle('light', light);
  }, [light]);

  function reportCreationPending(pending: boolean) {
    creationPendingRef.current = pending;
    setCreationPending(pending);
  }

  function acceptOperation(next: FreshOperation) {
    setOperation((current) => {
      // Keep newer durable state if an earlier Console read completes later.
      if (
        current &&
        (!sameIdentity(next.identity, current.identity) || next.revision < current.revision)
      )
        return current;
      return next;
    });
  }

  function clearAuthentication() {
    requestId.current++;
    reportCreationPending(false);
    installationPendingRef.current = false;
    setInstallationPending(false);
    retryPendingRef.current = false;
    setRetryPending(false);
    setRetryNavigation(0);
    setKey('');
    setCredential('');
    setOperation(null);
    setStrategyChoice(null);
    setBusy(false);
    setError('');
  }

  function logout() {
    if (creationPendingRef.current || installationPendingRef.current || retryPendingRef.current)
      return;
    clearAuthentication();
  }

  async function readOperation(event?: FormEvent) {
    event?.preventDefault();
    const candidate = credential || key;
    if (busy || installationPendingRef.current || !candidate) return;
    const currentRequest = ++requestId.current;
    setKey('');
    setBusy(true);
    setError('');
    try {
      const next = await client.get<FreshOperation>('operation', {
        kind: 'admin',
        value: candidate,
      });
      if (currentRequest !== requestId.current) return;
      if (
        !next?.identity ||
        !(
          ['admissionId', 'installationId', 'generation', 'databasePath', 'operationId'] as const
        ).every(
          (field) => typeof next.identity[field] === 'string' && next.identity[field].length > 0
        ) ||
        !next.identity.operationId ||
        !Number.isSafeInteger(next.revision) ||
        next.revision <= 0 ||
        next.adminInitialized !== true ||
        typeof next.gatewayPending !== 'boolean' ||
        next.routing?.committed !== true ||
        next.routing.stage !== 'committed' ||
        next.routing.target !== context.basePath ||
        next.routing.revision !== context.revision ||
        next.routing.operationId !== next.identity.operationId ||
        (operation && !sameIdentity(next.identity, operation.identity))
      ) {
        clearAuthentication();
        setError(t('invalidOperation'));
        return;
      }
      setCredential(candidate);
      acceptOperation(next);
    } catch (err) {
      if (currentRequest !== requestId.current) return;
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        clearAuthentication();
        setError(t('authError'));
      } else {
        setError(t('unavailable'));
      }
    } finally {
      if (currentRequest === requestId.current) setBusy(false);
    }
  }

  return (
    <div className="bootstrap-shell">
      <header className="bootstrap-header">
        <h1>{t('consoleTitle')}</h1>
        <button className="btn-icon" aria-label={t('theme')} onClick={() => setLight(!light)}>
          ☀
        </button>
      </header>
      <main className="bootstrap-main">
        <div className="wizard-container">
          <section className="wizard-card">
            <div className="wizard-card-body">
              {error && (
                <p className="alert alert-warn" role="alert">
                  {error}
                </p>
              )}
              {!operation ? (
                <form onSubmit={(event) => void readOperation(event)} data-testid="console-login">
                  <label className="form-label" htmlFor="console-admin-key">
                    {t('keyLabel')}
                  </label>
                  <div className="input-wrap">
                    <input
                      id="console-admin-key"
                      className="form-input"
                      type="password"
                      value={key}
                      onChange={(event) => setKey(event.target.value)}
                      autoComplete="off"
                      autoCorrect="off"
                      autoCapitalize="off"
                      spellCheck={false}
                      disabled={busy || installationPending}
                    />
                  </div>
                  <p>{t('loginHelp')}</p>
                  <button className="btn btn-primary" type="submit" disabled={busy || !key}>
                    {busy ? t('loggingIn') : t('login')}
                  </button>
                </form>
              ) : (
                <div data-testid="console-operation">
                  <h2>{t('operation')}</h2>
                  <nav className="console-navigation" aria-label={t('consoleNavigation')}>
                    <ol>
                      <li>{t('systemManagement')}</li>
                      <li>{t('gatewayManagement')}</li>
                      <li>
                        <a
                          href="#gateway-retry"
                          data-testid="gateway-retry-navigation"
                          onClick={(event) => {
                            event.preventDefault();
                            setRetryNavigation((activation) => activation + 1);
                          }}
                        >
                          {t('retryNavigationLeaf')}
                        </a>
                      </li>
                    </ol>
                  </nav>
                  <dl className="console-state">
                    <dt>{t('operation')}</dt>
                    <dd>{operation.identity.operationId}</dd>
                    <dt>{t('revision')}</dt>
                    <dd>{operation.revision}</dd>
                    <dt>{t('adminState')}</dt>
                    <dd>{String(operation.adminInitialized)}</dd>
                    <dt>{t('routingStage')}</dt>
                    <dd>{operation.routing.stage}</dd>
                    <dt>{t('pathLabel')}</dt>
                    <dd>{operation.routing.target}</dd>
                    <dt>{t('gatewayState')}</dt>
                    <dd>{String(operation.gatewayPending)}</dd>
                    <dt>{t('productComplete')}</dt>
                    <dd>{String(operation.productComplete ?? false)}</dd>
                  </dl>
                  {operation.gatewayPending && (
                    <p className="alert alert-info">{t('gatewayPending')}</p>
                  )}
                  <div className="wizard-card-footer">
                    <button
                      className="btn btn-secondary"
                      onClick={() => void readOperation()}
                      disabled={busy}
                    >
                      {t('refresh')}
                    </button>
                    <button
                      className="btn btn-secondary"
                      onClick={logout}
                      disabled={creationPending || installationPending || retryPending}
                      aria-describedby={creationPending ? 'key-exit-pending' : undefined}
                    >
                      {t('logout')}
                    </button>
                  </div>
                  {creationPending && (
                    <p id="key-exit-pending" role="status" className="alert alert-warn">
                      {t('keysExitBlocked')}
                    </p>
                  )}
                  {installationPending && (
                    <p role="status" className="alert alert-warn">
                      {t('installUnknown')}
                    </p>
                  )}
                  <FreshApiKeys
                    key={credential}
                    client={client}
                    credential={credential}
                    onCreationPending={reportCreationPending}
                    onUnauthorized={() => {
                      clearAuthentication();
                      setError(t('authError'));
                    }}
                  />
                  <FreshUpdateChannel
                    client={client}
                    credential={credential}
                    onUnauthorized={() => {
                      clearAuthentication();
                      setError(t('authError'));
                    }}
                  />
                  {!operation.strategy?.strategy && (
                    <section className="info-card" data-testid="fresh-strategy-selection">
                      <h2>{t('strategyChoiceTitle')}</h2>
                      <p>{t('strategyChoiceHelp')}</p>
                      <div className="wizard-card-footer">
                        <button type="button" className="btn btn-secondary" data-testid="fresh-select-new"
                          disabled={installationPending}
                          aria-pressed={strategyChoice === 'new_managed'}
                          onClick={() => {
                            if (!installationPendingRef.current) setStrategyChoice('new_managed');
                          }}>
                          {t('strategyNewManaged')}
                        </button>
                        <button type="button" className="btn btn-secondary" data-testid="fresh-select-external"
                          disabled={installationPending}
                          aria-pressed={strategyChoice === 'connect_external'}
                          onClick={() => {
                            if (!installationPendingRef.current) setStrategyChoice('connect_external');
                          }}>
                          {t('strategyExternal')}
                        </button>
                      </div>
                    </section>
                  )}
                  {(operation.strategy?.strategy === 'new_managed' ||
                    (!operation.strategy?.strategy && strategyChoice === 'new_managed')) && (
                    <FreshManagedRuntime
                      key={`runtime-${credential}`}
                      client={client}
                      credential={credential}
                      operation={operation}
                      onOperation={acceptOperation}
                      onPending={(pending) => {
                        installationPendingRef.current = pending;
                        setInstallationPending(pending);
                      }}
                      onUnauthorized={() => {
                        clearAuthentication();
                        setError(t('authError'));
                      }}
                    />
                  )}
                  {(operation.strategy?.strategy === 'connect_external' ||
                    (!operation.strategy?.strategy && strategyChoice === 'connect_external')) && (
                    <FreshExternalConnection
                      key={`external-${credential}`}
                      client={client}
                      credential={credential}
                      operation={operation}
                      onOperation={acceptOperation}
                      onPending={(pending) => {
                        installationPendingRef.current = pending;
                        setInstallationPending(pending);
                      }}
                      onUnauthorized={() => {
                        clearAuthentication();
                        setError(t('authError'));
                      }}
                    />
                  )}
                  {retryNavigation > 0 && (
                    <FreshRetryConfiguration
                      navigationRequest={retryNavigation}
                      client={client}
                      credential={credential}
                      onPending={(pending) => {
                        retryPendingRef.current = pending;
                        setRetryPending(pending);
                      }}
                      onUnauthorized={() => {
                        clearAuthentication();
                        setError(t('authError'));
                      }}
                    />
                  )}
                </div>
              )}
            </div>
          </section>
        </div>
      </main>
    </div>
  );
}
