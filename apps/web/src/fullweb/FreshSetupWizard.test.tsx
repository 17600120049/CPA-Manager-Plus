import { act, create, type ReactTestRenderer } from 'react-test-renderer';
import { beforeEach, afterEach, expect, it, vi } from 'vitest';
import { FreshSetupWizard } from './FreshSetupWizard';
import { BOOTSTRAP_UI, createBootstrapTransport, type FreshOperation } from './bootstrapTransport';
import { setFullWebLanguage } from '../i18n/fullweb';

const identity = {
  admissionId: 'admission',
  installationId: 'installation',
  generation: 'generation',
  databasePath: '/data/manager.sqlite',
  operationId: 'operation',
};
const admin = 'Admin_0123456789-abcdefghijklmnopqrstuvwxyz!';
const sourceKey = 'CPA-only-secret';
const runtime = {
  runtimeIdentity: 'runtime',
  runtimeGeneration: '18446744073709551615',
  activeGatewayArtifact: { artifactId: 'sha256:' + 'a'.repeat(64) },
  state: 'ready',
};
let renderer: ReactTestRenderer;
beforeEach(() => {
  setFullWebLanguage('zh-CN');
  vi.stubGlobal('IS_REACT_ACT_ENVIRONMENT', true);
  vi.stubGlobal('window', { location: new URL('https://manager.example' + BOOTSTRAP_UI) });
  vi.stubGlobal('localStorage', {
    setItem: vi.fn(() => {
      throw new Error('No setup secrets in storage');
    }),
  });
  vi.stubGlobal('sessionStorage', {
    setItem: vi.fn(() => {
      throw new Error('No setup secrets in storage');
    }),
  });
});
afterEach(async () => {
  if (renderer) await act(async () => renderer.unmount());
  vi.unstubAllGlobals();
});
function byID(id: string) {
  return renderer.root.findByProps({ 'data-testid': id });
}
async function click(id: string) {
  await act(async () => {
    await byID(id).props.onClick();
  });
}
async function input(id: string, value: string | boolean) {
  await act(async () => byID(id).props.onChange({ target: { value, checked: value } }));
}
async function setup(
  options: {
    external?: boolean;
    lost?: string;
    unresolved?: boolean;
    runtimePending?: boolean;
    rejectExternal?: boolean;
    loseIdentity?: boolean;
    partialRoute?: boolean;
  } = {}
) {
  let operation: FreshOperation = {
    version: 1,
    identity,
    revision: 1,
    adminInitialized: false,
    productComplete: false,
    gatewayPending: true,
    routing: { committed: false },
  };
  let lost = false;
  let recovered = false;
  let externalRejected = false;
  const transport = vi.fn<typeof fetch>(async (url, init) => {
    const resource = new URL(String(url)).pathname.split('/api/v2/')[1];
    const body = init?.body ? JSON.parse(String(init.body)) : {};
    if (resource === 'claim/exchange')
      return Response.json({ token: 'installation-session', operation });
    expect(init?.headers).toMatchObject({ Authorization: 'Fresh installation-session' });
    if (resource === 'installation/runtime')
      return Response.json({
        ...runtime,
        state:
          options.runtimePending && operation.strategy?.stage === 'committed'
            ? 'starting'
            : 'ready',
      });
    if (resource === 'admin/generate') return Response.json({ key: admin });
    if (resource === 'operation') {
      if (options.unresolved && lost && !recovered) throw new TypeError('Read unavailable');
      if (operation.routing.committed && String(url).includes(BOOTSTRAP_UI.replace('/ui', '/api/')))
        return new Response('', { status: 404 });
      return Response.json(operation);
    }
    if (resource === 'bootstrap/identity')
      throw new Error('Public identity must not carry a credential');
    if (resource === 'admin/commit') {
      expect(body.key).toBe(admin);
      operation = { ...operation, adminInitialized: true, revision: operation.revision + 1 };
    }
    if (resource === 'strategy') {
      expect(body.identity).toEqual(identity);
      expect(body.expectedRevision).toBe(operation.revision);
      if (body.strategy === 'connect_external') {
        if (options.rejectExternal && !externalRejected) {
          externalRejected = true;
          return new Response('', { status: 409 });
        }
        expect(body.managementKey).toBe(sourceKey);
        expect(body).not.toHaveProperty('key');
        operation = {
          ...operation,
          revision: operation.revision + 1,
          strategy: {
            strategy: body.strategy,
            stage: 'committed',
            targetIdentity: 'external:sha256:' + 'b'.repeat(64),
            targetGeneration: 1,
          },
        };
      } else {
        expect(String(init?.body)).toContain('"expectedRuntimeGeneration":18446744073709551615');
        operation = {
          ...operation,
          revision: operation.revision + 1,
          strategy: {
            strategy: body.strategy,
            stage: operation.strategy ? 'committed' : 'staged',
            targetIdentity: runtime.runtimeIdentity,
            targetGeneration: runtime.runtimeGeneration,
            targetArtifactId: runtime.activeGatewayArtifact.artifactId,
          },
        };
      }
    }
    if (resource === 'routing/prepare') {
      expect(operation.strategy?.stage).toBe('committed');
      operation = {
        ...operation,
        revision: operation.revision + 1,
        routing: {
          target: body.target,
          revision: 1,
          operationId: identity.operationId,
          stage: 'intent_persisted',
          committed: false,
        },
      };
    }
    if (resource === 'routing/activate') {
      if (options.partialRoute && !lost) {
        lost = true;
        operation = {
          ...operation,
          revision: operation.revision + 1,
          routing: { ...operation.routing, stage: 'route_activated' },
        };
        return new Response('', { status: 503 });
      }
      operation = {
        ...operation,
        revision: operation.revision + 1,
        routing: { ...operation.routing, stage: 'committed', committed: true },
      };
    }
    if (resource === 'complete') {
      expect(new URL(String(url)).pathname).toBe('/private/api/v2/complete');
      expect(operation.routing.committed).toBe(true);
      operation = { ...operation, revision: operation.revision + 1, productComplete: true };
    }
    if (resource === options.lost && !lost) {
      lost = true;
      throw new TypeError('Reply lost');
    }
    return Response.json(operation);
  });
  const fetcher = vi.fn<typeof fetch>((url, init) => {
    if (String(url).endsWith('/bootstrap/identity')) {
      expect(init?.headers).not.toHaveProperty('Authorization');
      if (options.loseIdentity && !recovered)
        return Promise.reject(new TypeError('Identity unavailable'));
      return Promise.resolve(
        Response.json(
          {
            installationId: identity.installationId,
            generation: identity.generation,
            operationId: identity.operationId,
            revision: 1,
            target: '/private',
            committed: operation.routing.committed,
          },
          { headers: { 'X-CPAMP-Route-Owner': 'manager' } }
        )
      );
    }
    return transport(url, init);
  });
  const client = createBootstrapTransport(fetcher);
  const initialOperation = await client.start({
    identity,
    claimGeneration: 1,
    token: 'claim-token',
    resume: false,
  });
  await act(async () => {
    renderer = create(<FreshSetupWizard initialOperation={initialOperation} transport={client} />);
  });
  if (options.external) await click('choose-connect_external');
  await click('setup-next');
  await input('admin-key-input', admin);
  await input('route-target-input', '/private');
  if (options.external) {
    await input('external-base-url', 'http://cpa:8317');
    await input('external-management-key', sourceKey);
  }
  return {
    fetcher,
    transport,
    client,
    recover: () => {
      recovered = true;
    },
  };
}

