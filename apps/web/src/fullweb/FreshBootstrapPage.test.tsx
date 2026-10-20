import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BOOTSTRAP_UI, createBootstrapTransport } from './bootstrapTransport';
import { FreshBootstrapPage } from './FreshBootstrapPage';
import { fullWebText } from '../i18n/fullweb';

const identity = {
  admissionId: 'admission-01',
  installationId: 'install-01',
  generation: 'gen-01',
  databasePath: '/data/manager.sqlite',
  operationId: 'op-fresh-01',
};

const pendingOperation = {
  version: 1,
  identity,
  revision: 1,
  adminInitialized: false,
  gatewayPending: true,
  routing: { committed: false },
};

const handoff = {
  identity,
  claimGeneration: 1,
  token: 'claim-secret-012345678901234567890123456789',
  resume: false,
};

function findByTestId(root: ReactTestInstance, testId: string): ReactTestInstance {
  return root.find((node) => node.props?.['data-testid'] === testId);
}

function textOf(node: ReactTestInstance): string {
  return node.children.map((child) => (typeof child === 'string' ? child : textOf(child))).join('');
}

describe('FreshBootstrapPage product preview workflow', () => {
  beforeEach(() => {
    (globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true;
    const location = new URL('https://manager.example' + BOOTSTRAP_UI);
    vi.stubGlobal('window', {
      location,
      history: {
        replaceState: (_: unknown, _title: string, path: string) => {
          location.href = new URL(path, location.origin).href;
        },
      },
    });
    vi.stubGlobal('document', {
      documentElement: {
        classList: {
          toggle: vi.fn(),
        },
      },
    });
    vi.stubGlobal('navigator', {
      clipboard: {
        writeText: vi.fn().mockResolvedValue(undefined),
      },
    });
    vi.stubGlobal('fetch', vi.fn().mockRejectedValue(new TypeError('Discovery unavailable')));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
  });

  it('shows host authorization gate when handoff is absent or already consumed on refresh', async () => {
    const transport = createBootstrapTransport(vi.fn());
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={null} transport={transport} />);
    });

    const gate = findByTestId(renderer.root, 'bootstrap-no-handoff');
    expect(textOf(gate)).toContain('未检测到有效的 Fresh Bootstrap Handoff 凭证');
    expect(renderer.root.findAllByProps({ 'data-testid': 'bootstrap-discovery-guidance' })).toHaveLength(0);
    expect(textOf(findByTestId(renderer.root, 'cli-handoff-command'))).toBe(
      'cpa-manager-plus initialize'
    );
  });

  it.each([
    [
      'fresh_claim_required',
      'fresh',
      'fresh_claim',
      'fresh_claim_required',
      true,
      '检测到需要宿主机认领的 Fresh 初始化状态',
    ],
  ])(
    'shows owned Fresh discovery status %s without claiming an operation',
    async (status, mode, authEntry, reason, authRequired, title) => {
      const discovery = vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            contract_version: 'cpamp.phase4-01.discovery.v1',
            status,
            mode,
            auth_entry: authEntry,
            reason,
            authentication_required: authRequired,
          })
        )
      );
      vi.stubGlobal('fetch', discovery);
      const transport = createBootstrapTransport(vi.fn());
      let renderer!: ReactTestRenderer;
      await act(async () => {
        renderer = create(<FreshBootstrapPage handoff={null} transport={transport} />);
      });

      expect(textOf(findByTestId(renderer.root, 'bootstrap-discovery-guidance'))).toContain(title);
      const command = renderer.root.findAllByProps({
        'data-testid': 'cli-handoff-command',
      });
      expect(command).toHaveLength(1);
      expect(textOf(command[0])).toBe('cpa-manager-plus initialize');
      expect(discovery).toHaveBeenCalledTimes(1);
      expect(discovery.mock.calls[0][0]).toBe('/.well-known/cpamp-bootstrap');
      expect(discovery.mock.calls[0][1]).toMatchObject({
        method: 'GET',
        mode: 'same-origin',
        credentials: 'omit',
        cache: 'no-store',
        redirect: 'error',
      });
      expect(discovery.mock.calls[0][1].headers).toEqual({ Accept: 'application/json' });
    }
  );

  it('does not project inconsistent Fresh discovery as valid guidance', async () => {
    vi.stubGlobal(
      'fetch',
      vi.fn().mockResolvedValue(
        new Response(
          JSON.stringify({
            contract_version: 'cpamp.phase4-01.discovery.v1',
            status: 'fresh_claim_required',
            mode: 'fresh',
            auth_entry: 'fresh_claim',
            reason: 'qualification_unavailable',
            authentication_required: true,
          })
        )
      )
    );
    const transport = createBootstrapTransport(vi.fn());
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={null} transport={transport} />);
    });
    expect(
      renderer.root.findAllByProps({ 'data-testid': 'bootstrap-discovery-guidance' })
    ).toHaveLength(0);
    expect(textOf(findByTestId(renderer.root, 'cli-handoff-command'))).toBe(
      'cpa-manager-plus initialize'
    );
  });

  it.each([
    ['unsupported_upgrade', 'unsupported_upgrade', 'admin_session', 'qualification_unavailable', true],
    ['recovery_required', 'recovery', 'host_intervention', 'recovery_host_intervention', false],
  ])('does not misrepresent non-Fresh %s as reachable Fresh guidance', async (
    status, mode, authEntry, reason, authRequired
  ) => {
    vi.stubGlobal('fetch', vi.fn().mockResolvedValue(new Response(JSON.stringify({
      contract_version: 'cpamp.phase4-01.discovery.v1',
      status, mode, auth_entry: authEntry, reason,
      authentication_required: authRequired,
    }))));
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={null} transport={createBootstrapTransport(vi.fn())} />);
    });
    expect(renderer.root.findAllByProps({ 'data-testid': 'bootstrap-discovery-guidance' })).toHaveLength(0);
  });

  it('completes real claim exchange, key generation, admin commit, and operation refresh', async () => {
    const generatedKey = 'cpamp_9876543210abcdefABCDEFGH12';
    const committedOperation = {
      ...pendingOperation,
      revision: 2,
      adminInitialized: true,
    };

    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ token: 'session-token-1', operation: pendingOperation }))
      )
      .mockResolvedValueOnce(new Response(JSON.stringify({ key: generatedKey })))
      .mockResolvedValueOnce(new Response(JSON.stringify(committedOperation)))
      .mockResolvedValueOnce(new Response(JSON.stringify(committedOperation)));

    const transport = createBootstrapTransport(fetchMock);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={handoff} transport={transport} />);
    });

    expect(textOf(findByTestId(renderer.root, 'op-id'))).toBe('op-fresh-01');
    expect(textOf(findByTestId(renderer.root, 'op-admin-status'))).toContain('待初始化');
    expect(textOf(findByTestId(renderer.root, 'op-gateway-status'))).toContain(
      'Pending · Fail-Closed'
    );

    const generateBtn = findByTestId(renderer.root, 'generate-key-btn');
    await act(async () => {
      generateBtn.props.onClick();
    });

    const input = findByTestId(renderer.root, 'admin-key-input');
    expect(input.props.value).toBe(generatedKey);

    const checkbox = findByTestId(renderer.root, 'confirm-key-saved-checkbox');
    await act(async () => {
      checkbox.props.onChange({ target: { checked: true } });
    });

    const form = findByTestId(renderer.root, 'admin-init-form');
    await act(async () => {
      form.props.onSubmit({ preventDefault: vi.fn() });
    });

    expect(fetchMock).toHaveBeenCalledTimes(3);
    const commitCallBody = JSON.parse(fetchMock.mock.calls[2][1].body as string);
    expect(commitCallBody).toMatchObject({
      identity,
      key: generatedKey,
    });
    expect(commitCallBody.commitId).toMatch(/^commit_/);

    const completePanel = findByTestId(renderer.root, 'admin-init-complete-panel');
    expect(textOf(completePanel)).toContain('本次管理员密钥初始化已成功写入服务端');
    expect(textOf(findByTestId(renderer.root, 'op-admin-status'))).toContain('已初始化');

    const refreshBtn = findByTestId(renderer.root, 'refresh-operation-btn');
    await act(async () => {
      refreshBtn.props.onClick();
    });
    expect(fetchMock).toHaveBeenCalledTimes(4);
    expect(fetchMock.mock.calls[3][0]).toContain('/.well-known/cpamp-bootstrap/api/v2/operation');
  });

  it('preserves identical commitId across network error retries and blocks invalid keys locally', async () => {
    const validKey = 'cpamp_9876543210abcdefABCDEFGH12';
    const committedOperation = {
      ...pendingOperation,
      revision: 2,
      adminInitialized: true,
    };

    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ token: 'session-token-1', operation: pendingOperation }))
      )
      .mockRejectedValueOnce(new TypeError('network down'))
      .mockResolvedValueOnce(new Response(JSON.stringify(committedOperation)));

    const transport = createBootstrapTransport(fetchMock);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={handoff} transport={transport} />);
    });

    const input = findByTestId(renderer.root, 'admin-key-input');
    const form = findByTestId(renderer.root, 'admin-init-form');

    // Submit weak key -> blocked locally before network call
    await act(async () => {
      input.props.onChange({ target: { value: 'too-short' } });
    });
    await act(async () => {
      form.props.onSubmit({ preventDefault: vi.fn() });
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(textOf(findByTestId(renderer.root, 'feedback-title'))).toContain(
      '管理员密钥未满足安全校验要求'
    );

    // Enter valid key and confirm saved
    await act(async () => {
      input.props.onChange({ target: { value: validKey } });
    });
    const checkbox = findByTestId(renderer.root, 'confirm-key-saved-checkbox');
    await act(async () => {
      checkbox.props.onChange({ target: { checked: true } });
    });

    // First submit fails due to network error
    await act(async () => {
      form.props.onSubmit({ preventDefault: vi.fn() });
    });
    expect(fetchMock).toHaveBeenCalledTimes(2);
    expect(textOf(findByTestId(renderer.root, 'feedback-title'))).toContain('网络连接异常');

    const firstCommitBody = JSON.parse(fetchMock.mock.calls[1][1].body as string);

    // Retry submit without changing key -> sends exact same commitId
    await act(async () => {
      form.props.onSubmit({ preventDefault: vi.fn() });
    });
    expect(fetchMock).toHaveBeenCalledTimes(3);
    const secondCommitBody = JSON.parse(fetchMock.mock.calls[2][1].body as string);
    expect(secondCommitBody.commitId).toBe(firstCommitBody.commitId);
    expect(textOf(findByTestId(renderer.root, 'op-admin-status'))).toContain('已初始化');
  });

  it('resumes an already initialized operation directly without claim exchange', async () => {
    const resumedOperation = {
      ...pendingOperation,
      revision: 2,
      adminInitialized: true,
    };
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response(JSON.stringify(resumedOperation)));
    const transport = createBootstrapTransport(fetchMock);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <FreshBootstrapPage handoff={{ ...handoff, resume: true }} transport={transport} />
      );
    });

    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(fetchMock.mock.calls[0][0]).toContain('/.well-known/cpamp-bootstrap/api/v2/operation');
    expect(textOf(findByTestId(renderer.root, 'op-session-mode'))).toContain('恢复会话');
    expect(textOf(findByTestId(renderer.root, 'admin-init-complete-panel'))).toContain(
      '已从服务端恢复管理员已初始化状态'
    );
  });

  it('displays authority rejection feedback when claim exchange returns HTTP 401', async () => {
    const fetchMock = vi.fn().mockResolvedValueOnce(new Response('unauthorized', { status: 401 }));
    const transport = createBootstrapTransport(fetchMock);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={handoff} transport={transport} />);
    });

    expect(textOf(findByTestId(renderer.root, 'init-error-title'))).toContain('HTTP 401');
  });

  it('reconciles operation state via GET operation when admin commit encounters HTTP 409 conflict', async () => {
    const validKey = 'cpamp_9876543210abcdefABCDEFGH12';
    const committedOperation = {
      ...pendingOperation,
      revision: 2,
      adminInitialized: true,
    };

    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ token: 'session-token-1', operation: pendingOperation }))
      )
      .mockResolvedValueOnce(new Response('conflict', { status: 409 }))
      .mockResolvedValueOnce(new Response(JSON.stringify(committedOperation)));

    const transport = createBootstrapTransport(fetchMock);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={handoff} transport={transport} />);
    });

    const input = findByTestId(renderer.root, 'admin-key-input');
    const checkbox = findByTestId(renderer.root, 'confirm-key-saved-checkbox');
    const form = findByTestId(renderer.root, 'admin-init-form');

    await act(async () => {
      input.props.onChange({ target: { value: validKey } });
      checkbox.props.onChange({ target: { checked: true } });
    });

    await act(async () => {
      form.props.onSubmit({ preventDefault: vi.fn() });
    });

    expect(fetchMock).toHaveBeenCalledTimes(3);
    expect(fetchMock.mock.calls[2][0]).toContain('/.well-known/cpamp-bootstrap/api/v2/operation');
    expect(textOf(findByTestId(renderer.root, 'op-admin-status'))).toContain('已初始化');
  });

  it('resets confirmedSaved when adminKey is edited after confirmation and blocks unconfirmed submission', async () => {
    const keyA = 'cpamp_9876543210abcdefABCDEFGH12';
    const keyB = 'cpamp_9876543210abcdefABCDEFGH99';

    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ token: 'session-token-1', operation: pendingOperation }))
      );

    const transport = createBootstrapTransport(fetchMock);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={handoff} transport={transport} />);
    });

    const input = findByTestId(renderer.root, 'admin-key-input');
    const checkbox = findByTestId(renderer.root, 'confirm-key-saved-checkbox');
    const form = findByTestId(renderer.root, 'admin-init-form');

    await act(async () => {
      input.props.onChange({ target: { value: keyA } });
    });
    await act(async () => {
      checkbox.props.onChange({ target: { checked: true } });
    });
    expect(findByTestId(renderer.root, 'confirm-key-saved-checkbox').props.checked).toBe(true);

    // Editing keyA -> keyB must invalidate confirmedSaved
    await act(async () => {
      input.props.onChange({ target: { value: keyB } });
    });
    expect(findByTestId(renderer.root, 'confirm-key-saved-checkbox').props.checked).toBe(false);

    // Submitting without re-checking must be blocked locally
    await act(async () => {
      form.props.onSubmit({ preventDefault: vi.fn() });
    });
    expect(fetchMock).toHaveBeenCalledTimes(1);
    expect(textOf(findByTestId(renderer.root, 'feedback-title'))).toContain(
      '请先确认已妥善保存管理员密钥'
    );
    expect(textOf(findByTestId(renderer.root, 'feedback-detail'))).toContain(
      '服务端仅保存不可逆密钥校验值，不保存明文'
    );
    expect(textOf(renderer.root)).not.toContain('Argon2id');
  });

  it('explains single retry and HTTP 401 re-initialization when initial claim exchange encounters a transport error', async () => {
    const fetchMock = vi.fn().mockRejectedValueOnce(new TypeError('network down'));
    const transport = createBootstrapTransport(fetchMock);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={handoff} transport={transport} />);
    });

    expect(textOf(findByTestId(renderer.root, 'init-error-detail'))).toContain(
      '若服务端已消费本次一次性 Claim Token 并返回 HTTP 401，请在宿主机重新执行 cpa-manager-plus initialize'
    );
    expect(textOf(findByTestId(renderer.root, 'bootstrap-authority-error'))).toContain(
      '可尝试重新连接一次；若返回 HTTP 401（令牌已被消费）'
    );
  });

  it('defines every CSS class used by FreshBootstrapPage in carrier.css without mismatched selectors', async () => {
    const fs = (
      globalThis as unknown as {
        process: {
          getBuiltinModule: (id: string) => {
            readFileSync: (path: URL, encoding: string) => string;
          };
        };
      }
    ).process.getBuiltinModule('node:fs');
    const carrierCss = fs.readFileSync(new URL('./carrier.css', import.meta.url), 'utf8');
    const definedClasses = new Set(
      Array.from(carrierCss.matchAll(/\.([a-zA-Z0-9_-]+)/g), (m) => m[1])
    );
    expect(definedClasses.has('cpamp-shell')).toBe(false);

    const renderedClasses = new Set<string>();
    const collectClasses = (root: ReactTestInstance) => {
      for (const node of root.findAll((n) => typeof n.props?.className === 'string')) {
        for (const cls of String(node.props.className).split(/\s+/)) {
          if (cls) renderedClasses.add(cls);
        }
      }
    };

    // 1. no_handoff state
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <FreshBootstrapPage handoff={null} transport={createBootstrapTransport(vi.fn())} />
      );
    });
    collectClasses(renderer.root);

    // 2. init_error state
    await act(async () => {
      renderer = create(
        <FreshBootstrapPage
          handoff={handoff}
          transport={createBootstrapTransport(vi.fn().mockRejectedValueOnce(new TypeError('down')))}
        />
      );
    });
    collectClasses(renderer.root);

    // 3. ready pending + completed states
    const committedOperation = { ...pendingOperation, revision: 2, adminInitialized: true };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        new Response(JSON.stringify({ token: 'session-token-1', operation: pendingOperation }))
      )
      .mockResolvedValueOnce(new Response(JSON.stringify(committedOperation)));
    await act(async () => {
      renderer = create(
        <FreshBootstrapPage handoff={handoff} transport={createBootstrapTransport(fetchMock)} />
      );
    });
    collectClasses(renderer.root);

    const input = findByTestId(renderer.root, 'admin-key-input');
    const checkbox = findByTestId(renderer.root, 'confirm-key-saved-checkbox');
    const form = findByTestId(renderer.root, 'admin-init-form');
    await act(async () => {
      input.props.onChange({ target: { value: 'cpamp_9876543210abcdefABCDEFGH12' } });
    });
    await act(async () => {
      checkbox.props.onChange({ target: { checked: true } });
    });
    collectClasses(renderer.root);
    await act(async () => {
      form.props.onSubmit({ preventDefault: vi.fn() });
    });
    collectClasses(renderer.root);

    for (const cls of renderedClasses) {
      expect(definedClasses.has(cls), `Missing CSS class .${cls} in carrier.css`).toBe(true);
    }
  });

  it('enables only a server-committed verified route and clears Fresh authority before continuing', async () => {
    const prepared = {
      ...pendingOperation,
      adminInitialized: true,
      revision: 3,
      routing: {
        target: '/console',
        operationId: identity.operationId,
        revision: 1,
        stage: 'intent_persisted',
        committed: false,
      },
    };
    const committed = {
      ...prepared,
      revision: 6,
      routing: { ...prepared.routing, stage: 'committed', committed: true },
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(
        Response.json({
          token: 'session',
          operation: { ...prepared, routing: { committed: false } },
        })
      )
      .mockResolvedValueOnce(Response.json(prepared))
      .mockResolvedValueOnce(Response.json(committed))
      .mockResolvedValueOnce(
        Response.json(
          {
            installationId: identity.installationId,
            generation: identity.generation,
            operationId: identity.operationId,
            revision: 1,
            target: '/console',
            committed: true,
          },
          { headers: { 'X-CPAMP-Route-Owner': 'manager' } }
        )
      );
    const transport = createBootstrapTransport(fetchMock);
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(<FreshBootstrapPage handoff={handoff} transport={transport} />);
    });
    expect(renderer.root.findAllByProps({ 'data-testid': 'continue-console' })).toHaveLength(0);
    await act(async () =>
      findByTestId(renderer.root, 'routing-form').props.onSubmit({ preventDefault: vi.fn() })
    );
    expect(findByTestId(renderer.root, 'continue-console').props.href).toBe('/console');
    expect(findByTestId(renderer.root, 'op-routing-status').children.join('')).toBe(
      fullWebText('routeVerifiedState')
    );
    expect(findByTestId(renderer.root, 'op-routing-status').children.join('')).not.toContain(
      '未切换'
    );
    expect(findByTestId(renderer.root, 'console-path').props.disabled).toBe(true);
    await expect(transport.fetchOperation()).rejects.toMatchObject({ kind: 'authority' });
  });

  it('reconciles lost prepare responses and locks the persisted target without claiming success', async () => {
    const initial = { ...pendingOperation, adminInitialized: true };
    const prepared = {
      ...initial,
      revision: 3,
      routing: {
        target: '/console',
        operationId: identity.operationId,
        revision: 1,
        stage: 'intent_persisted',
        committed: false,
      },
    };
    const fetchMock = vi
      .fn()
      .mockResolvedValueOnce(Response.json({ token: 'session', operation: initial }))
      .mockRejectedValueOnce(new Error('network'))
      .mockResolvedValueOnce(Response.json(prepared))
      .mockResolvedValueOnce(new Response('', { status: 503 }))
      .mockResolvedValueOnce(
        Response.json(
          {
            installationId: identity.installationId,
            generation: identity.generation,
            operationId: identity.operationId,
            revision: 1,
            target: '/console',
            committed: false,
          },
          { headers: { 'X-CPAMP-Route-Owner': 'manager' } }
        )
      )
      .mockResolvedValueOnce(Response.json(prepared));
    let renderer!: ReactTestRenderer;
    await act(async () => {
      renderer = create(
        <FreshBootstrapPage handoff={handoff} transport={createBootstrapTransport(fetchMock)} />
      );
    });
    await act(async () =>
      findByTestId(renderer.root, 'routing-form').props.onSubmit({ preventDefault: vi.fn() })
    );
    expect(findByTestId(renderer.root, 'console-path').props.disabled).toBe(true);
    await act(async () =>
      findByTestId(renderer.root, 'routing-form').props.onSubmit({ preventDefault: vi.fn() })
    );
    expect(fetchMock.mock.calls[3][0]).toContain('/routing/activate');
    expect(renderer.root.findAllByProps({ 'data-testid': 'continue-console' })).toHaveLength(0);
    expect(JSON.stringify(renderer.toJSON())).toContain('503');
  });
});
