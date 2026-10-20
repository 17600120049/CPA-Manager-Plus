export interface Key {
  id: string;
  requestId: string;
  maskedPreview: string;
  createdAtMs: number;
  managerState: 'persisted';
  runtimeState: 'not_applied' | 'effect_unknown';
  name: string;
  nameRevision: number;
}

export function validKey(value: Key): boolean {
  return (
    !!value &&
    /^[a-f0-9]{32}$/.test(value.id) &&
    /^[a-f0-9]{32}$/.test(value.requestId) &&
    /^cpamp_sk_…[a-f0-9]{4}$/.test(value.maskedPreview) &&
    Number.isSafeInteger(value.createdAtMs) &&
    value.createdAtMs > 0 &&
    value.managerState === 'persisted' &&
    ['not_applied', 'effect_unknown'].includes(value.runtimeState) &&
    typeof value.name === 'string' &&
    [...value.name].length <= 120 &&
    Number.isSafeInteger(value.nameRevision) &&
    value.nameRevision > 0
  );
}
