import { useEffect, useRef, useState, type FormEvent } from 'react';
import { fullWebLanguage, fullWebText as t } from '../i18n/fullweb';
import { keyWorkspaceText as k } from '../i18n/freshKeyWorkspace';
import { FullWebIcon } from './FullWebIcon';
import './freshKeys.css';
import { PublicApiError, type createPublicApiClient } from './publicApi';
import { validKey, type Key } from './freshKeyMetadata';
import { FreshKeyInstallation } from './FreshKeyInstallation';

function detailTime(value: number) {
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

export function FreshKeyDetails({
  id,
  client,
  credential,
  onUnauthorized,
  onPending,
  onSaved,
  onClose,
  onRequestOperation,
}: {
  id: string;
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  onUnauthorized: () => void;
  onPending: (value: boolean) => void;
  onSaved: (key: Key) => void;
  onClose: () => void;
  onRequestOperation?: (mode: 'replace' | 'revoke') => void;
}) {
  const [record, setRecord] = useState<Key | null>(null);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [unknown, setUnknown] = useState(false);
  const [installationPending, setInstallationPending] = useState(false);
  const installationPendingRef = useRef(false);
  const [rejectedDraft, setRejectedDraft] = useState(false);
  const [message, setMessage] = useState('');
  const inFlight = useRef(false);
  const active = useRef(true);
  const input = useRef<HTMLInputElement>(null);
  const auth = { kind: 'admin' as const, value: credential };
  const resource = 'api-keys/' + id;
  const dirty = rejectedDraft || (record !== null && name.trim() !== record.name);

  // Each mounted record/session owns its replies. The parent keys this view by
  // canonical ID and unmounts the entire key page when authentication is cleared.
  useEffect(() => {
    active.current = true;
    const previous = typeof document !== 'undefined' ? document.activeElement : null;
    void read();
    return () => {
      active.current = false;
      if (
        typeof HTMLElement !== 'undefined' &&
        previous instanceof HTMLElement &&
        previous.isConnected
      )
        previous.focus();
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps
  useEffect(() => {
    if (record) input.current?.focus();
  }, [record]);

  function validate(value: Key) {
    if (!validKey(value) || value.id !== id) throw new Error();
    return value;
  }
  function failure(err: unknown) {
    if (err instanceof PublicApiError && err.kind === 'authentication') onUnauthorized();
    else setMessage(t('keysUnavailable'));
  }
  async function read() {
    if (inFlight.current || installationPendingRef.current) return;
    if (dirty && !unknown && !window.confirm(t('keysDiscardName'))) return;
    inFlight.current = true;
    setBusy(true);
    setMessage('');
    try {
      const value = validate(await client.get<Key>(resource, auth));
      if (!active.current) return;
      setRecord(value);
      setName(value.name);
      setRejectedDraft(false);
      onSaved(value);
      if (unknown) setMessage(t('keysNameChecked'));
      setUnknown(false);
      onPending(false);
    } catch (err) {
      if (active.current) failure(err);
    } finally {
      inFlight.current = false;
      if (active.current) setBusy(false);
    }
  }
  async function save(event: FormEvent) {
    event.preventDefault();
    if (
      inFlight.current ||
      installationPendingRef.current ||
      !record ||
      unknown ||
      name.trim() === record.name
    )
      return;
    inFlight.current = true;
    setBusy(true);
    setMessage('');
    onPending(true);
    const submitted = name.trim();
    try {
      const value = validate(
        await client.put<Key>(
          resource,
          { name: submitted, nameRevision: record.nameRevision },
          auth
        )
      );
      if (!active.current) return;
      if (value.name !== submitted || value.nameRevision !== record.nameRevision + 1)
        throw new Error();
      setRecord(value);
      setName(value.name);
      onSaved(value);
      setMessage(t('keysNameSaved'));
      onPending(false);
    } catch (err) {
      if (!active.current) return;
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        onUnauthorized();
      } else if (err instanceof PublicApiError && [400, 404, 409].includes(err.status || 0)) {
        // These local responses prove rejection. A fresh read is required
        // before another explicit save; preserve the draft until that check.
        setRejectedDraft(true);
        setRecord(null);
        setMessage(t(err.status === 409 ? 'keysNameConflict' : 'keysNameRejected'));
        onPending(false);
      } else {
        setUnknown(true);
        setMessage(t('keysNameUnknown'));
      }
    } finally {
      inFlight.current = false;
      if (active.current) setBusy(false);
    }
  }
  function close() {
    if (inFlight.current || installationPendingRef.current || unknown) return;
    if (dirty && !window.confirm(t('keysDiscardName'))) return;
    onClose();
  }
  return (
    <section
      className="card key-detail-workspace"
      aria-labelledby="key-detail-title"
      aria-busy={busy}
    >
      <header className="key-detail-heading">
        <div>
          <span className="key-detail-eyebrow">{t('keysDetails')}</span>
          <h3 id="key-detail-title">{record?.name || t('keysUnnamed')}</h3>
        </div>
        <button
          className="btn btn-secondary"
          aria-label={t('keysCloseDetails')}
          disabled={busy || unknown || installationPending}
          onClick={close}
        >
          <FullWebIcon name="arrowLeft" />
          {t('keysCloseDetails')}
        </button>
      </header>
      {busy && (
        <p role="status" className="key-notice">
          {t('keysLoading')}
        </p>
      )}
      {message && (
        <p role="status" className="key-notice key-notice-warning">
          <FullWebIcon name="info" />
          <span>{message}</span>
        </p>
      )}
      {record && (
        <>
          <div className="key-detail-hero">
            <span className="key-resource-icon">
              <FullWebIcon name="key" size={24} />
            </span>
            <div>
              <strong>{record.maskedPreview}</strong>
              <p>{k('privateKey')}</p>
            </div>
            <div className="key-detail-badges">
              <span
                className={
                  'key-state-pill ' +
                  (record.lifecycle === 'superseded' ? 'is-muted' : 'is-saved')
                }
              >
                {t(record.lifecycle === 'superseded' ? 'keysRevokedHistory' : 'keysPersisted')}
              </span>
              <span className="key-state-pill is-pending">
                {k(record.runtimeState === 'effect_unknown' ? 'effectUnknown' : 'notApplied')}
              </span>
            </div>
          </div>
          <dl className="key-detail-meta-grid" data-testid="key-detail-metadata">
            <div>
              <dt>{k('created')}</dt>
              <dd>
                <time dateTime={detailTime(record.createdAtMs).iso}>
                  {detailTime(record.createdAtMs).label}
                </time>
              </dd>
            </div>
            <div>
              <dt>{k('revisionLabel')}</dt>
              <dd>v{record.nameRevision}</dd>
            </div>
            <div>
              <dt>{k('policyScopeLabel')}</dt>
              <dd>{k('policyGlobalDefault')}</dd>
            </div>
          </dl>
          <div className="key-detail-columns">
            <section className="key-detail-section">
              <header>
                <FullWebIcon name="key" />
                <h4>{k('basic')}</h4>
              </header>
              <div className="key-detail-section-body">
                <p className="key-detail-description">{k('detailHelp')}</p>
                <form onSubmit={(event) => void save(event)}>
                  <label htmlFor="key-name" className="form-label">
                    {t('keysName')}
                  </label>
                  <input
                    ref={input}
                    id="key-name"
                    className="input"
                    value={name}
                    maxLength={240}
                    autoComplete="off"
                    disabled={busy || unknown || installationPending}
                    onChange={(event) => setName(event.target.value)}
                  />
                  <p className="key-field-help">{t('keysNameHelp')}</p>
                  <button
                    className="btn btn-primary"
                    type="submit"
                    disabled={
                      busy ||
                      unknown ||
                      installationPending ||
                      name.trim() === record.name ||
                      [...name.trim()].length > 120
                    }
                  >
                    {t('keysSaveName')}
                  </button>
                </form>
                <div className="key-private-note">
                  <FullWebIcon name="shield" />
                  <p>{k('privateKeyHelp')}</p>
                </div>
              </div>
            </section>
            <section className="key-detail-section">
              <header>
                <FullWebIcon name="gateway" />
                <h4>{k('access')}</h4>
              </header>
              <div className="key-detail-section-body">
                <p className="key-detail-description">{k('accessHelp')}</p>
                {record.lifecycle !== 'superseded' ? (
                  <FreshKeyInstallation
                    id={id}
                    client={client}
                    credential={credential}
                    onUnauthorized={onUnauthorized}
                    canInstall={() => !inFlight.current}
                    onPending={(pending) => {
                      installationPendingRef.current = pending;
                      setInstallationPending(pending);
                      onPending(pending);
                    }}
                    disabled={busy || unknown || dirty}
                  />
                ) : (
                  <p>{t('keysRevokedHistory')}</p>
                )}
              </div>
            </section>
            <section className="key-detail-section" data-testid="key-detail-usage-policy">
              <header>
                <FullWebIcon name="info" />
                <h4>{k('usageSectionTitle')}</h4>
              </header>
              <div className="key-detail-section-body">
                <dl className="key-usage-boundary-list">
                  <div>
                    <dt>{k('usageStatusLabel')}</dt>
                    <dd>
                      <span className="key-state-pill is-muted">{k('usageUnavailableBadge')}</span>
                    </dd>
                  </div>
                  <div>
                    <dt>{k('usageCoverageLabel')}</dt>
                    <dd>
                      <span className="key-state-pill is-muted">{k('usageUnavailableBadge')}</span>
                    </dd>
                  </div>
                  <div>
                    <dt>{k('policyScopeLabel')}</dt>
                    <dd>
                      <span className="key-state-pill is-saved">{k('policyGlobalDefault')}</span>
                    </dd>
                  </div>
                </dl>
                <p className="key-detail-description">{k('usageUnavailableDetail')}</p>
                <p className="key-field-help">{k('policyGlobalHelp')}</p>
              </div>
            </section>
            {record.lifecycle !== 'superseded' && onRequestOperation && (
              <section className="key-detail-section" data-testid="key-detail-operation-entry">
                <header>
                  <FullWebIcon name="shield" />
                  <h4>{k('manage')}</h4>
                </header>
                <div className="key-detail-section-body">
                  <p className="key-detail-description">{k('detailReplaceHelp')}</p>
                  <ol className="key-replace-steps">
                    <li>{k('replaceStep1')}</li>
                    <li>{k('replaceStep2')}</li>
                    <li>{k('replaceStep3')}</li>
                  </ol>
                  <div className="key-toolbar-actions">
                    <button
                      type="button"
                      className="btn btn-secondary"
                      data-testid="detail-replace-key"
                      disabled={busy || unknown || installationPending || dirty}
                      onClick={() => onRequestOperation('replace')}
                    >
                      {k('quickReplace')}
                    </button>
                    <button
                      type="button"
                      className="btn btn-ghost"
                      data-testid="detail-revoke-key"
                      disabled={busy || unknown || installationPending || dirty}
                      onClick={() => onRequestOperation('revoke')}
                    >
                      {k('quickRevoke')}
                    </button>
                  </div>
                </div>
              </section>
            )}
          </div>
        </>
      )}
      <footer className="key-detail-footer">
        <details className="key-record-identifiers">
          <summary>{k('diagnostics')}</summary>
          <dl>
            <dt>{t('keysIdentity')}</dt>
            <dd>
              <code>{id}</code>
            </dd>
          </dl>
        </details>
        <button
          className="btn btn-secondary"
          aria-label={t('keysReadDetails')}
          disabled={busy || installationPending}
          onClick={() => void read()}
        >
          <FullWebIcon name="refresh" />
          {t('keysReadDetails')}
        </button>
      </footer>
    </section>
  );
}
