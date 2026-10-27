import { describe, expect, it } from 'vitest';
import {
  isWideInteger,
  parseGatewayJSON,
  stringifyGatewayJSON,
  wideIntegerText,
} from './gatewayRuleJSON';
import { parseRuleText } from './gatewayRuleForm';
import { gatewayFamilies } from './gatewayCatalog';

// 2^53 + 1: the first integer a double cannot hold, and the literal a rule
// parameter can legally carry.
const WIDE = '9007199254740993';

describe('lossless gateway rule JSON', () => {
  it('keeps a wide integer exact instead of rounding it', () => {
    const parsed = parseGatewayJSON(`{"size":${WIDE},"name":"a"}`);
    expect(parsed).toEqual({ size: { __cpamp_wide_integer__: WIDE }, name: 'a' });
    if (typeof parsed !== 'object' || parsed === null || !('size' in parsed)) {
      throw new Error('parsed rule lost its size parameter');
    }
    const value = parsed.size;
    expect(isWideInteger(value)).toBe(true);
    if (isWideInteger(value)) expect(wideIntegerText(value)).toBe(WIDE);
    // The obvious alternative would already have lost it.
    expect(JSON.parse(`{"size":${WIDE}}`)).toEqual({ size: 9007199254740992 });
  });

  it('re-serialises the same wide integer as a JSON number, unchanged', () => {
    const source = `{"models":[{"name":"*"}],"params":{"size":${WIDE},"ok":true,"name":"x"}}`;
    const parsed = parseGatewayJSON(source);
    expect(stringifyGatewayJSON(parsed)).toBe(source);
    // A string that merely looks like a number stays a string.
    expect(stringifyGatewayJSON(parseGatewayJSON('{"a":"9007199254740993"}'))).toBe(
      '{"a":"9007199254740993"}'
    );
  });

  it('survives a draft clone, which drops symbol-keyed markers', () => {
    const parsed = parseGatewayJSON(`{"size":${WIDE}}`);
    const cloned = structuredClone(parsed);
    expect(stringifyGatewayJSON(cloned)).toBe(`{"size":${WIDE}}`);
  });

  it('keeps safe integers and fractional numbers untouched', () => {
    expect(stringifyGatewayJSON(parseGatewayJSON('{"a":3,"b":1.5,"c":-2}'))).toBe(
      '{"a":3,"b":1.5,"c":-2}'
    );
  });

  it('leaves zero and every safe integer a plain number', () => {
    // A leaf whose value is 0 is ordinary data, not a wide integer: tagging it
    // would render "[object Object]" as both value and placeholder.
    const parsed = parseGatewayJSON('{"request-retry":0,"sixteen":9007199254740991}');
    expect(parsed).toEqual({ 'request-retry': 0, sixteen: 9007199254740991 });
    expect(isWideInteger((parsed as Record<string, unknown>)['request-retry'])).toBe(false);
    // A rule parameter 0 stays a number through the round trip too.
    expect(stringifyGatewayJSON(parseGatewayJSON('{"params":{"temperature":0}}'))).toBe(
      '{"params":{"temperature":0}}'
    );
    expect(parseGatewayJSON('{"a":0}')).toEqual({ a: 0 });
    // 2^53 - 1 is the widest safe integer; 2^53 is not.
    expect(isWideInteger((parseGatewayJSON('{"a":9007199254740991}') as Record<string, unknown>).a)).toBe(false);
    expect(isWideInteger((parseGatewayJSON('{"a":9007199254740992}') as Record<string, unknown>).a)).toBe(true);
    expect(isWideInteger((parseGatewayJSON('{"a":-9007199254740993}') as Record<string, unknown>).a)).toBe(true);
  });

  it('parses bracket digits inside a string as a string, not as numbers', () => {
    // A regex that only requires a structural character before a number can see
    // "[0,1]" inside a string as two numbers, corrupting the text it rewrites.
    const source = '{"h":"[0,1]","q":"a\\"1\\"b","n":1}';
    expect(parseGatewayJSON(source)).toEqual({ h: '[0,1]', q: 'a"1"b', n: 1 });
    expect(stringifyGatewayJSON(parseGatewayJSON(source))).toBe(source);
    expect(stringifyGatewayJSON(parseGatewayJSON('{"h":"[9007199254740993,2]"}')))
      .toBe('{"h":"[9007199254740993,2]"}');
  });

  it('carries the wide integer through the rule editor round trip', () => {
    const family = gatewayFamilies().find((entry) => entry.family === 'filter')!;
    const rule = `{"models":[{"name":"gpt-*"}],"params":["metadata.${WIDE}"]}`;
    const parsed = parseRuleText(rule, family);
    expect(parsed.ok).toBe(true);
    if (!parsed.ok) return;
    // A typed JSON edit is kept verbatim, so the wide parameter survives.
    expect(stringifyGatewayJSON(parsed.rule)).toBe(rule);
  });

  it('carries a wide integer parameter through the structured form', () => {
    const parsed = parseGatewayJSON(
      `{"models":[{"name":"*"}],"params":{"requests.payload.limit":${WIDE}}}`
    );
    const text = stringifyGatewayJSON(parsed);
    expect(text).toContain(WIDE);
    expect(text).not.toContain('9007199254740992');
  });
});
