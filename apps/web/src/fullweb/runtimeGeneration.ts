// Supervisor generations are uint64 and commonly exceed JS's safe integers.
// Preserve only the existing Runtime fence fields; no alternate API contract.
export type RuntimeGeneration = number | string;

export function validRuntimeGeneration(value: unknown): value is RuntimeGeneration {
  if (typeof value === 'number') return Number.isSafeInteger(value) && value > 0;
  return (
    typeof value === 'string' &&
    /^[1-9][0-9]{0,19}$/.test(value) &&
    BigInt(value) <= BigInt('18446744073709551615')
  );
}

export function parseRuntimeJSON(text: string): unknown {
  // String contents have escaped quotes and cannot match this key/token
  // boundary. Fractions/exponents are not accepted as decimal uint64 fences.
  return JSON.parse(
    text.replace(
      /("(?:runtimeGeneration|targetGeneration)"\s*:\s*)([0-9]+)(?=\s*[,}])/g,
      (match, key: string, value: string) =>
        BigInt(value) > BigInt(Number.MAX_SAFE_INTEGER) ? key + JSON.stringify(value) : match
    )
  );
}

export function runtimeRequestJSON(body: unknown): string {
  const value = (body as { expectedRuntimeGeneration?: unknown } | null)?.expectedRuntimeGeneration;
  if (value !== undefined && !validRuntimeGeneration(value))
    throw new Error('Invalid Runtime generation');
  const json = JSON.stringify(body);
  // The existing strategy body has one top-level generation. Encode its
  // validated decimal string as the exact JSON integer Go uint64 expects.
  return typeof value === 'string'
    ? json.replace(
        /("expectedRuntimeGeneration":)"[0-9]+"(?=[,}])/g,
        (_match, key: string) => key + value
      )
    : json;
}
