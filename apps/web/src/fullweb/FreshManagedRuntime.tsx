import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { shellText as s } from '../i18n/consoleShell';
import { sameIdentity, type FreshOperation } from './bootstrapTransport';
import { PublicApiError, type createPublicApiClient } from './publicApi';
import { parseRuntimeJSON, validRuntimeGeneration } from './runtimeGeneration';

// Host observation is a fence for the existing strategy, never qualification
// or authority to select a different target after the checkpoint is persisted.
function hostTarget(value: string) {
  const status = parseRuntimeJSON(value) as {
    runtimeIdentity?: unknown;
    runtimeGeneration?: unknown;
    activeGatewayArtifact?: { artifactId?: string };
  } | null;
  if (
    !status ||
    typeof status.runtimeIdentity !== 'string' ||
    !status.runtimeIdentity.trim() ||
    status.runtimeIdentity.length > 256 ||
    !validRuntimeGeneration(status.runtimeGeneration) ||
    !/^sha256:[a-f0-9]{64}$/.test(status.activeGatewayArtifact?.artifactId || '')
  )
    throw new Error('Invalid host observation');
  return {
    expectedRuntimeIdentity: status.runtimeIdentity,
    expectedRuntimeGeneration: status.runtimeGeneration,
    expectedArtifactId: status.activeGatewayArtifact!.artifactId as string,
  };
}

type PendingManagedWrite = {
  revision: number;
  action: 'stage' | 'start' | 'complete';
  previousStage?: string;
};

function confirmsManagedWrite(next: FreshOperation, pending: PendingManagedWrite): boolean {
  if (next.revision <= pending.revision) return false;
  if (pending.action === 'complete') return next.productComplete === true;
  if (!validManagedCheckpoint(next.strategy)) return false;
  if (pending.action === 'stage') return true;
  // Re-reading the same staged/starting state is not a receipt for Start.
  return pending.previousStage === 'starting'
    ? next.strategy?.stage === 'committed'
    : next.strategy?.stage === 'starting' || next.strategy?.stage === 'committed';
}

function validManagedCheckpoint(checkpoint: FreshOperation['strategy']) {
  return (
    checkpoint?.strategy === 'new_managed' &&
    typeof checkpoint.targetIdentity === 'string' &&
    !!checkpoint.targetIdentity.trim() &&
    validRuntimeGeneration(checkpoint.targetGeneration) &&
    /^sha256:[a-f0-9]{64}$/.test(checkpoint.targetArtifactId || '') &&
    ['staged', 'starting', 'committed'].includes(checkpoint.stage || '')
  );
}

