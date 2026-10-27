import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { configurationText as c } from '../i18n/configurationWorkspace';
import type { GatewayFieldMeta } from './gatewayCatalog';
import type { RetryDraftDiff } from './FreshRetryConfiguration';
import { PublicApiError, type PublicApiClient } from './publicApi';

type Client = PublicApiClient;
type Expected = { sourceId: string; generation: string; revision: number; sha256: string };
type FieldView = {
  value?: unknown;
  presence: string;
  editable: boolean;
  expected?: Expected;
  operation?: { requestId?: string; state: string; code?: string };
  reason?: string;
};

function resource(path: string) {
  return `gateway/config/${path.split('.').join('/')}`;
}

// Every field-level message is a translated workspace string, never a server
// code or an English literal.
const fieldErrorText: Record<string, Parameters<typeof c>[0]> = {
  invalid: 'fieldInvalid',
  read_failed: 'fieldReadFailed',
  write_failed: 'fieldWriteFailed',
};

// A reason is a stable server code; the card renders its translation only.
const fieldReasonText: Record<string, Parameters<typeof c>[0]> = {
  configuration_journal_invalid: 'fieldReasonJournalInvalid',
  config_key_writer_not_isolated: 'fieldReasonKeyWriter',
  private_source_unavailable: 'fieldReasonSourceUnavailable',
  original_operation_or_source_requires_reconciliation: 'fieldReasonReconcile',
};

// 16 random bytes: the Manager journal key and Runtime operation ID must be
// 32 lowercase hex characters, and a request ID is never reused.
function requestId() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

// draftValue is the editor draft a server value maps to. A missing value is an
// empty integer draft or a switched-off boolean.
function draftValue(field: GatewayFieldMeta, view?: FieldView) {
  if (!view) return field.type === 'boolean' ? false : '';
  return view.value ?? (field.type === 'boolean' ? false : '');
}

// comparable normalises a draft and a server value so an integer never compares
// its number form against its string form: typing the saved value is not a change.
function comparable(field: GatewayFieldMeta, value: unknown) {
  if (field.type === 'integer') {
    if (value === '' || value === null || value === undefined) return '';
    const number = Number(value);
    return Number.isNaN(number) ? String(value) : String(number);
  }
  if (field.type === 'boolean') return String(Boolean(value));
  return String(value ?? '');
}

function invalidDraft(field: GatewayFieldMeta, draft: unknown) {
  if (field.type !== 'integer') return false;
  const text = String(draft ?? '');
  if (text.trim() === '') return true;
  const number = Number(text);
  return (
    !Number.isInteger(number) ||
    number < (field.min ?? 0) ||
    number > (field.max ?? Number.MAX_SAFE_INTEGER)
  );
}

