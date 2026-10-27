// Panel-only build replacement for the Full Web public API module. Shared
// screens keep their imports, but Panel Lite has no verified Full Web routing
// authority and must never bundle the Full Web transport. Type exports
// disappear during build and pull in no Full Web module.
export type * from '../fullweb/publicApi';
import type { PublicApiClient, PublicApiErrorKind } from '../fullweb/publicApi';

export class PublicApiError extends Error {
  constructor(
    readonly kind: PublicApiErrorKind,
    readonly status?: number,
    readonly operationResult?: unknown,
    readonly code?: string
  ) {
    super(`Public API ${kind}`);
    this.name = 'PublicApiError';
  }
}

// Every Full Web resource stops before any network I/O, mirroring the panel
// replacement for the legacy Manager transport.
const unavailable = async <T,>(): Promise<T> => {
  throw new PublicApiError('routing_unavailable');
};

export function createPublicApiClient(): PublicApiClient {
  return { get: unavailable, post: unavailable, put: unavailable };
}
