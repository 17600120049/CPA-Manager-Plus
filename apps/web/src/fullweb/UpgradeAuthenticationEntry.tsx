import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { FullWebLoginPage } from './FullWebLoginPage';
import { upgradeEntryText as text } from '../i18n/upgradeEntry';
import {
  rememberUpgradeCredential,
  rememberedUpgradeCredential,
  UpgradeAuthenticationError,
  verifyUpgradeCredential,
} from './upgradeSession';

export function UpgradeAuthenticationEntry() {
  const { i18n } = useTranslation();
  const language = i18n.resolvedLanguage || i18n.language;
  const [revision, setRevision] = useState('');
  const [error, setError] = useState('');
  const [checking, setChecking] = useState(false);
  const credential = useRef('');
  const request = useRef<AbortController | null>(null);
  const sequence = useRef(0);
  const restored = useRef(false);
  const expiresAt = useRef(0);

  const revoke = useCallback((message = '') => {
    ++sequence.current;
    request.current?.abort();
    credential.current = '';
    expiresAt.current = 0;
    setRevision('');
    setChecking(false);
    setError(message);
  }, []);

  const authenticate = useCallback(
    async (key: string, remember: boolean, source?: string) => {
      const current = ++sequence.current;
      request.current?.abort();
      const controller = new AbortController();
      request.current = controller;
      // A pending verification never leaves the previous authorization visible.
      setRevision('');
      setChecking(true);
      setError('');
      const timeout = window.setTimeout(() => controller.abort(), 10_000);
      try {
        const next = await verifyUpgradeCredential(key, controller.signal, source);
        if (sequence.current !== current || controller.signal.aborted) return false;
        credential.current = key;
        expiresAt.current = Date.now() + 60_000;
        if (!source) rememberUpgradeCredential(key, remember);
        setRevision(next);
        return true;
      } catch (err) {
        if (sequence.current === current)
          revoke(text(err instanceof UpgradeAuthenticationError ? err.reason : 'network'));
        return false;
      } finally {
        window.clearTimeout(timeout);
        if (sequence.current === current) setChecking(false);
      }
    },
    [revoke]
  );

  const restore = useCallback(async () => {
    if (restored.current) return;
    restored.current = true;
    const candidate = rememberedUpgradeCredential();
    if (candidate) await authenticate(candidate, true);
  }, [authenticate]);

  const adapter = useMemo(
    () => ({
      address: window.location.origin,
      error,
      connectionHint: text('loginHint', language),
      credentialPlaceholder: text('placeholder', language),
      restore,
      authenticate: (key: string, remember: boolean) => authenticate(key, remember),
    }),
    [authenticate, error, language, restore]
  );

  useEffect(
    () => () => {
      ++sequence.current;
      request.current?.abort();
      credential.current = '';
    },
    []
  );

  useEffect(() => {
    if (!revision) return;
    const refresh = () => {
      if (Date.now() >= expiresAt.current) {
        revoke(text('expired'));
        return;
      }
      void authenticate(credential.current, false, revision);
    };
    const timer = window.setTimeout(refresh, 30_000);
    // BFCache/background returns must never reuse an expired observation.
    const visible = () => {
      if (document.visibilityState === 'visible') refresh();
    };
    const pagehide = () => revoke();
    document.addEventListener('visibilitychange', visible);
    window.addEventListener('pagehide', pagehide);
    return () => {
      window.clearTimeout(timer);
      document.removeEventListener('visibilitychange', visible);
      window.removeEventListener('pagehide', pagehide);
    };
  }, [authenticate, revision, revoke]);

  if (!revision)
    return (
      <>
        {checking && <p role="status">{text('checking')}</p>}
        <FullWebLoginPage adapter={adapter} />
      </>
    );

  return (
    <main className="bootstrap-main" data-testid="upgrade-authenticated">
      <section className="wizard-card wizard-container">
        <div className="wizard-card-header">
          <div className="wizard-step-tag">{text('verified')}</div>
          <h1 className="wizard-title">{text('title')}</h1>
          <p className="wizard-desc">{text('retained')}</p>
        </div>
        <div className="wizard-card-body">
          <div className="alert alert-warn" role="status">
            <div className="alert-content">
              <div className="alert-title">{text('blocked')}</div>
              <p>{text('boundary')}</p>
              <p>{text('next')}</p>
            </div>
          </div>
        </div>
        <div className="wizard-card-footer">
          <button className="btn btn-secondary" onClick={() => revoke()}>
            {text('logout')}
          </button>
          <button
            className="btn btn-primary"
            onClick={() => void authenticate(credential.current, false, revision)}
          >
            {text('verify')}
          </button>
        </div>
      </section>
    </main>
  );
}