it.each([false, true])(
  'keeps key, route and CPA in one confirmed journey; external=%s',
  async (external) => {
    const { transport } = await setup({ external });
    await click('setup-review');
    expect(JSON.stringify(renderer.toJSON())).not.toContain(admin);
    expect(JSON.stringify(renderer.toJSON())).not.toContain(sourceKey);
    expect(transport.mock.calls.filter(([, init]) => init?.method === 'POST')).toHaveLength(1); // only claim, no product writes
    await input('confirm-key-saved-checkbox', true);
    await click('setup-execute');
    expect(byID('enter-console').props.href).toBe('/private');
    expect(JSON.stringify(renderer.toJSON())).toContain('安装已完成');
    expect(localStorage.setItem).not.toHaveBeenCalled();
    expect(sessionStorage.setItem).not.toHaveBeenCalled();
    const writes = transport.mock.calls
      .filter(([, init]) => init?.method === 'POST')
      .map(([url]) => new URL(String(url)).pathname.split('/api/v2/')[1]);
    expect(writes).toEqual(
      external
        ? [
            'claim/exchange',
            'admin/commit',
            'strategy',
            'routing/prepare',
            'routing/activate',
            'complete',
          ]
        : [
            'claim/exchange',
            'admin/commit',
            'strategy',
            'strategy',
            'routing/prepare',
            'routing/activate',
            'complete',
          ]
    );
  }
);

