// Bounded pre-canonical carrier. It has no CPA/Admin transport and persists no
// secrets. This module belongs to the existing Full Web entry graph.
export const BOOTSTRAP_ROOT = '/.well-known/cpamp-bootstrap';
export const BOOTSTRAP_UI = `${BOOTSTRAP_ROOT}/ui`;
const API = `${BOOTSTRAP_ROOT}/api/v2/`;

export interface FreshIdentity {
  admissionId: string;
  installationId: string;
  generation: string;
  databasePath: string;
  operationId: string;
}

export interface FreshRoutingCheckpoint {
  operationId?: string;
  revision?: number;
  target?: string;
  stage?: string;
  committed: boolean;
}

export interface FreshStrategyCheckpoint {
  strategy?: string;
  targetIdentity?: string;
  targetArtifactId?: string;
  targetGeneration?: number;
  stage?: string;
}

export interface FreshOperation {
  version?: number;
  identity: FreshIdentity;
  revision: number;
  adminInitialized: boolean;
  productComplete?: boolean;
  gatewayPending: boolean;
  routing: FreshRoutingCheckpoint;
  strategy?: FreshStrategyCheckpoint;
}

export interface Handoff {
  identity: FreshIdentity;
  claimGeneration: number;
  token: string;
  resume: boolean;
}

export interface AdminCommitPayload {
  identity: FreshIdentity;
  commitId: string;
  key: string;
}

export type BootstrapErrorKind =
  | 'authority'
  | 'transport'
  | 'rejected'
  | 'bad_request'
  | 'conflict'
  | 'not_found'
  | 'unavailable'
  | 'response'
  | 'mismatch';

export class BootstrapTransportError extends Error {
  readonly kind: BootstrapErrorKind;
  readonly status?: number;

  constructor(message: string, kind: BootstrapErrorKind, status?: number) {
    super(message);
    this.name = 'BootstrapTransportError';
    this.kind = kind;
    this.status = status;
  }
}

export interface AdminKeyValidationResult {
  valid: boolean;
  byteLength: number;
  distinctCount: number;
  classCount: number;
  hasForbiddenChar: boolean;
  reason: string;
}

// Mirrors apps/manager-server/internal/security/fresh_admin.go ValidateFreshAdminKey.
export function validateFreshAdminKey(key: string): AdminKeyValidationResult {
  const byteLength = new TextEncoder().encode(key).length;
  const trimmedMatches = key.trim() === key;
  let hasForbiddenChar = !trimmedMatches;
  const classes = new Set<number>();
  const unique = new Set<string>();

  for (const ch of key) {
    if (/[\s\p{Cc}]/u.test(ch)) {
      hasForbiddenChar = true;
    }
    unique.add(ch);
    if (/\p{Ll}/u.test(ch)) {
      classes.add(0);
    } else if (/\p{Lu}/u.test(ch)) {
      classes.add(1);
    } else if (/\p{Nd}/u.test(ch)) {
      classes.add(2);
    } else {
      classes.add(3);
    }
  }

  const classCount = classes.size;
  const distinctCount = unique.size;

  if (!key) {
    return {
      valid: false,
      byteLength,
      distinctCount,
      classCount,
      hasForbiddenChar,
      reason: '可手动输入高强度管理员密钥，或点击“一键生成”由服务端生成。',
    };
  }
  if (hasForbiddenChar) {
    return {
      valid: false,
      byteLength,
      distinctCount,
      classCount,
      hasForbiddenChar,
      reason: '管理员密钥不能包含空格、换行或控制字符。',
    };
  }
  if (byteLength < 24 || byteLength > 256) {
    return {
      valid: false,
      byteLength,
      distinctCount,
      classCount,
      hasForbiddenChar,
      reason: '管理员密钥长度需为 24 至 256 个非空白字符。',
    };
  }
  if (classCount < 3 || distinctCount < 12) {
    return {
      valid: false,
      byteLength,
      distinctCount,
      classCount,
      hasForbiddenChar,
      reason: '需包含大小写字母、数字、符号中的至少 3 类，且不少于 12 个不同字符。',
    };
  }
  return {
    valid: true,
    byteLength,
    distinctCount,
    classCount,
    hasForbiddenChar: false,
    reason: '密钥强度符合 CPAMP 初始化安全要求。',
  };
}

function sameIdentity(a: FreshIdentity | undefined, b: FreshIdentity | undefined): boolean {
  if (!a || !b) return false;
  return (
    a.admissionId === b.admissionId &&
    a.installationId === b.installationId &&
    a.generation === b.generation &&
    a.databasePath === b.databasePath &&
    a.operationId === b.operationId
  );
}

function classifyStatus(status: number): BootstrapErrorKind {
  if (status === 401 || status === 403) return 'rejected';
  if (status === 400) return 'bad_request';
  if (status === 404) return 'not_found';
  if (status === 409) return 'conflict';
  return 'unavailable';
}

