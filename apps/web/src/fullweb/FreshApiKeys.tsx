import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { PublicApiError, type createPublicApiClient } from './publicApi';
import { FreshKeyDetails } from './FreshKeyDetails';
import { FreshKeyRevocation } from './FreshKeyRevocation';
import { validKey, type Key } from './freshKeyMetadata';

export function FreshApiKeys({
  client,
  credential,
  onUnauthorized,
  onCreationPending,
}: {
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  onUnauthorized: () => void;
  onCreationPending: (pending: boolean) => void;
}) {
  const [keys, setKeys] = useState<Key[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [pending, setPending] = useState('');
  const [retryAllowed, setRetryAllowed] = useState(false);
  const [secret, setSecret] = useState('');
  const [copyFeedback, setCopyFeedback] = useState<'copied' | 'failed' | ''>('');
  const [confirmed, setConfirmed] = useState(false);
  const [selected, setSelected] = useState('');
  const [detailPending, setDetailPending] = useState(false);
  const [revocationAvailable, setRevocationAvailable] = useState(false);
  const [operationPending, setOperationPending] = useState(false);
  const detailPendingRef = useRef(false);
  const alive = useRef(true);
  const inFlight = useRef(false);
  const copyAttempt = useRef(0);
  const auth = { kind: 'admin' as const, value: credential };

  useEffect(() => {
    alive.current = true;
    return () => {
      alive.current = false;
    };
  }, []);

  function failure(err: unknown) {
    if (err instanceof PublicApiError && err.kind === 'authentication') onUnauthorized();
    else setError(t('keysUnavailable'));
  }

  async function refresh() {
    if (inFlight.current || selected || detailPendingRef.current) return;
    inFlight.current = true;
    setBusy(true);
    setError('');
    try {
      const result = await client.get<{
        keys: Key[];
        gatewayPending: boolean;
        revocationAvailable: boolean;
      }>('api-keys', auth);
      if (!alive.current) return;
      if (
        !Array.isArray(result.keys) ||
        !result.keys.every(validKey) ||
        new Set(result.keys.map((key) => key.id)).size !== result.keys.length ||
        result.gatewayPending !== true ||
        typeof result.revocationAvailable !== 'boolean'
      )
        throw new Error();
      setKeys(result.keys);
      setRevocationAvailable(result.revocationAvailable);
    } catch (err) {
      if (alive.current) failure(err);
    } finally {
      inFlight.current = false;
      if (alive.current) setBusy(false);
    }
  }

  // No credential, secret or request journal enters persistent browser storage.
  useEffect(() => {
    void refresh();
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  async function copyCreatedSecret() {
    if (!secret) return;
    const attempt = ++copyAttempt.current;
    setCopyFeedback('');
    try {
      if (!navigator.clipboard?.writeText) throw new Error('Clipboard unavailable');
      await navigator.clipboard.writeText(secret);
      if (alive.current && attempt === copyAttempt.current) setCopyFeedback('copied');
    } catch {
      if (alive.current && attempt === copyAttempt.current) setCopyFeedback('failed');
    }
  }

  function dismissCreatedSecret() {
    ++copyAttempt.current;
    setCopyFeedback('');
    setSecret('');
  }

  async function createKey() {
    if (
      inFlight.current ||
      selected ||
      detailPendingRef.current ||
      secret ||
      (pending && !retryAllowed) ||
      operationPending
    )
      return;
    inFlight.current = true;
    setBusy(true);
    setError('');
    setConfirmed(false);
    setRetryAllowed(false);
    ++copyAttempt.current;
    setCopyFeedback('');
    try {
      const requestId =
        pending ||
        Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
          byte.toString(16).padStart(2, '0')
        ).join('');
      setPending(requestId);
      onCreationPending(true);
      const result = await client.post<{ key: Key; createdSecret?: string }>(
        'api-keys',
        { requestId },
        auth
      );
      if (!alive.current) return;
      if (
        !validKey(result.key) ||
        result.key.requestId !== requestId ||
        (result.createdSecret !== undefined &&
          (!/^cpamp_sk_[a-f0-9]{64}$/.test(result.createdSecret) ||
            result.key.maskedPreview !== 'cpamp_sk_…' + result.createdSecret.slice(-4)))
      )
        throw new Error();
      setKeys((previous) => [
        ...(previous || []).filter((key) => key.id !== result.key.id),
        result.key,
      ]);
      setSecret(result.createdSecret || '');
      setConfirmed(true);
      setPending('');
      onCreationPending(false);
    } catch (err) {
      if (alive.current) {
        // HTTP errors are also reconciled: a proxy failure can lose a committed reply.
        failure(err);
      }
    } finally {
      inFlight.current = false;
      if (alive.current) setBusy(false);
    }
  }

  async function reconcile() {
    if (!pending || inFlight.current) return;
    inFlight.current = true;
    setBusy(true);
    setError('');
    try {
      const key = await client.get<Key>('api-keys/requests/' + pending, auth);
      if (!alive.current) return;
      if (!validKey(key) || key.requestId !== pending) throw new Error();
      setKeys((previous) => [...(previous || []).filter((item) => item.id !== key.id), key]);
      setConfirmed(true);
      setPending('');
      onCreationPending(false);
      setRetryAllowed(false);
    } catch (err) {
      if (alive.current) {
        if (err instanceof PublicApiError && err.status === 404) setRetryAllowed(true);
        else failure(err);
      }
    } finally {
      inFlight.current = false;
      if (alive.current) setBusy(false);
    }
  }

  return (
    <section className="console-keys" aria-labelledby="client-key-title" aria-busy={busy}>
      <div className="console-keys-heading">
        <h2 id="client-key-title">{t('keysTitle')}</h2>
        <button
          className="btn btn-primary"
          onClick={() => void createKey()}
          disabled={
            busy ||
            !!selected ||
            detailPending ||
            operationPending ||
            !!secret ||
            !!pending ||
            keys === null ||
            !!error
          }
        >
          {t('keysCreate')}
        </button>
      </div>
      <p className="alert alert-info">{t('keysStaged')}</p>
      {error && (
        <p role="alert" className="alert alert-warn">
          {error}
        </p>
      )}
      {pending && (
        <div role="status" className="alert alert-warn">
          <p>{t('keysUnknown')}</p>
          <button className="btn btn-secondary" disabled={busy} onClick={() => void reconcile()}>
            {t('keysCheck')}
          </button>
          {retryAllowed && (
            <button className="btn btn-secondary" disabled={busy} onClick={() => void createKey()}>
              {t('keysRetry')}
            </button>
          )}
        </div>
      )}
      {confirmed && !secret && <p role="status">{t('keysConfirmed')}</p>}
      {secret && (
        <div className="console-key-result" role="status">
          <h3>{t('keysCreated')}</h3>
          <p>{t('keysOnce')}</p>
          <code data-testid="created-client-secret">{secret}</code>
          <button
            className="btn btn-secondary"
            data-testid="copy-created-client-secret"
            onClick={() => void copyCreatedSecret()}
          >
            {t('keysCopy')}
          </button>
          {copyFeedback && (
            <p role="status" data-testid="copy-created-client-secret-feedback">
              {t(copyFeedback === 'copied' ? 'keysCopied' : 'keysCopyFailed')}
            </p>
          )}
          <button className="btn btn-secondary" onClick={dismissCreatedSecret}>
            {t('keysDismiss')}
          </button>
        </div>
      )}
      {keys === null ? (
        !error && <p>{t('keysLoading')}</p>
      ) : keys.length === 0 ? (
        <p>{t('keysEmpty')}</p>
      ) : (
        <ul className="console-key-list">
          {keys.map((key) => (
            <li key={key.id}>
              <button
                className="btn btn-secondary"
                disabled={busy || !!selected || detailPending || !!pending || !!secret}
                onClick={() => setSelected(key.id)}
              >
                {key.name || t('keysUnnamed')} — {t('keysDetails')}
              </button>
              <strong>{key.maskedPreview}</strong>
              <span>
                {t('keysIdentity')}: <code>{key.id}</code>
              </span>
              <span>
                {t(key.lifecycle === 'superseded' ? 'keysRevokedHistory' : 'keysPersisted')}
              </span>
            </li>
          ))}
        </ul>
      )}
      {selected && (
        <FreshKeyDetails
          key={selected}
          id={selected}
          client={client}
          credential={credential}
          onUnauthorized={onUnauthorized}
          onPending={(value) => {
            detailPendingRef.current = value;
            setDetailPending(value);
            onCreationPending(value);
          }}
          onSaved={(key) =>
            setKeys((previous) => (previous || []).map((item) => (item.id === key.id ? key : item)))
          }
          onClose={() => setSelected('')}
        />
      )}
      {revocationAvailable && keys ? (
        <FreshKeyRevocation
          keys={keys}
          client={client}
          credential={credential}
          disabled={busy || !!selected || detailPending || !!pending || !!secret}
          onUnauthorized={onUnauthorized}
          onPending={(value) => {
            setOperationPending(value);
            onCreationPending(value);
          }}
          onChanged={() => void refresh()}
        />
      ) : (
        <p>{t('keysRevokeUnavailable')}</p>
      )}
      <button
        className="btn btn-secondary"
        disabled={busy || !!selected || detailPending}
        onClick={() => void refresh()}
      >
        {t('refresh')}
      </button>
    </section>
  );
}