it.each(['admin/commit', 'strategy', 'routing/activate', 'complete'])(
  'reconciles lost %s replies without repeating writes or logging in',
  async (lost) => {
    const { transport } = await setup({ external: true, lost });
    await click('setup-review');
    await input('confirm-key-saved-checkbox', true);
    await click('setup-execute');
    expect(byID('enter-console').props.href).toBe('/private');
    expect(
      transport.mock.calls.filter(
        ([url, init]) => init?.method === 'POST' && String(url).endsWith('/' + lost)
      )
    ).toHaveLength(1);
  }
);

it('blocks unconfirmed writes when both POST response and readback are lost', async () => {
  const { transport } = await setup({ external: true, lost: 'admin/commit', unresolved: true });
  await click('setup-review');
  await input('confirm-key-saved-checkbox', true);
  await click('setup-execute');
  expect(byID('setup-resume').props.disabled).toBe(true);
  await click('setup-refresh');
  expect(
    transport.mock.calls.filter(([url]) => String(url).endsWith('/admin/commit'))
  ).toHaveLength(1);
  expect(renderer.root.findAllByProps({ 'data-testid': 'enter-console' })).toHaveLength(0);
});

it('does not commit the route or finish while managed CPA is not ready', async () => {
  const { transport } = await setup({ runtimePending: true });
  await click('setup-review');
  await input('confirm-key-saved-checkbox', true);
  await click('setup-execute');
  expect(
    transport.mock.calls.some(
      ([url]) => String(url).endsWith('/routing/activate') || String(url).endsWith('/complete')
    )
  ).toBe(false);
  expect(JSON.stringify(renderer.toJSON())).toContain('CPA 正在启动');
});

it('recovers the committed route using reads after activation and identity replies were both lost', async () => {
  const { transport, recover } = await setup({
    external: true,
    lost: 'routing/activate',
    unresolved: true,
    loseIdentity: true,
  });
  await click('setup-review');
  await input('confirm-key-saved-checkbox', true);
  await click('setup-execute');
  expect(byID('setup-resume').props.disabled).toBe(true);
  recover();
  await click('setup-refresh');
  expect(byID('setup-resume').props.disabled).toBe(false);
  await click('setup-resume');
  expect(byID('enter-console').props.href).toBe('/private');
  expect(
    transport.mock.calls.filter(([url]) => String(url).endsWith('/routing/activate'))
  ).toHaveLength(1);
  expect(
    transport.mock.calls.filter(([url]) => String(url).endsWith('/admin/commit'))
  ).toHaveLength(1);
});

it('allows correcting a rejected external connection without recreating the saved administrator', async () => {
  const { transport } = await setup({ external: true, rejectExternal: true });
  await click('setup-review');
  await input('confirm-key-saved-checkbox', true);
  await click('setup-execute');
  expect(renderer.root.findAllByProps({ 'data-testid': 'admin-key-input' })).toHaveLength(0);
  expect(byID('external-base-url').props.value).toBe('http://cpa:8317');
  expect(byID('external-management-key').props.value).toBe(sourceKey);
  await click('setup-review');
  expect(byID('setup-execute').props.disabled).toBe(true);
  await input('confirm-key-saved-checkbox', true);
  await click('setup-execute');
  expect(byID('enter-console').props.href).toBe('/private');
  expect(
    transport.mock.calls.filter(([url]) => String(url).endsWith('/admin/commit'))
  ).toHaveLength(1);
});

