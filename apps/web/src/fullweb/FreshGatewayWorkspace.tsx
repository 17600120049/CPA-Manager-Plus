import { useCallback, useEffect, useMemo, useRef, useState } from 'react';
import { useTranslation } from 'react-i18next';
import { Modal } from '../components/ui/Modal';
import { configurationText as c } from '../i18n/configurationWorkspace';
import { referenceText as r } from '../i18n/configurationReference';
import type { PublicApiClient } from './publicApi';
import { PublicApiError } from './publicApi';
import {
  COMMON_GATEWAY_PATHS,
  GATEWAY_TABS,
  gatewayFamilies,
  gatewayGroups,
  gatewayOwners,
  type GatewayFamilyMeta,
  type GatewayFieldMeta,
  type GatewayGroupMeta,
  type GatewayOwnerMeta,
  type GatewayTabKey,
} from './gatewayCatalog';
import {
  draftEquals,
  draftFor,
  draftIssue,
  editFor,
  newGatewayRequestId,
  readGatewayOperation,
  readGatewayView,
  saveGatewayFamily,
  saveGatewayGroup,
  type GatewayDraft,
  type GatewayEdit,
  type GatewayFamilyView,
  type GatewayFieldView,
  type GatewayGroupView,
  type GatewayView,
} from './gatewayConfigClient';
import { GatewayRuleEditor } from './GatewayRuleEditor';
import { ruleSummary } from './gatewayRuleForm';

// The gateway configuration page. One write is one page group: the page collects
// every edited leaf of a group into a single PATCH, restores a leaf to CPA's own
// default with one DELETE, writes a rule family with one PUT, and never sends a
// second write for an unresolved one.

const GATEWAY_SAVE_UNKNOWN_CODES: Record<string, true> = {
  original_operation_unknown: true,
  configuration_unavailable: true,
  config_authority_unavailable: true,
};

