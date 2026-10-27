// CPA's payload rules are arbitrary JSON and can carry integers wider than JS's
// safe range: a rule parameter is whatever the user's YAML held, and a 64-bit
// counter is a legal value. `JSON.parse` rounds such a literal, so the page would
// PUT a different number than the one it showed. These helpers keep every wide
// integer exact: it parses into a tagged value carrying the original decimal
// literal, and it is re-emitted as a JSON number — never a string — on the way
// back to the Manager.
//
// The tag is a single ordinary string key, not a symbol, because editor drafts
// are deep-cloned with `structuredClone`, which drops symbol-keyed properties and
// prototypes. A tagged value is therefore still a plain, cloneable object.

const WIDE_KEY = '__cpamp_wide_integer__';
const WIDE_INTEGER = /^-?(?:0|[1-9][0-9]{15,})$/;

/** A JSON integer this runtime cannot represent exactly. */
export interface WideInteger {
  readonly [WIDE_KEY]: string;
}

export function isWideInteger(value: unknown): value is WideInteger {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const entries = Object.entries(value);
  return entries.length === 1 && entries[0][0] === WIDE_KEY && WIDE_INTEGER.test(entries[0][1] as string);
}

/** The exact decimal literal a wide integer carries. */
export function wideIntegerText(value: WideInteger): string {
  return value[WIDE_KEY];
}

function wide(literal: string): WideInteger {
  return { [WIDE_KEY]: literal } as WideInteger;
}

// A JSON number in value position: preceded by a structural character and in a
// value slot, so digits inside a string literal cannot match.
const NUMBER_TOKEN = /([:[,]\s*)(-?(?:0|[1-9][0-9]*)(?:\.[0-9]+)?(?:[eE][+-]?[0-9]+)?)(?=\s*[,\]}])/g;

// A per-call nonce keeps the placeholder unguessable, so a real string that
// happens to look like one cannot be confused with a preserved number.
let nonceCounter = 0;
function nextNonce(): string {
  nonceCounter += 1;
  return `__cpamp_wide_${nonceCounter}_${Math.random().toString(36).slice(2)}__`;
}

/**
 * Parse JSON keeping every integer too wide for a double as its exact literal.
 * A quoted number stays a string, so the two are never confused.
 */
export function parseGatewayJSON(text: string): unknown {
  const nonce = nextNonce();
  const encoded = text.replace(NUMBER_TOKEN, (match, prefix: string, literal: string) =>
    WIDE_INTEGER.test(literal) ? `${prefix}${JSON.stringify(nonce + literal)}` : match
  );
  const revive = (value: unknown): unknown => {
    if (typeof value === 'string' && value.startsWith(nonce)) return wide(value.slice(nonce.length));
    if (Array.isArray(value)) return value.map(revive);
    if (typeof value === 'object' && value !== null) {
      const out: Record<string, unknown> = {};
      for (const [key, item] of Object.entries(value)) out[key] = revive(item);
      return out;
    }
    return value;
  };
  return revive(JSON.parse(encoded));
}

/**
 * Serialize a value for a Manager write, emitting every wide integer as the exact
 * JSON number it was parsed from. Everything else is ordinary JSON.
 */
export function stringifyGatewayJSON(value: unknown, space?: number): string {
  const nonce = nextNonce();
  const encoded = JSON.stringify(
    value,
    (_key, item: unknown) => (isWideInteger(item) ? nonce + wideIntegerText(item) : item),
    space
  );
  if (encoded === undefined) return '';
  // Only the placeholder strings become numbers; nothing else can match the nonce.
  const escaped = nonce.replace(/[.*+?^${}()|[\]\\]/g, '\\$&');
  return encoded.replace(new RegExp(`"${escaped}(-?[0-9]+)"`, 'g'), '$1');
}
