import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
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
}: {
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  operation: FreshOperation;
  onOperation: (operation: FreshOperation) => void;
  onUnauthorized: () => void;
  onPending: (pending: boolean) => void;
}) {
  const [observation, setObservation] = useState('');
  const [busy, setBusy] = useState(false);
  const [unknown, setUnknown] = useState(false);
  const [conflict, setConflict] = useState(false);
  const [error, setError] = useState('');
  const inFlight = useRef(false);
  const alive = useRef(true);
  const expectedTarget = useRef<ReturnType<typeof hostTarget> | null>(null);
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
    onOperation(next);
    setUnknown(false);
    onPending(false);
  }

  async function reconcile() {
    const next = await client.get<FreshOperation>('operation', auth);
    if (alive.current) accept(next);
  }

  async function act(write: boolean) {
    if (
      inFlight.current ||
      (write && (unknown || conflict || foreign || invalidCheckpoint || operation.productComplete))
    )
      return;
    let body: Record<string, unknown> = {};
    if (write && checkpoint?.stage !== 'committed') {
      try {
        if (!checkpoint?.strategy && !expectedTarget.current)
          expectedTarget.current = hostTarget(observation);
        body = {
          identity: operation.identity,
          expectedRevision: operation.revision,
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
        setUnknown(true);
        await client.post<FreshOperation>(
          checkpoint?.stage === 'committed' ? 'complete' : 'strategy',
          body,
          auth
        );
        // Read the durable checkpoint even when the write reply was received.
      }
      await reconcile();
    } catch (err) {
      if (!alive.current) return;
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        onUnauthorized();
        return;
      }
      const stopped = err instanceof PublicApiError && err.status === 409;
      if (stopped) setConflict(true);
      setError(t(stopped ? 'installConflict' : 'installUnavailable'));
      try {
        if (!write) throw err;
        await reconcile();
        if (!stopped) setError('');
      } catch (readError) {
        if (!alive.current) return;
        if (readError instanceof PublicApiError && readError.kind === 'authentication')
          onUnauthorized();
        else {
          setUnknown(true);
          onPending(true);
        }
      }
    } finally {
      if (alive.current) {
        inFlight.current = false;
        setBusy(false);
      }
    }
  }

  if (foreign) return null;

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
