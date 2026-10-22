import { useEffect, useRef, useState } from 'react';
import { BrowserRouter, MemoryRouter, useInRouterContext } from 'react-router-dom';
import '../i18n';
import { AppLifecycle } from '../app/AppLifecycle';
import { MainLayout } from '../components/layout/MainLayout';
import { LoadingSpinner } from '../components/ui/LoadingSpinner';
import { apiClient } from '../services/api/client';
import { useAuthStore } from '../stores';
import { FreshManagedConfigPage } from './FreshManagedConfigPage';
import type { createPublicApiClient } from './publicApi';
import { resolveRoutingContext, type RoutingContext } from './routingContext';

// The legacy Console is deliberately a post-installation seam.  This module
// is loaded only after Fresh has authenticated the administrator and observed
// ProductComplete, so its auth/config stores cannot become an installation
// discovery or pre-admission transport.
export function LegacyConsolePage({
  context,
  credential,
  onUnauthorized,
  client,
}: {
  context: RoutingContext;
  credential: string;
  onUnauthorized: () => void;
  client?: ReturnType<typeof createPublicApiClient>;
}) {
  // Keep this hook unconditional: the legacy page can render once while the
  // in-memory credential is being installed, so router context must be read
  // before any early loading/unauthorized return.
  const inRouter = useInRouterContext();
  const [activated, setActivated] = useState(false);
  const [freshPending, setFreshPending] = useState(false);
  const onUnauthorizedRef = useRef(onUnauthorized);

  useEffect(() => {
    onUnauthorizedRef.current = onUnauthorized;
  }, [onUnauthorized]);

  useEffect(() => {
    const verified = resolveRoutingContext({
      state: 'verified_effective',
      verifiedEffectiveControlBasePath: context.basePath,
      revision: context.revision,
    });
    if (!verified || !credential) {
      onUnauthorizedRef.current();
      return;
    }

    // Keep the legacy client below the canonical Public API namespace. Its
    // normal request builder appends /v0/management, so the backend's bounded
    // Fresh adapter receives <base>/api/v2/cpamp/v0/management/*.
    const apiBase = new URL(`${verified.apiBase}/cpamp`, window.location.origin).toString();
    // Feature probes use the same bounded bridge. The public control prefix
    // itself intentionally does not expose the legacy usage-service paths.
    const panelBase = apiBase;

    // Clear any older panel session before installing the Fresh credential.
    // The key itself is only held by the in-memory Zustand state and the
    // axios client; rememberPassword stays false so persistence never stores it.
    try {
      useAuthStore.getState().logout();
    } catch {
      // Storage may be unavailable in a private or test browser. The state is
      // still replaced below, and the current Fresh credential remains scoped.
    }
    apiClient.setConfig({ apiBase, managementKey: credential });
    try {
      useAuthStore.setState({
        isAuthenticated: true,
        apiBase,
        managementKey: credential,
        rememberPassword: false,
        sessionMode: 'manager_embedded',
        sessionPanelBase: panelBase,
        connectionStatus: 'connected',
        connectionError: null,
        supportsPlugin: false,
        serverVersion: null,
        serverCommit: null,
        serverBuildDate: null,
      });
    } catch {
      // Persist is best-effort. Zustand applies the in-memory state before a
      // storage adapter reports an error, which is all this bridge requires.
    }
    // The store update and this local gate must both settle before mounting the
    // legacy tree. This is an intentional effect-to-view transition: the
    // credential has just been verified by the canonical Fresh boundary.
    setActivated(true); // eslint-disable-line react-hooks/set-state-in-effect

    return () => {
      setActivated(false);
      const current = useAuthStore.getState();
      if (current.managementKey === credential) {
        try {
          current.logout();
        } catch {
          // Keep unmount cleanup best-effort without retaining the credential.
          apiClient.setConfig({ apiBase: '', managementKey: '' });
        }
      }
    };
  }, [context.basePath, context.revision, credential]);

  const authenticated = useAuthStore((state) => state.isAuthenticated);
  useEffect(() => {
    if (activated && !authenticated) onUnauthorizedRef.current();
  }, [activated, authenticated]);

  if (!activated || !authenticated) {
    return (
      <div className="main-content" role="status">
        <LoadingSpinner />
      </div>
    );
  }

  const content = (
    <div data-testid="legacy-console">
      <AppLifecycle />
      {/*
       * The Full Web router is mounted below the embedded `/console` base.
       * MainLayout already supports an explicit route base (used by the demo
       * shell); passing it here keeps its active item, breadcrumbs and
       * MainRoutes in the same coordinate system as the mounted router.
       */}
      <MainLayout
        routeBase={context.basePath}
        navigationBase={context.basePath}
        logoutDisabled={freshPending}
        renderRoute={(pathname) => {
          if (!client) return undefined;
          // Keep the original ConfigPage at /config: it owns the visual/source
          // editor, Manager→CPA binding form, and the existing CPA API-key
          // controls. Fresh's Manager-owned credentials get their own explicit
          // route so adding Fresh New does not remove or replace the shipped
          // CPAMP configuration surface.
          if (pathname === '/api-keys') {
            return (
              <FreshManagedConfigPage
                client={client}
                credential={credential}
                onUnauthorized={onUnauthorizedRef.current}
                onPending={setFreshPending}
              />
            );
          }
          return undefined;
        }}
      />
    </div>
  );
  // The production Full Web entry normally supplies the BrowserRouter from
  // main.tsx. Keep a browser-backed fallback for direct mounts and embedded
  // hosts that render this seam without that provider: a MemoryRouter would
  // make MainLayout links change only an in-memory location, leaving the
  // address bar stuck at /console and making refresh/deep links impossible.
  if (inRouter) return content;
  // Non-DOM unit mounts provide only a minimal window stub; retain an
  // in-memory harness there while real browser mounts always get URL-backed
  // navigation.
  if (typeof window === 'undefined' || typeof window.history?.replaceState !== 'function') {
    return (
      <MemoryRouter basename={context.basePath} initialEntries={[context.basePath]}>
        {content}
      </MemoryRouter>
    );
  }
  return <BrowserRouter basename={context.basePath}>{content}</BrowserRouter>;
}
