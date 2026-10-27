import { isRecord } from '../utils/helpers';
import type { PublicApiClient } from './publicApi';

// The Manager's gateway configuration contract. Every response is re-validated
// here: a malformed projection is a read failure, never a silently rendered page.

export type GatewayFieldType = 'int' | 'bool' | 'string' | 'image-mode';
export type GatewayFieldKind = 'number' | 'bool' | 'text' | 'duration' | 'url' | 'select';
export type GatewayPresence = 'present' | 'missing' | 'null' | 'unknown';
export type GatewayMode = 'managed' | 'external' | 'private' | 'unavailable';
export type GatewayOperationState = 'unknown' | 'persisted' | 'failed';

export interface GatewayCapability {
  mode: GatewayMode;
  readable: boolean;
  writable: boolean;
  rulesWritable: boolean;
  reason?: string;
}

export interface GatewayFieldView {
  path: string;
  type: GatewayFieldType;
  kind: GatewayFieldKind;
  group: string;
  presence: GatewayPresence;
  value: unknown;
  editable: boolean;
  /**
   * The CPA default this leaf resolves to while it is unset. Only a `*bool` leaf
   * whose nil and false differ carries it, because there a missing key is a third
   * state: the control offers inherit / on / off instead of inventing an off.
   * Presence of the key is the signal; its value is the label, never a truth test.
   */
  inherit?: boolean;
  reason?: string;
}

/** What one profile accepts in a single group save. */
export interface GatewayProfileLimits {
  singleEdit: boolean;
  useDefault: boolean;
}

export interface GatewayOperation {
  requestId: string;
  group: string;
  target: 'group' | 'family';
  state: GatewayOperationState;
  code?: string;
  http200?: boolean;
}

export interface GatewayGroupView {
  id: string;
  prefix: string;
  paths: string[];
  baseline: string;
  writable: boolean;
  reason?: string;
  persisted: string;
  effective: string;
  operation?: GatewayOperation;
}

export interface GatewayFamilyView {
  family: string;
  path: string;
  rules: unknown;
  baseline: string;
  writable: boolean;
  readOnly: boolean;
  reason?: string;
  /** The family's journaled operation: what a reload reconciles from. */
  operation?: GatewayOperation;
}

export interface GatewayView {
  capability: GatewayCapability;
  generation: string;
  groups: GatewayGroupView[];
  fields: GatewayFieldView[];
  families: GatewayFamilyView[];
  groupsInvalid: boolean;
  /** Emitted for the private profile only; absence really means no limits. */
  profileLimits?: GatewayProfileLimits;
}

export type GatewayEditAction = 'set' | 'default';

export interface GatewayEdit {
  path: string;
  action: GatewayEditAction;
  value?: unknown;
}

export interface GatewayDraft {
  action: GatewayEditAction;
  value: unknown;
}

export interface GatewayExpected {
  generation: string;
  baseline: string;
}

const FIELD_TYPES: Record<string, true> = { int: true, bool: true, string: true, 'image-mode': true };
const FIELD_KINDS: Record<string, true> = {
  number: true,
  bool: true,
  text: true,
  duration: true,
  url: true,
  select: true,
};
const PRESENCES: Record<string, true> = { present: true, missing: true, null: true, unknown: true };
const MODES: Record<string, true> = {
  managed: true,
  external: true,
  private: true,
  unavailable: true,
};
const STATES: Record<string, true> = { unknown: true, persisted: true, failed: true };

function text(value: unknown): string {
  return typeof value === 'string' ? value : '';
}

function parseOperation(value: unknown): GatewayOperation | undefined {
  if (!isRecord(value)) return undefined;
  const state = text(value.state);
  if (!STATES[state] || typeof value.requestId !== 'string') return undefined;
  return {
    requestId: value.requestId,
    group: text(value.group),
    target: value.target === 'family' ? 'family' : 'group',
    state: state as GatewayOperationState,
    ...(typeof value.code === 'string' && value.code ? { code: value.code } : {}),
    ...(value.http200 === true ? { http200: true } : {}),
  };
}

/**
 * The save shape one profile accepts. Absent means "no limits stated", which
 * keeps an older Manager working; only an explicit flag restricts the page.
 */
function parseProfileLimits(value: unknown): GatewayProfileLimits | undefined {
  if (!isRecord(value)) return undefined;
  if (typeof value.single_edit !== 'boolean' || typeof value.use_default !== 'boolean') {
    return undefined;
  }
  return { singleEdit: value.single_edit, useDefault: value.use_default };
}