export function FreshGatewayWorkspace({
  client,
  credential,
  onUnauthorized,
  onPending,
  onDirtyChange,
}: {
  client: PublicApiClient;
  credential: string;
  onUnauthorized: () => void;
  onPending: (pending: boolean) => void;
  onDirtyChange?: (dirty: boolean) => void;
}) {
  // Subscribe to language changes just like the original Console pages.
  const { i18n } = useTranslation();
  const language = i18n.resolvedLanguage;

  const [view, setView] = useState<GatewayView | null>(null);
  const [loading, setLoading] = useState(true);
  const [loadFailed, setLoadFailed] = useState(false);
  const [currentTab, setCurrentTab] = useState<GatewayTabKey>('overview');
  const [searchText, setSearchText] = useState('');
  const [changedOnly, setChangedOnly] = useState(false);
  const [drafts, setDrafts] = useState<Record<string, GatewayDraft>>({});
  const [issues, setIssues] = useState<Record<string, string>>({});
  const [groupErrors, setGroupErrors] = useState<Record<string, string>>({});
  const [familyDrafts, setFamilyDrafts] = useState<Record<string, unknown[]>>({});
  const [saving, setSaving] = useState('');
  const [reconciling, setReconciling] = useState('');
  const [conflictGroup, setConflictGroup] = useState('');
  const [diffOpen, setDiffOpen] = useState(false);
  const [discardGroup, setDiscardGroup] = useState<string | null>(null);
  const [discardAll, setDiscardAll] = useState(false);
  const [ruleFamily, setRuleFamily] = useState<GatewayFamilyMeta | null>(null);
  const [activeOwnerPath, setActiveOwnerPath] = useState<string>('');
  const issued = useRef<Set<string>>(new Set());
  const pendingWrite = useRef<Record<string, string>>({});

  // eslint-disable-next-line react-hooks/exhaustive-deps
  const groups = useMemo(() => gatewayGroups(), [language]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const families = useMemo(() => gatewayFamilies(), [language]);
  // eslint-disable-next-line react-hooks/exhaustive-deps
  const owners = useMemo(() => gatewayOwners(), [language]);

  const handleError = useCallback(
    (error: unknown) => {
      if (error instanceof PublicApiError && error.code === 'unauthorized') onUnauthorized();
    },
    [onUnauthorized]
  );

  const load = useCallback(
    async (options: { keepDrafts?: boolean } = {}) => {
      setLoading(true);
      try {
        const next = await readGatewayView(client, { kind: 'admin', value: credential });
        setView(next);
        setLoadFailed(false);
        if (!options.keepDrafts) {
          setDrafts({});
          setIssues({});
          setGroupErrors({});
          setFamilyDrafts({});
        }
      } catch (error) {
        handleError(error);
        setLoadFailed(true);
      } finally {
        setLoading(false);
      }
    },
    [client, credential, handleError]
  );

  useEffect(() => {
    void load();
  }, [load]);

  const fieldViews = useMemo(() => {
    const index = new Map<string, GatewayFieldView>();
    for (const field of view?.fields ?? []) index.set(field.path, field);
    return index;
  }, [view]);

  const groupViews = useMemo(() => {
    const index = new Map<string, GatewayGroupView>();
    for (const group of view?.groups ?? []) index.set(group.id, group);
    return index;
  }, [view]);

  const familyViews = useMemo(() => {
    const index = new Map<string, GatewayFamilyView>();
    for (const family of view?.families ?? []) index.set(family.family, family);
    return index;
  }, [view]);

  const dirtyEdits = useMemo(() => {
    const edits: GatewayEdit[] = [];
    for (const field of view?.fields ?? []) {
      const draft = drafts[field.path];
      if (!draft) continue;
      if (draftEquals(field, draft)) continue;
      edits.push({ ...editFor(field, draft), path: field.path });
    }
    return edits;
  }, [drafts, view]);

  const dirtyByGroup = useMemo(() => {
    const index = new Map<string, GatewayEdit[]>();
    for (const edit of dirtyEdits) {
      const field = fieldViews.get(edit.path);
      if (!field) continue;
      const list = index.get(field.group) ?? [];
      list.push(edit);
      index.set(field.group, list);
    }
    return index;
  }, [dirtyEdits, fieldViews]);

  const dirtyFamilies = useMemo(() => {
    const index = new Map<string, unknown[]>();
    for (const family of families) {
      const draft = familyDrafts[family.family];
      if (!draft) continue;
      const server = familyViews.get(family.family)?.rules ?? [];
      if (JSON.stringify(draft) === JSON.stringify(server)) continue;
      index.set(family.family, draft);
    }
    return index;
  }, [families, familyDrafts, familyViews]);

  const dirtyCount = dirtyEdits.length + dirtyFamilies.size;

  useEffect(() => {
    onDirtyChange?.(dirtyCount > 0);
  }, [dirtyCount, onDirtyChange]);

  useEffect(() => {
    onPending(!!saving || !!reconciling);
  }, [onPending, reconciling, saving]);

  useEffect(() => {
    if (!dirtyCount || typeof window === 'undefined' || typeof window.addEventListener !== 'function')
      return;
    const beforeUnload = (event: BeforeUnloadEvent) => {
      event.preventDefault();
      event.returnValue = '';
    };
    window.addEventListener('beforeunload', beforeUnload);
    return () => window.removeEventListener('beforeunload', beforeUnload);
  }, [dirtyCount]);

  const metaOf = useCallback(
    (path: string): GatewayFieldMeta | undefined => {
      for (const group of groups) {
        const found = group.fields.find((field) => field.path === path);
        if (found) return found;
      }
      return undefined;
    },
    [groups]
  );

  function formatted(presence: string, path: string, value: unknown): string {
    if (presence === 'missing') return c('valueMissing');
    if (presence === 'null') return c('valueNull');
    if (presence === 'unknown') return c('valueUnknown');
    const meta = metaOf(path);
    if (typeof value === 'boolean') return value ? c('valueOn') : c('valueOff');
    const option = meta?.options?.find((entry) => String(entry.value) === String(value));
    if (option) return r(option.labelKey);
    if (typeof value === 'string' && value === '') return c('valueEmpty');
    return String(value);
  }

  function setDraft(path: string, draft: GatewayDraft) {
    setDrafts((current) => ({ ...current, [path]: draft }));
    setIssues((current) => {
      if (!current[path]) return current;
      const next = { ...current };
      delete next[path];
      return next;
    });
  }

  function clearGroupDrafts(groupId: string | null) {
    const paths = new Set(
      groupId === null
        ? dirtyEdits.map((edit) => edit.path)
        : (dirtyByGroup.get(groupId) ?? []).map((edit) => edit.path)
    );
    setDrafts((current) => {
      const next = { ...current };
      for (const path of paths) delete next[path];
      return next;
    });
    setIssues({});
    if (groupId === null) {
      setFamilyDrafts({});
      setGroupErrors({});
    } else {
      setGroupErrors((current) => {
        const next = { ...current };
        delete next[groupId];
        return next;
      });
    }
  }

  function reportWriteError(scope: string, error: unknown) {
    handleError(error);
    const code = error instanceof PublicApiError ? error.code : '';
    const message = error instanceof PublicApiError ? (error.detail ?? '') : '';
    if (code === 'source_or_operation_conflict') {
      setConflictGroup(scope);
      setGroupErrors((current) => ({ ...current, [scope]: c('groupSaveConflict') }));
      // A conflict keeps the draft: the user decides against the new baseline.
      void load({ keepDrafts: true });
      return;
    }
    if (code && GATEWAY_SAVE_UNKNOWN_CODES[code]) {
      setGroupErrors((current) => ({ ...current, [scope]: c('groupSaveUnknown') }));
      return;
    }
    if (code === 'cpa_rejected_config') {
      const field = error instanceof PublicApiError ? error.field : undefined;
      setGroupErrors((current) => ({
        ...current,
        [scope]: message ? `${c('groupSaveRejectedDetail')} ${message}` : c('groupSaveRejected'),
      }));
      if (field) setIssues((current) => ({ ...current, [field]: c('fieldCpaRejected') }));
      return;
    }
    setGroupErrors((current) => ({ ...current, [scope]: c('groupSaveFailed') }));
  }

  async function saveGroup(group: GatewayGroupMeta) {
    const groupView = groupViews.get(group.id);
    const edits = dirtyByGroup.get(group.id) ?? [];
    if (!groupView || !edits.length || !groupView.writable) return;
    const problems: Record<string, string> = {};
    for (const edit of edits) {
      const field = fieldViews.get(edit.path);
      const draft = drafts[edit.path];
      if (!field || !draft) continue;
      const issue = draftIssue(field, draft);
      if (issue) problems[edit.path] = c(issue === 'required' ? 'valueRequired' : 'valueInteger');
    }
    if (Object.keys(problems).length) {
      setIssues((current) => ({ ...current, ...problems }));
      return;
    }
    const requestId = newGatewayRequestId(issued.current);
    pendingWrite.current[group.id] = requestId;
    setSaving(group.id);
    setGroupErrors((current) => {
      const next = { ...current };
      delete next[group.id];
      return next;
    });
    setConflictGroup('');
    try {
      const result = await saveGatewayGroup(client, { kind: 'admin', value: credential }, group.id, {
        requestId,
        expected: { generation: view?.generation ?? '', baseline: groupView.baseline },
        edits,
      });
      delete pendingWrite.current[group.id];
      if (result.operation.state === 'failed') {
        setGroupErrors((current) => ({ ...current, [group.id]: c('groupSaveRejected') }));
      } else {
        clearGroupDrafts(group.id);
      }
      await load({ keepDrafts: true });
    } catch (error) {
      reportWriteError(group.id, error);
    } finally {
      setSaving('');
    }
  }

  async function saveFamily(family: GatewayFamilyMeta) {
    const rules = dirtyFamilies.get(family.family);
    const familyView = familyViews.get(family.family);
    if (!rules || !familyView || !familyView.writable) return;
    const requestId = newGatewayRequestId(issued.current);
    pendingWrite.current[`rules:${family.family}`] = requestId;
    setSaving(`rules:${family.family}`);
    try {
      const result = await saveGatewayFamily(client, { kind: 'admin', value: credential }, family.family, {
        requestId,
        expected: { generation: view?.generation ?? '', baseline: familyView.baseline },
        rules,
      });
      delete pendingWrite.current[`rules:${family.family}`];
      if (result.operation.state === 'failed') {
        setGroupErrors((current) => ({ ...current, rules: c('groupSaveRejected') }));
      } else {
        setFamilyDrafts((current) => {
          const next = { ...current };
          delete next[family.family];
          return next;
        });
      }
      await load({ keepDrafts: true });
    } catch (error) {
      reportWriteError('rules', error);
    } finally {
      setSaving('');
    }
  }

  async function queryOriginal(scope: string) {
    const requestId = pendingWrite.current[scope];
    setReconciling(scope);
    try {
      if (requestId) {
        const result = await readGatewayOperation(client, { kind: 'admin', value: credential }, requestId, true);
        if (result.operation.state === 'persisted') {
          delete pendingWrite.current[scope];
          clearGroupDrafts(scope === 'rules' ? null : scope);
        }
      }
      await load({ keepDrafts: true });
    } catch (error) {
      handleError(error);
    } finally {
      setReconciling('');
    }
  }

  const query = searchText.trim().toLowerCase();
  const visibleGroups = useMemo(() => {
    const matches = (group: GatewayGroupMeta, field: GatewayFieldMeta) => {
      // A search is global: it is not confined to the tab it started from.
      if (query) {
        const haystack = [group.titleKey ? c(group.titleKey) : '', r(field.labelKey), field.path]
          .join(' ')
          .toLowerCase();
        return haystack.includes(query);
      }
      if (changedOnly) return (dirtyByGroup.get(group.id) ?? []).some((edit) => edit.path === field.path);
      if (currentTab === 'common') return COMMON_GATEWAY_PATHS[field.path] === true;
      if (currentTab === 'overview' || currentTab === 'owners') return false;
      return group.tab === currentTab;
    };
    return groups
      .map((group) => ({ ...group, fields: group.fields.filter((field) => matches(group, field)) }))
      .filter((group) => group.fields.length > 0);
  }, [changedOnly, currentTab, dirtyByGroup, groups, query]);

  // The payload rule lists live on the request tab, exactly where the accepted
  // design placed them; a search still reaches them from anywhere.
  const visibleFamilies = useMemo(
    () =>
      families.filter((family) => {
        if (query) return [c(family.labelKey), family.path].join(' ').toLowerCase().includes(query);
        if (changedOnly) return dirtyFamilies.has(family.family);
        if (currentTab === 'overview' || currentTab === 'owners') return false;
        return currentTab === 'requests';
      }),
    [changedOnly, currentTab, dirtyFamilies, families, query]
  );

  const capability = view?.capability;
  const pageReason = capability?.reason;
  const readOnlyNotice = capability
    ? capability.writable
      ? capability.rulesWritable
        ? ''
        : c('reasonExternalRules')
      : c(reasonKey(pageReason))
    : '';

  const jumpToOwner = (owner: GatewayOwnerMeta) => {
    setActiveOwnerPath(owner.path);
    const target = owner.fields[0] ?? '';
    const owning = groups.find((group) => group.fields.some((field) => field.path === target));
    setCurrentTab(owning?.tab ?? 'owners');
    setSearchText('');
    setChangedOnly(false);
  };

  function renderField(field: GatewayFieldView) {
    const meta = metaOf(field.path);
    const draft = drafts[field.path] ?? draftFor(field);
    const dirty = !draftEquals(field, draft);
    const disabled = !field.editable || saving !== '';
    const dependsOn = meta?.depends ? fieldViews.get(meta.depends) : undefined;
    const dependencyOff = !!dependsOn && dependsOn.presence === 'present' && dependsOn.value === false;
    const locked = !field.editable || dependencyOff;
    const label = meta ? r(meta.labelKey) : field.path;
    const help = meta ? r(meta.helpKey) : '';
    const issue = issues[field.path];
    const testId = `gateway-field-${field.path}`;
    return (
      <div
        className={'config-field' + (dirty ? ' field-dirty' : '') + (locked ? ' field-locked' : '')}
        key={field.path}
        data-testid={testId}
        data-dirty={dirty ? 'true' : 'false'}
      >
        <div className="field-head">
          <span>{label}</span>
          {meta?.risk ? <span className="badge warn">{c('risk')}</span> : null}
          <code>{field.path}</code>
        </div>
        <div className="field-control">
          {field.type === 'bool' ? (
            <label className="switch">
              <input
                type="checkbox"
                aria-label={label}
                disabled={disabled || dependencyOff}
                checked={draft.action === 'set' && draft.value === true}
                onChange={(event) => setDraft(field.path, { action: 'set', value: event.target.checked })}
              />
              <span>
                {draft.action === 'set' && draft.value === true ? c('valueOn') : c('valueOff')}
              </span>
            </label>
          ) : field.kind === 'select' ? (
            <select
              aria-label={label}
              disabled={disabled}
              value={draft.action === 'default' ? '' : String(draft.value ?? '')}
              onChange={(event) => {
                if (event.target.value === '') {
                  setDraft(field.path, { action: 'default', value: '' });
                  return;
                }
                if (field.type === 'bool') {
                  setDraft(field.path, { action: 'set', value: event.target.value === 'true' });
                  return;
                }
                setDraft(field.path, { action: 'set', value: event.target.value });
              }}
            >
              {meta?.options?.map((option) => (
                <option key={option.value} value={option.value}>
                  {r(option.labelKey)}
                </option>
              ))}
              {meta?.options?.some((option) => option.value === String(field.value ?? '')) ? null : (
                <option value={String(field.value ?? '')}>
                  {formatted(field.presence, field.path, field.value)}
                </option>
              )}
            </select>
          ) : (
            <input
              aria-label={label}
              type={field.type === 'int' ? 'number' : 'text'}
              inputMode={field.type === 'int' ? 'numeric' : undefined}
              disabled={disabled || dependencyOff}
              placeholder={formatted(field.presence, field.path, field.value)}
              value={draft.action === 'default' ? '' : String(draft.value ?? '')}
              onChange={(event) => {
                const value = event.target.value;
                const alreadyDefault = draft.action === 'default';
                if (alreadyDefault && value === '') return;
                setDraft(field.path, { action: 'set', value });
              }}
            />
          )}
          {field.presence === 'missing' ? (
            <span className="presence">{c('valueMissing')}</span>
          ) : (
            <button
              className="btn btn-secondary btn-sm"
              disabled={disabled}
              onClick={() => setDraft(field.path, { action: 'default', value: '' })}
            >
              {c('fieldUseDefault')}
            </button>
          )}
        </div>
        <p className="detail">{help}</p>
        <p className="detail">
          {c('fieldCurrentValue')} <strong>{formatted(field.presence, field.path, field.value)}</strong>
          {dirty
            ? ' · ' +
              c('fieldDraftValue') +
              ' ' +
              (draft.action === 'default'
                ? c('valueMissing')
                : formatted('present', field.path, draft.value))
            : ''}
        </p>
        {field.reason && !field.editable ? (
          <p className="detail field-reason">{c(reasonKey(field.reason))}</p>
        ) : null}
        {dependencyOff ? <p className="detail field-reason">{c('fieldDependencyOff')}</p> : null}
        {issue ? (
          <p className="field-error" role="alert">
            {issue}
          </p>
        ) : null}
      </div>
    );
  }

  function renderGroupCard(group: GatewayGroupMeta) {
    const groupView = groupViews.get(group.id);
    const edits = dirtyByGroup.get(group.id) ?? [];
    const operation = groupView?.operation;
    const error = groupErrors[group.id];
    // `group` here is the filtered projection: an alias view never renders a
    // leaf the current tab, search or changed-only filter hides.
    const members = group.fields;
    const fieldViewsOfGroup = members
      .map((meta) => fieldViews.get(meta.path))
      .filter((field): field is GatewayFieldView => !!field);
    return (
      <section className="card" key={group.id} data-testid={`gateway-group-${group.id}`}>
        <header className="card-head">
          <h2>{c(group.titleKey)}</h2>
          <span className="badge">{c('fieldCount', members.length)}</span>
        </header>
        <p className="detail">{c(group.helpKey)}</p>
        {fieldViewsOfGroup.map((field) => renderField(field))}
        <div className="group-footer">
          <span className="detail">
            {edits.length ? c('groupDirtyCount', edits.length) : c('noDrafts')}
          </span>
          <div className="actions">
            <button
              className="btn btn-secondary btn-sm"
              disabled={!edits.length}
              onClick={() => {
                setDiscardGroup(group.id);
              }}
            >
              {c('discard')}
            </button>
            <button
              className="btn btn-primary btn-sm"
              disabled={!edits.length || !groupView?.writable || saving !== ''}
              onClick={() => void saveGroup(group)}
            >
              {saving === group.id ? c('groupSaving') : c('groupSave')}
            </button>
          </div>
        </div>
        {groupView && !groupView.writable ? (
          <p className="notice warn">{c(reasonKey(groupView.reason))}</p>
        ) : null}
        {error ? (
          <p className="field-error" role="alert" data-testid={`gateway-group-error-${group.id}`}>
            {error}
            {error === c('groupSaveUnknown') ? (
              <button className="btn btn-secondary btn-sm" onClick={() => void queryOriginal(group.id)}>
                {c('groupQueryOriginal')}
              </button>
            ) : null}
          </p>
        ) : null}
        {operation ? (
          <p className="detail" data-testid={`gateway-group-status-${group.id}`}>
            {operation.state === 'persisted'
              ? operation.code === 'superseded'
                ? c('operationSuperseded')
                : c('savedPersisted')
              : operation.state === 'failed'
                ? c('groupSaveRejected')
                : c('groupSaveUnknown')}
            {operation.state === 'persisted' ? ' · ' + c('savedEffectiveUnknown') : ''}
          </p>
        ) : null}
      </section>
    );
  }

  function renderFamilyCard(family: GatewayFamilyMeta) {
    const familyView = familyViews.get(family.family);
    const draft = familyDrafts[family.family];
    const dirty = dirtyFamilies.has(family.family);
    const rules = draft ?? (Array.isArray(familyView?.rules) ? (familyView.rules as unknown[]) : []);
    return (
      <section className="card" key={family.family} data-testid={`gateway-family-${family.family}`}>
        <header className="card-head">
          <h2>{c(family.labelKey)}</h2>
          <span className="badge">{c('ruleCount', rules.length)}</span>
        </header>
        <p className="detail">{c(family.hintKey)}</p>
        <code>{family.path}</code>
        {rules.length ? (
          <ul className="rule-preview">
            {rules.map((rule, index) => (
              <li key={index}>{ruleSummary(rule, family)}</li>
            ))}
          </ul>
        ) : (
          <p className="detail">{c('ruleEmpty')}</p>
        )}
        <div className="actions">
          <button
            className="btn btn-secondary btn-sm"
            disabled={!familyView?.writable}
            onClick={() => setRuleFamily(family)}
          >
            {c('ruleEditFamily')}
          </button>
          <button
            className="btn btn-secondary btn-sm"
            disabled={!dirty}
            onClick={() =>
              setFamilyDrafts((current) => {
                const next = { ...current };
                delete next[family.family];
                return next;
              })
            }
          >
            {c('discard')}
          </button>
          <button
            className="btn btn-primary btn-sm"
            disabled={!dirty || !familyView?.writable || saving !== ''}
            onClick={() => void saveFamily(family)}
          >
            {saving === `rules:${family.family}` ? c('groupSaving') : c('groupSave')}
          </button>
        </div>
        {familyView?.readOnly ? <p className="notice warn">{c('rulesReadOnly')}</p> : null}
      </section>
    );
  }

  return (
    <section className="config-workspace" data-testid="gateway-config-workspace">
      <header className="workspace-head">
        <div>
          <h1>{c('gateway')}</h1>
          <p className="detail">{c('gatewayHelp')}</p>
        </div>
        <div className="actions">
          <span className="badge" data-testid="gateway-capability">
            {c(capabilityKey(capability?.mode))}
          </span>
          <button className="btn btn-secondary btn-sm" disabled={loading} onClick={() => void load()}>
            {c('reload')}
          </button>
        </div>
      </header>
      <p className="notice">{c('catalogNotice')}</p>
      {loadFailed ? (
        <p className="alert alert-warn" role="alert" data-testid="gateway-load-failed">
          {c('loadFailed')}
        </p>
      ) : null}
      {view?.groupsInvalid ? (
        <p className="alert alert-warn" role="alert" data-testid="gateway-journal-invalid">
          {c('reasonJournalInvalid')}
        </p>
      ) : null}
      {readOnlyNotice ? (
        <p className="alert alert-warn" data-testid="gateway-readonly">
          {readOnlyNotice}
        </p>
      ) : null}
      {conflictGroup ? (
        <div className="alert alert-warn" role="alert" data-testid="gateway-conflict">
          <strong>{c('conflictTitle')}</strong> {c('conflictHelp')}
          <button className="btn btn-secondary btn-sm" onClick={() => setDiffOpen(true)}>
            {c('viewChanges')}
          </button>
        </div>
      ) : null}
      <div className="workspace-toolbar">
        <label className="search">
          <span>{c('search')}</span>
          <input
            type="search"
            value={searchText}
            placeholder={c('searchPlaceholder')}
            onChange={(event) => setSearchText(event.target.value)}
          />
        </label>
        <label className="toggle">
          <input
            type="checkbox"
            checked={changedOnly}
            onChange={(event) => setChangedOnly(event.target.checked)}
          />
          <span>{c('changedOnly')}</span>
        </label>
        <span className="badge" data-testid="gateway-dirty-count">
          {c('dirtyCount', dirtyCount)}
        </span>
        <button
          className="btn btn-secondary btn-sm"
          disabled={!dirtyCount}
          onClick={() => setDiffOpen(true)}
        >
          {c('viewChanges')}
        </button>
        <button
          className="btn btn-secondary btn-sm"
          disabled={!dirtyCount}
          onClick={() => setDiscardAll(true)}
        >
          {c('discard')}
        </button>
      </div>
      <nav className="gateway-tabs" role="tablist" aria-label={c('gateway')}>
        {GATEWAY_TABS.map((tab) => (
          <button
            key={tab}
            role="tab"
            aria-selected={currentTab === tab}
            className={'btn btn-secondary btn-sm' + (currentTab === tab ? ' active' : '')}
            onClick={() => {
              setCurrentTab(tab);
              setActiveOwnerPath('');
            }}
          >
            {c(tab)}
          </button>
        ))}
      </nav>
      <div role="tabpanel" className="workspace-body">
        {currentTab === 'overview' ? (
          <div className="overview-grid" data-testid="gateway-overview">
            <section className="card">
              <h2>{c('overview')}</h2>
              <p className="detail">{c('overviewHelp')}</p>
              <dl className="stat-grid">
                <div>
                  <dt>{c('fields')}</dt>
                  <dd>{view?.fields.length ?? 0}</dd>
                </div>
                <div>
                  <dt>{c('ruleFamilies')}</dt>
                  <dd>{view?.families.length ?? 0}</dd>
                </div>
                <div>
                  <dt>{c('ownerGroups')}</dt>
                  <dd>{owners.length}</dd>
                </div>
                <div>
                  <dt>{c('dirty')}</dt>
                  <dd>{dirtyCount}</dd>
                </div>
              </dl>
            </section>
            <section className="card" key="rules">
              <header className="card-head">
                <h2>{c('ruleFamilies')}</h2>
                <span className="badge">{c('ruleCount', families.length)}</span>
              </header>
              <p className="detail">{c('familyDefaultHelp')}</p>
              <div className="actions">
                <button className="btn btn-secondary btn-sm" onClick={() => setCurrentTab('requests')}>
                  {c('gotoGroup')}
                </button>
              </div>
            </section>
            {groups.map((group) => (
              <section className="card" key={group.id}>
                <header className="card-head">
                  <h2>{c(group.titleKey)}</h2>
                  <span className="badge">{c('fieldCount', group.fields.length)}</span>
                </header>
                <p className="detail">{c(group.helpKey)}</p>
                <div className="actions">
                  <button className="btn btn-secondary btn-sm" onClick={() => setCurrentTab(group.tab)}>
                    {c('gotoGroup')}
                  </button>
                </div>
              </section>
            ))}
          </div>
        ) : null}
        {currentTab === 'owners' ? (
          <div className="owner-grid" data-testid="gateway-owners">
            {owners.map((owner) => (
              <section className="card" key={owner.path}>
                <header className="card-head">
                  <h2>{r(owner.ownerKey)}</h2>
                </header>
                <code>{owner.path}</code>
                <p className="detail">{c(owner.detailKey)}</p>
                <details>
                  <summary>{c('ownerDetail')}</summary>
                  <ul>
                    {owner.fields.map((field) => (
                      <li key={field}>
                        <code>{field}</code>
                      </li>
                    ))}
                  </ul>
                </details>
                {owner.target ? (
                  <a className="btn btn-secondary btn-sm" href={owner.target}>
                    {c('openOwner')}
                  </a>
                ) : (
                  <p className="detail">{c('ownerUnavailable')}</p>
                )}
                <button className="btn btn-secondary btn-sm" onClick={() => jumpToOwner(owner)}>
                  {c('gotoGateway')}
                </button>
              </section>
            ))}
          </div>
        ) : null}
        {currentTab !== 'overview' && currentTab !== 'owners'
          ? visibleGroups.map((group) => renderGroupCard(group))
          : null}
        {visibleFamilies.length
          ? visibleFamilies.map((family) => renderFamilyCard(family))
          : null}
        {currentTab !== 'overview' && currentTab !== 'owners' && !visibleGroups.length && !visibleFamilies.length ? (
          <p className="detail" data-testid="gateway-no-matches">
            {c('noMatches')}
          </p>
        ) : null}
        {activeOwnerPath ? (
          <p className="detail" data-testid="gateway-owner-active">
            {c('ownerActive')} <code>{activeOwnerPath}</code>
          </p>
        ) : null}
      </div>
      <Modal open={diffOpen} width={720} title={c('diffTitle')} onClose={() => setDiffOpen(false)}>
        <p className="detail">{c('diffHelp')}</p>
        {dirtyEdits.length ? (
          <ul className="diff-list" data-testid="gateway-diff">
            {dirtyEdits.map((edit) => {
              const field = fieldViews.get(edit.path);
              return (
                <li key={edit.path}>
                  <code>{edit.path}</code>
                  <span>{field ? formatted(field.presence, field.path, field.value) : ''}</span>
                  <span aria-hidden="true">→</span>
                  <span>
                    {edit.action === 'default'
                      ? c('valueMissing')
                      : field
                        ? formatted('present', edit.path, edit.value)
                        : String(edit.value)}
                  </span>
                </li>
              );
            })}
          </ul>
        ) : (
          <p className="detail">{c('noDrafts')}</p>
        )}
        {[...dirtyFamilies.entries()].map(([family, rules]) => (
          <p className="detail" key={family}>
            <code>{family}</code> · {c('ruleCount', rules.length)}
          </p>
        ))}
        <p className="detail">{c('diffNotice')}</p>
      </Modal>
      <Modal
        open={discardGroup !== null || discardAll}
        title={c('discardTitle')}
        onClose={() => {
          setDiscardGroup(null);
          setDiscardAll(false);
        }}
        footer={
          <>
            <button
              className="btn btn-secondary"
              onClick={() => {
                setDiscardGroup(null);
                setDiscardAll(false);
              }}
            >
              {c('keep')}
            </button>
            <button
              className="btn btn-primary"
              onClick={() => {
                clearGroupDrafts(discardAll ? null : discardGroup);
                setDiscardGroup(null);
                setDiscardAll(false);
              }}
            >
              {c('confirmDiscard')}
            </button>
          </>
        }
      >
        <p className="detail">{c('discardHelp')}</p>
      </Modal>
      {ruleFamily ? (
        <GatewayRuleEditor
          family={ruleFamily}
          rules={
            familyDrafts[ruleFamily.family] ??
            (Array.isArray(familyViews.get(ruleFamily.family)?.rules)
              ? (familyViews.get(ruleFamily.family)?.rules as unknown[])
              : [])
          }
          readOnly={!familyViews.get(ruleFamily.family)?.writable}
          onCancel={() => setRuleFamily(null)}
          onApply={(rules) => {
            setFamilyDrafts((current) => ({ ...current, [ruleFamily.family]: rules }));
            setRuleFamily(null);
          }}
        />
      ) : null}
      {loading && !view ? <p className="detail">{c('loading')}</p> : null}
    </section>
  );
}

function capabilityKey(mode: string | undefined): Parameters<typeof c>[0] {
  switch (mode) {
    case 'managed':
      return 'capabilityManaged';
    case 'external':
      return 'capabilityExternal';
    case 'private':
      return 'capabilityPrivate';
    default:
      return 'capabilityUnavailable';
  }
}

function reasonKey(reason: string | undefined): Parameters<typeof c>[0] {
  switch (reason) {
    case 'external_rule_write_unproven':
      return 'reasonExternalRules';
    case 'private_profile_scope_frozen':
      return 'reasonPrivateFrozen';
    case 'config_journal_invalid':
      return 'reasonJournalInvalid';
    case 'original_operation_unknown':
      return 'reasonUnknownOriginal';
    case 'config_read_unavailable':
      return 'reasonReadUnavailable';
    case 'installation_incomplete':
      return 'reasonNotInstalled';
    default:
      return 'reasonAuthorityUnavailable';
  }
}

