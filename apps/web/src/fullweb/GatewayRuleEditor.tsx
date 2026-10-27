import { useState } from 'react';
import { Modal } from '../components/ui/Modal';
import { configurationText as c } from '../i18n/configurationWorkspace';
import type { GatewayFamilyMeta } from './gatewayCatalog';
import {
  CONDITION_KINDS,
  PROTOCOLS,
  VALUE_TYPES,
  formToRule,
  parseRuleText,
  ruleSummary,
  ruleToForm,
  type RuleConditionKind,
  type RuleConditionRow,
  type RuleForm,
  type RuleIssue,
  type RuleParamType,
  type RuleValueType,
} from './gatewayRuleForm';

// One payload rule family editor. The structured form and the advanced JSON view
// are two representations of the same draft; the structured form is authoritative
// while it is open, and switching to JSON serializes it first so nothing is lost.

const ROW_TYPE_LABEL: Record<RuleValueType, Parameters<typeof c>[0]> = {
  string: 'typeString',
  number: 'typeNumber',
  boolean: 'typeBoolean',
  null: 'typeNull',
  array: 'typeArray',
  object: 'typeObject',
};

const KIND_LABEL: Record<RuleConditionKind, Parameters<typeof c>[0]> = {
  headers: 'conditionHeaders',
  match: 'conditionMatch',
  'not-match': 'conditionNotMatch',
  exist: 'conditionExist',
  'not-exist': 'conditionNotExist',
};

