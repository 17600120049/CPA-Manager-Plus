import { useEffect, useRef, useState } from 'react';
import { bootstrapJourneyText as j } from '../i18n/bootstrapJourney';
import { shellText as s } from '../i18n/consoleShell';
import { fullWebText as t } from '../i18n/fullweb';
import {
  BootstrapTransportError,
  validateFreshAdminKey,
  type BootstrapTransportClient,
  type FreshOperation,
} from './bootstrapTransport';
import { validControlBasePath } from './routingContext';
import { validRuntimeGeneration, type RuntimeGeneration } from './runtimeGeneration';
import { FullWebIcon } from './FullWebIcon';

type Strategy = 'new_managed' | 'connect_external';
type Target = {
  runtimeIdentity: string;
  runtimeGeneration: RuntimeGeneration;
  activeGatewayArtifact: { artifactId: string };
  state: string;
};
function managedTarget(value: unknown): Target {
  const target = value as Target;
  if (
    !target ||
    typeof target.runtimeIdentity !== 'string' ||
    !target.runtimeIdentity.trim() ||
    !validRuntimeGeneration(target.runtimeGeneration) ||
    !/^sha256:[a-f0-9]{64}$/.test(target.activeGatewayArtifact?.artifactId || '')
  )
    throw new Error('Invalid installation target');
  return target;
}
function externalURL(value: string): string {
  const normalized = value.trim().replace(/\/+$/, '');
  if (/[\\%@?#\s]/.test(normalized)) throw new Error('Invalid CPA address');
  const url = new URL(normalized);
  if (
    !['http:', 'https:'].includes(url.protocol) ||
    url.username ||
    url.password ||
    url.origin + url.pathname.replace(/\/+$/, '') !== normalized
  )
    throw new Error('Invalid CPA address');
  return normalized;
}
type Pending = {
  revision: number;
  stage: 'admin' | 'strategy' | 'route' | 'complete';
  previous?: string;
};
function attemptID() {
  const bytes = crypto.getRandomValues(new Uint8Array(16));
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}
function confirmed(op: FreshOperation, pending: Pending) {
  if (op.revision <= pending.revision) return false;
  if (pending.stage === 'admin') return op.adminInitialized;
  if (pending.stage === 'route') return op.routing.committed;
  if (pending.stage === 'complete') return op.productComplete === true;
  return (
    !!op.strategy?.stage &&
    (pending.previous === 'staged'
      ? ['starting', 'committed'].includes(op.strategy.stage)
      : pending.previous === 'starting'
        ? op.strategy.stage === 'committed'
        : true)
  );
}

// One product journey in the existing Full Web shell. Configuration stays in
// memory until confirmation; the Fresh operation, not browser storage, owns
// execution and recovery. Console authentication happens only after completion.
export function FreshSetupWizard({
  initialOperation,
  transport,
  onOperation,
}: {
  initialOperation: FreshOperation;
  transport: BootstrapTransportClient;
  onOperation?: (operation: FreshOperation) => void;
}) {
  const [operation, setOperation] = useState(initialOperation);
  const [strategy, setStrategy] = useState<Strategy>(
    initialOperation.strategy?.strategy === 'connect_external' ? 'connect_external' : 'new_managed'
  );
  const [step, setStep] = useState(initialOperation.strategy?.strategy ? 3 : 0);
  const [key, setKey] = useState('');
  const [visible, setVisible] = useState(false);
  const [path, setPath] = useState(initialOperation.routing.target || '/console');
  const [baseURL, setBaseURL] = useState('');
  const [cpaKey, setCPAKey] = useState('');
  const [savedKey, setSavedKey] = useState(false);
  // The plugin opt-in is the operator's explicit decision, so it starts off and
  // is submitted with the operation that owns it. The Supervisor only ever reads
  // this persisted value, so a retry cannot silently turn plugins on.
  const [plugins, setPlugins] = useState(initialOperation.strategy?.pluginsEnabled === true);
  const [target, setTarget] = useState<Target | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [note, setNote] = useState('');
  const [unknown, setUnknown] = useState(false);
  const inflight = useRef(false);
  const alive = useRef(true);
  const pending = useRef<Pending | null>(null);
  const commitID = useRef(`commit_${attemptID()}`);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  function accept(next: FreshOperation) {
    if (!alive.current) throw new Error('Installation closed');
    if (next.strategy?.strategy && next.strategy.strategy !== strategy)
      throw new Error('Installation strategy changed');
    if (next.routing.target && next.routing.target !== path)
      throw new Error('Installation route changed');
    if (
      next.strategy?.strategy === 'new_managed' &&
      target &&
      (next.strategy.targetIdentity !== target.runtimeIdentity ||
        next.strategy.targetGeneration !== target.runtimeGeneration ||
        next.strategy.targetArtifactId !== target.activeGatewayArtifact.artifactId)
    )
      throw new Error('Installation target changed');
    setOperation(next);
    onOperation?.(next);
    return next;
  }
  async function read(allowRouteRecovery = false) {
    const next = accept(await transport.fetchOperation());
    // An explicit refresh may unlock the original routing operation after a
    // successful checkpoint read. Activate itself inspects Ingress before any
    // apply, so the next user-initiated resume reconciles that same owner; it
    // neither treats a partial route as success nor automatically replays it.
    const recoverableRoute =
      allowRouteRecovery &&
      pending.current?.stage === 'route' &&
      next.routing.target === path &&
      next.routing.operationId === next.identity.operationId &&
      ['intent_persisted', 'route_activated', 'health_verified'].includes(next.routing.stage || '');
    if (pending.current && !confirmed(next, pending.current) && !recoverableRoute)
      throw new Error('Original write is unconfirmed');
    pending.current = null;
    setUnknown(false);
    if (next.adminInitialized) setKey('');
    return next;
  }
  async function write(
    current: FreshOperation,
    stage: Pending['stage'],
    action: () => Promise<FreshOperation>
  ) {
    pending.current = { revision: current.revision, stage, previous: current.strategy?.stage };
    try {
      const next = accept(await action());
      if (!confirmed(next, pending.current)) throw new Error('Original write is unconfirmed');
      pending.current = null;
      setUnknown(false);
      return next;
    } catch (err) {
      try {
        return await read();
      } catch {
        /* Keep the original uncertain write, never replay it. */
      }
      if (
        err instanceof BootstrapTransportError &&
        (err.kind === 'bad_request' ||
          (strategy === 'connect_external' && stage === 'strategy' && err.kind === 'conflict'))
      ) {
        pending.current = null;
        setStep(1);
      }
      setUnknown(pending.current !== null);
      throw err;
    }
  }
  async function task(action: () => Promise<void>) {
    if (inflight.current) return;
    inflight.current = true;
    setBusy(true);
    setError('');
    setNote('');
    try {
      await action();
    } catch {
      if (alive.current) {
        setError(j('setupError'));
        setUnknown(pending.current !== null);
      }
    } finally {
      inflight.current = false;
      if (alive.current) setBusy(false);
    }
  }
  async function review() {
    if (
      (!operation.adminInitialized && !validateFreshAdminKey(key).valid) ||
      !validControlBasePath(path)
    ) {
      setError(j('invalidSetup'));
      return;
    }
    await task(async () => {
      if (strategy === 'new_managed')
        setTarget(managedTarget(await transport.request('installation/runtime')));
      else {
        setBaseURL(externalURL(baseURL));
        if (!cpaKey || cpaKey.length > 4096 || /[\s\p{Cc}]/u.test(cpaKey))
          throw new Error('Invalid CPA key');
      }
      setSavedKey(false);
      setStep(2);
    });
  }
  async function execute() {
    if (pending.current || (!operation.adminInitialized && !savedKey)) return;
    await task(async () => {
      setStep(3);
      let current = await read();
      if (!current.adminInitialized) {
        current = await write(current, 'admin', () =>
          transport.commitAdminKey(commitID.current, key)
        );
        setKey('');
        setVisible(false);
      }
      for (let action = 0; action < 3 && current.strategy?.stage !== 'committed'; action++) {
        let body: Record<string, unknown>;
        if (strategy === 'connect_external') {
          body = { strategy, baseUrl: baseURL, managementKey: cpaKey, attemptId: attemptID() };
        } else {
          const checkpoint = current.strategy;
          const observed = checkpoint?.strategy
            ? null
            : target || managedTarget(await transport.request('installation/runtime'));
          body = {
            strategy,
            expectedRuntimeIdentity: checkpoint?.targetIdentity || observed!.runtimeIdentity,
            expectedRuntimeGeneration: checkpoint?.targetGeneration || observed!.runtimeGeneration,
            expectedArtifactId:
              checkpoint?.targetArtifactId || observed!.activeGatewayArtifact.artifactId,
            // Every New Managed attempt carries the same persisted decision, so a
            // retried destination publishes what the operator chose and nothing
            // else. Adoption and External never send it.
            ...(strategy === 'new_managed' && plugins ? { pluginsEnabled: true } : {}),
          };
        }
        current = await write(current, 'strategy', () => transport.executeStrategy(body));
        setCPAKey('');
        if (current.strategy?.stage === 'starting') {
          setNote(j('waitingRuntime'));
          return;
        }
      }
      if (current.strategy?.stage !== 'committed') throw new Error('CPA not committed');
      if (strategy === 'new_managed') {
        const status = managedTarget(await transport.request('installation/runtime'));
        if (
          status.runtimeIdentity !== current.strategy.targetIdentity ||
          status.runtimeGeneration !== current.strategy.targetGeneration ||
          status.activeGatewayArtifact.artifactId !== current.strategy.targetArtifactId
        )
          throw new Error('CPA identity changed');
        if (status.state !== 'ready') {
          setNote(j('waitingRuntime'));
          return;
        }
      }
      if (!current.routing.target) current = accept(await transport.prepareRouting(path));
      if (!current.routing.committed) {
        pending.current = { revision: current.revision, stage: 'route' };
        await transport.activateRouting();
        current = await read();
      } else {
        // Also install the canonical carrier when resuming a lost activation reply.
        await transport.activateRouting();
      }
      if (!current.productComplete)
        current = await write(current, 'complete', () => transport.complete());
      accept(current);
    });
  }
  const complete = operation.productComplete === true;
  const summary = [
    [s('choiceTitle'), strategy === 'new_managed' ? s('newManaged') : s('external')],
    [j('key'), operation.adminInitialized ? j('keepKey') : j('keyHidden')],
    [s('consoleAddress'), path],
    ...(strategy === 'connect_external' && baseURL ? [[t('externalBaseURL'), baseURL]] : []),
    ...(strategy === 'new_managed'
      ? [[j('plugins'), plugins ? j('enabled') : j('disabled')]]
      : []),
  ];
  return (
    <section className="cpamp-installation" data-testid="fresh-setup-wizard" aria-busy={busy}>
      <div className="wizard-card">
        <ol className="journey-steps" aria-label={s('installation')}>
          {(['choose', 'configure', 'confirm', 'execute'] as const).map((label, index) => (
            <li
              key={label}
              className={complete || index < step ? 'done' : index === step ? 'active' : ''}
              aria-current={!complete && index === step ? 'step' : undefined}
            >
              <span className="journey-step-dot">
                {complete || index < step ? <FullWebIcon name="check" size={22} /> : index + 1}
              </span>
              <span>{s(label)}</span>
            </li>
          ))}
        </ol>
        <div className="journey-body">
          <div className={`journey-heading ${complete ? 'journey-success-heading' : ''}`}>
            {complete && (
              <div className="journey-success-symbol">
                <FullWebIcon name="check" size={28} />
              </div>
            )}
            <div>
              <h1 className="wizard-title">
                {complete
                  ? s('ready')
                  : step === 0
                    ? j('welcome')
                    : step === 1
                      ? j('configureHeading')
                      : step === 2
                        ? s('reviewTitle')
                        : note
                          ? j('waitingRuntime')
                          : s(busy ? 'installing' : unknown ? 'unknown' : 'failure')}
              </h1>
              <p className="wizard-desc">
                {complete
                  ? j('successHelp')
                  : step === 0
                    ? j('chooseHelp')
                    : step === 1
                      ? j('draftHelp')
                      : step === 2
                        ? s('reviewHelp')
                        : j('progressHelp')}
              </p>
            </div>
          </div>
          {error && (
            <p role="alert" className="alert alert-warn">
              {error}
            </p>
          )}
          {note && (
            <p role="status" className="alert alert-info">
              {note}
            </p>
          )}
          {unknown && (
            <p role="status" className="alert alert-warn">
              {s('unknownHelp')}
            </p>
          )}
          {step === 0 && (
            <div>
              {(['new_managed', 'connect_external'] as const).map((value) => (
                <button
                  type="button"
                  key={value}
                  data-testid={`choose-${value}`}
                  className={`journey-choice ${strategy === value ? 'selected' : ''}`}
                  aria-pressed={strategy === value}
                  disabled={!!operation.strategy?.strategy}
                  onClick={() => {
                    setStrategy(value);
                    setTarget(null);
                  }}
                >
                  <span className="journey-radio" aria-hidden="true" />
                  <span className="journey-tile-icon">
                    <FullWebIcon name={value === 'new_managed' ? 'server' : 'link'} size={30} />
                  </span>
                  <span>
                    <h2>{s(value === 'new_managed' ? 'newManaged' : 'external')}</h2>
                    <p>{s(value === 'new_managed' ? 'newDetail' : 'externalHelp')}</p>
                  </span>
                </button>
              ))}
              <div className="journey-choice is-unavailable" aria-disabled="true">
                <span className="journey-radio" aria-hidden="true" />
                <span className="journey-tile-icon">
                  <FullWebIcon name="copy" size={30} />
                </span>
                <span>
                  <h2>{s('adopt')}</h2>
                  <p>{s('adoptHelp')}</p>
                </span>
              </div>
            </div>
          )}
          {step === 1 && (
            <div className="journey-configuration">
              {operation.adminInitialized ? (
                <p className="journey-key-strip">
                  {j('key')} · {j('keepKey')}
                </p>
              ) : (
                <div className="journey-field">
                  <label className="form-label" htmlFor="fresh-admin-key">
                    {j('key')}
                  </label>
                  <input
                    id="fresh-admin-key"
                    data-testid="admin-key-input"
                    className="form-input"
                    type={visible ? 'text' : 'password'}
                    value={key}
                    autoComplete="off"
                    spellCheck={false}
                    onChange={(e) => {
                      setKey(e.target.value);
                      setSavedKey(false);
                    }}
                  />
                  <p className="journey-help">{j('keyRules')}</p>
                  <div className="header-actions">
                    <button
                      type="button"
                      className="btn btn-secondary"
                      disabled={busy}
                      data-testid="generate-key-btn"
                      onClick={() =>
                        void task(async () => {
                          setKey(await transport.generateAdminKey());
                          setVisible(true);
                          setSavedKey(false);
                        })
                      }
                    >
                      {j('generate')}
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost"
                      onClick={() => setVisible(!visible)}
                    >
                      {j(visible ? 'hide' : 'show')}
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost"
                      disabled={!key}
                      onClick={() =>
                        void task(async () => {
                          try {
                            await navigator.clipboard.writeText(key);
                            setNote(j('copied'));
                          } catch {
                            setVisible(true);
                            setError(j('copyFailed'));
                          }
                        })
                      }
                    >
                      {j('copy')}
                    </button>
                  </div>
                </div>
              )}
              {strategy === 'new_managed' && (
                <div className="journey-field">
                  <label className="journey-checkline" htmlFor="setup-plugin-optin">
                    <input
                      id="setup-plugin-optin"
                      data-testid="plugin-optin-checkbox"
                      type="checkbox"
                      checked={plugins}
                      disabled={!!operation.strategy?.strategy}
                      onChange={(e) => setPlugins(e.target.checked)}
                    />
                    <span>{j('pluginsOptIn')}</span>
                  </label>
                  <p className="journey-help">{j('pluginsOptInHelp')}</p>
                </div>
              )}
              <div className="journey-field">
                <label className="form-label" htmlFor="setup-control-path">
                  {j('routeTitle')}
                </label>
                <input
                  id="setup-control-path"
                  data-testid="route-target-input"
                  className="form-input"
                  value={path}
                  disabled={!!operation.routing.target}
                  onChange={(e) => setPath(e.target.value)}
                />
                <p className="journey-help">{j('pathHelp')}</p>
              </div>
              {strategy === 'connect_external' && (
                <>
                  <div className="journey-field">
                    <label className="form-label" htmlFor="setup-cpa-url">
                      {t('externalBaseURL')}
                    </label>
                    <input
                      id="setup-cpa-url"
                      data-testid="external-base-url"
                      className="form-input"
                      autoComplete="off"
                      value={baseURL}
                      onChange={(e) => setBaseURL(e.target.value)}
                    />
                  </div>
                  <div className="journey-field">
                    <label className="form-label" htmlFor="setup-cpa-key">
                      {t('externalManagementKey')}
                    </label>
                    <input
                      id="setup-cpa-key"
                      data-testid="external-management-key"
                      className="form-input"
                      type="password"
                      autoComplete="off"
                      value={cpaKey}
                      onChange={(e) => setCPAKey(e.target.value)}
                    />
                  </div>
                  <p className="journey-help">{t('externalHelp')}</p>
                </>
              )}
            </div>
          )}
          {(step === 2 || complete) && (
            <>
              <dl className="journey-summary">
                {summary.map(([label, value]) => (
                  <div className="journey-summary-row" key={label}>
                    <FullWebIcon name="check" size={22} />
                    <dt>{label}</dt>
                    <dd>{value}</dd>
                  </div>
                ))}
              </dl>
              {!complete && (
                <>
                  <p className="alert alert-info">{j('noMigration')}</p>
                  <label className="journey-checkline">
                    <input
                      type="checkbox"
                      data-testid="confirm-key-saved-checkbox"
                      checked={savedKey}
                      onChange={(e) => setSavedKey(e.target.checked)}
                    />
                    {operation.adminInitialized ? s('acknowledgement') : j('savedConfirm')}
                  </label>
                </>
              )}
              {complete && <p className="journey-quiet">{j('bookmark')}</p>}
            </>
          )}
          {step === 3 && !complete && (
            <ol className="journey-stages" aria-live="polite">
              {[
                [j('administrator'), operation.adminInitialized],
                [
                  strategy === 'new_managed' ? s('newManaged') : s('external'),
                  operation.strategy?.stage === 'committed',
                ],
                [j('routeTitle'), operation.routing.committed],
                [s('complete'), complete],
              ].map(([label, done]) => (
                <li key={String(label)}>
                  <span className="journey-stage-mark">
                    <FullWebIcon name={done ? 'check' : 'clock'} size={22} />
                  </span>
                  <strong>{label}</strong>
                  <small>{s(done ? 'done' : 'pending')}</small>
                </li>
              ))}
            </ol>
          )}
        </div>
        <footer className="journey-footer">
          {complete ? (
            <a
              className="btn btn-primary"
              data-testid="enter-console"
              href={operation.routing.target}
              onClick={() => transport.clear()}
            >
              {j('finishLogin')}
              <FullWebIcon name="arrowRight" size={20} />
            </a>
          ) : (
            <>
              {step < 3 && (
                <button
                  type="button"
                  className="btn btn-secondary"
                  disabled={busy || step === 0}
                  onClick={() => {
                    setStep(step - 1);
                    setSavedKey(false);
                    setError('');
                  }}
                >
                  {s('back')}
                </button>
              )}
              {step === 0 && (
                <button
                  type="button"
                  className="btn btn-primary"
                  data-testid="setup-next"
                  onClick={() => setStep(1)}
                >
                  {s('next')}
                </button>
              )}
              {step === 1 && (
                <button
                  type="button"
                  className="btn btn-primary"
                  data-testid="setup-review"
                  disabled={busy}
                  onClick={() => void review()}
                >
                  {s('next')}
                </button>
              )}
              {step === 2 && (
                <button
                  type="button"
                  className="btn btn-primary"
                  data-testid="setup-execute"
                  disabled={busy || !savedKey}
                  onClick={() => void execute()}
                >
                  {s('submit')}
                </button>
              )}
              {step === 3 && (
                <>
                  <button
                    type="button"
                    className="btn btn-secondary"
                    data-testid="setup-refresh"
                    disabled={busy}
                    onClick={() =>
                      void task(async () => {
                        await read(true);
                      })
                    }
                  >
                    {s('refresh')}
                  </button>
                  <button
                    type="button"
                    className="btn btn-primary"
                    data-testid="setup-resume"
                    disabled={busy || unknown}
                    onClick={() => void execute()}
                  >
                    {s('recover')}
                  </button>
                </>
              )}
            </>
          )}
        </footer>
      </div>
    </section>
  );
}
