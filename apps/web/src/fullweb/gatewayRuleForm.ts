import { isRecord } from '../utils/helpers';
import { configurationText as c } from '../i18n/configurationWorkspace';
import type { GatewayFamilyMeta } from './gatewayCatalog';
import { parseGatewayJSON, stringifyGatewayJSON } from './gatewayRuleJSON';

export type RuleConditionKind = 'headers' | 'match' | 'not-match' | 'exist' | 'not-exist';
export type RuleValueType = 'string' | 'number' | 'boolean' | 'null' | 'array' | 'object';
export type RuleParamType = RuleValueType | 'raw';

export interface RuleConditionRow {
  kind: RuleConditionKind;
  path: string;
  type: RuleValueType;
  value: string;
}

export interface RuleModelForm {
  name: string;
  protocol: string;
  from: string;
  rows: RuleConditionRow[];
}

export interface RuleParamRow {
  path: string;
  type: RuleParamType;
  value: string;
}

export interface RuleForm {
  models: RuleModelForm[];
  params: RuleParamRow[];
}

export type RuleIssue =
  | 'modelName'
  | 'conditionPath'
  | 'duplicateCondition'
  | 'conditionValue'
  | 'headerValue'
  | 'paramPath'
  | 'duplicateParam'
  | 'paramValue'
  | 'rawValue'
  | 'emptyParams'
  | 'jsonInvalid'
  | 'jsonConditions'
  | 'jsonParams';

export type RuleResult = { ok: true; rule: unknown } | { ok: false; issue: RuleIssue; detail?: string };

export const CONDITION_KINDS: RuleConditionKind[] = ['headers', 'match', 'not-match', 'exist', 'not-exist'];
export const VALUE_TYPES: RuleValueType[] = ['string', 'number', 'boolean', 'null', 'array', 'object'];
export const PROTOCOLS = ['', 'openai', 'responses', 'gemini', 'claude', 'codex', 'antigravity'];

function typedValue(type: RuleValueType, raw: string): unknown | { issue: RuleIssue } {
  switch (type) {
    case 'number': {
      const trimmed = raw.trim();
      if (trimmed === '' || !Number.isFinite(Number(trimmed))) return { issue: 'conditionValue' };
      return Number(trimmed);
    }
    case 'boolean':
      return raw === 'true';
    case 'null':
      return null;
    case 'array':
    case 'object': {
      try {
        const parsed: unknown = JSON.parse(raw);
        if (type === 'array' ? !Array.isArray(parsed) : !isRecord(parsed)) {
          return { issue: 'conditionValue' };
        }
        return parsed;
      } catch {
        return { issue: 'conditionValue' };
      }
    }
    default:
      return raw;
  }
}

function displayValue(value: unknown): string {
  if (typeof value === 'string') return value;
  if (value === null) return 'null';
  return stringifyGatewayJSON(value);
}

function typeOfValue(value: unknown): RuleValueType {
  if (value === null) return 'null';
  if (Array.isArray(value)) return 'array';
  if (typeof value === 'number') return 'number';
  if (typeof value === 'boolean') return 'boolean';
  if (typeof value === 'object') return 'object';
  return 'string';
}

/** ruleToForm renders one CPA rule into the structured editor's state. */
export function ruleToForm(rule: unknown, family: GatewayFamilyMeta): RuleForm {
  const object = isRecord(rule) ? rule : {};
  const models = Array.isArray(object.models) ? object.models : [];
  const form: RuleForm = {
    models: models.map((model) => {
      const entry = isRecord(model) ? model : {};
      const rows: RuleConditionRow[] = [];
      if (isRecord(entry.headers)) {
        for (const [path, value] of Object.entries(entry.headers)) {
          rows.push({ kind: 'headers', path, type: 'string', value: displayValue(value) });
        }
      }
      for (const kind of ['match', 'not-match'] as const) {
        const values = entry[kind];
        if (!Array.isArray(values)) continue;
        for (const item of values) {
          if (!isRecord(item)) continue;
          for (const [path, value] of Object.entries(item)) {
            rows.push({ kind, path, type: typeOfValue(value), value: displayValue(value) });
          }
        }
      }
      for (const kind of ['exist', 'not-exist'] as const) {
        const values = entry[kind];
        if (!Array.isArray(values)) continue;
        for (const path of values) {
          rows.push({ kind, path: String(path), type: 'string', value: '' });
        }
      }
      return {
        name: typeof entry.name === 'string' ? entry.name : '',
        protocol: typeof entry.protocol === 'string' ? entry.protocol : '',
        from: typeof entry['from-protocol'] === 'string' ? entry['from-protocol'] : '',
        rows,
      };
    }),
    params: [],
  };
  const params = object.params;
  if (family.filter && Array.isArray(params)) {
    form.params = params.map((path) => ({ path: String(path), type: 'string', value: '' }));
    return form;
  }
  if (isRecord(params)) {
    form.params = Object.entries(params).map(([path, value]) => ({
      path,
      type: family.raw ? 'raw' : typeOfValue(value),
      value: displayValue(value),
    }));
  }
  if (!form.models.length) {
    form.models = [{ name: '', protocol: '', from: '', rows: [] }];
  }
  return form;
}

