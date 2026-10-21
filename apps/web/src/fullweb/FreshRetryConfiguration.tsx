import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { PublicApiError, type createPublicApiClient } from './publicApi';

const retryResource = 'gateway/retry/request-retry';
const retryOperationResource = 'gateway/retry/operations/';

type RetryTextKey =
  | 'retryPresenceMissing'
  | 'retryPresenceNull'
  | 'retryPresencePresent'
  | 'retryPresenceUnknown'
  | 'retryLoading'
  | 'retryReadUnavailable'
  | 'retryReadConflict'
  | 'retrySaveRejected'
  | 'retrySaveUnknown'
  | 'retrySaved'
  | 'retrySavedReadUnknown'
  | 'retryReconciled'
  | 'retryRecovered'
  | 'retryOperationUnknown'
  | 'retryOperationNotFound'
  | 'retryConflict'
  | 'retrySourceUnavailable'
  | 'retryWriterNotIsolated'
  | 'retryJournalInvalid'
  | 'retryNeedsReconciliation'
  | 'retryPermission'
  | 'retryValueRequired'
  | 'retryValueInvalid'
  | 'retryRequestIdUnavailable';

type RetryPresence = 'missing' | 'null' | 'present' | 'unknown';
type RetryOperationState = 'unknown' | 'persisted' | 'recovered';

interface RetryExpected {
  sourceId: string;
  generation: string;
  revision: number;
  sha256: string;
}

interface RetryOperation {
  requestId: string;
  value: number;
  sourceId: string;
  generation: string;
  revision: number;
  sha256: string;
  state: RetryOperationState;
}

interface RetryView {
  field: 'routing.retry.request-retry';
  presence: RetryPresence;
  value: number | null;
  source: string;
  authority: string;
  editable: boolean;
  reason: string;
  persisted: string;
  effective: 'unknown';
  concurrentProtection: string;
  expected?: RetryExpected;
  operation?: RetryOperation;
}

interface RetryOperationResponse {
  operation: RetryOperation;
  effective: 'unknown';
}

function isRecord(value: unknown): value is Record<string, unknown> {
  return !!value && typeof value === 'object' && !Array.isArray(value);
}

function isHex(value: unknown, length: number): value is string {
  return typeof value === 'string' && new RegExp(`^[a-f0-9]{${length}}$`).test(value);
}

function isRetryValue(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0 && value <= 10;
}

function isRevision(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
}

function isOperationState(value: unknown): value is RetryOperationState {
  return value === 'unknown' || value === 'persisted' || value === 'recovered';
}

function parseExpected(value: unknown): RetryExpected {
  if (
    !isRecord(value) ||
    typeof value.sourceId !== 'string' ||
    value.sourceId.length === 0 ||
    value.sourceId.length > 256 ||
    typeof value.generation !== 'string' ||
    value.generation.length === 0 ||
    value.generation.length > 64 ||
    !isRevision(value.revision) ||
    !isHex(value.sha256, 64)
  )
    throw new Error('Invalid retry source expectation');
  return {
    sourceId: value.sourceId,
    generation: value.generation,
    revision: value.revision,
    sha256: value.sha256,
  };
}

function parseOperation(value: unknown): RetryOperation {
  if (
    !isRecord(value) ||
    !isHex(value.requestId, 32) ||
    !isRetryValue(value.value) ||
    typeof value.sourceId !== 'string' ||
    value.sourceId.length === 0 ||
    value.sourceId.length > 256 ||
    typeof value.generation !== 'string' ||
    value.generation.length === 0 ||
    value.generation.length > 64 ||
    !isRevision(value.revision) ||
    !isHex(value.sha256, 64) ||
    !isOperationState(value.state)
  )
    throw new Error('Invalid retry operation');
  return {
    requestId: value.requestId,
    value: value.value,
    sourceId: value.sourceId,
    generation: value.generation,
    revision: value.revision,
    sha256: value.sha256,
    state: value.state,
  };
}