it('allows explicit same-operation route recovery only after reading the persisted partial checkpoint', async () => {
  const { transport } = await setup({ external: true, partialRoute: true });
  await click('setup-review');
  await input('confirm-key-saved-checkbox', true);
  await click('setup-execute');
  expect(byID('setup-resume').props.disabled).toBe(true);
  expect(
    transport.mock.calls.filter(([url]) => String(url).endsWith('/routing/activate'))
  ).toHaveLength(1);
  await click('setup-refresh');
  expect(byID('setup-resume').props.disabled).toBe(false);
  // Reading never attempts another activation by itself.
  expect(
    transport.mock.calls.filter(([url]) => String(url).endsWith('/routing/activate'))
  ).toHaveLength(1);
  await click('setup-resume');
  expect(byID('enter-console').props.href).toBe('/private');
  const activations = transport.mock.calls.filter(([url]) =>
    String(url).endsWith('/routing/activate')
  );
  expect(activations).toHaveLength(2);
  expect(activations.map(([, init]) => JSON.parse(String(init?.body)).identity)).toEqual([
    identity,
    identity,
  ]);
  expect(
    transport.mock.calls.filter(([url]) => String(url).endsWith('/admin/commit'))
  ).toHaveLength(1);
});

it('validates key and path before confirmation and does not submit an unconfirmed plan', async () => {
  const { transport } = await setup();
  await input('admin-key-input', 'weak');
  await click('setup-review');
  expect(renderer.root.findAllByProps({ 'data-testid': 'setup-execute' })).toHaveLength(0);
  await input('admin-key-input', admin);
  await input('route-target-input', '/v1');
  await click('setup-review');
  expect(renderer.root.findAllByProps({ 'data-testid': 'setup-execute' })).toHaveLength(0);
  await input('route-target-input', '/private');
  await click('setup-review');
  expect(byID('setup-execute').props.disabled).toBe(true);
  await click('setup-execute');
  expect(transport.mock.calls.some(([url]) => String(url).endsWith('/admin/commit'))).toBe(false);
});

it('generates a key only on request and requires fresh confirmation after editing it', async () => {
  const { transport } = await setup();
  await input('admin-key-input', '');
  await click('generate-key-btn');
  expect(byID('admin-key-input').props.value).toBe(admin);
  expect(byID('admin-key-input').props.type).toBe('text');
  await click('setup-review');
  await input('confirm-key-saved-checkbox', true);
  await act(async () =>
    renderer.root
      .findAllByType('button')
      .find((node) => node.children.includes('上一步'))!
      .props.onClick()
  );
  await input('admin-key-input', admin + 'changed');
  await click('setup-review');
  expect(byID('setup-execute').props.disabled).toBe(true);
  expect(
    transport.mock.calls.filter(([url]) => String(url).endsWith('/admin/generate'))
  ).toHaveLength(1);
  expect(transport.mock.calls.some(([url]) => String(url).endsWith('/admin/commit'))).toBe(false);
});

it.each(['zh-CN', 'zh-TW', 'en'])('keeps the unified journey localized (%s)', async (language) => {
  await setup({ external: true });
  setFullWebLanguage(language);
  await click('setup-review');
  expect(JSON.stringify(renderer.toJSON())).toContain(
    language === 'en'
      ? 'Review installation'
      : language === 'zh-TW'
        ? '確認安裝資訊'
        : '确认安装信息'
  );
  await input('confirm-key-saved-checkbox', true);
  await click('setup-execute');
  expect(byID('enter-console').props.href).toBe('/private');
  expect(JSON.stringify(renderer.toJSON())).toContain(
    language === 'en'
      ? 'Sign in and enter CPAMP'
      : language === 'zh-TW'
        ? '登入並進入 CPAMP'
        : '登录并进入 CPAMP'
  );
});

it('uses the maintained shell and shared CSS classes across configuration, review and completion', async () => {
  const { readFileSync } = (
    globalThis as unknown as {
      process: {
        getBuiltinModule: (name: string) => {
          readFileSync: (path: URL, encoding: string) => string;
        };
      };
    }
  ).process.getBuiltinModule('node:fs');
  const css =
    readFileSync(new URL('./carrier.css', import.meta.url), 'utf8') +
    readFileSync(new URL('../styles/components.scss', import.meta.url), 'utf8');
  const defined = new Set(Array.from(css.matchAll(/\.([a-zA-Z0-9_-]+)/g), (match) => match[1]));
  const check = () => {
    for (const node of renderer.root.findAll((node) => typeof node.props.className === 'string')) {
      for (const name of node.props.className.split(/\s+/))
        if (name) expect(defined.has(name), `Missing CSS class .${name}`).toBe(true);
    }
  };
  await setup({ external: true });
  check();
  await click('setup-review');
  check();
  await input('confirm-key-saved-checkbox', true);
  await click('setup-execute');
  check();
});