export function GatewayRuleEditor({
  family,
  rules,
  readOnly,
  onCancel,
  onApply,
}: {
  family: GatewayFamilyMeta;
  rules: unknown[];
  readOnly: boolean;
  onCancel: () => void;
  onApply: (rules: unknown[]) => void;
}) {
  const [list, setList] = useState<unknown[]>(() => rules.map((rule) => structuredClone(rule)));
  const [editing, setEditing] = useState<number | null>(null);
  const [form, setForm] = useState<RuleForm | null>(null);
  const [jsonText, setJsonText] = useState('');
  const [mode, setMode] = useState<'structured' | 'json'>('structured');
  const [issue, setIssue] = useState<string>('');

  const hint = c(family.hintKey);
  function startEdit(index: number | null) {
    const target = index === null ? emptyRule(family) : list[index];
    setForm(ruleToForm(target, family));
    setJsonText(JSON.stringify(target, null, 2));
    setMode('structured');
    setIssue('');
    setEditing(index === null ? list.length : index);
  }

  function commitEdit() {
    if (editing === null || !form) return;
    const result = mode === 'json' ? parseRuleText(jsonText, family) : formToRule(form, family);
    if (!result.ok) {
      setIssue(c(RULE_ISSUE_TEXT[result.issue]));
      return;
    }
    setList((current) => {
      const next = [...current];
      next[editing] = result.rule;
      return next;
    });
    setEditing(null);
    setForm(null);
    setIssue('');
  }

  function switchMode(next: 'structured' | 'json') {
    if (!form) return;
    if (next === 'json') {
      const result = formToRule(form, family);
      if (result.ok) setJsonText(JSON.stringify(result.rule, null, 2));
      setMode('json');
      return;
    }
    const result = parseRuleText(jsonText, family);
    if (!result.ok) {
      setIssue(c(RULE_ISSUE_TEXT[result.issue]));
      return;
    }
    setForm(ruleToForm(result.rule, family));
    setMode('structured');
    setIssue('');
  }

  function move(from: number, to: number) {
    setList((current) => {
      if (to < 0 || to >= current.length) return current;
      const next = [...current];
      const [item] = next.splice(from, 1);
      next.splice(to, 0, item);
      return next;
    });
  }

  function copy(index: number) {
    setList((current) => {
      const next = [...current];
      next.splice(index + 1, 0, structuredClone(current[index]));
      return next;
    });
  }

  const footer = editing === null ? (
    <>
      <button className="btn btn-secondary" data-testid="rule-close" onClick={onCancel}>
        {c('ruleCancel')}
      </button>
      <button
        className="btn btn-primary"
        data-testid="rule-save"
        disabled={readOnly}
        onClick={() => onApply(list)}
      >
        {c('ruleSaveDraft')}
      </button>
    </>
  ) : (
    <>
      <button className="btn btn-secondary" data-testid="rule-editor-cancel" onClick={() => setEditing(null)}>
        {c('ruleBackToList')}
      </button>
      <button className="btn btn-primary" data-testid="rule-editor-apply" onClick={commitEdit}>
        {c('ruleApplyToDraft')}
      </button>
    </>
  );

  const editorOpen = form !== null && editing !== null;

  return (
    <Modal
      open
      className="gateway-rule-dialog"
      width={editorOpen ? 880 : 760}
      title={c('rulesTitle') + ' · ' + c(family.labelKey)}
      onClose={onCancel}
      footer={footer}
    >
      <div className="rule-editor" data-testid="gateway-rule-editor">
        <p className="notice">{hint}</p>
        {readOnly && (
          <p className="notice warn" data-testid="rule-readonly">
            {c('rulesReadOnly')}
          </p>
        )}
        {issue && (
          <p className="field-error" role="alert" data-testid="rule-error">
            {issue}
          </p>
        )}
        {!editorOpen && (
          <div className="rule-list" data-testid="rule-list">
            {list.map((rule, index) => (
              <div className="rule-summary" key={index} data-testid={'rule-' + index}>
                <div>
                  <span className="badge">{index + 1}</span>
                  <strong>{ruleSummary(rule, family)}</strong>
                </div>
                <div className="actions">
                  <button
                    className="btn btn-secondary btn-sm"
                    data-testid={'rule-edit-' + index}
                    disabled={readOnly}
                    onClick={() => startEdit(index)}
                  >
                    {c('ruleEdit')}
                  </button>
                  <button
                    className="btn btn-secondary btn-sm"
                    data-testid={'rule-copy-' + index}
                    disabled={readOnly}
                    onClick={() => copy(index)}
                  >
                    {c('ruleCopy')}
                  </button>
                  <button
                    className="btn btn-secondary btn-sm"
                    data-testid={'rule-up-' + index}
                    disabled={readOnly || index === 0}
                    onClick={() => move(index, index - 1)}
                  >
                    {c('ruleMoveUp')}
                  </button>
                  <button
                    className="btn btn-secondary btn-sm"
                    data-testid={'rule-down-' + index}
                    disabled={readOnly || index === list.length - 1}
                    onClick={() => move(index, index + 1)}
                  >
                    {c('ruleMoveDown')}
                  </button>
                  <button
                    className="btn btn-secondary btn-sm"
                    data-testid={'rule-remove-' + index}
                    disabled={readOnly}
                    onClick={() => setList((current) => current.filter((_, at) => at !== index))}
                  >
                    {c('ruleRemove')}
                  </button>
                </div>
              </div>
            ))}
            {!list.length && (
              <p className="muted" data-testid="rule-empty">
                {c('ruleEmpty')}
              </p>
            )}
            <button
              className="btn btn-secondary"
              data-testid="rule-add"
              disabled={readOnly}
              onClick={() => startEdit(null)}
            >
              {c('ruleAdd')}
            </button>
          </div>
        )}
        {editorOpen && form && (
          <div className="rule-form">
            <div className="editor-modes" role="group" aria-label={c('ruleMode')}>
              <button
                className="btn btn-secondary btn-sm"
                aria-pressed={mode === 'structured'}
                data-testid="rule-mode-structured"
                onClick={() => switchMode('structured')}
              >
                {c('ruleStructured')}
              </button>
              <button
                className="btn btn-secondary btn-sm"
                aria-pressed={mode === 'json'}
                data-testid="rule-mode-json"
                onClick={() => switchMode('json')}
              >
                {c('ruleJson')}
              </button>
            </div>
            {mode === 'json' ? (
              <label className="dialog-label">
                {c('ruleJsonLabel')}
                <textarea
                  data-testid="rule-json"
                  rows={16}
                  value={jsonText}
                  onChange={(event) => setJsonText(event.target.value)}
                />
              </label>
            ) : (
              <>
                <h3>{c('ruleModels')}</h3>
                {form.models.map((model, modelIndex) => (
                  <fieldset key={modelIndex} data-testid={'rule-model-' + modelIndex}>
                    <legend>{c('ruleModelLegend', modelIndex + 1)}</legend>
                    <div className="model-grid">
                      <label>
                        {c('ruleModelName')}
                        <input
                          data-testid={'rule-model-name-' + modelIndex}
                          value={model.name}
                          placeholder="gpt-*"
                          onChange={(event) =>
                            setForm((current) =>
                              current
                                ? {
                                    ...current,
                                    models: current.models.map((item, at) =>
                                      at === modelIndex ? { ...item, name: event.target.value } : item
                                    ),
                                  }
                                : current
                            )
                          }
                        />
                      </label>
                      <label>
                        {c('ruleProtocol')}
                        <select
                          data-testid={'rule-model-protocol-' + modelIndex}
                          value={model.protocol}
                          onChange={(event) =>
                            setForm((current) =>
                              current
                                ? {
                                    ...current,
                                    models: current.models.map((item, at) =>
                                      at === modelIndex ? { ...item, protocol: event.target.value } : item
                                    ),
                                  }
                                : current
                            )
                          }
                        >
                          {PROTOCOLS.map((value) => (
                            <option key={value} value={value}>
                              {value || c('ruleAny')}
                            </option>
                          ))}
                        </select>
                      </label>
                      <label>
                        {c('ruleFromProtocol')}
                        <select
                          data-testid={'rule-model-from-' + modelIndex}
                          value={model.from}
                          onChange={(event) =>
                            setForm((current) =>
                              current
                                ? {
                                    ...current,
                                    models: current.models.map((item, at) =>
                                      at === modelIndex ? { ...item, from: event.target.value } : item
                                    ),
                                  }
                                : current
                            )
                          }
                        >
                          {PROTOCOLS.map((value) => (
                            <option key={value} value={value}>
                              {value || c('ruleAny')}
                            </option>
                          ))}
                        </select>
                      </label>
                    </div>
                    <h3>{c('ruleConditions')}</h3>
                    <p className="detail">{c('ruleConditionsHelp')}</p>
                    {model.rows.map((row, rowIndex) => (
                      <div className="structured-row" key={rowIndex} data-condition-row={rowIndex}>
                        <label>
                          {c('ruleConditionKind')}
                          <select
                            value={row.kind}
                            onChange={(event) =>
                              updateRow(setForm, modelIndex, rowIndex, {
                                kind: event.target.value as RuleConditionKind,
                                type: 'string',
                                value: '',
                              })
                            }
                          >
                            {CONDITION_KINDS.map((kind) => (
                              <option key={kind} value={kind}>
                                {c(KIND_LABEL[kind])}
                              </option>
                            ))}
                          </select>
                        </label>
                        <label>
                          {c('rulePath')}
                          <input
                            value={row.path}
                            placeholder={row.kind === 'headers' ? 'X-Client-Tier' : 'metadata.client'}
                            onChange={(event) =>
                              updateRow(setForm, modelIndex, rowIndex, { path: event.target.value })
                            }
                          />
                        </label>
                        {row.kind === 'exist' || row.kind === 'not-exist' ? null : (
                          <>
                            <label>
                              {c('ruleType')}
                              <select
                                value={row.type}
                                disabled={row.kind === 'headers'}
                                onChange={(event) =>
                                  updateRow(setForm, modelIndex, rowIndex, {
                                    type: event.target.value as RuleValueType,
                                    value: event.target.value === 'boolean' ? 'true' : row.value,
                                  })
                                }
                              >
                                {VALUE_TYPES.map((type) => (
                                  <option key={type} value={type}>
                                    {c(ROW_TYPE_LABEL[type])}
                                  </option>
                                ))}
                              </select>
                            </label>
                            <label>
                              {c('ruleValue')}
                              <input
                                value={row.value}
                                disabled={row.type === 'null'}
                                onChange={(event) =>
                                  updateRow(setForm, modelIndex, rowIndex, { value: event.target.value })
                                }
                              />
                            </label>
                          </>
                        )}
                        <button
                          className="btn btn-secondary btn-sm"
                          onClick={() =>
                            setForm((current) =>
                              current
                                ? {
                                    ...current,
                                    models: current.models.map((item, at) =>
                                      at === modelIndex
                                        ? { ...item, rows: item.rows.filter((_, index) => index !== rowIndex) }
                                        : item
                                    ),
                                  }
                                : current
                            )
                          }
                        >
                          {c('ruleRemove')}
                        </button>
                      </div>
                    ))}
                    <button
                      className="btn btn-secondary btn-sm"
                      data-testid={'rule-condition-add-' + modelIndex}
                      onClick={() =>
                        setForm((current) =>
                          current
                            ? {
                                ...current,
                                models: current.models.map((item, at) =>
                                  at === modelIndex
                                    ? {
                                        ...item,
                                        rows: [...item.rows, { kind: 'headers', path: '', type: 'string', value: '' }],
                                      }
                                    : item
                                ),
                              }
                            : current
                        )
                      }
                    >
                      {c('ruleAddCondition')}
                    </button>
                    <div className="actions">
                      <button
                        className="btn btn-secondary btn-sm"
                        disabled={form.models.length === 1}
                        onClick={() =>
                          setForm((current) =>
                            current
                              ? { ...current, models: current.models.filter((_, at) => at !== modelIndex) }
                              : current
                          )
                        }
                      >
                        {c('ruleRemoveModel')}
                      </button>
                    </div>
                  </fieldset>
                ))}
                <button
                  className="btn btn-secondary btn-sm"
                  data-testid="rule-model-add"
                  onClick={() =>
                    setForm((current) =>
                      current
                        ? {
                            ...current,
                            models: [...current.models, { name: '', protocol: '', from: '', rows: [] }],
                          }
                        : current
                    )
                  }
                >
                  {c('ruleAddModel')}
                </button>
                <h3>{c(family.filter ? 'rulePaths' : 'ruleParams')}</h3>
                {form.params.map((row, rowIndex) => (
                  <div
                    className={'structured-row' + (family.filter ? ' path-only' : '')}
                    key={rowIndex}
                    data-param-row={rowIndex}
                  >
                    <label>
                      {c('rulePath')}
                      <input
                        value={row.path}
                        placeholder={family.filter ? 'metadata.client' : 'reasoning.effort'}
                        onChange={(event) =>
                          setForm((current) =>
                            current
                              ? {
                                  ...current,
                                  params: current.params.map((item, at) =>
                                    at === rowIndex ? { ...item, path: event.target.value } : item
                                  ),
                                }
                              : current
                          )
                        }
                      />
                    </label>
                    {family.filter ? null : (
                      <>
                        <label>
                          {c('ruleType')}
                          <select
                            value={row.type}
                            disabled={family.raw}
                            onChange={(event) =>
                              setForm((current) =>
                                current
                                  ? {
                                      ...current,
                                      params: current.params.map((item, at) =>
                                        at === rowIndex
                                          ? { ...item, type: event.target.value as RuleParamType }
                                          : item
                                      ),
                                    }
                                  : current
                              )
                            }
                          >
                            {(family.raw ? (['raw'] as RuleParamType[]) : (VALUE_TYPES as RuleParamType[])).map(
                              (type) => (
                                <option key={type} value={type}>
                                  {type === 'raw' ? c('typeRaw') : c(ROW_TYPE_LABEL[type as RuleValueType])}
                                </option>
                              )
                            )}
                          </select>
                        </label>
                        <label>
                          {c('ruleValue')}
                          <input
                            value={row.value}
                            disabled={row.type === 'null'}
                            onChange={(event) =>
                              setForm((current) =>
                                current
                                  ? {
                                      ...current,
                                      params: current.params.map((item, at) =>
                                        at === rowIndex ? { ...item, value: event.target.value } : item
                                      ),
                                    }
                                  : current
                              )
                            }
                          />
                        </label>
                      </>
                    )}
                    <button
                      className="btn btn-secondary btn-sm"
                      onClick={() =>
                        setForm((current) =>
                          current
                            ? { ...current, params: current.params.filter((_, index) => index !== rowIndex) }
                            : current
                        )
                      }
                    >
                      {c('ruleRemove')}
                    </button>
                  </div>
                ))}
                <button
                  className="btn btn-secondary btn-sm"
                  data-testid="rule-param-add"
                  onClick={() =>
                    setForm((current) =>
                      current
                        ? {
                            ...current,
                            params: [
                              ...current.params,
                              { path: '', type: family.raw ? 'raw' : 'string', value: '' },
                            ],
                          }
                        : current
                    )
                  }
                >
                  {c(family.filter ? 'ruleAddPath' : 'ruleAddParam')}
                </button>
              </>
            )}
            <p className="detail">{c('ruleDraftNotice')}</p>
          </div>
        )}
      </div>
    </Modal>
  );
}