export function FreshManagedRuntime({
  client,
  credential,
  operation,
  onOperation,
  onUnauthorized,
  onPending,
  guided = false,
  onContinue,
}: {
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  operation: FreshOperation;
  onOperation: (operation: FreshOperation) => void;
  onUnauthorized: () => void;
  onPending: (pending: boolean) => void;
  guided?: boolean;
  onContinue?: () => void;
}) {
  const [observation, setObservation] = useState('');
  const [busy, setBusy] = useState(false);
  const [unknown, setUnknown] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [error, setError] = useState('');
  const [step, setStep] = useState(operation.strategy?.strategy ? 3 : 0);
  const [acknowledged, setAcknowledged] = useState(false);
  const [running, setRunning] = useState(false);
  const [readiness, setReadiness] = useState(false);
  const [checking, setChecking] = useState(false);
  const runningRef = useRef(false);
  const inFlight = useRef(false);
  const alive = useRef(true);
  const expectedTarget = useRef<ReturnType<typeof hostTarget> | null>(null);
  const pendingWrite = useRef<PendingManagedWrite | null>(null);
  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);
  const auth = { kind: 'admin' as const, value: credential };
  const checkpoint = operation.strategy;
  const foreign = !!checkpoint?.strategy && checkpoint.strategy !== 'new_managed';
  const invalidCheckpoint = !!checkpoint?.strategy && !validManagedCheckpoint(checkpoint);

  function accept(next: FreshOperation) {
    if (
      !sameIdentity(next?.identity, operation.identity) ||
      next.adminInitialized !== true ||
      !Number.isSafeInteger(next.revision) ||
      next.revision < operation.revision ||
      next.gatewayPending !== true ||
      next.routing?.committed !== true ||
      next.routing.stage !== 'committed' ||
      next.routing.target !== operation.routing.target ||
      next.routing.revision !== operation.routing.revision ||
      next.routing.operationId !== operation.identity.operationId ||
      (next.strategy?.strategy && !validManagedCheckpoint(next.strategy)) ||
      (next.productComplete && next.strategy?.stage !== 'committed') ||
      (expectedTarget.current &&
        next.strategy?.strategy &&
        (next.strategy.targetIdentity !== expectedTarget.current.expectedRuntimeIdentity ||
          next.strategy.targetGeneration !== expectedTarget.current.expectedRuntimeGeneration ||
          next.strategy.targetArtifactId !== expectedTarget.current.expectedArtifactId)) ||
      (checkpoint?.strategy &&
        (next.strategy?.strategy !== checkpoint.strategy ||
          next.strategy.targetIdentity !== checkpoint.targetIdentity ||
          next.strategy.targetGeneration !== checkpoint.targetGeneration ||
          next.strategy.targetArtifactId !== checkpoint.targetArtifactId))
    )
      throw new Error('Invalid installation response');
    // A successful GET can still be a pre-write snapshot. Only the original
    // stage/start/complete transition may discharge an ambiguous POST.
    if (pendingWrite.current) {
      if (!confirmsManagedWrite(next, pendingWrite.current)) {
        throw new Error('Original installation write has not been confirmed');
      }
      pendingWrite.current = null;
    }
    onOperation(next);
    setUnknown(false);
    onPending(false);
  }

  async function reconcile() {
    const next = await client.get<FreshOperation>('operation', auth);
    if (alive.current) accept(next);
    return next;
  }

  async function act(write: boolean, current = operation) {
    if (
      inFlight.current ||
      (write && (unknown || conflict || foreign || invalidCheckpoint || current.productComplete))
    )
      return;
    let body: Record<string, unknown> = {};
    const checkpoint = current.strategy;
    if (write && checkpoint?.stage !== 'committed') {
      try {
        if (!checkpoint?.strategy && !expectedTarget.current)
          expectedTarget.current = hostTarget(observation);
        body = {
          identity: current.identity,
          expectedRevision: current.revision,
          strategy: 'new_managed',
          ...(checkpoint?.strategy
            ? {
                expectedRuntimeIdentity: checkpoint.targetIdentity,
                expectedRuntimeGeneration: checkpoint.targetGeneration,
                expectedArtifactId: checkpoint.targetArtifactId,
              }
            : expectedTarget.current),
        };
      } catch {
        setError(t('installObservationInvalid'));
        return;
      }
    }
    inFlight.current = true;
    setBusy(true);
    setError('');
    onPending(true);
    try {
      if (write) {
        pendingWrite.current = {
          revision: current.revision,
          action:
            checkpoint?.stage === 'committed'
              ? 'complete'
              : checkpoint?.strategy
                ? 'start'
                : 'stage',
          previousStage: checkpoint?.stage,
        };
        setUnknown(true);
        await client.post<FreshOperation>(
          checkpoint?.stage === 'committed' ? 'complete' : 'strategy',
          body,
          auth
        );
        // Read the durable checkpoint even when the write reply was received.
      }
      return await reconcile();
    } catch (err) {
      if (!alive.current) return;
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        onUnauthorized();
        return;
      }
      const stopped = err instanceof PublicApiError && err.status === 409;
      if (stopped && write) setConflict(true);
      setError(t(stopped ? 'installConflict' : 'installUnavailable'));
      try {
        if (!write) throw err;
        const next = await reconcile();
        if (!stopped) setError('');
        if (!stopped) return next;
      } catch (readError) {
        if (!alive.current) return;
        if (readError instanceof PublicApiError && readError.kind === 'authentication')
          onUnauthorized();
        else if (write) {
          setUnknown(true);
          onPending(true);
        } else {
          // A failed read alone is not an ambiguous write.
          onPending(unknown);
        }
      }
    } finally {
      if (alive.current) {
        inFlight.current = false;
        setBusy(false);
      }
    }
  }

  async function inspect() {
    if (checking || running) return;
    setChecking(true);
    setError('');
    try {
      const status = await client.get<unknown>('installation/runtime', auth);
      const raw = JSON.stringify(status);
      const target = hostTarget(raw);
      if (alive.current) {
        expectedTarget.current = target;
        setObservation(raw);
        setStep(2);
      }
    } catch (err) {
      if (alive.current) {
        if (err instanceof PublicApiError && err.kind === 'authentication') onUnauthorized();
        else setError(s('unavailable'));
      }
    } finally {
      if (alive.current) setChecking(false);
    }
  }

  async function run() {
    if (runningRef.current || inFlight.current || unknown || conflict) return;
    runningRef.current = true;
    setRunning(true);
    setStep(3);
    let current = operation;
    try {
      // Every next action consumes a confirmed durable read of this operation.
      // A lost reply with no confirming read pauses, never retries the write.
      for (let attempt = 0; attempt < 4 && alive.current && !current.productComplete; attempt++) {
        if (current.strategy?.stage === 'committed') {
          let ready = false;
          for (let probe = 0; probe < 30 && alive.current; probe++) {
            const status = await client.get<{ state: string }>('installation/runtime', auth);
            const target = hostTarget(JSON.stringify(status));
            if (
              target.expectedRuntimeIdentity !== current.strategy.targetIdentity ||
              target.expectedRuntimeGeneration !== current.strategy.targetGeneration ||
              target.expectedArtifactId !== current.strategy.targetArtifactId
            )
              throw new Error('Runtime identity changed');
            if (status.state === 'ready') {
              ready = true;
              break;
            }
            await new Promise((resolve) => setTimeout(resolve, 1000));
          }
          if (!alive.current) return;
          if (!ready) throw new Error('Readiness unavailable');
          setReadiness(true);
        }
        const next = await act(true, current);
        if (!next || !alive.current) return;
        current = next;
        if (current.strategy?.stage === 'starting') return;
      }
    } catch (err) {
      if (alive.current) {
        if (err instanceof PublicApiError && err.kind === 'authentication') onUnauthorized();
        else setError(s('failureHelp'));
      }
    } finally {
      runningRef.current = false;
      if (alive.current) setRunning(false);
    }
  }

  if (foreign) return null;

  if (guided) {
    const complete = operation.productComplete === true;
    const progress = complete
      ? 4
      : checkpoint?.stage === 'committed'
        ? readiness
          ? 3
          : 2
        : checkpoint?.stage === 'starting'
          ? 1
          : checkpoint?.stage === 'staged'
            ? 1
            : 0;
    return (
      <section
        className="installation-flow"
        data-testid="fresh-managed-runtime"
        aria-busy={running || checking}
      >
        <ol className="installation-steps" aria-label={s('installation')}>
          {(['choose', 'configure', 'confirm', 'execute', 'finish'] as const).map(
            (label, index) => (
              <li key={label} aria-current={(complete ? 4 : step) === index ? 'step' : undefined}>
                <span>{index + 1}</span>
                {s(label)}
              </li>
            )
          )}
        </ol>
        <div className="wizard-card">
          <div className="wizard-card-header">
            <h1>
              {complete
                ? s('ready')
                : step === 2
                  ? s('reviewTitle')
                  : step === 3
                    ? s('execute')
                    : s('newTitle')}
            </h1>
            <p className="wizard-desc">
              {complete ? s('readyHelp') : step === 2 ? s('reviewHelp') : s('newHelp')}
            </p>
          </div>
          <div className="wizard-card-body">
            {error && (
              <p className="alert alert-warn" role="alert">
                {error}
              </p>
            )}
            {unknown && (
              <div role="status" className="alert alert-warn">
                <strong>{s('unknown')}</strong>
                <p>{s('unknownHelp')}</p>
              </div>
            )}
            {invalidCheckpoint && <p role="alert">{s('failureHelp')}</p>}
            {!complete && step === 0 && (
              <div className="installation-choices">
                <div className="installation-choice selected">
                  <h2>{s('newManaged')}</h2>
                  <p>{s('newDetail')}</p>
                </div>
                <div className="installation-choice">
                  <h2>{s('adopt')}</h2>
                  <p>{s('adoptHelp')}</p>
                </div>
                <div className="installation-choice">
                  <h2>{s('external')}</h2>
                  <p>{s('externalHelp')}</p>
                </div>
                <p>{s('upgradeHelp')}</p>
                <button className="btn btn-primary" onClick={() => setStep(1)}>
                  {s('next')}
                </button>
              </div>
            )}
            {!complete && step === 1 && (
              <>
                <p>{s('configHelp')}</p>
                <dl className="console-state">
                  <dt>{s('consoleAddress')}</dt>
                  <dd>{operation.routing.target}</dd>
                  <dt>{s('destination')}</dt>
                  <dd>
                    {s('host')} · {s('version')}
                  </dd>
                </dl>
                <div className="wizard-card-footer">
                  <button
                    className="btn btn-secondary"
                    disabled={checking}
                    onClick={() => setStep(0)}
                  >
                    {s('back')}
                  </button>
                  <button
                    className="btn btn-primary"
                    data-testid="inspect-installation"
                    disabled={checking}
                    onClick={() => void inspect()}
                  >
                    {s(checking ? 'inspecting' : 'inspect')}
                  </button>
                </div>
              </>
            )}
            {!complete && step === 2 && (
              <>
                <dl className="console-state">
                  <dt>{s('destination')}</dt>
                  <dd>{s('host')}</dd>
                  <dt>CPA</dt>
                  <dd>{s('version')}</dd>
                  <dt>{s('consoleAddress')}</dt>
                  <dd>{operation.routing.target}</dd>
                  <dt>{s('observation')}</dt>
                  <dd>✓</dd>
                </dl>
                <label className="checkbox-card">
                  <input
                    type="checkbox"
                    checked={acknowledged}
                    onChange={(event) => setAcknowledged(event.target.checked)}
                  />
                  {s('acknowledgement')}
                </label>
                <div className="wizard-card-footer">
                  <button className="btn btn-secondary" onClick={() => setStep(1)}>
                    {s('back')}
                  </button>
                  <button
                    className="btn btn-primary"
                    data-testid="install-runtime"
                    disabled={!acknowledged || running}
                    onClick={() => void run()}
                  >
                    {s('submit')}
                  </button>
                </div>
              </>
            )}
            {(step === 3 || complete) && (
              <>
                <ol className="installation-progress" aria-live="polite">
                  {(['stage', 'start', 'verify', 'complete'] as const).map((label, index) => (
                    <li key={label}>
                      <span aria-hidden="true">{index < progress ? '✓' : index + 1}</span>
                      <strong>{s(label)}</strong>
                      <small>
                        {s(
                          index < progress
                            ? 'done'
                            : running && index === progress
                              ? 'active'
                              : 'pending'
                        )}
                      </small>
                    </li>
                  ))}
                </ol>
                <p data-testid="install-stage">
                  {complete
                    ? t('installComplete')
                    : unknown
                      ? s('unknown')
                      : running
                        ? s('installing')
                        : s('failure')}
                </p>
                <div className="wizard-card-footer">
                  {complete ? (
                    <button className="btn btn-primary" onClick={onContinue}>
                      {s('console')}
                    </button>
                  ) : (
                    <>
                      <button
                        className="btn btn-secondary"
                        data-testid="refresh-installation"
                        disabled={running || busy}
                        onClick={() => void act(false)}
                      >
                        {s('refresh')}
                      </button>
                      <button
                        className="btn btn-primary"
                        data-testid="install-runtime"
                        disabled={running || busy || unknown || conflict || invalidCheckpoint}
                        onClick={() => void run()}
                      >
                        {s(running ? 'installing' : 'recover')}
                      </button>
                    </>
                  )}
                </div>
              </>
            )}
            <details className="installation-diagnostics">
              <summary>{s('technical')}</summary>
              <dl className="console-state">
                <dt>{t('operation')}</dt>
                <dd>{operation.identity.operationId}</dd>
                <dt>{t('revision')}</dt>
                <dd>{operation.revision}</dd>
                <dt>{s('destination')}</dt>
                <dd>
                  {checkpoint?.targetIdentity ||
                    expectedTarget.current?.expectedRuntimeIdentity ||
                    '—'}
                </dd>
              </dl>
            </details>
          </div>
        </div>
      </section>
    );
  }

  return (
    <section className="info-card" data-testid="fresh-managed-runtime">
      <h2>{t('installTitle')}</h2>
      <p>{t('installHelp')}</p>
      <p data-testid="install-stage">
        {operation.productComplete
          ? t('installComplete')
          : checkpoint?.stage || t('installNotStarted')}
      </p>
      {error && (
        <p role="alert" className="alert alert-warn">
          {error}
        </p>
      )}
      {invalidCheckpoint && (
        <p role="alert" className="alert alert-warn">
          {t('installConflict')}
        </p>
      )}
      {unknown && (
        <p role="status" className="alert alert-warn">
          {t('installUnknown')}
        </p>
      )}
      {!checkpoint?.strategy && !operation.productComplete && (
        <label className="form-label">
          {t('installObservation')}
          <textarea
            className="form-input"
            data-testid="runtime-observation"
            value={observation}
            onChange={(event) => {
              if (!inFlight.current && !unknown && !expectedTarget.current)
                setObservation(event.target.value);
            }}
            disabled={busy || unknown || conflict || !!expectedTarget.current}
            autoComplete="off"
            spellCheck={false}
            rows={5}
          />
        </label>
      )}
      <div className="wizard-card-footer">
        {!operation.productComplete && !foreign && (
          <button
            className="btn btn-primary"
            data-testid="install-runtime"
            onClick={() => void act(true)}
            disabled={busy || unknown || conflict || invalidCheckpoint}
          >
            {t(
              checkpoint?.stage === 'committed'
                ? 'installFinish'
                : checkpoint?.strategy
                  ? 'installStart'
                  : 'installStage'
            )}
          </button>
        )}
        <button
          className="btn btn-secondary"
          data-testid="refresh-installation"
          onClick={() => void act(false)}
          disabled={busy}
        >
          {t('refresh')}
        </button>
      </div>
    </section>
  );
}
