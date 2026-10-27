// The smallest wire projection the gateway page accepts: one writeable group and
// its leaves, no rule families. Only the tests that mount the whole Console need
// it — they care that a draft survives navigation, not about the full inventory,
// which `FreshGatewayWorkspace.test.tsx` already builds leaf by leaf.
//
// This is deliberately the on-the-wire shape, so it goes through the same strict
// parser a real Manager response does; a fixture the parser rejects would make
// those tests pass on an empty page.

const BASELINE = 'a'.repeat(64);

const RETRY_PATHS = [
  'routing.retry.request-retry',
  'routing.retry.max-retry-credentials',
  'routing.retry.max-retry-interval',
];

export function gatewayViewFixture() {
  return {
    capability: { mode: 'managed', readable: true, writable: true, rulesWritable: true },
    generation: '7',
    groups: [
      {
        id: 'retry',
        prefix: 'routing.retry',
        paths: RETRY_PATHS,
        baseline: BASELINE,
        writable: true,
        persisted: 'persisted',
        effective: 'unknown',
      },
    ],
    fields: RETRY_PATHS.map((path) => ({
      path,
      type: 'int',
      kind: 'number',
      group: 'retry',
      presence: 'present',
      value: 2,
      editable: true,
    })),
    families: [],
    groupsInvalid: false,
  };
}
