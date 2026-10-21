import { useEffect, useRef, useState } from 'react';
import { fullWebText as t } from '../i18n/fullweb';
import { PublicApiError, type createPublicApiClient } from './publicApi';

interface State {
  keyId: string;
  managerStaged: boolean;
  cpaConfigured: boolean;
  runtimeState: 'effect_unknown' | 'effective_observed';
  runtimeGeneration: string;
  observedFromMs: number;
  observedUntilMs: number;
  expiresAtMs: number;
  ingressAuthorized: false;
  installAvailable: boolean;
  attempted: boolean;
}

export function FreshKeyInstallation({
  id,
  client,
  credential,
  onUnauthorized,
  onPending,
  disabled,
}: {
  id: string;
  client: ReturnType<typeof createPublicApiClient>;
  credential: string;
  onUnauthorized: () => void;
  onPending: (pending: boolean) => void;
  disabled: boolean;
}) {
  const [state, setState] = useState<State | null>(null);
  const [busy, setBusy] = useState(false);
  const [message, setMessage] = useState('');
  const [expired, setExpired] = useState(true);
  const active = useRef(true);
  const inFlight = useRef(false);
  const used = useRef(false);
  // A lost POST reply stays locked until a fresh read sees the durable S1 attempt.
  const uncertainInstall = useRef(false);
  useEffect(() => {
    active.current = true;
    return () => {
      active.current = false;
    };
  }, []);
  useEffect(() => {
    setExpired(!state || state.expiresAtMs <= Date.now());
    if (!state || state.expiresAtMs <= Date.now()) return;
    const timer = setTimeout(() => setExpired(true), state.expiresAtMs - Date.now());
    return () => clearTimeout(timer);
  }, [state]);

  async function request(install: boolean) {
    if (
      inFlight.current ||
      disabled ||
      (install && (used.current || !state?.installAvailable || state.attempted))
    )
      return;
    inFlight.current = true;
    setBusy(true);
    setMessage(!install && uncertainInstall.current ? t('keysInstallUnknown') : '');
    if (install) {
      uncertainInstall.current = true;
      onPending(true); // synchronous before dispatch, including same-turn close/logout
      setState(null);
      used.current = true;
    }
    const auth = { kind: 'admin' as const, value: credential };
    const resource = `api-keys/${id}/initial-install`;
    try {
      // No client-supplied runtime identity, proof flags or secret.
      const value = install
        ? await client.post<State>(resource, {}, auth)
        : await client.get<State>(resource, auth);
      if (!active.current) return;
      if (
        !value ||
        value.keyId !== id ||
        value.managerStaged !== true ||
        value.ingressAuthorized !== false ||
        typeof value.cpaConfigured !== 'boolean' ||
        typeof value.attempted !== 'boolean' ||
        typeof value.installAvailable !== 'boolean' ||
        typeof value.runtimeGeneration !== 'string' ||
        !['effect_unknown', 'effective_observed'].includes(value.runtimeState) ||
        (value.installAvailable && value.attempted) ||
        (value.runtimeState === 'effective_observed' &&
          (!value.cpaConfigured ||
            !value.attempted ||
            !/^[1-9][0-9]*$/.test(value.runtimeGeneration) ||
            !Number.isSafeInteger(value.observedFromMs) ||
            !Number.isSafeInteger(value.observedUntilMs) ||
            !Number.isSafeInteger(value.expiresAtMs) ||
            value.observedFromMs <= 0 ||
            value.observedUntilMs < value.observedFromMs ||
            value.expiresAtMs <= value.observedUntilMs ||
            value.expiresAtMs - value.observedUntilMs > 5000 ||
            value.observedUntilMs > Date.now() + 1000))
      )
        throw new Error();
      if (install && !value.attempted) throw new Error('Missing durable install attempt');
      used.current ||= value.attempted;
      setState(value);
      if (uncertainInstall.current && value.attempted) {
        uncertainInstall.current = false;
        setMessage('');
        onPending(false);
      }
    } catch (err) {
      if (!active.current) return;
      setExpired(true);
      if (err instanceof PublicApiError && err.kind === 'authentication') onUnauthorized();
      else
        setMessage(
          !install && uncertainInstall.current
            ? `${t('keysInstallUnknown')} ${t('keysInstallUnavailable')}`
            : t(install ? 'keysInstallUnknown' : 'keysInstallUnavailable')
        );
    } finally {
      inFlight.current = false;
      if (active.current) setBusy(false);
    }
  }

  return (
    <section aria-label={t('keysInstallTitle')} aria-busy={busy}>
      <h4>{t('keysInstallTitle')}</h4>
      <p>{t('keysInstallScope')}</p>
      {busy && <p role="status">{t('keysLoading')}</p>}
      {message && <p role="status">{message}</p>}
      {state && (
        <>
          <p>{t('keysManagerStaged')}</p>
          <p>{t(state.cpaConfigured ? 'keysCPAConfigured' : 'keysCPAUnknown')}</p>
          <p>
            {t(
              state.runtimeState === 'effective_observed' &&
                !expired &&
                state.expiresAtMs > Date.now()
                ? 'keysLiveObserved'
                : 'keysLiveUnknown'
            )}
          </p>
          <p>{t('keysIngressPending')}</p>
          {state.observedUntilMs > 0 && (
            <p>
              {t('keysObservation')}: {state.runtimeGeneration} ·{' '}
              {new Date(state.observedFromMs).toISOString()} —{' '}
              {new Date(state.observedUntilMs).toISOString()}
            </p>
          )}
        </>
      )}
      <button
        className="btn btn-secondary"
        style={{ backgroundColor: 'var(--bg-elevated)' }}
        disabled={busy || disabled}
        onClick={() => void request(false)}
      >
        {t('keysCheckInstallation')}
      </button>
      {state?.installAvailable && !state.attempted && !used.current && (
        <button
          className="btn btn-primary"
          disabled={busy || disabled}
          onClick={() => void request(true)}
        >
          {t('keysInstallOne')}
        </button>
      )}
    </section>
  );
}
