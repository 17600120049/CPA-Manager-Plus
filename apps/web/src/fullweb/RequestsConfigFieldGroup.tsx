import { useEffect, useMemo, useState } from 'react';
import type { GatewayFieldMeta } from './gatewayCatalog';
import type { RetryDraftDiff } from './FreshRetryConfiguration';
import { PublicApiError, type PublicApiClient } from './publicApi';

type Client = PublicApiClient;
type Expected = { sourceId: string; generation: string; revision: number; sha256: string };
type FieldView = { value?: unknown; presence: string; editable: boolean; expected?: Expected; operation?: { requestId?: string; state: string }; reason?: string };

function resource(path: string) { return `gateway/config/${path.split('.').join('/')}`; }
// 16 random bytes: the Manager journal key and Runtime operation ID must be
// 32 lowercase hex characters, and a request ID is never reused.
function requestId() {
  const bytes = new Uint8Array(16);
  crypto.getRandomValues(bytes);
  return Array.from(bytes, (byte) => byte.toString(16).padStart(2, '0')).join('');
}

export function RequestsConfigFieldGroup({
  fields,
  client,
  credential,
  onUnauthorized,
  onPending,
  onDraftChange,
}: {
  fields: GatewayFieldMeta[];
  client: Client;
  credential: string;
  onUnauthorized: () => void;
  onPending: (pending: boolean) => void;
  onDraftChange: (diffs: RetryDraftDiff[]) => void;
}) {
  const editable = useMemo(() => fields.filter((field) => field.editable), [fields]);
  const [views, setViews] = useState<Record<string, FieldView>>({});
  const [drafts, setDrafts] = useState<Record<string, unknown>>({});
  const [busy, setBusy] = useState<Record<string, boolean>>({});
  const [errors, setErrors] = useState<Record<string, string>>({});

  useEffect(() => {
    let cancelled = false;
    void Promise.all(
      editable.map(async (field) => {
        try {
          const view = await client.get<FieldView>(resource(field.path), { kind: 'admin', value: credential });
          if (!cancelled) {
            setViews((current) => ({ ...current, [field.path]: view }));
            setDrafts((current) => ({ ...current, [field.path]: view.value ?? (field.type === 'boolean' ? false : '') }));
          }
        } catch (error) {
          if (error instanceof PublicApiError && error.kind === 'authentication') onUnauthorized();
          if (!cancelled) setErrors((current) => ({ ...current, [field.path]: 'read_failed' }));
        }
      })
    );
    return () => { cancelled = true; };
  }, [client, credential, editable, onUnauthorized]);

  useEffect(() => {
    onDraftChange(editable.map((field) => {
      const view = views[field.path];
      const draft = drafts[field.path];
      const invalid = field.type === 'integer' && (draft === '' || !Number.isInteger(Number(draft)) || Number(draft) < (field.min ?? 0) || Number(draft) > (field.max ?? Number.MAX_SAFE_INTEGER));
      return { path: field.path, label: field.label, oldVal: String(view?.value ?? ''), newVal: String(draft ?? ''), dirty: view?.value !== draft, invalid };
    }));
  }, [drafts, editable, onDraftChange, views]);

  const read = async (field: GatewayFieldMeta) => {
    const view = await client.get<FieldView>(resource(field.path), { kind: 'admin', value: credential });
    setViews((current) => ({ ...current, [field.path]: view }));
    setDrafts((current) => ({ ...current, [field.path]: view.value ?? (field.type === 'boolean' ? false : '') }));
  };

  const reconcile = async (field: GatewayFieldMeta) => {
    const request = views[field.path]?.operation?.requestId;
    if (!request) return;
    setBusy((current) => ({ ...current, [field.path]: true }));
    onPending(true);
    try {
      await client.post(`gateway/config/operations/${request}/reconcile`, {}, { kind: 'admin', value: credential });
      await read(field);
      setErrors((current) => ({ ...current, [field.path]: '' }));
    } catch (error) {
      if (error instanceof PublicApiError && error.kind === 'authentication') onUnauthorized();
      setErrors((current) => ({ ...current, [field.path]: 'write_failed' }));
    } finally {
      setBusy((current) => ({ ...current, [field.path]: false }));
      onPending(false);
    }
  };

  const save = async (field: GatewayFieldMeta) => {
    const view = views[field.path];
    const draft = drafts[field.path];
    const numeric = field.type === 'integer';
    const invalid = numeric && (draft === '' || !Number.isInteger(Number(draft)) || Number(draft) < (field.min ?? 0) || Number(draft) > (field.max ?? Number.MAX_SAFE_INTEGER));
    if (!view?.expected || invalid || view.operation?.state === 'unknown') return;
    const value = numeric ? Number(draft) : Boolean(draft);
    setBusy((current) => ({ ...current, [field.path]: true }));
    setErrors((current) => ({ ...current, [field.path]: '' }));
    onPending(true);
    try {
      await client.put(resource(field.path), { requestId: requestId(), value, expected: view.expected }, { kind: 'admin', value: credential });
      // Readback is the only proof: the view re-reads CPA through the Manager.
      await read(field);
      setErrors((current) => ({ ...current, [field.path]: '' }));
    } catch (error) {
      if (error instanceof PublicApiError && error.kind === 'authentication') onUnauthorized();
      if (error instanceof PublicApiError && error.operationResult) {
        if (typeof error.operationResult === 'object' && error.operationResult !== null && 'operation' in error.operationResult) {
          const rawOperation = error.operationResult.operation;
          if (typeof rawOperation === 'object' && rawOperation !== null && 'state' in rawOperation) {
            const state = rawOperation.state;
            if (typeof state === 'string') setViews((current) => ({ ...current, [field.path]: { ...current[field.path], operation: { state } } }));
          }
        }
      }
      setErrors((current) => ({ ...current, [field.path]: error instanceof PublicApiError && error.code === 'invalid_config_field' ? 'invalid' : 'write_failed' }));
    } finally {
      setBusy((current) => ({ ...current, [field.path]: false }));
      onPending(false);
    }
  };

  return <section className="card gateway-requests-group" data-testid="gateway-requests-group">
    <div className="card-head"><div><h2>Requests streaming</h2><p className="muted">CPA-owned request timing and passthrough controls.</p></div></div>
    {editable.map((field) => {
      const view = views[field.path];
      const draft = drafts[field.path] ?? (field.type === 'boolean' ? false : '');
      const invalid = field.type === 'integer' && (draft === '' || !Number.isInteger(Number(draft)) || Number(draft) < (field.min ?? 0) || Number(draft) > (field.max ?? Number.MAX_SAFE_INTEGER));
      return <div className="config-field" key={field.path} data-testid={`config-field-${field.path}`}>
        <div><label htmlFor={`config-input-${field.path}`}>{field.label}</label><code>{field.path}</code><small>{view?.operation?.state === 'unknown' ? 'UNKNOWN: reconcile original operation before retrying.' : view?.presence === 'unknown' ? 'UNKNOWN' : view?.reason || ''}</small></div>
        <div className="field-control">
          {field.type === 'boolean' ? <input id={`config-input-${field.path}`} data-testid={`config-input-${field.path}`} type="checkbox" checked={Boolean(draft)} disabled={busy[field.path] || view?.operation?.state === 'unknown'} onChange={(event) => setDrafts((current) => ({ ...current, [field.path]: event.target.checked }))} /> : <input id={`config-input-${field.path}`} data-testid={`config-input-${field.path}`} type="number" min={field.min} max={field.max} value={String(draft)} disabled={busy[field.path] || view?.operation?.state === 'unknown'} onChange={(event) => setDrafts((current) => ({ ...current, [field.path]: event.target.value }))} />}
          <button className="btn btn-primary btn-sm" data-testid={`config-save-${field.path}`} disabled={busy[field.path] || invalid || !view?.expected || view?.operation?.state === 'unknown'} onClick={() => void save(field)}>Save</button>
          {view?.operation?.state === 'unknown' && view.operation.requestId && <button className="btn btn-sm" data-testid={`config-reconcile-${field.path}`} disabled={busy[field.path]} onClick={() => void reconcile(field)}>Reconcile</button>}
          {errors[field.path] && <span role="alert">{errors[field.path]}</span>}
        </div>
      </div>;
    })}
  </section>;
}
