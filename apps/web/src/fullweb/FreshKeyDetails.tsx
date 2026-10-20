import { useEffect, useRef, useState, type FormEvent } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { PublicApiError, type createPublicApiClient } from './publicApi';
import { validKey, type Key } from './freshKeyMetadata';

export function FreshKeyDetails({
  id,
  client,
  credential,
  onUnauthorized,
  onPending,
  onSaved,
  onClose,
}: {
  id: string;
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  onUnauthorized: () => void;
  onPending: (value: boolean) => void;
  onSaved: (key: Key) => void;
  onClose: () => void;
}) {
  const [record, setRecord] = useState<Key | null>(null);
  const [name, setName] = useState('');
  const [busy, setBusy] = useState(false);
  const [unknown, setUnknown] = useState(false);
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
    if (inFlight.current) return;
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
    if (inFlight.current || !record || unknown || name.trim() === record.name) return;
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
    if (inFlight.current || unknown) return;
    if (dirty && !window.confirm(t('keysDiscardName'))) return;
    onClose();
  }
  return (
    <section className="console-key-result" aria-labelledby="key-detail-title" aria-busy={busy}>
      <h3 id="key-detail-title">{t('keysDetails')}</h3>
      {busy && <p role="status">{t('keysLoading')}</p>}
      <p>
        {t('keysIdentity')}: <code>{id}</code>
      </p>
      {message && (
        <p role="status" className="alert alert-warn">
          {message}
        </p>
      )}
      {record && (
        <>
          <strong>{record.maskedPreview}</strong>
          <p>{t('keysPersisted')}</p>
          <form onSubmit={(event) => void save(event)}>
            <label htmlFor="key-name" className="form-label">
              {t('keysName')}
            </label>
            <input
              ref={input}
              id="key-name"
              className="form-input"
              value={name}
              maxLength={240}
              autoComplete="off"
              disabled={busy || unknown}
              onChange={(event) => setName(event.target.value)}
            />
            <p>{t('keysNameHelp')}</p>
            <button
              className="btn btn-primary"
              type="submit"
              disabled={
                busy || unknown || name.trim() === record.name || [...name.trim()].length > 120
              }
            >
              {t('keysSaveName')}
            </button>
          </form>
        </>
      )}
      <button className="btn btn-secondary" disabled={busy} onClick={() => void read()}>
        {t('keysReadDetails')}
      </button>
      <button className="btn btn-secondary" disabled={busy || unknown} onClick={close}>
        {t('keysCloseDetails')}
      </button>
    </section>
  );
}