const RULE_ISSUE_TEXT: Record<RuleIssue, Parameters<typeof c>[0]> = {
  modelName: 'ruleIssueModelName',
  conditionPath: 'ruleIssueConditionPath',
  duplicateCondition: 'ruleIssueDuplicateCondition',
  conditionValue: 'ruleIssueConditionValue',
  headerValue: 'ruleIssueHeaderValue',
  paramPath: 'ruleIssueParamPath',
  duplicateParam: 'ruleIssueDuplicateParam',
  paramValue: 'ruleIssueParamValue',
  rawValue: 'ruleIssueRawValue',
  emptyParams: 'ruleIssueEmptyParams',
  jsonInvalid: 'ruleIssueJsonInvalid',
  jsonConditions: 'ruleIssueJsonConditions',
  jsonParams: 'ruleIssueJsonParams',
};

function emptyRule(family: GatewayFamilyMeta): unknown {
  return family.filter
    ? { models: [{ name: '*', params: [] }], params: [] }
    : { models: [{ name: '*' }], params: {} };
}

function updateRow(
  setForm: (updater: (current: RuleForm | null) => RuleForm | null) => void,
  modelIndex: number,
  rowIndex: number,
  patch: Partial<RuleConditionRow>
) {
  setForm((current) =>
    current
      ? {
          ...current,
          models: current.models.map((model, at) =>
            at === modelIndex
              ? {
                  ...model,
                  rows: model.rows.map((row, index) =>
                    index === rowIndex ? { ...row, ...patch } : row
                  ),
                }
              : model
          ),
        }
      : current
  );
}