// Remove the fragment before decoding or making a request, including malformed
// handoffs. Errors deliberately contain neither input nor request bodies.
export function consumeHandoff(
  location: Pick<Location, 'hash' | 'pathname' | 'search'>,
  history: Pick<History, 'replaceState'>
): Handoff | null {
  const fragment = location.hash;
  if (fragment) history.replaceState(null, '', location.pathname + location.search);
  if (location.pathname !== BOOTSTRAP_UI || !fragment) return null;
  try {
    const params = new URLSearchParams(fragment.slice(1));
    if (params.size !== 1 || !params.has('handoff')) return null;
    const encoded = params.get('handoff')!;
    if (!/^[A-Za-z0-9_-]{1,8192}$/.test(encoded)) return null;
    const bytes = Uint8Array.from(atob(encoded.replace(/-/g, '+').replace(/_/g, '/')), (c) =>
      c.charCodeAt(0)
    );
    const value = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)) as Handoff;
    if (
      !value ||
      typeof value !== 'object' ||
      typeof value.resume !== 'boolean' ||
      typeof value.token !== 'string' ||
      value.token.length < 32 ||
      value.token.length > 256 ||
      !Number.isSafeInteger(value.claimGeneration) ||
      value.claimGeneration < 0 ||
      !value.identity ||
      ['admissionId', 'installationId', 'generation', 'databasePath', 'operationId'].some(
        (key) =>
          typeof value.identity[key as keyof FreshIdentity] !== 'string' ||
          !value.identity[key as keyof FreshIdentity]
      )
    )
      return null;
    return value;
  } catch {
    return null;
  }
}

const methods = {
  'claim/exchange': 'POST',
  operation: 'GET',
  'admin/generate': 'POST',
  'admin/commit': 'POST',
  'routing/prepare': 'POST',
  'routing/activate': 'POST',
  strategy: 'POST',
} as const;
type Resource = keyof typeof methods;

export function createBootstrapTransport(transport: typeof fetch = globalThis.fetch) {
  let token: string | undefined;
  let operation: FreshOperation | undefined;
  async function request<T>(resource: Resource, body?: unknown): Promise<T> {
    if (
      window.location.pathname !== BOOTSTRAP_UI ||
      !['http:', 'https:'].includes(window.location.protocol) ||
      !Object.prototype.hasOwnProperty.call(methods, resource) ||
      (resource !== 'claim/exchange' && !token)
    ) {
      throw new BootstrapTransportError('Bootstrap authority unavailable', 'authority');
    }
    const target = new URL(API + resource, window.location.origin);
    const headers: Record<string, string> = { Accept: 'application/json' };
    if (body !== undefined) headers['Content-Type'] = 'application/json';
    if (token) headers.Authorization = `Fresh ${token}`;
    let response: Response;
    try {
      response = await transport(target.href, {
        method: methods[resource],
        headers,
        ...(body === undefined ? {} : { body: JSON.stringify(body) }),
        mode: 'same-origin',
        credentials: 'omit',
        redirect: 'error',
        cache: 'no-store',
      });
    } catch {
      throw new BootstrapTransportError('Bootstrap transport unavailable', 'transport');
    }
    if (!response.ok) {
      throw new BootstrapTransportError(
        'Bootstrap request unavailable',
        classifyStatus(response.status),
        response.status
      );
    }
    try {
      return (await response.json()) as T;
    } catch {
      throw new BootstrapTransportError(
        'Bootstrap response unavailable',
        'response',
        response.status
      );
    }
  }
  return {
    async start(handoff: Handoff): Promise<FreshOperation> {
      if (token) {
        throw new BootstrapTransportError('Bootstrap authority already installed', 'authority');
      }
      if (handoff.resume) {
        token = handoff.token;
        try {
          operation = await request<FreshOperation>('operation');
        } catch (err) {
          token = undefined;
          operation = undefined;
          throw err;
        }
      } else {
        const result = await request<{ token: string; operation: FreshOperation }>(
          'claim/exchange',
          {
            identity: handoff.identity,
            claimGeneration: handoff.claimGeneration,
            token: handoff.token,
          }
        );
        token = result.token;
        operation = result.operation;
      }
      if (!operation || !sameIdentity(operation.identity, handoff.identity)) {
        token = undefined;
        operation = undefined;
        throw new BootstrapTransportError('Bootstrap authority mismatch', 'mismatch');
      }
      return operation;
    },
    async fetchOperation(): Promise<FreshOperation> {
      const next = await request<FreshOperation>('operation');
      if (!operation || !sameIdentity(next.identity, operation.identity)) {
        token = undefined;
        operation = undefined;
        throw new BootstrapTransportError('Bootstrap authority mismatch', 'mismatch');
      }
      operation = next;
      return next;
    },
    async generateAdminKey(): Promise<string> {
      const result = await request<{ key: string }>('admin/generate');
      if (!result || typeof result.key !== 'string' || !validateFreshAdminKey(result.key).valid) {
        throw new BootstrapTransportError('Bootstrap response unavailable', 'response');
      }
      return result.key;
    },
    async commitAdminKey(commitId: string, key: string): Promise<FreshOperation> {
      if (!operation || !commitId || commitId.length > 128) {
        throw new BootstrapTransportError('Bootstrap authority unavailable', 'authority');
      }
      const payload: AdminCommitPayload = {
        identity: operation.identity,
        commitId,
        key,
      };
      const next = await request<FreshOperation>('admin/commit', payload);
      if (!next || !sameIdentity(next.identity, operation.identity) || !next.adminInitialized) {
        throw new BootstrapTransportError('Bootstrap response unavailable', 'response');
      }
      operation = next;
      return next;
    },
    request,
    clear() {
      token = undefined;
      operation = undefined;
    },
  };
}

export type BootstrapTransportClient = ReturnType<typeof createBootstrapTransport>;
