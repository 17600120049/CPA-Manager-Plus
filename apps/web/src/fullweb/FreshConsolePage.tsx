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
import { ConsoleShell, consolePage, type ConsolePage } from './ConsoleShell';
import { shellText as s } from '../i18n/consoleShell';
import { workspaceText as w } from '../i18n/consoleWorkspace';
import { ConsoleHome } from './ConsoleHome';
import { FullWebResourcePage, type FullWebResource } from './FullWebResourcePage';
import { FullWebIcon } from './FullWebIcon';
import logo from '../assets/brand/cpamp-logo-horizontal-color.svg?inline';
import logoOnDark from '../assets/brand/cpamp-logo-horizontal-on-dark.svg?inline';
import './consoleWorkspace.css';

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
  const [enteredConsole, setEnteredConsole] = useState(false);
  const [page, setPage] = useState<ConsolePage>(() =>
    consolePage(window.location.pathname, context.basePath)
  );
  const [visited, setVisited] = useState<ConsolePage[]>([page]);
  const [, updateLanguage] = useState(0);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [light, setLight] = useState(true);
  const [, setThemeMode] = useState<'auto' | 'white' | 'dark'>('white');
  const [refreshEpoch, setRefreshEpoch] = useState(0);
  const [creationPending, setCreationPending] = useState(false);
  const creationPendingRef = useRef(false);
  const [installationPending, setInstallationPending] = useState(false);
  const installationPendingRef = useRef(false);
  const [retryNavigation, setRetryNavigation] = useState(page === 'gateway' ? 1 : 0);
  const [retryPending, setRetryPending] = useState(false);
  const retryPendingRef = useRef(false);
  const requestId = useRef(0);
  const currentRoute = useRef(window.location.pathname);
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
    document.documentElement.setAttribute('data-theme', light ? 'white' : 'dark');
  }, [light]);

  function changeTheme(mode: 'auto' | 'white' | 'dark') {
    setThemeMode(mode);
    if (mode === 'dark') setLight(false);
    else if (mode === 'white') setLight(true);
    else setLight(window.matchMedia?.('(prefers-color-scheme: dark)').matches !== true);
  }

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
    setEnteredConsole(false);
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
      setEnteredConsole(next.productComplete === true);
      setCredential(candidate);
      acceptOperation(next);
      setRefreshEpoch((revision) => revision + 1);
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

  function visit(next: ConsolePage) {
    setPage(next);
    setVisited((current) => (current.includes(next) ? current : [...current, next]));
    if (next === 'gateway') setRetryNavigation((activation) => activation + 1);
  }
  useEffect(() => {
    const pop = () => {
      if (creationPendingRef.current || installationPendingRef.current || retryPendingRef.current) {
        window.history.replaceState(null, '', currentRoute.current);
        return;
      }
      currentRoute.current = window.location.pathname;
      visit(consolePage(window.location.pathname, context.basePath));
    };
    window.addEventListener?.('popstate', pop);
    return () => window.removeEventListener?.('popstate', pop);
  }, [context.basePath]);
  function navigate(path: string) {
    if (creationPendingRef.current || installationPendingRef.current || retryPendingRef.current)
      return;
    window.history.pushState(null, '', path);
    currentRoute.current = path;
    visit(consolePage(path, context.basePath));
  }
  const unauthorized = () => {
    clearAuthentication();
    setError(t('authError'));
  };
  const blocked = creationPending || installationPending || retryPending;
  if (!operation || !enteredConsole)
    return (
      <div className="bootstrap-shell">
        <header className="bootstrap-header">
          <a className="console-login-brand" href={context.basePath}>
            <img src={light ? logo : logoOnDark} alt="CPAMP" />
          </a>
          <button className="btn-icon" aria-label={t('theme')} onClick={() => setLight(!light)}>
            <FullWebIcon name={light ? 'moon' : 'sun'} />
          </button>
        </header>
        <main className="bootstrap-main">
          <div className="wizard-container">
            {error && (
              <p className="alert alert-warn" role="alert">
                {error}
              </p>
            )}
            {!operation ? (
              <section className="wizard-card installation-login console-login-panel">
                <div className="wizard-card-header">
                  <span>
                    <FullWebIcon name="key" />
                  </span>
                  <h1>{s('loginTitle')}</h1>
                  <p>{w('securityHelp')}</p>
                </div>
                <div className="wizard-card-body">
                  <form onSubmit={(event) => void readOperation(event)} data-testid="console-login">
                    <label className="form-label" htmlFor="console-admin-key">
                      {t('keyLabel')}
                    </label>
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
                      disabled={busy}
                    />
                    <button className="btn btn-primary" type="submit" disabled={busy || !key}>
                      {busy ? t('loggingIn') : t('login')}
                    </button>
                  </form>
                </div>
              </section>
            ) : operation.strategy?.strategy === 'connect_external' ? (
              <FreshExternalConnection
                client={client}
                credential={credential}
                operation={operation}
                onOperation={acceptOperation}
                onUnauthorized={unauthorized}
                onPending={(pending) => {
                  installationPendingRef.current = pending;
                  setInstallationPending(pending);
                }}
              />
            ) : (
              <FreshManagedRuntime
                guided
                client={client}
                credential={credential}
                operation={operation}
                onOperation={acceptOperation}
                onUnauthorized={unauthorized}
                onPending={(pending) => {
                  installationPendingRef.current = pending;
                  setInstallationPending(pending);
                }}
                onContinue={() => {
                  setEnteredConsole(true);
                  navigate(context.basePath);
                }}
              />
            )}
          </div>
        </main>
      </div>
    );
  return (
    <ConsoleShell
      basePath={context.basePath}
      page={page}
      onNavigate={navigate}
      onLogout={logout}
      onRefresh={() => void readOperation()}
      blocked={blocked}
      light={light}
      onTheme={() => setLight(!light)}
      onThemeMode={changeTheme}
      onLanguage={() => updateLanguage((revision) => revision + 1)}
    >
      {error && (
        <p className="alert alert-warn" role="alert">
          {error}
        </p>
      )}
      {creationPending && (
        <p id="key-exit-pending" role="status" className="alert alert-warn">
          {t('keysExitBlocked')}
        </p>
      )}
      {retryPending && (
        <p role="status" className="alert alert-warn">
          {t('retryNeedsReconciliation')}
        </p>
      )}
      {page === 'home' && (
        <ConsoleHome
          client={client}
          credential={credential}
          operation={operation}
          onUnauthorized={unauthorized}
          onNavigate={navigate}
          basePath={context.basePath}
          blocked={blocked}
          refreshEpoch={refreshEpoch}
        />
      )}
      {visited.includes('keys') && (
        <div className="console-page-view" hidden={page !== 'keys'}>
          <FreshApiKeys
            key={credential}
            client={client}
            credential={credential}
            onCreationPending={reportCreationPending}
            onUnauthorized={unauthorized}
          />
        </div>
      )}
      {visited
        .filter((item): item is ConsolePage =>
          [
            'accounts',
            'providers',
            'models',
            'health',
            'policies',
            'monitoring',
            'usage',
            'prices',
            'runtime',
            'config',
            'maintenance',
            'logs',
            'updates',
            'plugins',
          ].includes(item)
        )
        .map((item) => (
          <div className="console-page-view" hidden={page !== item} key={item}>
            <FullWebResourcePage
              resource={item === 'runtime' ? 'system' : item === 'prices' ? 'model-prices' : (item as FullWebResource)}
              client={client}
              credential={credential}
              onUnauthorized={unauthorized}
            />
          </div>
        ))}
      {visited.includes('gateway') && (
        <div className="console-page-view" hidden={page !== 'gateway'}>
          <div className="workspace-settings">
            <aside className="card workspace-section-nav" aria-label={w('configuration')}>
              <span>
                <FullWebIcon name="gateway" />
                {w('connection')}
              </span>
            </aside>
            <div className="card workspace-settings-content">
              <header className="workspace-settings-heading">
                <FullWebIcon name="gateway" />
                <div>
                  <h2>{w('connection')}</h2>
                  <p>{w('connectionHelp')}</p>
                </div>
              </header>
              <FreshRetryConfiguration
                navigationRequest={retryNavigation}
                client={client}
                credential={credential}
                onPending={(pending) => {
                  retryPendingRef.current = pending;
                  setRetryPending(pending);
                }}
                onUnauthorized={unauthorized}
              />
            </div>
          </div>
        </div>
      )}
      {visited.includes('settings') && (
        <div className="console-page-view" hidden={page !== 'settings'}>
          <div className="workspace-settings">
            <aside className="card workspace-section-nav" aria-label={w('settings')}>
              <span>
                <FullWebIcon name="settings" />
                {w('preferences')}
              </span>
            </aside>
            <div className="card workspace-settings-content">
              <header className="workspace-settings-heading">
                <FullWebIcon name="settings" />
                <div>
                  <h2>{w('preferences')}</h2>
                  <p>{w('settingsHelp')}</p>
                </div>
              </header>
              <FreshUpdateChannel
                client={client}
                credential={credential}
                onUnauthorized={unauthorized}
              />
            </div>
          </div>
        </div>
      )}
      {page === 'installation' && (
        <section className="card workspace-installation-summary">
          <div className="workspace-installation-heading">
            <span>
              <FullWebIcon name="check" />
            </span>
            <div>
              <h2>{s('ready')}</h2>
              <p>{w('completedHelp')}</p>
            </div>
          </div>
          <dl className="console-state">
            <dt>{s('consoleAddress')}</dt>
            <dd>{operation.routing.target}</dd>
            <dt>{w('installation')}</dt>
            <dd>{w(operation.productComplete ? 'installed' : 'pending')}</dd>
            <dt>{w('deployment')}</dt>
            <dd>{w('embedded')}</dd>
            <dt>{w('runtime')}</dt>
            <dd>{w('version')}</dd>
            <dt>{w('gateway')}</dt>
            <dd>{w('gatewayPending')}</dd>
          </dl>
          <div className="workspace-notice">
            <FullWebIcon name="alert" />
            <p>{w('gatewayHelp')}</p>
          </div>
          <details>
            <summary>{s('technical')}</summary>
            <dl className="console-state">
              <dt>{t('operation')}</dt>
              <dd>{operation.identity.operationId}</dd>
              <dt>{t('revision')}</dt>
              <dd>{operation.revision}</dd>
              <dt>{s('destination')}</dt>
              <dd>{operation.strategy?.targetIdentity}</dd>
            </dl>
          </details>
        </section>
      )}
    </ConsoleShell>
  );
}