export function parseGatewayView(value: unknown): GatewayView {
  if (!isRecord(value) || !isRecord(value.capability)) throw new Error('invalid gateway view');
  const capability = value.capability;
  const mode = text(capability.mode);
  if (!MODES[mode] || !Array.isArray(value.groups) || !Array.isArray(value.fields))
    throw new Error('invalid gateway view');
  // A capability that does not state each switch is not a capability: the page
  // would otherwise treat a missing field as "not permitted" by accident.
  if (
    typeof capability.readable !== 'boolean' ||
    typeof capability.writable !== 'boolean' ||
    typeof capability.rulesWritable !== 'boolean'
  )
    throw new Error('invalid gateway view');
  if (!Array.isArray(value.families)) throw new Error('invalid gateway view');
  const view: GatewayView = {
    generation: typeof value.generation === 'string' ? value.generation : '',
    capability: {
      mode: mode as GatewayMode,
      readable: capability.readable === true,
      writable: capability.writable === true,
      rulesWritable: capability.rulesWritable === true,
      ...(typeof capability.reason === 'string' && capability.reason
        ? { reason: capability.reason }
        : {}),
    },
    groups: value.groups.map((entry) => {
      if (!isRecord(entry) || typeof entry.id !== 'string' || typeof entry.baseline !== 'string')
        throw new Error('invalid gateway group');
      return {
        id: entry.id,
        prefix: text(entry.prefix),
        paths: Array.isArray(entry.paths) ? entry.paths.filter((path) => typeof path === 'string') : [],
        baseline: entry.baseline,
        writable: entry.writable === true,
        ...(typeof entry.reason === 'string' && entry.reason ? { reason: entry.reason } : {}),
        persisted: text(entry.persisted) || 'unknown',
        effective: text(entry.effective) || 'unknown',
        ...(parseOperation(entry.operation) ? { operation: parseOperation(entry.operation) } : {}),
      };
    }),
    fields: value.fields.map((entry) => {
      if (!isRecord(entry) || typeof entry.path !== 'string') throw new Error('invalid gateway field');
      const type = text(entry.type);
      const kind = text(entry.kind);
      const presence = text(entry.presence);
      if (!FIELD_TYPES[type] || !FIELD_KINDS[kind] || !PRESENCES[presence])
        throw new Error('invalid gateway field');
      return {
        path: entry.path,
        type: type as GatewayFieldType,
        kind: kind as GatewayFieldKind,
        group: text(entry.group),
        presence: presence as GatewayPresence,
        value: entry.value ?? null,
        editable: entry.editable === true,
        // A *bool leaf: the key's presence marks the third state, so only an
        // actual boolean is accepted as the default it resolves to.
        ...(typeof entry.inherit === 'boolean' ? { inherit: entry.inherit } : {}),
        ...(typeof entry.reason === 'string' && entry.reason ? { reason: entry.reason } : {}),
      };
    }),
    families: value.families.map((entry) => {
      if (!isRecord(entry) || typeof entry.family !== 'string') throw new Error('invalid gateway family');
      return {
        family: entry.family,
        path: text(entry.path),
        rules: Array.isArray(entry.rules) ? entry.rules : null,
        baseline: text(entry.baseline),
        writable: entry.writable === true,
        readOnly: entry.readOnly !== false,
        ...(typeof entry.reason === 'string' && entry.reason ? { reason: entry.reason } : {}),
        ...(parseOperation(entry.operation) ? { operation: parseOperation(entry.operation) } : {}),
      };
    }),
    groupsInvalid: value.groupsInvalid === true,
    ...(parseProfileLimits(value.profile_limits) ? { profileLimits: parseProfileLimits(value.profile_limits) } : {}),
  };
  return view;
}

export interface GatewayOperationResponse {
  operation: GatewayOperation;
  effective: 'unknown';
}

export function parseGatewayOperationResponse(value: unknown): GatewayOperationResponse {
  if (!isRecord(value) || value.effective !== 'unknown') throw new Error('invalid operation result');
  const operation = parseOperation(value.operation);
  if (!operation) throw new Error('invalid operation result');
  return { operation, effective: 'unknown' };
}

// 16 random bytes: the Manager journal key must be 32 lowercase hex characters,
// and a request ID is never reused.
export function newGatewayRequestId(issued: Set<string> = new Set()): string {
  for (;;) {
    const bytes = new Uint8Array(16);
    if (!globalThis.crypto?.getRandomValues) throw new Error('Secure random unavailable');
    globalThis.crypto.getRandomValues(bytes);
    const id = Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
    if (!issued.has(id)) {
      issued.add(id);
      return id;
    }
  }
}

export function readGatewayView(client: PublicApiClient, credential?: { kind: 'admin'; value: string }) {
  return client
    .get<unknown>('gateway/config', credential)
    .then((value) => parseGatewayView(value));
}

export function saveGatewayGroup(
  client: PublicApiClient,
  credential: { kind: 'admin'; value: string },
  groupId: string,
  body: { requestId: string; expected: GatewayExpected; edits: GatewayEdit[] }
) {
  return client
    .post<unknown>(`gateway/config/groups/${groupId}`, body, credential)
    .then((value) => parseGatewayOperationResponse(value));
}

