import type { FullWebCredential, PublicApiClient } from '@/fullweb/publicApi';
import { isRecord } from '@/utils/helpers';

// The Manager's plugin runtime switch, as the plugin page reads and writes it.
//
// It is the plugin-scoped sibling of the gateway configuration page and shares
// that page's journal, CAS and read-back machinery, but it is a NARROWER
// projection: one group and one leaf. Its contract therefore lives here, with its
// only consumer, instead of in the Full Web gateway module — Panel Lite bundles
// this page and must never pull in the Full Web transport.
export const PLUGIN_RUNTIME_RESOURCE = 'plugins/runtime';
export const PLUGIN_SWITCH_GROUP = 'plugins';
export const PLUGIN_SWITCH_PATH = 'plugins.enabled';

export type PluginRuntimeOperationState = 'unknown' | 'persisted' | 'failed';

export interface PluginRuntimeOperation {
  requestId: string;
  state: PluginRuntimeOperationState;
  code?: string;
}

export interface PluginRuntimeCapability {
  mode: string;
  readable: boolean;
  writable: boolean;
  reason?: string;
}

export interface PluginRuntimeGroup {
  id: string;
  baseline: string;
  writable: boolean;
  persisted: string;
  effective: string;
  operation?: PluginRuntimeOperation;
}

export interface PluginRuntimeField {
  path: string;
  presence: string;
  value: unknown;
}

export interface PluginRuntimeView {
  capability: PluginRuntimeCapability;
  generation: string;
  groups: PluginRuntimeGroup[];
  fields: PluginRuntimeField[];
}

export interface PluginRuntimeOperationResponse {
  operation: PluginRuntimeOperation;
  effective: 'unknown';
}

const OPERATION_STATES: Record<string, true> = { unknown: true, persisted: true, failed: true };

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function parseOperation(value: unknown): PluginRuntimeOperation | undefined {
  if (!isRecord(value)) return undefined;
  const state = text(value.state);
  if (!OPERATION_STATES[state] || typeof value.requestId !== 'string' || !value.requestId)
    return undefined;
  return {
    requestId: value.requestId,
    state: state as PluginRuntimeOperationState,
    ...(typeof value.code === 'string' && value.code ? { code: value.code } : {}),
  };
}

/** A malformed projection is a read failure, never a silently rendered page. */
export function parsePluginRuntimeView(value: unknown): PluginRuntimeView {
  if (!isRecord(value) || !isRecord(value.capability)) throw new Error('invalid plugin runtime');
  const capability = value.capability;
  if (
    typeof capability.readable !== 'boolean' ||
    typeof capability.writable !== 'boolean' ||
    !Array.isArray(value.groups) ||
    !Array.isArray(value.fields)
  )
    throw new Error('invalid plugin runtime');
  return {
    capability: {
      mode: text(capability.mode),
      readable: capability.readable,
      writable: capability.writable,
      ...(typeof capability.reason === 'string' && capability.reason
        ? { reason: capability.reason }
        : {}),
    },
    generation: text(value.generation),
    groups: value.groups.map((entry) => {
      if (!isRecord(entry) || typeof entry.id !== 'string' || typeof entry.baseline !== 'string')
        throw new Error('invalid plugin runtime group');
      const operation = parseOperation(entry.operation);
      return {
        id: entry.id,
        baseline: entry.baseline,
        writable: entry.writable === true,
        persisted: text(entry.persisted) || 'unknown',
        effective: text(entry.effective) || 'unknown',
        ...(operation ? { operation } : {}),
      };
    }),
    fields: value.fields.map((entry) => {
      if (!isRecord(entry) || typeof entry.path !== 'string')
        throw new Error('invalid plugin runtime field');
      return { path: entry.path, presence: text(entry.presence), value: entry.value };
    }),
  };
}

export function parsePluginRuntimeOperationResponse(value: unknown): PluginRuntimeOperationResponse {
  if (!isRecord(value) || value.effective !== 'unknown') throw new Error('invalid operation result');
  const operation = parseOperation(value.operation);
  if (!operation) throw new Error('invalid operation result');
  return { operation, effective: 'unknown' };
}

// 16 random bytes: the Manager journal keys an operation by 32 lowercase hex
// characters, and a request ID is never reused.
export function newPluginRequestId(): string {
  const bytes = new Uint8Array(16);
  if (!globalThis.crypto?.getRandomValues) throw new Error('Secure random unavailable');
  globalThis.crypto.getRandomValues(bytes);
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

export function readPluginRuntime(client: PublicApiClient, credential?: FullWebCredential) {
  return client.get<unknown>(PLUGIN_RUNTIME_RESOURCE, credential).then(parsePluginRuntimeView);
}

/**
 * Writes exactly one leaf: `plugins.enabled`. `plugins.dir` is never written from
 * the page because the Runtime Supervisor owns the plugin root.
 */
export function savePluginEnabled(
  client: PublicApiClient,
  credential: FullWebCredential,
  body: { requestId: string; generation: string; baseline: string; enabled: boolean }
) {
  return client
    .post<unknown>(
      `${PLUGIN_RUNTIME_RESOURCE}/groups/${PLUGIN_SWITCH_GROUP}`,
      {
        requestId: body.requestId,
        expected: { generation: body.generation, baseline: body.baseline },
        edits: [{ path: PLUGIN_SWITCH_PATH, action: 'set', value: body.enabled }],
      },
      credential
    )
    .then(parsePluginRuntimeOperationResponse);
}

/** Read-back only: the reconcile endpoint never repeats the original write. */
export function reconcilePluginSwitch(
  client: PublicApiClient,
  credential: FullWebCredential,
  requestId: string
) {
  return client
    .post<unknown>(`${PLUGIN_RUNTIME_RESOURCE}/operations/${requestId}/reconcile`, {}, credential)
    .then(parsePluginRuntimeOperationResponse);
}
