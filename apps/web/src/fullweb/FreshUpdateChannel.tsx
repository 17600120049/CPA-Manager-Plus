import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { PublicApiError, type createPublicApiClient } from './publicApi';

const channels = ['auto', 'stable', 'rc', 'beta'] as const;
type Channel = (typeof channels)[number];
const resource = 'settings/update-channel';

function preference(result: unknown): Channel {
  const value = (result as { channel_preference?: unknown } | null)?.channel_preference;
  if (!channels.includes(value as Channel)) throw new Error('Invalid preference response');
  return value as Channel;
}

export function FreshUpdateChannel({
  client,
  credential,
  onUnauthorized,
}: {
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  onUnauthorized: () => void;
}) {
  const [saved, setSaved] = useState<Channel | null>(null);
  const [selected, setSelected] = useState<Channel>('auto');
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState('');
  const [message, setMessage] = useState('');
  const inFlight = useRef(false);
  const sequence = useRef(0);
  const auth = { kind: 'admin' as const, value: credential };

  function failure(err: unknown) {
    if (err instanceof PublicApiError && err.kind === 'authentication') onUnauthorized();
    else setError(t('channelUnavailable'));
  }

  // Keep an already rendered value while refreshing. A failed or malformed
  // read disables writes until a successful authoritative read.
  async function read() {
    if (inFlight.current) return;
    inFlight.current = true;
    const id = ++sequence.current;
    setBusy(true);
    setError('');
    setMessage('');
    try {
      const value = preference(await client.get(resource, auth));
      if (id !== sequence.current) return;
      setSaved(value);
      setSelected(value);
      setMessage(t('channelReloaded'));
    } catch (err) {
      if (id === sequence.current) failure(err);
    } finally {
      if (id === sequence.current) {
        inFlight.current = false;
        setBusy(false);
      }
    }
  }

  useEffect(() => {
    void read();
    return () => {
      // This is a request counter, not a DOM ref; retire every outstanding ID.
      // eslint-disable-next-line react-hooks/exhaustive-deps
      sequence.current++;
      inFlight.current = false;
    };
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

  async function save() {
    if (inFlight.current || saved === null || selected === saved || error) return;
    inFlight.current = true;
    const id = ++sequence.current;
    const wanted = selected;
    setBusy(true);
    setError('');
    setMessage('');
    try {
      const value = preference(await client.put(resource, { channel_preference: wanted }, auth));
      if (id !== sequence.current) return;
      if (value !== wanted) throw new Error('Unexpected save response');
      setSaved(value);
      setSelected(value);
      setMessage(t('channelSaved'));
    } catch (err) {
      if (id !== sequence.current) return;
      if (err instanceof PublicApiError && err.kind === 'authentication') {
        onUnauthorized();
        return;
      }
      // An HTTP error, timeout or malformed reply can hide a committed save.
      // Reconcile by GET only. Never automatically repeat a write, even if
      // another tab changed the value in the meantime.
      setError(t('channelUnknown'));
      try {
        const value = preference(await client.get(resource, auth));
        if (id !== sequence.current) return;
        setSaved(value);
        setSelected(value);
        setError('');
        setMessage(t('channelReconciled'));
      } catch (readError) {
        if (id === sequence.current) {
          if (readError instanceof PublicApiError && readError.kind === 'authentication')
            onUnauthorized();
          // Keep the unknown-result explanation; the reload button retries
          // only a read and the editor remains locked until it succeeds.
        }
      }
    } finally {
      if (id === sequence.current) {
        inFlight.current = false;
        setBusy(false);
      }
    }
  }

  return (
    <section className="console-keys" aria-labelledby="update-channel-title" aria-busy={busy}>
      <h2 id="update-channel-title">{t('channelTitle')}</h2>
      <p>{t('channelHelp')}</p>
      <label className="form-label" htmlFor="update-channel">
        {t('channelLabel')}
      </label>
      <div className="input-wrap">
        <select
          id="update-channel"
          className="form-input"
          value={selected}
          disabled={busy || saved === null || !!error}
          onChange={(event) => {
            setSelected(event.target.value as Channel);
            setMessage('');
          }}
        >
          {channels.map((channel) => (
            <option key={channel} value={channel}>
              {t(
                channel === 'auto'
                  ? 'channelAuto'
                  : channel === 'stable'
                    ? 'channelStable'
                    : channel === 'rc'
                      ? 'channelRc'
                      : 'channelBeta'
              )}
            </option>
          ))}
        </select>
      </div>
      {saved === null && !error && <p role="status">{t('channelLoading')}</p>}
      {error && (
        <p className="alert alert-warn" role="alert">
          {error}
        </p>
      )}
      {message && <p role="status">{message}</p>}
      <div className="wizard-card-footer">
        <button
          className="btn btn-primary"
          onClick={() => void save()}
          disabled={busy || saved === null || selected === saved || !!error}
        >
          {busy ? t('channelBusy') : t('channelSave')}
        </button>
        <button className="btn btn-secondary" onClick={() => void read()} disabled={busy}>
          {t('channelReload')}
        </button>
      </div>
    </section>
  );
}
