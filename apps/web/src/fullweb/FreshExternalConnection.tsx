import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { sameIdentity, type FreshOperation } from './bootstrapTransport';
import { PublicApiError, type createPublicApiClient } from './publicApi';

type Client = ReturnType<typeof createPublicApiClient>;

const externalDigest = /^external:sha256:[a-f0-9]{64}$/;

// Restrict the browser to URLs whose canonical bytes match Go's strict v8
// normalizer. An ambiguity is refused before the one-shot POST, not guessed.
function canonicalExternalURL(raw: string): string | null {
  if (!raw || /[\\%@?#\s]/.test(raw)) return null;
  try {
    const url = new URL(raw);
    if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password ||
        url.search || url.hash) return null;
    const canonical = url.origin + url.pathname.replace(/\/+$/, '');
    return canonical === raw ? canonical : null;
  } catch {
    return null;
  }
}

async function targetDigest(normalizedURL: string): Promise<string> {
  if (!globalThis.crypto?.subtle) throw new Error('Secure origin required');
  const data = new TextEncoder().encode(normalizedURL);
  const digest = await globalThis.crypto.subtle.digest('SHA-256', data);
  return 'external:sha256:' + Array.from(new Uint8Array(digest), (value) =>
    value.toString(16).padStart(2, '0')).join('');
}


// A Manager checkpoint is not an acknowledgement of the browser's specific
// Management Key. Only POST-ack + a matching subsequent GET can confirm it.
function sameExternalReceipt(
  operation: FreshOperation,
  post: FreshOperation,
  read: FreshOperation,
  submittedTarget: string
): boolean {
  const sameRouting = (next: FreshOperation) =>
    next.routing.committed === true &&
    next.routing.stage === 'committed' &&
    next.routing.target === operation.routing.target &&
    next.routing.revision === operation.routing.revision &&
    next.routing.operationId === operation.identity.operationId;
  const valid = (next: FreshOperation) =>
    sameIdentity(next?.identity, operation.identity) &&
    next.adminInitialized === true &&
    next.gatewayPending === true &&
    next.productComplete === false &&
    sameRouting(next) &&
    next.strategy?.strategy === 'connect_external' &&
    next.strategy.stage === 'committed' &&
    next.strategy.targetGeneration === 1 &&
    externalDigest.test(next.strategy.targetIdentity || '') &&
    !next.strategy.targetArtifactId &&
    Number.isSafeInteger(next.revision);
  return (
    valid(post) &&
    valid(read) &&
    post.revision === operation.revision + 2 &&
    read.revision === post.revision &&
    read.strategy?.targetIdentity === post.strategy?.targetIdentity &&
    post.strategy?.targetIdentity === submittedTarget
  );
}

export function FreshExternalConnection({
  client,
  credential,
  operation,
  onOperation,
  onPending,
  onUnauthorized,
}: {
  client: Client;
  credential: string;
  operation: FreshOperation;
  onOperation: (next: FreshOperation) => void;
  onPending: (pending: boolean) => void;
  onUnauthorized: () => void;
}) {
  const [baseURL, setBaseURL] = useState('');
  const [managementKey, setManagementKey] = useState('');
  const [busy, setBusy] = useState(false);
  const [unknown, setUnknown] = useState(false);
  const [confirmed, setConfirmed] = useState(false);
  const [error, setError] = useState('');
  const inflight = useRef(false);
  const alive = useRef(true);
  const strategy = operation.strategy;
  const selected = strategy?.strategy === 'connect_external';
  const locked = !!strategy?.strategy;
  const hostStop = selected && strategy?.stage === 'validated';

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  useEffect(() => {
    if (hostStop) onPending(true);
  }, [hostStop, onPending]);

  const auth = { kind: 'admin' as const, value: credential };

  async function readCurrent(): Promise<FreshOperation> {
    const next = await client.get<FreshOperation>('operation', auth);
    if (
      !sameIdentity(next?.identity, operation.identity) ||
      !Number.isSafeInteger(next.revision) ||
      next.revision < operation.revision ||
      next.adminInitialized !== true ||
      next.gatewayPending !== true ||
      next.routing?.committed !== true ||
      next.routing.stage !== 'committed' ||
      next.routing.target !== operation.routing.target ||
      next.routing.revision !== operation.routing.revision ||
      next.routing.operationId !== operation.identity.operationId ||
      (strategy?.strategy && next.strategy?.strategy !== strategy.strategy) ||
      (strategy?.targetIdentity && next.strategy?.targetIdentity !== strategy.targetIdentity)
    ) {
      throw new Error('Untrusted External checkpoint');
    }
    if (alive.current) onOperation(next);
    return next;
  }

  async function refresh() {
    if (inflight.current) return;
    inflight.current = true;
    setBusy(true);
    setError('');
    try {
      await readCurrent();
      // UNKNOWN cannot be discharged by GET: no key identity or POST receipt.
    } catch (err) {
      if (!alive.current) return;
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        onUnauthorized();
        return;
      }
      setError(t('externalUnavailable'));
    } finally {
      inflight.current = false;
      if (alive.current) setBusy(false);
    }
  }

  async function submit() {
    if (inflight.current || locked || unknown || busy || confirmed) return;

    const submittedBaseURL = canonicalExternalURL(baseURL.trim());
    const submittedKey = managementKey;
    if (!submittedBaseURL || !submittedKey || submittedKey.trim() !== submittedKey ||
        submittedKey.length > 4096 || /[\x00-\x20\x7f]/.test(submittedKey)) {
      setError(t('externalInvalid'));
      return;
    }
    let submittedTarget: string;
    try {
      submittedTarget = await targetDigest(submittedBaseURL);
    } catch {
      setError(t('externalUnavailable'));
      return;
    }
    // After hashing, another click must not start a parallel request.
    if (inflight.current || locked || unknown || busy || confirmed) return;
    inflight.current = true;
    setBusy(true);
    setUnknown(true);
    setError('');
    // Never retain/reveal the Management Key once a write may have been sent.
    setManagementKey('');
    onPending(true);
    try {
      const post = await client.post<FreshOperation>(
        'strategy',
        {
          identity: operation.identity,
          expectedRevision: operation.revision,
          strategy: 'connect_external',
          baseUrl: submittedBaseURL,
          managementKey: submittedKey,
        },
        auth
      );
      if (
        !sameIdentity(post?.identity, operation.identity) ||
        post.strategy?.strategy !== 'connect_external' ||
        post.strategy.stage !== 'committed' ||
        !externalDigest.test(post.strategy.targetIdentity || '') ||
        post.gatewayPending !== true ||
        post.productComplete !== false
      ) {
        throw new Error('No trustworthy POST acknowledgement');
      }
      const read = await readCurrent();
      if (!sameExternalReceipt(operation, post, read, submittedTarget)) {
        throw new Error('Unmatched External receipt');
      }
      if (alive.current) {
        setConfirmed(true);
        setUnknown(false);
        onPending(false);
      }
    } catch (err) {
      if (!alive.current) return;
      // A 401/403 retires the Admin credential; a previously issued POST may
      // still have landed. The durable server fence, not this UI, owns it.
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        onUnauthorized();
        return;
      }
      setError(t(err instanceof PublicApiError && err.code === 'management_authentication_rejected'
        ? 'externalSourceAuthRejected'
        : err instanceof PublicApiError && err.status === 409 ? 'externalConflict' : 'externalUnknown'));
      try {
        await readCurrent();
      } catch (readError) {
        if (readError instanceof PublicApiError && readError.kind === 'authentication') {
          onUnauthorized();
          return;
        }
      }
      // Never clear UNKNOWN on a later GET, including a matching target hash.
    } finally {
      inflight.current = false;
      if (alive.current) setBusy(false);
    }
  }

  return (
    <section className="info-card" data-testid="fresh-external-connection">
      <h2>{t('externalTitle')}</h2>
      <p>{t('externalHelp')}</p>
      {confirmed ? (
        <p role="status" className="alert alert-info">{t('externalConfirmed')}</p>
      ) : locked ? (
        <p role="status" className="alert alert-warn">
          {hostStop ? t('externalHostStop') : t('externalExisting')}
        </p>
      ) : null}
      {(unknown || hostStop) && (
        <p role="status" className="alert alert-warn">{t('externalUnknown')}</p>
      )}
      {error && <p role="alert" className="alert alert-warn">{error}</p>}
      {!locked && !unknown && !confirmed && (
        <>
          <label className="form-label" htmlFor="fresh-external-base-url">{t('externalBaseURL')}</label>
          <input
            className="form-input"
            id="fresh-external-base-url"
            data-testid="external-base-url"
            value={baseURL}
            onChange={(event) => setBaseURL(event.target.value)}
            disabled={busy}
            autoComplete="off"
            spellCheck={false}
          />
          <label className="form-label" htmlFor="fresh-external-management-key">{t('externalManagementKey')}</label>
          <input
            className="form-input"
            id="fresh-external-management-key"
            type="password"
            data-testid="external-management-key"
            value={managementKey}
            onChange={(event) => setManagementKey(event.target.value)}
            disabled={busy}
            autoComplete="off"
            autoCorrect="off"
            autoCapitalize="off"
            spellCheck={false}
          />
        </>
      )}
      {locked && (
        <p data-testid="external-stage">{strategy?.stage}</p>
      )}
      <div className="wizard-card-footer">
        {!locked && !unknown && !confirmed && (
          <button type="button" className="btn btn-primary" data-testid="external-connect"
            disabled={busy || !baseURL.trim() || !managementKey}
            onClick={() => void submit()}>
            {busy ? t('externalSubmitting') : t('externalSubmit')}
          </button>
        )}
        <button type="button" className="btn btn-secondary" data-testid="external-refresh"
          disabled={busy} onClick={() => void refresh()}>
          {t('refresh')}
        </button>
      </div>
    </section>
  );
}