function parseRetryView(value: unknown): RetryView {
  if (!isRecord(value)) throw new Error('Invalid retry view');
  const presence = value.presence;
  if (
    presence !== 'missing' &&
    presence !== 'null' &&
    presence !== 'present' &&
    presence !== 'unknown'
  )
    throw new Error('Invalid retry presence');
  const parsedValue = presence === 'present' ? value.value : null;
  if (
    (presence === 'present' && !isRetryValue(parsedValue)) ||
    value.field !== 'routing.retry.request-retry' ||
    typeof value.source !== 'string' ||
    typeof value.authority !== 'string' ||
    typeof value.editable !== 'boolean' ||
    typeof value.reason !== 'string' ||
    typeof value.persisted !== 'string' ||
    value.effective !== 'unknown' ||
    typeof value.concurrentProtection !== 'string'
  )
    throw new Error('Invalid retry view');
  const safeValue: number | null = presence === 'present' ? (parsedValue as number) : null;
  let expected: RetryExpected | undefined;
  if (value.expected !== undefined) expected = parseExpected(value.expected);
  let operation: RetryOperation | undefined;
  if (value.operation !== undefined && value.operation !== null)
    operation = parseOperation(value.operation);
  return {
    field: value.field,
    presence,
    value: safeValue,
    source: value.source,
    authority: value.authority,
    editable: value.editable,
    reason: value.reason,
    persisted: value.persisted,
    effective: 'unknown',
    concurrentProtection: value.concurrentProtection,
    ...(expected ? { expected } : {}),
    ...(operation ? { operation } : {}),
  };
}

function parseRetryOperationResponse(value: unknown): RetryOperationResponse {
  if (!isRecord(value) || value.effective !== 'unknown')
    throw new Error('Invalid retry operation result');
  return { operation: parseOperation(value.operation), effective: 'unknown' };
}

function createRequestId(issued: Set<string>): string {
  const bytes = new Uint8Array(16);
  if (!globalThis.crypto?.getRandomValues) throw new Error('Secure random unavailable');
  globalThis.crypto.getRandomValues(bytes);
  const id = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
  if (!isHex(id, 32) || issued.has(id)) throw new Error('Unable to create a unique request ID');
  issued.add(id);
  return id;
}

function parseDraft(value: string): number | null {
  if (!/^(?:0|[1-9][0-9]*)$/.test(value)) return null;
  const parsed = Number(value);
  return isRetryValue(parsed) ? parsed : null;
}

function lockReasonKey(reason: string, code?: string): RetryTextKey {
  switch (code || reason) {
    case 'private_source_unavailable':
    case 'private_source_not_authorized':
      return 'retrySourceUnavailable';
    case 'config_key_writer_not_isolated':
      return 'retryWriterNotIsolated';
    case 'configuration_journal_invalid':
      return 'retryJournalInvalid';
    case 'original_operation_or_source_requires_reconciliation':
    case 'original_operation_unknown':
      return 'retryNeedsReconciliation';
    case 'source_or_operation_conflict':
      return 'retryConflict';
    default:
      return 'retryPermission';
  }
}

function responseOperation(error: unknown, expectedId?: string): RetryOperation | null {
  if (!(error instanceof PublicApiError) || !error.operationResult) return null;
  try {
    const operation = parseRetryOperationResponse(error.operationResult).operation;
    return !expectedId || operation.requestId === expectedId ? operation : null;
  } catch {
    return null;
  }
}

