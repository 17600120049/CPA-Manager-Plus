import { apiClient } from '../services/api/client';
import { useAuthStore } from '../stores/useAuthStore';
import type { RoutingContext } from './routingContext';

export function freshSessionBase(context: RoutingContext) {
  return new URL(`${context.apiBase}/cpamp`, window.location.origin).toString();
}

// A stored credential is only a candidate. FreshConsolePage must verify it
// through the canonical operation endpoint before activating any session.
export function rememberedFreshCredential(context: RoutingContext) {
  const state = useAuthStore.getState();
  const base = freshSessionBase(context);
  return state.rememberPassword &&
    state.sessionMode === 'manager_embedded' &&
    state.apiBase === base &&
    state.sessionPanelBase === base
    ? state.managementKey
    : '';
}

export function activateFreshSession(
  context: RoutingContext,
  credential: string,
  rememberPassword: boolean
) {
  const apiBase = freshSessionBase(context);
  const state = useAuthStore.getState();
  if (state.apiBase !== apiBase || state.managementKey !== credential) {
    state.logout();
  }
  apiClient.setConfig({ apiBase, managementKey: credential });
  useAuthStore.setState({
    isAuthenticated: true,
    apiBase,
    managementKey: credential,
    rememberPassword,
    sessionMode: 'manager_embedded',
    sessionPanelBase: apiBase,
    connectionStatus: 'connected',
    connectionError: null,
  });
}

export function clearFreshSession() {
  useAuthStore.getState().logout();
}