/** formToRule serializes the structured editor back into a CPA rule. */
export function formToRule(form: RuleForm, family: GatewayFamilyMeta): RuleResult {
  const models: Record<string, unknown>[] = [];
  for (const model of form.models) {
    const name = model.name.trim();
    if (!name) return { ok: false, issue: 'modelName' };
    const conditions: Record<string, unknown> = {};
    const headerNames = new Set<string>();
    const paths = new Set<string>();
    for (const row of model.rows) {
      const path = row.path.trim();
      if (!path) return { ok: false, issue: 'conditionPath' };
      if (row.kind === 'headers') {
        const folded = path.toLowerCase();
        if (headerNames.has(folded)) return { ok: false, issue: 'duplicateCondition', detail: path };
        headerNames.add(folded);
        const headers = isRecord(conditions.headers) ? conditions.headers : {};
        headers[path] = row.value;
        conditions.headers = headers;
        continue;
      }
      if (row.kind === 'exist' || row.kind === 'not-exist') {
        const list = Array.isArray(conditions[row.kind]) ? (conditions[row.kind] as string[]) : [];
        if (list.includes(path)) return { ok: false, issue: 'duplicateCondition', detail: path };
        list.push(path);
        conditions[row.kind] = list;
        continue;
      }
      const list = Array.isArray(conditions[row.kind])
        ? (conditions[row.kind] as Record<string, unknown>[])
        : [];
      if (paths.has(`${row.kind}:${path}`)) {
        return { ok: false, issue: 'duplicateCondition', detail: path };
      }
      paths.add(`${row.kind}:${path}`);
      const value = typedValue(row.type, row.value);
      if (isRecord(value) && 'issue' in value) return { ok: false, issue: 'conditionValue' };
      list.push({ [path]: value });
      conditions[row.kind] = list;
    }
    if (conditions.headers && Object.values(conditions.headers as Record<string, unknown>).some((value) => typeof value !== 'string')) {
      return { ok: false, issue: 'headerValue' };
    }
    models.push({
      name,
      ...(model.protocol ? { protocol: model.protocol } : {}),
      ...(model.from ? { 'from-protocol': model.from } : {}),
      ...conditions,
    });
  }
  if (family.filter) {
    const params: string[] = [];
    for (const row of form.params) {
      const path = row.path.trim();
      if (!path) return { ok: false, issue: 'paramPath' };
      if (params.includes(path)) return { ok: false, issue: 'duplicateParam', detail: path };
      params.push(path);
    }
    if (!params.length) return { ok: false, issue: 'emptyParams' };
    return { ok: true, rule: { models, params } };
  }
  const params: Record<string, unknown> = {};
  for (const row of form.params) {
    const path = row.path.trim();
    if (!path) return { ok: false, issue: 'paramPath' };
    if (Object.prototype.hasOwnProperty.call(params, path)) return { ok: false, issue: 'duplicateParam', detail: path };
    if (family.raw) {
      try {
        JSON.parse(row.value);
      } catch {
        return { ok: false, issue: 'rawValue', detail: path };
      }
      params[path] = row.value;
      continue;
    }
    const value = typedValue(row.type === 'raw' ? 'string' : row.type, row.value);
    if (isRecord(value) && 'issue' in value) return { ok: false, issue: 'paramValue', detail: path };
    params[path] = value;
  }
  if (!Object.keys(params).length) return { ok: false, issue: 'emptyParams' };
  return { ok: true, rule: { models, params } };
}

/** parseRuleText validates one rule typed as JSON and keeps it verbatim. */
export function parseRuleText(source: string, family: GatewayFamilyMeta): RuleResult {
  let parsed: unknown;
  try {
    // A rule parameter can be a 64-bit integer; the plain parser would round it
    // and the page would PUT a different number than the user typed.
    parsed = parseGatewayJSON(source);
  } catch {
    return { ok: false, issue: 'jsonInvalid' };
  }
  if (!isRecord(parsed)) return { ok: false, issue: 'jsonInvalid' };
  for (const key of Object.keys(parsed)) {
    if (key !== 'models' && key !== 'params') return { ok: false, issue: 'jsonConditions' };
  }
  if (family.filter ? !Array.isArray(parsed.params) : !isRecord(parsed.params)) {
    return { ok: false, issue: 'jsonParams' };
  }
  const models = parsed.models;
  if (!Array.isArray(models) || !models.length) return { ok: false, issue: 'jsonConditions' };
  for (const model of models) {
    if (!isRecord(model) || typeof model.name !== 'string' || !model.name.trim()) {
      return { ok: false, issue: 'jsonConditions' };
    }
  }
  if (family.raw) {
    for (const value of Object.values(parsed.params as Record<string, unknown>)) {
      if (typeof value !== 'string') return { ok: false, issue: 'jsonParams' };
    }
  }
  return { ok: true, rule: parsed };
}

export function ruleSummary(rule: unknown, family: GatewayFamilyMeta): string {
  const object = isRecord(rule) ? rule : {};
  const models = Array.isArray(object.models)
    ? object.models
        .map((model) => (isRecord(model) && typeof model.name === 'string' ? model.name : '?'))
        .join(', ')
    : '?';
  const params = family.filter
    ? Array.isArray(object.params)
      ? object.params.length
      : 0
    : isRecord(object.params)
      ? Object.keys(object.params).length
      : 0;
  return `${models} · ${c(family.filter ? 'rulePathCount' : 'ruleParamCount', params)}`;
}
