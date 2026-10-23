import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { fullWebLanguage } from '../i18n/fullweb';
import { keyWorkspaceText as k } from '../i18n/freshKeyWorkspace';
import { FullWebIcon } from './FullWebIcon';
import './freshKeys.css';
import { PublicApiError, type createPublicApiClient } from './publicApi';
import { FreshKeyDetails } from './FreshKeyDetails';
import { FreshKeyRevocation } from './FreshKeyRevocation';
import { validKey, type Key } from './freshKeyMetadata';

function keyCreationTime(value: number) {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return { iso: undefined, label: '—' };
  return {
    iso: date.toISOString(),
    label: date.toLocaleString(fullWebLanguage(), {
      year: 'numeric',
      month: '2-digit',
      day: '2-digit',
      hour: '2-digit',
      minute: '2-digit',
    }),
  };
}

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
  const [search, setSearch] = useState('');
  const [filter, setFilter] = useState<'all' | 'saved' | 'replaced'>('all');
  const [detailPending, setDetailPending] = useState(false);
  const [revocationAvailable, setRevocationAvailable] = useState(false);
  const [operationPending, setOperationPending] = useState(false);
  const detailPendingRef = useRef(false);
  const creationPendingRef = useRef(false);
  const operationPendingRef = useRef(false);
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

  // Multiple Key operations share the Console exit lock; a completed read in
  // one panel must not release another panel's unresolved write.
  function reportPending() {
    onCreationPending(
      creationPendingRef.current || detailPendingRef.current || operationPendingRef.current
    );
  }

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
      operationPendingRef.current
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
      creationPendingRef.current = true;
      reportPending();
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
      creationPendingRef.current = false;
      reportPending();
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
      creationPendingRef.current = false;
      reportPending();
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

  const query = search.trim().toLocaleLowerCase();
  const visibleKeys = (keys || []).filter(
    (key) =>
      (filter === 'all' || (filter === 'replaced') === (key.lifecycle === 'superseded')) &&
      (!query ||
        [key.name, key.maskedPreview, key.id].some((value) =>
          value.toLocaleLowerCase().includes(query)
        ))
  );
  const savedCount = (keys || []).filter((key) => key.lifecycle !== 'superseded').length;
  const locked = busy || !!selected || detailPending || !!pending || !!secret;
  return (
    <section className="key-workspace" aria-labelledby="client-key-title" aria-busy={busy}>
      <header className="key-workspace-heading">
        <div>
          <h2 id="client-key-title">
            {t('keysTitle')} <span className="key-count">{keys?.length ?? '—'}</span>
          </h2>
          <p>{k('description')}</p>
        </div>
        <div className="key-toolbar-actions">
          <button
            className="btn btn-secondary"
            aria-label={t('refresh')}
            disabled={busy || !!selected || detailPending}
            onClick={() => void refresh()}
          >
            <FullWebIcon name="refresh" />
            {t('refresh')}
          </button>
          <button
            className="btn btn-primary"
            aria-label={t('keysCreate')}
            onClick={() => void createKey()}
            disabled={locked || operationPending || keys === null || !!error}
          >
            <FullWebIcon name="plus" />
            {t('keysCreate')}
          </button>
        </div>
      </header>
      {error && (
        <p role="alert" className="key-notice key-notice-warning">
          <FullWebIcon name="alert" />
          <span>{error}</span>
        </p>
      )}
      {pending && (
        <div role="status" className="key-creation-recovery">
          <FullWebIcon name="clock" />
          <div>
            <h3>{k('pending')}</h3>
            <p>{t('keysUnknown')}</p>
            <div className="key-toolbar-actions">
              <button
                className="btn btn-secondary"
                disabled={busy}
                onClick={() => void reconcile()}
              >
                {t('keysCheck')}
              </button>
              {retryAllowed && (
                <button
                  className="btn btn-secondary"
                  disabled={busy}
                  onClick={() => void createKey()}
                >
                  {t('keysRetry')}
                </button>
              )}
            </div>
          </div>
        </div>
      )}
      {confirmed && !secret && (
        <p role="status" className="key-notice">
          <FullWebIcon name="check" />
          <span>{t('keysConfirmed')}</span>
        </p>
      )}
      {secret && (
        <div className="key-secret-panel" role="status">
          <div className="key-secret-title">
            <span className="key-resource-icon">
              <FullWebIcon name="key" size={24} />
            </span>
            <div>
              <h3>{k('secretTitle')}</h3>
              <p>{t('keysCreated')}</p>
            </div>
          </div>
          <p>{k('secretHelp')}</p>
          <p className="key-once-note">{t('keysOnce')}</p>
          <div className="key-secret-value">
            <code data-testid="created-client-secret">{secret}</code>
          </div>
          <div className="key-toolbar-actions">
            <button
              className="btn btn-primary"
              aria-label={t('keysCopy')}
              data-testid="copy-created-client-secret"
              onClick={() => void copyCreatedSecret()}
            >
              <FullWebIcon name="copy" />
              {t('keysCopy')}
            </button>
            <button className="btn btn-secondary" onClick={dismissCreatedSecret}>
              {t('keysDismiss')}
            </button>
          </div>
          {copyFeedback && (
            <p role="status" data-testid="copy-created-client-secret-feedback">
              {t(copyFeedback === 'copied' ? 'keysCopied' : 'keysCopyFailed')}
            </p>
          )}
        </div>
      )}
      <div className="key-list-workspace" hidden={!!selected}>
        <div className="card key-filter-panel">
          <div className="key-filter-tabs" role="group" aria-label={k('list')}>
            {(['all', 'saved', 'replaced'] as const).map((value) => (
              <button
                key={value}
                className={`btn btn-sm ${filter === value ? 'btn-primary' : 'btn-ghost'}`}
                aria-pressed={filter === value}
                onClick={() => setFilter(value)}
              >
                {k(value)}{' '}
                <span>
                  {keys === null
                    ? '—'
                    : value === 'all'
                      ? keys.length
                      : value === 'saved'
                        ? savedCount
                        : keys.length - savedCount}
                </span>
              </button>
            ))}
          </div>
          <label className="key-search">
            <FullWebIcon name="search" />
            <input
              className="input"
              type="search"
              aria-label={k('search')}
              placeholder={k('search')}
              value={search}
              onChange={(event) => setSearch(event.target.value)}
            />
          </label>
        </div>
        <div className="card key-table-panel">
          <div className="key-table-heading">
            <div>
              <strong>{k('list')}</strong>
              <small>{k('scope')}</small>
            </div>
            <span className="key-table-count">
              {keys === null ? '—' : visibleKeys.length} / {keys?.length ?? '—'}
            </span>
          </div>
          {keys === null ? (
            !error && (
              <div className="key-empty" role="status">
                <FullWebIcon name="refresh" size={28} />
                <p>{t('keysLoading')}</p>
              </div>
            )
          ) : keys.length === 0 ? (
            <div className="key-empty">
              <span className="key-resource-icon">
                <FullWebIcon name="key" size={26} />
              </span>
              <h3>{k('emptyTitle')}</h3>
              <p>{k('emptyHelp')}</p>
              <small>{t('keysEmpty')}</small>
            </div>
          ) : visibleKeys.length === 0 ? (
            <div className="key-empty">
              <FullWebIcon name="search" size={28} />
              <h3>{k('noMatches')}</h3>
              <p>{k('noMatchesHelp')}</p>
              <button
                className="btn btn-secondary"
                onClick={() => {
                  setSearch('');
                  setFilter('all');
                }}
              >
                {k('clear')}
              </button>
            </div>
          ) : (
            <div className="key-table-scroll">
              <table className="key-table">
                <thead>
                  <tr>
                    {(
                      ['name', 'credential', 'lifecycle', 'created', 'runtime', 'actions'] as const
                    ).map((label) => (
                      <th key={label} scope="col">
                        {k(label)}
                      </th>
                    ))}
                  </tr>
                </thead>
                <tbody>
                  {visibleKeys.map((key) => (
                    <tr key={key.id} data-testid={'key-row-' + key.id}>
                      <td data-label={k('name')}>
                        <div className="key-name-cell">
                          <span className="key-resource-icon">
                            <FullWebIcon name="key" />
                          </span>
                          <div>
                            <strong>{key.name || t('keysUnnamed')}</strong>
                            <small title={key.id}>{key.id.slice(0, 8)}…</small>
                          </div>
                        </div>
                      </td>
                      <td data-label={k('credential')}>
                        <code>{key.maskedPreview}</code>
                      </td>
                      <td data-label={k('lifecycle')}>
                        <span
                          className={
                            'key-state-pill ' +
                            (key.lifecycle === 'superseded' ? 'is-muted' : 'is-saved')
                          }
                        >
                          {t(
                            key.lifecycle === 'superseded' ? 'keysRevokedHistory' : 'keysPersisted'
                          )}
                        </span>
                      </td>
                      <td data-label={k('created')}>
                        <time dateTime={keyCreationTime(key.createdAtMs).iso}>
                          {keyCreationTime(key.createdAtMs).label}
                        </time>
                      </td>
                      <td data-label={k('runtime')}>
                        <span className="key-state-pill is-pending">
                          {k(
                            key.runtimeState === 'effect_unknown' ? 'effectUnknown' : 'notApplied'
                          )}
                        </span>
                      </td>
                      <td data-label={k('actions')}>
                        <button
                          className="btn btn-ghost btn-xs key-detail-link"
                          data-testid={'open-key-' + key.id}
                          disabled={locked}
                          onClick={() => setSelected(key.id)}
                        >
                          {t('keysDetails')}
                          <FullWebIcon name="chevronRight" size={15} />
                        </button>
                      </td>
                    </tr>
                  ))}
                </tbody>
              </table>
            </div>
          )}
          {keys !== null && (
            <footer className="key-table-footer">
              <span>
                {k('listed')} {visibleKeys.length} {k('count')}
              </span>
              <span>
                {k('total')} {keys.length} {k('count')}
              </span>
            </footer>
          )}
        </div>
        <aside className="key-gateway-note">
          <FullWebIcon name="shield" />
          <div>
            <strong>{k('unavailable')}</strong>
            <p>{k('unavailableHelp')}</p>
            <small>{t('keysStaged')}</small>
          </div>
        </aside>
      </div>
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
            reportPending();
          }}
          onSaved={(key) =>
            setKeys((previous) => (previous || []).map((item) => (item.id === key.id ? key : item)))
          }
          onClose={() => setSelected('')}
        />
      )}
      <details className="card key-management-section" open={operationPending || undefined}>
        <summary>
          <FullWebIcon name="shield" />
          {k('manage')}
          <FullWebIcon name="chevron" size={15} />
        </summary>
        {revocationAvailable && keys ? (
          <FreshKeyRevocation
            keys={keys}
            client={client}
            credential={credential}
            disabled={locked}
            onUnauthorized={onUnauthorized}
            onPending={(value) => {
              operationPendingRef.current = value;
              setOperationPending(value);
              reportPending();
            }}
            onChanged={() => void refresh()}
          />
        ) : (
          <p>{t('keysRevokeUnavailable')}</p>
        )}
      </details>
    </section>
  );
}