export function FreshRetryConfiguration({
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
  const [view, setView] = useState<RetryView | null>(null);
  const [draft, setDraft] = useState('');
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState<RetryTextKey | ''>('');
  const [error, setError] = useState<RetryTextKey | ''>('');
  const [lockCode, setLockCode] = useState('');
  const [operation, setOperation] = useState<RetryOperation | null>(null);
  const [pendingId, setPendingId] = useState('');
  const [operationUnknown, setOperationUnknown] = useState(false);
  const [readUnavailable, setReadUnavailable] = useState(false);
  const alive = useRef(true);
  const flight = useRef(false);
  const pendingIdRef = useRef('');
  const issuedIds = useRef(new Set<string>());
  const auth = { kind: 'admin' as const, value: credential };

  function setPendingState(value: boolean) {
    if (alive.current) onPending(value);
  }

  function acceptOperation(next: RetryOperation) {
    if (!alive.current) return;
    setOperation(next);
    if (next.state === 'unknown') {
      pendingIdRef.current = next.requestId;
      setPendingId(next.requestId);
      setOperationUnknown(true);
      setPendingState(true);
      return;
    }
    pendingIdRef.current = '';
    setPendingId('');
    setOperationUnknown(false);
    setPendingState(false);
  }

  function acceptView(next: RetryView) {
    if (!alive.current) return;
    setView(next);
    setDraft(next.presence === 'present' && next.value !== null ? String(next.value) : '');
    setReadUnavailable(false);
    setLockCode('');
    const existingId = pendingIdRef.current || pendingId;
    if (next.operation && (!existingId || next.operation.requestId === existingId)) {
      acceptOperation(next.operation);
    } else if (existingId) {
      pendingIdRef.current = existingId;
      setPendingId(existingId);
      setOperationUnknown(true);
      setPendingState(true);
    } else if (!operationUnknown) setPendingState(false);
  }

  function reportReadFailure(err: unknown) {
    if (!alive.current) return;
    if (err instanceof PublicApiError && err.kind === 'authentication') {
      onUnauthorized();
      return;
    }
    const recovered = responseOperation(err, pendingIdRef.current || pendingId || undefined);
    if (recovered) acceptOperation(recovered);
    if (err instanceof PublicApiError && err.status === 409) {
      setLockCode(err.code || 'source_or_operation_conflict');
      setError('retryReadConflict');
    } else {
      setError('retryReadUnavailable');
    }
    setReadUnavailable(true);
  }

  async function readView(): Promise<boolean> {
    try {
      const next = parseRetryView(await client.get(retryResource, auth));
      acceptView(next);
      return true;
    } catch (err) {
      reportReadFailure(err);
      return false;
    }
  }

  function beginFlight(): boolean {
    if (flight.current) return false;
    flight.current = true;
    setBusy(true);
    return true;
  }

  async function refresh() {
    if (!beginFlight()) return;
    setMessage('');
    setError('');
    await readView();
    if (alive.current) {
      flight.current = false;
      setBusy(false);
    }
  }

  function keepOriginalUnknown(id: string, text: RetryTextKey) {
    pendingIdRef.current = id;
    setPendingId(id);
    setOperationUnknown(true);
    setPendingState(true);
    setMessage(text);
  }

  async function handleMutationFailure(err: unknown, id: string) {
    if (err instanceof PublicApiError && err.kind === 'authentication') {
      onUnauthorized();
      return;
    }
    if (err instanceof PublicApiError && err.code === 'invalid_retry_request') {
      pendingIdRef.current = '';
      setPendingId('');
      setOperationUnknown(false);
      setPendingState(false);
      setMessage('retrySaveRejected');
      return;
    }
    const recovered = responseOperation(err, id);
    if (recovered) {
      acceptOperation(recovered);
      if (recovered.state !== 'unknown') {
        if (await readView())
          setMessage(recovered.state === 'recovered' ? 'retryRecovered' : 'retryReconciled');
        else setMessage('retrySavedReadUnknown');
        return;
      }
    } else {
      keepOriginalUnknown(id, 'retrySaveUnknown');
    }
    if (err instanceof PublicApiError && err.status === 409) setMessage('retryConflict');
    else setMessage('retrySaveUnknown');
  }

  async function save() {
    if (
      !view ||
      !view.editable ||
      view.presence === 'unknown' ||
      view.persisted !== 'persisted' ||
      view.reason !== '' ||
      !view.expected ||
      operationUnknown ||
      readUnavailable ||
      draft === '' ||
      parseDraft(draft) === null ||
      (view.presence === 'present' && String(view.value) === draft) ||
      !beginFlight()
    )
      return;
    const value = parseDraft(draft)!;
    let id = pendingIdRef.current;
    if (!id) {
      try {
        id = createRequestId(issuedIds.current);
      } catch {
        setError('retryRequestIdUnavailable');
        flight.current = false;
        setBusy(false);
        return;
      }
      pendingIdRef.current = id;
      setPendingId(id);
    }
    setMessage('');
    setError('');
    try {
      const result = parseRetryOperationResponse(
        await client.put(retryResource, { requestId: id, value, expected: view.expected }, auth)
      );
      if (result.operation.requestId !== id) throw new Error('Retry operation identity changed');
      acceptOperation(result.operation);
      if (result.operation.state === 'unknown') {
        setMessage('retryOperationUnknown');
      } else if (await readView()) {
        setMessage(result.operation.state === 'recovered' ? 'retryRecovered' : 'retrySaved');
      } else {
        setMessage('retrySavedReadUnknown');
      }
    } catch (err) {
      await handleMutationFailure(err, id);
    } finally {
      if (alive.current) {
        flight.current = false;
        setBusy(false);
      }
    }
  }

  async function inspectOriginal(reconcile: boolean) {
    const id = pendingIdRef.current || pendingId;
    if (!id || !beginFlight()) return;
    setError('');
    setMessage('');
    try {
      const result = parseRetryOperationResponse(
        reconcile
          ? await client.post(retryOperationResource + id + '/reconcile', {}, auth)
          : await client.get(retryOperationResource + id, auth)
      );
      if (result.operation.requestId !== id) throw new Error('Retry operation identity changed');
      acceptOperation(result.operation);
      if (result.operation.state === 'unknown') {
        setMessage('retryOperationUnknown');
      } else if (await readView()) {
        setMessage(result.operation.state === 'recovered' ? 'retryRecovered' : 'retryReconciled');
      } else {
        setMessage('retrySavedReadUnknown');
      }
    } catch (err) {
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        onUnauthorized();
      } else {
        const recovered = responseOperation(err, id);
        if (recovered) acceptOperation(recovered);
        else keepOriginalUnknown(id, reconcile ? 'retrySaveUnknown' : 'retryOperationUnknown');
        setMessage(
          err instanceof PublicApiError && err.status === 404
            ? 'retryOperationNotFound'
            : reconcile
              ? 'retrySaveUnknown'
              : 'retryOperationUnknown'
        );
        setPendingState(true);
      }
    } finally {
      if (alive.current) {
        flight.current = false;
        setBusy(false);
      }
    }
  }

  useEffect(() => {
    alive.current = true;
    setPendingState(false);
    void refresh();
    return () => {
      alive.current = false;
      onPending(false);
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  const currentValue =
    view?.presence === 'present' && view.value !== null
      ? String(view.value)
      : view?.presence === 'missing'
        ? t('retryPresenceMissing')
        : view?.presence === 'null'
          ? t('retryPresenceNull')
          : t('retryPresenceUnknown');
  const canEdit =
    !!view?.editable &&
    view.presence !== 'unknown' &&
    view.persisted === 'persisted' &&
    view.reason === '' &&
    !!view.expected &&
    !operationUnknown &&
    !readUnavailable;
  const inputError =
    draft === '' ? 'retryValueRequired' : parseDraft(draft) === null ? 'retryValueInvalid' : '';
  const lockReason = view ? lockReasonKey(view.reason, lockCode) : '';

  return (
    <section
      id="gateway-retry"
      className="console-keys retry-editor"
      aria-labelledby="gateway-retry-title"
      aria-busy={busy}
      data-testid="retry-editor"
    >
      <div className="console-keys-heading">
        <div>
          <p className="console-section-kicker">{t('retryNavigation')}</p>
          <h2 id="gateway-retry-title">{t('retryTitle')}</h2>
        </div>
        <span className={`badge ${canEdit ? 'badge-emerald' : 'badge-amber'}`}>
          {busy ? t('retryBusy') : canEdit ? t('retryEditable') : t('retryReadonly')}
        </span>
      </div>
      <p>{t('retryHelp')}</p>
      {message && (
        <p className="alert alert-info" role="status">
          {t(message)}
        </p>
      )}
      {error && (
        <p className="alert alert-warn" role="alert">
          {t(error)}
        </p>
      )}
      {view ? (
        <>
          <div className="info-card retry-facts">
            <div className="info-row">
              <span className="info-key">{t('retryField')}</span>
              <code className="info-val">{view.field}</code>
            </div>
            <div className="info-row">
              <span className="info-key">{t('retryCurrentValue')}</span>
              <span className="info-val">{currentValue}</span>
            </div>
            <div className="info-row">
              <span className="info-key">{t('retryPresence')}</span>
              <span className="info-val">
                {t(
                  `retryPresence${view.presence[0].toUpperCase()}${view.presence.slice(1)}` as RetryTextKey
                )}
              </span>
            </div>
            <div className="info-row">
              <span className="info-key">{t('retrySource')}</span>
              <span className="info-val">{view.source}</span>
            </div>
            <div className="info-row">
              <span className="info-key">{t('retryAuthority')}</span>
              <span className="info-val">{view.authority}</span>
            </div>
            <div className="info-row">
              <span className="info-key">{t('retryPermission')}</span>
              <span className="info-val">
                {view.editable ? t('retryPermissionEditable') : t('retryPermissionReadonly')}
              </span>
            </div>
            <div className="info-row">
              <span className="info-key">{t('retryPersisted')}</span>
              <span className="info-val">
                {view.persisted === 'persisted'
                  ? t('retryPersistedConfirmed')
                  : view.persisted === 'recovered'
                    ? t('retryPersistedRecovered')
                    : t('retryStatusUnknown')}
              </span>
            </div>
            <div className="info-row">
              <span className="info-key">{t('retryEffective')}</span>
              <span className="info-val">{t('retryEffectiveUnknown')}</span>
            </div>
            <div className="info-row">
              <span className="info-key">{t('retryProtection')}</span>
              <span className="info-val">{view.concurrentProtection}</span>
            </div>
          </div>
          {!canEdit && !readUnavailable && !operationUnknown && (
            <p className="alert alert-warn">{t(lockReason || 'retryPermission')}</p>
          )}
          {view.operation?.state === 'unknown' && (
            <p className="alert alert-warn">{t('retryOperationUnknown')}</p>
          )}
          <div className="form-group">
            <div className="form-label">
              <label htmlFor="gateway-retry-value">{t('retryEditLabel')}</label>
              <span className="form-hint">{t('retryEditHint')}</span>
            </div>
            <div className="input-wrap">
              <input
                id="gateway-retry-value"
                data-testid="retry-value"
                className="form-input"
                type="text"
                inputMode="numeric"
                value={draft}
                disabled={busy || !canEdit}
                aria-invalid={canEdit && !!inputError}
                onChange={(event) => {
                  setDraft(event.target.value);
                  setMessage('');
                }}
              />
            </div>
            {canEdit && inputError && (
              <span className="form-hint retry-input-error" data-testid="retry-input-error">
                {t(inputError)}
              </span>
            )}
          </div>
          <div className="wizard-card-footer">
            <button
              className="btn btn-primary"
              data-testid="retry-save"
              disabled={
                busy ||
                !canEdit ||
                !!inputError ||
                (view.presence === 'present' && String(view.value) === draft)
              }
              onClick={() => void save()}
            >
              {busy ? t('retrySaving') : t('retrySave')}
            </button>
            <button
              className="btn btn-secondary"
              data-testid="retry-refresh"
              disabled={busy}
              onClick={() => void refresh()}
            >
              {t('retryRefresh')}
            </button>
          </div>
        </>
      ) : (
        !error && <p role="status">{t('retryLoading')}</p>
      )}
      {(operation || (operationUnknown && pendingId)) && (
        <div className="console-key-result retry-operation" data-testid="retry-operation">
          <h3>{t('retryOperationTitle')}</h3>
          <p>
            {t('retryOperationId')}:{' '}
            <code data-testid="retry-operation-id">{operation?.requestId || pendingId}</code>
          </p>
          {operation ? (
            <p>
              {t('retryOperationState')}:{' '}
              {operation.state === 'unknown'
                ? t('retryOperationUnknown')
                : operation.state === 'recovered'
                  ? t('retryOperationRecovered')
                  : t('retryOperationPersisted')}
            </p>
          ) : (
            <p>{t('retryOperationUnknown')}</p>
          )}
          {operationUnknown && <p className="alert alert-warn">{t('retryOperationNoPutReplay')}</p>}
          {operationUnknown && (
            <div className="wizard-card-footer">
              <button
                className="btn btn-secondary"
                data-testid="retry-check-operation"
                disabled={busy}
                onClick={() => void inspectOriginal(false)}
              >
                {t('retryCheckOperation')}
              </button>
              <button
                className="btn btn-primary"
                data-testid="retry-reconcile"
                disabled={busy}
                onClick={() => void inspectOriginal(true)}
              >
                {t('retryReconcile')}
              </button>
            </div>
          )}
        </div>
      )}
    </section>
  );
}
