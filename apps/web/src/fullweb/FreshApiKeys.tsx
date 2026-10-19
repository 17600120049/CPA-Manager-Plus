import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { PublicApiError, type createPublicApiClient } from './publicApi';

interface Key {
  id: string;
  requestId: string;
  maskedPreview: string;
  createdAtMs: number;
  managerState: 'persisted';
  runtimeState: 'not_applied';
}

function validKey(value: Key): boolean {
  return (
    !!value &&
    /^[a-f0-9]{32}$/.test(value.id) &&
    /^[a-f0-9]{32}$/.test(value.requestId) &&
    /^cpamp_sk_…[a-f0-9]{4}$/.test(value.maskedPreview) &&
    Number.isSafeInteger(value.createdAtMs) &&
    value.createdAtMs > 0 &&
    value.managerState === 'persisted' &&
    value.runtimeState === 'not_applied'
  );
}

export function FreshApiKeys({
  client,
  credential,
  onUnauthorized,
}: {
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  onUnauthorized: () => void;
}) {
  const [keys, setKeys] = useState<Key[] | null>(null);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [pending, setPending] = useState('');
  const [retryAllowed, setRetryAllowed] = useState(false);
  const [secret, setSecret] = useState('');
  const [confirmed, setConfirmed] = useState(false);
  const alive = useRef(true);
  const inFlight = useRef(false);
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
    if (inFlight.current) return;
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
        result.revocationAvailable !== false
      )
        throw new Error();
      setKeys(result.keys);
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

  async function createKey() {
    if (inFlight.current || secret || (pending && !retryAllowed)) return;
    inFlight.current = true;
    setBusy(true);
    setError('');
    setConfirmed(false);
    setRetryAllowed(false);
    try {
      const requestId =
        pending ||
        Array.from(crypto.getRandomValues(new Uint8Array(16)), (byte) =>
          byte.toString(16).padStart(2, '0')
        ).join('');
      setPending(requestId);
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
          disabled={busy || !!secret || !!pending || keys === null || !!error}
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
          <button className="btn btn-secondary" onClick={() => setSecret('')}>
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
              <strong>{key.maskedPreview}</strong>
              <span>
                {t('keysIdentity')}: <code>{key.id}</code>
              </span>
              <span>{t('keysPersisted')}</span>
            </li>
          ))}
        </ul>
      )}
      <p>{t('keysRevokeUnavailable')}</p>
      <button className="btn btn-secondary" disabled={busy} onClick={() => void refresh()}>
        {t('refresh')}
      </button>
    </section>
  );
}
