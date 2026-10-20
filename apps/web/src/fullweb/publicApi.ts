import { readRoutingContext, resolveRoutingContext, type RoutingContext } from './routingContext';
import { parseRuntimeJSON, runtimeRequestJSON } from './runtimeGeneration';

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
  async function request<T>(
    resource: string,
    credential?: FullWebCredential,
    body?: unknown,
    method = body === undefined ? 'GET' : 'POST'
  ): Promise<T> {
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
      !['http:', 'https:'].includes(window.location.protocol)
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
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    const controller =
      body !== undefined ||
      resource.startsWith('api-keys/') ||
      resource === 'settings/update-channel'
        ? new AbortController()
        : undefined;
    const timer = controller ? setTimeout(() => controller.abort(), 15000) : undefined;
    try {
      const response = await transport(target.href, {
        method,
        headers,
        ...(body !== undefined
          ? { body: resource === 'strategy' ? runtimeRequestJSON(body) : JSON.stringify(body) }
          : {}),
        ...(controller ? { signal: controller.signal } : {}),
        mode: 'same-origin',
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
      });
      if (response.status === 401 || response.status === 403) {
        throw new PublicApiError('authentication', response.status);
      }
      if (!response.ok) throw new PublicApiError('http', response.status);
      return (
        ['operation', 'strategy', 'complete'].includes(resource)
          ? parseRuntimeJSON(await response.text())
          : await response.json()
      ) as T;
    } catch (err) {
      if (err instanceof PublicApiError) throw err;
      throw new PublicApiError('network');
    } finally {
      if (timer !== undefined) clearTimeout(timer);
    }
  }
  return {
    get: <T = unknown>(resource: string, credential?: FullWebCredential) =>
      request<T>(resource, credential),
    post: <T = unknown>(resource: string, body: unknown, credential: FullWebCredential) =>
      request<T>(resource, credential, body),
    put: <T = unknown>(resource: string, body: unknown, credential: FullWebCredential) =>
      request<T>(resource, credential, body, 'PUT'),
  };
}
