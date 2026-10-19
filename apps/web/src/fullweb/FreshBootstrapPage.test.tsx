import { execFileSync, spawn } from 'node:child_process';
import { mkdtempSync, rmSync, writeFileSync } from 'node:fs';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { act, create, type ReactTestInstance, type ReactTestRenderer } from 'react-test-renderer';
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest';
import { BOOTSTRAP_UI, consumeHandoff, createBootstrapTransport } from './bootstrapTransport';
import { FreshBootstrapPage } from './FreshBootstrapPage';

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
  return node.children
    .map((child) => (typeof child === 'string' ? child : textOf(child)))
    .join('');
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
    expect(textOf(findByTestId(renderer.root, 'cli-handoff-command'))).toBe(
      'cpa-manager-plus initialize'
    );
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

  it.skipIf(process.env.CPAMP_FRESH_LIVE_E2E !== '1')(
    'completes live Web -> Manager Server Fresh Bootstrap initialization and failure checks over real HTTP',
    async () => {
      const tempDir = mkdtempSync(path.join(tmpdir(), 'cpamp-fresh-e2e-'));
      const binPath = path.join(tempDir, 'cpa-manager-plus');
      const dataDir = path.join(tempDir, 'data');
      const dbPath = path.join(dataDir, 'usage.sqlite');
      const configPath = path.join(dataDir, 'config.json');
      const dataKeyPath = path.join(dataDir, 'data.key');

      execFileSync('mkdir', ['-p', dataDir]);
      writeFileSync(
        configPath,
        JSON.stringify({
          bootstrap: {
            provisioning: 'owned',
            sourceVersion: 'v2.0.0-dev',
          },
        })
      );

      const serverRepoDir = path.resolve(process.cwd(), 'apps/manager-server');
      const goWorkServerDir = serverRepoDir.endsWith('apps/web/apps/manager-server')
        ? path.resolve(process.cwd(), '../manager-server')
        : serverRepoDir;
      execFileSync('go', ['build', '-o', binPath, './cmd/cpa-manager-plus'], {
        cwd: goWorkServerDir,
        stdio: 'inherit',
      });

      const env: NodeJS.ProcessEnv = {
        ...process.env,
        CPA_MANAGER_CONFIG_PATH: configPath,
        CPA_MANAGER_DATA_KEY_PATH: dataKeyPath,
        CPA_MANAGER_ADMIN_KEY: '',
        CPA_MANAGER_ADMIN_KEY_FILE: '',
        HTTP_ADDR: '127.0.0.1:0',
        USAGE_DATA_DIR: dataDir,
        USAGE_DB_PATH: dbPath,
      };

      const handoffLine = execFileSync(binPath, ['initialize'], {
        env,
        encoding: 'utf8',
      }).trim();
      expect(handoffLine).toMatch(/^\/\.well-known\/cpamp-bootstrap\/ui#handoff=/);

      const proc = spawn(binPath, [], { env, stdio: ['ignore', 'pipe', 'pipe'] });
      try {
        const addr = await new Promise<string>((resolve, reject) => {
          let logs = '';
          const onData = (chunk: Buffer) => {
            logs += chunk.toString('utf8');
            const match = logs.match(/cpa-manager-plus listening on (127\.0\.0\.1:\d+)/);
            if (match) {
              resolve(match[1]);
            }
          };
          proc.stderr.on('data', onData);
          proc.stdout.on('data', onData);
          proc.on('exit', (code) => reject(new Error(`Server exited early (${code}): ${logs}`)));
          setTimeout(() => reject(new Error(`Timeout waiting for listener: ${logs}`)), 10000);
        });

        const origin = `http://${addr}`;
        let discoveryReady = false;
        for (let i = 0; i < 50; i += 1) {
          const res = await fetch(`${origin}/.well-known/cpamp-bootstrap`);
          const body = await res.text();
          if (res.status === 200 && body.includes('fresh_claim_required')) {
            discoveryReady = true;
            break;
          }
          await new Promise((r) => setTimeout(r, 50));
        }
        expect(discoveryReady).toBe(true);

        const uiRes = await fetch(`${origin}${BOOTSTRAP_UI}`, {
          headers: { Accept: 'text/html' },
        });
        const uiHtml = await uiRes.text();
        expect(uiRes.status).toBe(200);
        expect(uiHtml).toContain('<title>CPAMP · 初始化向导</title>');

        const trace: Array<{ method: string; path: string; status: number }> = [];
        const pendingRequests = new Set<Promise<unknown>>();
        const realFetch: typeof fetch = async (input, init) => {
          const task = (async () => {
            const urlStr =
              typeof input === 'string' ? input : input instanceof URL ? input.href : input.url;
            const u = new URL(urlStr);
            const res = await globalThis.fetch(input, init);
            const text = await res.text();
            trace.push({
              method: init?.method || 'GET',
              path: u.pathname,
              status: res.status,
            });
            return new Response(text, {
              status: res.status,
              statusText: res.statusText,
              headers: res.headers,
            });
          })();
          pendingRequests.add(task);
          try {
            return await task;
          } finally {
            pendingRequests.delete(task);
          }
        };
        const flushNetwork = async () => {
          while (pendingRequests.size > 0) {
            await Promise.allSettled([...pendingRequests]);
            await new Promise((r) => setTimeout(r, 10));
          }
        };

        const liveLocation = new URL(`${origin}${handoffLine}`);
        vi.stubGlobal('window', {
          location: liveLocation,
          history: {
            replaceState: (_: unknown, _title: string, nextPath: string) => {
              liveLocation.href = new URL(nextPath, liveLocation.origin).href;
            },
          },
        });

        const liveHandoff = consumeHandoff(window.location, window.history);
        expect(liveHandoff).not.toBeNull();
        expect(window.location.hash).toBe('');

        const transport = createBootstrapTransport(realFetch);
        let renderer!: ReactTestRenderer;
        await act(async () => {
          renderer = create(<FreshBootstrapPage handoff={liveHandoff} transport={transport} />);
        });
        await act(async () => {
          await flushNetwork();
        });

        expect(textOf(findByTestId(renderer.root, 'op-id'))).toBe(
          liveHandoff!.identity.operationId
        );
        expect(textOf(findByTestId(renderer.root, 'op-admin-status'))).toContain('待初始化');
        expect(textOf(findByTestId(renderer.root, 'op-gateway-status'))).toContain(
          'Pending · Fail-Closed'
        );

        // Negative Scenario A: Replay consumed claim token -> 401
        const replayTransport = createBootstrapTransport(realFetch);
        await expect(replayTransport.start(liveHandoff!)).rejects.toMatchObject({
          kind: 'rejected',
          status: 401,
        });

        // Negative Scenario B: Weak key rejected by live backend -> 400
        await expect(
          transport.commitAdminKey('commit-weak-live', 'too-short')
        ).rejects.toMatchObject({
          kind: 'bad_request',
          status: 400,
        });

        // Happy Path: Generate key via UI -> Confirm -> Submit Admin Commit via UI
        const generateBtn = findByTestId(renderer.root, 'generate-key-btn');
        await act(async () => {
          generateBtn.props.onClick();
          await flushNetwork();
        });

        const keyInput = findByTestId(renderer.root, 'admin-key-input');
        const generatedKey = keyInput.props.value as string;
        expect(generatedKey).toMatch(/^cpamp_[A-Za-z0-9]{32}$/);

        const checkbox = findByTestId(renderer.root, 'confirm-key-saved-checkbox');
        await act(async () => {
          checkbox.props.onChange({ target: { checked: true } });
        });

        const form = findByTestId(renderer.root, 'admin-init-form');
        await act(async () => {
          form.props.onSubmit({ preventDefault: vi.fn() });
          await flushNetwork();
        });

        expect(textOf(findByTestId(renderer.root, 'admin-init-complete-panel'))).toContain(
          '本次管理员密钥初始化已成功写入服务端'
        );
        expect(textOf(findByTestId(renderer.root, 'op-admin-status'))).toContain('已初始化');
        expect(textOf(findByTestId(renderer.root, 'op-gateway-status'))).toContain(
          'Pending · Fail-Closed'
        );

        const refreshBtn = findByTestId(renderer.root, 'refresh-operation-btn');
        await act(async () => {
          refreshBtn.props.onClick();
          await flushNetwork();
        });

        expect(trace).toEqual([
          {
            method: 'POST',
            path: '/.well-known/cpamp-bootstrap/api/v2/claim/exchange',
            status: 200,
          },
          {
            method: 'POST',
            path: '/.well-known/cpamp-bootstrap/api/v2/claim/exchange',
            status: 401,
          },
          {
            method: 'POST',
            path: '/.well-known/cpamp-bootstrap/api/v2/admin/commit',
            status: 400,
          },
          {
            method: 'POST',
            path: '/.well-known/cpamp-bootstrap/api/v2/admin/generate',
            status: 200,
          },
          {
            method: 'POST',
            path: '/.well-known/cpamp-bootstrap/api/v2/admin/commit',
            status: 200,
          },
          {
            method: 'GET',
            path: '/.well-known/cpamp-bootstrap/api/v2/operation',
            status: 200,
          },
        ]);
      } finally {
        proc.kill('SIGINT');
        await new Promise((r) => proc.once('exit', r));
        rmSync(tempDir, { recursive: true, force: true });
      }
    },
    30000
  );
});