export function RequestsConfigFieldGroup({
  fields,
  visiblePaths,
  client,
  credential,
  onUnauthorized,
  onPending,
  onDraftChange,
}: {
  fields: GatewayFieldMeta[];
  visiblePaths: ReadonlySet<string>;
  client: Client;
  credential: string;
  onUnauthorized: () => void;
  onPending: (pending: boolean) => void;
  onDraftChange: (diffs: RetryDraftDiff[]) => void;
}) {
  // The catalog identity is stable: it is derived from the section, never from a
  // draft, so typing cannot restart the loaders below. Visibility is applied at
  // render time instead.
  const editable = useMemo(() => fields.filter((field) => field.editable), [fields]);
  const shown = useMemo(
    () => editable.filter((field) => visiblePaths.has(field.path)),
    [editable, visiblePaths]
  );
  const [views, setViews] = useState<Record<string, FieldView>>({});
  const [drafts, setDrafts] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});
  // A path the user has edited is never overwritten by a load; only a save or an
  // explicit reconcile may replace its draft.
  const edited = useRef(new Set<string>());
  // Callback identities from the parent must never re-run a loader.
  const unauthorized = useRef(onUnauthorized);
  unauthorized.current = onUnauthorized;

  const load = useCallback(
    async (targets: GatewayFieldMeta[], reset: boolean) => {
      await Promise.all(
        targets.map(async (field) => {
          try {
            const view = await client.get<FieldView>(resource(field.path), {
              kind: 'admin',
              value: credential,
            });
            setViews((current) => ({ ...current, [field.path]: view }));
            if (reset) edited.current.delete(field.path);
            if (reset || !edited.current.has(field.path)) {
              setDrafts((current) => ({ ...current, [field.path]: draftValue(field, view) }));
            }
            setErrors((current) => ({ ...current, [field.path]: '' }));
          } catch (error) {
            if (error instanceof PublicApiError && error.kind === 'authentication')
              unauthorized.current();
            setErrors((current) => ({ ...current, [field.path]: 'read_failed' }));
          }
        })
      );
    },
    [client, credential]
  );

  // One load per catalog identity and credential. Drafts, visibility and the
  // dirty report are deliberately not dependencies: none of them may restart it.
  useEffect(() => {
    void load(editable, false);
  }, [editable, load]);

  const onDraft = useRef(onDraftChange);
  onDraft.current = onDraftChange;
  const emitted = useRef('');
  useEffect(() => {
    const diffs = editable.map((field) => {
      const view = views[field.path];
      const draft = drafts[field.path];
      return {
        path: field.path,
        label: field.label,
        oldVal: String(view?.value ?? ''),
        newVal: String(draft ?? ''),
        dirty: comparable(field, view?.value) !== comparable(field, draft),
        invalid: invalidDraft(field, draft),
      };
    });
    // The same draft state is reported once: the workspace re-derives its
    // catalog from these diffs, so a repeated report would re-render this group.
    const signature = JSON.stringify(diffs);
    if (signature === emitted.current) return;
    emitted.current = signature;
    onDraft.current(diffs);
  }, [drafts, editable, views]);

  const conflict = async (field: GatewayFieldMeta) => {
    // A stale expectation is repaired by one group re-read; drafts are untouched.
    await load(editable, false);
    setErrors((current) => ({ ...current, [field.path]: 'write_failed' }));
  };

  const reconcile = async (field: GatewayFieldMeta) => {
    const request = views[field.path]?.operation?.requestId;
    if (!request) return;
    setBusy((current) => ({ ...current, [field.path]: true }));
    onPending(true);
    try {
      await client.post(`gateway/config/operations/${request}/reconcile`, {}, {
        kind: 'admin',
        value: credential,
      });
      await load([field], true);
    } catch (error) {
      if (error instanceof PublicApiError && error.kind === 'authentication') onUnauthorized();
      setErrors((current) => ({ ...current, [field.path]: 'write_failed' }));
      await load([field], true);
    } finally {
      setBusy((current) => ({ ...current, [field.path]: false }));
      onPending(false);
    }
  };

  const save = async (field: GatewayFieldMeta) => {
    const view = views[field.path];
    const draft = drafts[field.path];
    if (!view?.expected || invalidDraft(field, draft) || view.operation?.state === 'unknown') return;
    const value = field.type === 'integer' ? Number(draft) : Boolean(draft);
    setBusy((current) => ({ ...current, [field.path]: true }));
    setErrors((current) => ({ ...current, [field.path]: '' }));
    onPending(true);
    try {
      await client.put(
        resource(field.path),
        { requestId: requestId(), value, expected: view.expected },
        { kind: 'admin', value: credential }
      );
      // Readback is the only proof: the view re-reads CPA through the Manager.
      await load([field], true);
    } catch (error) {
      if (error instanceof PublicApiError && error.kind === 'authentication') onUnauthorized();
      if (error instanceof PublicApiError && error.operationResult) {
        const result = error.operationResult;
        if (typeof result === 'object' && result !== null && 'operation' in result) {
          const rawOperation = result.operation;
          if (typeof rawOperation === 'object' && rawOperation !== null && 'state' in rawOperation) {
            const state = rawOperation.state;
            const code = 'code' in rawOperation && typeof rawOperation.code === 'string' ? rawOperation.code : undefined;
            if (typeof state === 'string')
              setViews((current) => ({
                ...current,
                [field.path]: { ...current[field.path], operation: { state, code } },
              }));
          }
        }
      }
      if (error instanceof PublicApiError && error.code === 'source_or_operation_conflict') {
        await conflict(field);
      } else {
        setErrors((current) => ({
          ...current,
          [field.path]: error instanceof PublicApiError && error.code === 'invalid_config_field' ? 'invalid' : 'write_failed',
        }));
      }
    } finally {
      setBusy((current) => ({ ...current, [field.path]: false }));
      onPending(false);
    }
  };

  const edit = (field: GatewayFieldMeta, value: unknown) => {
    edited.current.add(field.path);
    setDrafts((current) => ({ ...current, [field.path]: value }));
  };

  return (
    <section className="card gateway-requests-group" data-testid="gateway-requests-group">
      <div className="card-head">
        <div>
          <h2>{c('requestsStreamingTitle')}</h2>
          <p className="muted">{c('requestsStreamingHelp')}</p>
        </div>
      </div>
      {shown.map((field) => {
        const view = views[field.path];
        const draft = drafts[field.path] ?? (field.type === 'boolean' ? false : '');
        const invalid = invalidDraft(field, draft);
        const unknown = view?.operation?.state === 'unknown';
        const failed = view?.operation?.state === 'failed';
        const reason = view?.reason ? fieldReasonText[view.reason] : undefined;
        return (
          <div className="config-field" key={field.path} data-testid={`config-field-${field.path}`}>
            <div>
              <label htmlFor={`config-input-${field.path}`}>{field.label}</label>
              <code>{field.path}</code>
              <small>
                {unknown
                  ? c('fieldOperationUnknown')
                  : failed
                    ? c('fieldWriteNotApplied')
                    : view?.presence === 'unknown'
                      ? c('fieldValueUnknown')
                      : reason
                        ? c(reason)
                        : ''}
              </small>
            </div>
            <div className="field-control">
              {field.type === 'boolean' ? (
                <input
                  id={`config-input-${field.path}`}
                  data-testid={`config-input-${field.path}`}
                  type="checkbox"
                  checked={Boolean(draft)}
                  disabled={busy[field.path] || unknown}
                  onChange={(event) => edit(field, event.target.checked)}
                />
              ) : (
                <input
                  id={`config-input-${field.path}`}
                  data-testid={`config-input-${field.path}`}
                  type="number"
                  min={field.min}
                  max={field.max}
                  value={String(draft)}
                  disabled={busy[field.path] || unknown}
                  onChange={(event) => edit(field, event.target.value)}
                />
              )}
              <button
                className="btn btn-primary btn-sm"
                data-testid={`config-save-${field.path}`}
                disabled={busy[field.path] || invalid || !view?.expected || unknown}
                onClick={() => void save(field)}
              >
                {c('fieldSave')}
              </button>
              {unknown && view.operation?.requestId && (
                <button
                  className="btn btn-sm"
                  data-testid={`config-reconcile-${field.path}`}
                  disabled={busy[field.path]}
                  onClick={() => void reconcile(field)}
                >
                  {c('fieldReconcile')}
                </button>
              )}
              {errors[field.path] && (
                <span role="alert">{c(fieldErrorText[errors[field.path]] || 'fieldWriteFailed')}</span>
              )}
            </div>
          </div>
        );
      })}
    </section>
  );
}