export function saveGatewayFamily(
  client: PublicApiClient,
  credential: { kind: 'admin'; value: string },
  family: string,
  body: { requestId: string; expected: GatewayExpected; rules: unknown[] }
) {
  return client
    .post<unknown>(`gateway/config/rules/${family}`, body, credential)
    .then((value) => parseGatewayOperationResponse(value));
}

export function readGatewayOperation(
  client: PublicApiClient,
  credential: { kind: 'admin'; value: string },
  requestId: string,
  reconcile: boolean
) {
  const resource = `gateway/config/operations/${requestId}${reconcile ? '/reconcile' : ''}`;
  const read = reconcile
    ? client.post<unknown>(resource, {}, credential)
    : client.get<unknown>(resource, credential);
  return read.then((value) => parseGatewayOperationResponse(value));
}

/** The value a control shows for a server observation or a draft. */
export function draftFor(view: GatewayFieldView): GatewayDraft {
  if (view.presence === 'present') return { action: 'set', value: view.value };
  // A `*bool` leaf that is unset inherits CPA's own default; showing "off"
  // would invent a value the installation never chose.
  if (isInheritable(view)) return { action: 'default', value: '' };
  return { action: 'set', value: view.type === 'bool' ? false : '' };
}

/** A `*bool` leaf: an absent key and an explicit false are different states. */
export function isInheritable(view: GatewayFieldView): boolean {
  return view.type === 'bool' && typeof view.inherit === 'boolean';
}

/**
 * Whether the leaf currently holds no value of its own. An explicit null on a
 * `*bool` leaf resolves to the same CPA default as an absent key, so a draft
 * that inherits is not a change there.
 */
export function isUnset(view: GatewayFieldView): boolean {
  return view.presence === 'missing' || (isInheritable(view) && view.presence === 'null');
}

export type GatewayBoolState = 'inherit' | 'on' | 'off';

export function boolState(draft: GatewayDraft): GatewayBoolState {
  if (draft.action === 'default') return 'inherit';
  return draft.value === true ? 'on' : 'off';
}

export function boolDraft(state: GatewayBoolState): GatewayDraft {
  if (state === 'inherit') return { action: 'default', value: '' };
  return { action: 'set', value: state === 'on' };
}

export function draftEquals(view: GatewayFieldView, draft: GatewayDraft): boolean {
  if (draft.action === 'default') return isUnset(view);
  // An unset inheritable leaf has no value of its own, so any explicit value —
  // including false — is the change the user asked for.
  if (isInheritable(view) && isUnset(view)) return false;
  if (view.presence !== 'present') {
    return view.type === 'bool' ? draft.value === false : draft.value === '';
  }
  if (view.type === 'int') return Number(draft.value) === Number(view.value) && draft.value !== '';
  if (view.type === 'bool') return draft.value === view.value;
  return draft.value === view.value;
}

/**
 * Whether this profile accepts the draft as one save. The private H1 profile
 * writes one field per operation and cannot delete a leaf, so "use default" and
 * multi-edit saves are refused by the Manager; the page must not offer them.
 */
export function limitIssue(
  limits: GatewayProfileLimits | undefined,
  editCount: number
): 'single_edit' | null {
  return limits?.singleEdit === true && editCount > 1 ? 'single_edit' : null;
}

export function allowsDefaultEdit(limits: GatewayProfileLimits | undefined): boolean {
  return limits?.useDefault !== false;
}

/** The edit a dirty draft contributes to its group save. */
export function editFor(view: GatewayFieldView, draft: GatewayDraft): GatewayEdit {
  if (draft.action === 'default') return { path: view.path, action: 'default' };
  return { path: view.path, action: 'set', value: normalizeDraft(view, draft.value) };
}

export function normalizeDraft(view: GatewayFieldView, value: unknown): unknown {
  if (view.type === 'int') {
    const number = typeof value === 'number' ? value : Number(String(value).trim());
    return Number.isFinite(number) ? number : value;
  }
  if (view.type === 'bool') return value === true;
  return value;
}

/** Only the shape CPAMP owns is checked here; CPA owns every bound. */
export function draftIssue(view: GatewayFieldView, draft: GatewayDraft): 'required' | 'integer' | null {
  if (draft.action === 'default') return null;
  const value = draft.value;
  if (view.type === 'int') {
    const text = String(value ?? '').trim();
    if (text === '') return 'required';
    if (!/^-?\d+$/.test(text)) return 'integer';
    return null;
  }
  if (view.type === 'string' && (view.kind === 'url' || view.kind === 'duration')) {
    if (String(value ?? '').trim() === '') return 'required';
  }
  return null;
}
