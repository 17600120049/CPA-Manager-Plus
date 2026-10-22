import { lazy, Suspense, useEffect, useRef, useState } from 'react';
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
import { FullWebIcon } from './FullWebIcon';
import logo from '../assets/brand/cpamp-logo-horizontal-color.svg?inline';
import logoOnDark from '../assets/brand/cpamp-logo-horizontal-on-dark.svg?inline';
import './consoleWorkspace.css';
import { activateFreshSession, clearFreshSession, rememberedFreshCredential } from './freshSession';

const defaultClient = createPublicApiClient();
const LegacyConsolePage = lazy(() =>
  import('./LegacyConsolePage').then((module) => ({ default: module.LegacyConsolePage }))
);
const FullWebLoginPage = lazy(() =>
  import('./FullWebLoginPage').then((module) => ({ default: module.FullWebLoginPage }))
);

export function FreshConsolePage({
  context,
  client = defaultClient,
}: {
  context: RoutingContext;
  client?: ReturnType<typeof createPublicApiClient>;
}) {
  const [credential, setCredential] = useState('');
  const [rememberPassword, setRememberPassword] = useState(false);
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
    clearFreshSession();
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

  async function readOperation(candidate = credential, remember = rememberPassword) {
    if (busy || installationPendingRef.current || !candidate) return false;
    const currentRequest = ++requestId.current;
    setBusy(true);
    setError('');
    try {
      const next = await client.get<FreshOperation>('operation', {
        kind: 'admin',
        value: candidate,
      });
      if (currentRequest !== requestId.current) return false;
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
        return false;
      }
      activateFreshSession(context, candidate, remember);
      setRememberPassword(remember);
      setEnteredConsole(next.productComplete === true);
      setCredential(candidate);
      acceptOperation(next);
      setRefreshEpoch((revision) => revision + 1);
      return true;
    } catch (err) {
      if (currentRequest !== requestId.current) return false;
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        clearAuthentication();
        setError(t('authError'));
      } else {
        setError(t('unavailable'));
      }
      return false;
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
    // FreshConsolePage owns the guided shell navigation, while the existing
    // CPAMP pages are rendered inside the Full Web data router. `pushState`
    // alone does not notify React Router, so emit the same browser event a
    // user history action would produce before reconciling our local page.
    // Test and embedded hosts may expose only a minimal history stub; those
    // environments keep the local state path without requiring a DOM event.
    if (typeof window.dispatchEvent === 'function') {
      window.dispatchEvent(
        typeof window.PopStateEvent === 'function'
          ? new window.PopStateEvent('popstate')
          : new Event('popstate')
      );
    }
    currentRoute.current = path;
    visit(consolePage(path, context.basePath));
  }
  const unauthorized = () => {
    clearAuthentication();
    setError(t('authError'));
  };
  const blocked = creationPending || installationPending || retryPending;
  if (!operation) {
    return (
      <Suspense fallback={<div role="status">{t('loggingIn')}</div>}>
        <FullWebLoginPage
          adapter={{
            address: new URL(context.basePath, window.location.origin).toString(),
            error,
            authenticate: readOperation,
            restore: async () => {
              const candidate = rememberedFreshCredential(context);
              if (candidate) await readOperation(candidate, true);
            },
          }}
        />
      </Suspense>
    );
  }
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
            {operation.strategy?.strategy === 'connect_external' ? (
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
  if (operation.productComplete) {
    return (
      <Suspense
        fallback={
          <div className="main-content" role="status">
            <span className="loading-spinner" aria-label={t('unavailable')} />
          </div>
        }
      >
        <LegacyConsolePage
          context={context}
          credential={credential}
          client={client}
          onUnauthorized={unauthorized}
        />
      </Suspense>
    );
  }
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
