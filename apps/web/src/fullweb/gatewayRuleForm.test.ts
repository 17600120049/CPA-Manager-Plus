import { describe, expect, it } from 'vitest';
import { isRecord } from '../utils/helpers';
import {
  formToRule,
  parseRuleText,
  ruleSummary,
  ruleToForm,
} from './gatewayRuleForm';
import { gatewayFamilies } from './gatewayCatalog';

const families = Object.fromEntries(gatewayFamilies().map((family) => [family.family, family]));

describe('gateway rule form', () => {
  it('round-trips a standard rule without losing a condition or a parameter', () => {
    const rule = {
      models: [
        {
          name: 'gpt-*',
          protocol: 'codex',
          'from-protocol': 'responses',
          headers: { 'X-Tier': 'gold' },
          match: [{ 'metadata.client': 'x' }, { 'metadata.n': 2 }],
          'not-match': [{ 'metadata.beta': true }],
          exist: ['metadata.tags'],
          'not-exist': ['tools'],
        },
      ],
      params: { 'reasoning.effort': 'high', temperature: 0.2 },
    };
    const form = ruleToForm(rule, families.default);
    const result = formToRule(form, families.default);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    // The structured form is lossless for everything the CPAMP editor accepts.
    expect(result.rule).toEqual(rule);
  });

  it('keeps raw family values as JSON strings and rejects a non-JSON value', () => {
    const form = ruleToForm(
      { models: [{ name: 'gpt-*' }], params: { response_format: '{"type":"json_object"}' } },
      families['default-raw']
    );
    const result = formToRule(form, families['default-raw']);
    expect(result.ok).toBe(true);
    if (!result.ok) return;
    expect(isRecord(result.rule) && isRecord(result.rule.params)).toBe(true);
    if (isRecord(result.rule) && isRecord(result.rule.params)) {
      expect(result.rule.params.response_format).toBe('{"type":"json_object"}');
    }

    form.params[0].value = '{';
    const broken = formToRule(form, families['default-raw']);
    expect(broken.ok).toBe(false);
    if (!broken.ok) expect(broken.issue).toBe('rawValue');
  });

  it('rejects a duplicated rule path', () => {
    const filter = ruleToForm(
      { models: [{ name: '*' }], params: ['metadata.client'] },
      families.filter
    );
    filter.params.push({ path: 'metadata.client', type: 'string', value: '' });
    const duplicated = formToRule(filter, families.filter);
    expect(duplicated.ok).toBe(false);
    if (!duplicated.ok) expect(duplicated.issue).toBe('duplicateParam');

    const standard = ruleToForm({ models: [{ name: '*' }], params: {} }, families.default);
    standard.models[0].rows.push(
      { kind: 'exist', path: 'metadata.tags', type: 'string', value: '' },
      { kind: 'exist', path: 'metadata.tags', type: 'string', value: '' }
    );
    standard.params.push({ path: 'top_p', type: 'number', value: '1' });
    const duplicateCondition = formToRule(standard, families.default);
    expect(duplicateCondition.ok).toBe(false);
    if (!duplicateCondition.ok) expect(duplicateCondition.issue).toBe('duplicateCondition');
  });

  it('requires a model pattern and at least one parameter', () => {
    const form = ruleToForm({ models: [{ name: '*' }], params: {} }, families.default);
    form.models[0].name = '';
    const noName = formToRule(form, families.default);
    expect(noName.ok).toBe(false);
    if (!noName.ok) expect(noName.issue).toBe('modelName');

    form.models[0].name = '*';
    const noParams = formToRule(form, families.default);
    expect(noParams.ok).toBe(false);
    if (!noParams.ok) expect(noParams.issue).toBe('emptyParams');
  });

  it('validates advanced JSON without silently rewriting it', () => {
    const verbatim = '{"models":[{"name":"*","custom":1}],"params":{"a":"b"}}';
    const accepted = parseRuleText(verbatim, families.default);
    expect(accepted.ok).toBe(true);
    if (accepted.ok) expect(JSON.stringify(accepted.rule)).toBe(verbatim);

    for (const [family, body] of [
      ['default', '{'],
      ['default', '{"models":[],"params":{}}'],
      ['default', '{"models":[{"name":""}],"params":{}}'],
      ['default', '{"models":[{"name":"*"}],"params":{},"extra":1}'],
      ['default', '{"models":[{"name":"*"}],"params":[]}'],
      ['filter', '{"models":[{"name":"*"}],"params":{}}'],
      ['default-raw', '{"models":[{"name":"*"}],"params":{"a":{"b":1}}}'],
    ] as const) {
      expect(parseRuleText(body, families[family]).ok, `${family} ${body}`).toBe(false);
    }
  });

  it('summarizes a rule for the list without a translated value', () => {
    const summary = ruleSummary(
      { models: [{ name: 'gpt-*' }, { name: 'o3-*' }], params: { a: 1, b: 2 } },
      families.default
    );
    expect(summary).toContain('gpt-*, o3-*');
    expect(ruleSummary({ models: [{ name: '*' }], params: ['a'] }, families.filter)).toContain('*');
  });
});
