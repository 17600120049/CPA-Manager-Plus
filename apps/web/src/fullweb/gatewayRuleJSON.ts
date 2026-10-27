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
// A JSON integer literal: no fraction, no exponent, no leading zero.
const INTEGER_LITERAL = /^-?(?:0|[1-9][0-9]*)$/;

/**
 * Whether an integer literal is one a double cannot hold. `0`, any ordinary
 * count and every 16-digit safe integer stay plain numbers: only a value outside
 * `Number.isSafeInteger` needs the exact-literal tag.
 */
function isWideLiteral(literal: string): boolean {
  if (!INTEGER_LITERAL.test(literal)) return false;
  return !Number.isSafeInteger(Number(literal));
}

/** A JSON integer this runtime cannot represent exactly. */
export interface WideInteger {
  readonly [WIDE_KEY]: string;
}

export function isWideInteger(value: unknown): value is WideInteger {
  if (typeof value !== 'object' || value === null || Array.isArray(value)) return false;
  const entries = Object.entries(value);
  return (
    entries.length === 1 &&
    entries[0][0] === WIDE_KEY &&
    typeof entries[0][1] === 'string' &&
    isWideLiteral(entries[0][1])
  );
}

/** The exact decimal literal a wide integer carries. */
export function wideIntegerText(value: WideInteger): string {
  return value[WIDE_KEY];
}

function wide(literal: string): WideInteger {
  return { [WIDE_KEY]: literal } as WideInteger;
}

// A per-call nonce keeps the placeholder unguessable, so a real string that
// happens to look like one cannot be confused with a preserved number.
let nonceCounter = 0;
function nextNonce(): string {
  nonceCounter += 1;
  return `__cpamp_wide_${nonceCounter}_${Math.random().toString(36).slice(2)}__`;
}

/** The characters a JSON number literal can continue with. */
const NUMBER_BODY = /[-+.eE0-9]/;

/**
 * Replace every wide integer literal with its nonce-tagged string.
 *
 * This is a real JSON scan, not a regex over the text: a string literal is
 * copied verbatim — including escaped quotes — so digits inside one can never be
 * mistaken for a number. JSON has no numeric object key, so every number outside
 * a string is in value position and no other context tracking is needed.
 */
function encodeWideIntegers(text: string, nonce: string): string {
  let out = '';
  let index = 0;
  while (index < text.length) {
    const character = text[index];
    if (character === '"') {
      let end = index + 1;
      while (end < text.length) {
        const current = text[end];
        if (current === '\\') {
          end += 2;
          continue;
        }
        end += 1;
        if (current === '"') break;
      }
      out += text.slice(index, end);
      index = end;
      continue;
    }
    if (character === '-' || (character >= '0' && character <= '9')) {
      let end = index + 1;
      while (end < text.length && NUMBER_BODY.test(text[end])) end += 1;
      const literal = text.slice(index, end);
      out += isWideLiteral(literal) ? JSON.stringify(nonce + literal) : literal;
      index = end;
      continue;
    }
    out += character;
    index += 1;
  }
  return out;
}

/**
 * Parse JSON keeping every integer too wide for a double as its exact literal.
 * A quoted number stays a string, so the two are never confused.
 */
export function parseGatewayJSON(text: string): unknown {
  const nonce = nextNonce();
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
  return revive(JSON.parse(encodeWideIntegers(text, nonce)));
}

/** The placeholder a wide integer becomes while the value is being re-encoded. */
const placeholder = (nonce: string) => new RegExp(`"${nonce.replace(/[.*+?^${}()|[\]\\]/g, '\\$&')}(-?(?:0|[1-9][0-9]*))"`, 'g');

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
  return encoded.replace(placeholder(nonce), '$1');
}
