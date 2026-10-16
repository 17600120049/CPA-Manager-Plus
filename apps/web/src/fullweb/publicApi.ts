import { readRoutingContext, resolveRoutingContext, type RoutingContext } from './routingContext';

export type PublicApiErrorKind = 'routing_unavailable' | 'network' | 'http' | 'authentication';

export class PublicApiError extends Error {
  constructor(
    readonly kind: PublicApiErrorKind,
    readonly status?: number
  ) {
    super(`Public API ${kind}`);
    this.name = 'PublicApiError';
  }
}

// Only an explicit in-memory Full Web credential may be attached. There is no
// connection-store import, persisted credential lookup, or CPA compatibility.
export interface FullWebCredential {
  readonly kind: 'admin' | 'session';
  readonly value: string;
}

export function createPublicApiClient(
  readContext: () => RoutingContext | null = () => readRoutingContext(document),
  transport: typeof fetch = globalThis.fetch
) {
  return {
    async get<T = unknown>(resource: string, credential?: FullWebCredential): Promise<T> {
      const context = readContext();
      const canonical = resolveRoutingContext({
        state: 'verified_effective',
        verifiedEffectiveControlBasePath: context?.basePath,
        revision: context?.revision,
      });
      // One normalized resource below the canonical namespace. Query/absolute
      // URLs and URL normalization cannot select another transport destination.
      if (
        !context ||
        !canonical ||
        context.apiBase !== canonical.apiBase ||
        !/^[a-zA-Z0-9_-]+(?:\/[a-zA-Z0-9_-]+)*$/.test(resource) ||
        !['http:', 'https:'].includes(window.location.protocol) ||
        ['8317', '9081'].includes(window.location.port)
      ) {
        throw new PublicApiError('routing_unavailable');
      }
      const target = new URL(`${context.apiBase}/${resource}`, window.location.origin);
      if (
        target.origin !== window.location.origin ||
        target.pathname !== `${context.apiBase}/${resource}` ||
        target.search ||
        target.hash
      ) {
        throw new PublicApiError('routing_unavailable');
      }
      const headers: Record<string, string> = { Accept: 'application/json' };
      if (credential) headers.Authorization = `Bearer ${credential.value}`;
      let response: Response;
      try {
        response = await transport(target.href, {
          method: 'GET',
          headers,
          mode: 'same-origin',
          credentials: 'omit',
          redirect: 'error',
        });
      } catch {
        throw new PublicApiError('network');
      }
      if (response.status === 401 || response.status === 403) {
        throw new PublicApiError('authentication', response.status);
      }
      if (!response.ok) throw new PublicApiError('http', response.status);
      try {
        return (await response.json()) as T;
      } catch {
        throw new PublicApiError('network');
      }
    },
  };
}
