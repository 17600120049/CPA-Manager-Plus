#!/usr/bin/env node
// Disposable normal-Compose Fresh New acceptance. No S1/S2 seed or shared state.
import assert from 'node:assert/strict';
import { spawnSync } from 'node:child_process';
import { randomBytes, createHash } from 'node:crypto';
import { mkdirSync, writeFileSync, readFileSync } from 'node:fs';
import { createRequire } from 'node:module';
import { createServer } from 'node:net';
import { resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';

const root = resolve(dirname(fileURLToPath(import.meta.url)), '../..');
const [runtimeImage, managerImage, ingressImage, output] = process.argv.slice(2);
assert(
  runtimeImage && managerImage && ingressImage && output,
  'Usage: fresh-new-installation.mjs RUNTIME_IMAGE MANAGER_IMAGE INGRESS_IMAGE NEW_OUTPUT'
);
const { chromium } = createRequire(import.meta.url)(process.env.PLAYWRIGHT_MODULE || 'playwright');
const out = resolve(output);
mkdirSync(out, { recursive: false, mode: 0o700 });
const project = 'cpamp-fresh-new-' + randomBytes(6).toString('hex');
const bootstrap = '/.well-known/cpamp-bootstrap';
const api = bootstrap + '/api/v2/';
const pin = 'b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41';
const report = {
  result: 'FAIL',
  project,
  checks: [],
  screenshots: [],
  pageErrors: [],
  notRun: [
    'Real Provider/model traffic, public Gateway, other four journeys, arm64, power-loss and production rollback',
    'Natural ten-minute claim expiry (expired server timestamp is injected offline only in this disposable fixture)',
    'Non-Chinese locale layouts, other browser engines',
  ],
};
// Pin a free loopback port in HostConfig. Publishing port 0 can allocate a new
// host port after restart, which would invalidate the browser's saved origin.
const portProbe = createServer();
await new Promise((resolve, reject) => {
  portProbe.once('error', reject);
  portProbe.listen(0, '127.0.0.1', resolve);
});
const publicPort = portProbe.address().port;
await new Promise((resolve) => portProbe.close(resolve));
const env = { ...process.env, CPAMP_PUBLIC_PORT: `127.0.0.1:${publicPort}` };
const secretValues = [];
let browser;
let page;
function run(args, input, check = true) {
  const result = spawnSync(args[0], args.slice(1), {
    cwd: root,
    env,
    input,
    encoding: 'utf8',
    timeout: 180000,
    maxBuffer: 8 << 20,
  });
  if (check && result.status !== 0)
    throw new Error(
      'Command failed: ' +
        args.slice(0, 3).join(' ') +
        '\n' +
        secretValues.reduce(
          (message, value) => message.replaceAll(value, '[REDACTED]'),
          result.stderr || ''
        )
    );
  return result;
}
const docker = (...args) => run(['docker', ...args]).stdout.trim();
const images = Object.fromEntries(
  [
    ['cpamp-runtime', runtimeImage],
    ['cpamp-manager', managerImage],
    ['cpamp-ingress', ingressImage],
  ].map(([service, image]) => {
    const meta = JSON.parse(docker('image', 'inspect', image))[0];
    assert(meta.Os === 'linux' && meta.Architecture === 'amd64', 'Linux/amd64 image required');
    return [service, meta.Id];
  })
);
report.images = images;
report.imageSourceRevisions = Object.fromEntries(
  Object.entries(images).map(([service, image]) => [
    service,
    JSON.parse(docker('image', 'inspect', image))[0].Config.Labels?.[
      'org.opencontainers.image.revision'
    ] || 'unknown',
  ])
);
report.testedSourceCommit = report.imageSourceRevisions['cpamp-manager'];
report.source = docker('version', '--format', '{{.Server.Os}}/{{.Server.Arch}}');
report.gitHead = run(['git', 'rev-parse', 'HEAD']).stdout.trim();
report.diffSha256 = createHash('sha256')
  .update(run(['git', 'diff', '--binary', 'HEAD']).stdout)
  .digest('hex');
report.sourceFiles = Object.fromEntries(
  [
    ...new Set(
      (
        run(['git', 'diff', '--name-only', 'HEAD']).stdout +
        run(['git', 'ls-files', '--others', '--exclude-standard']).stdout
      )
        .trim()
        .split('\n')
    ),
  ]
    .filter((file) =>
      /^(apps\/(web\/src|manager-server\/cmd)\/|bin\/ci\/fresh-new-installation)/.test(file)
    )
    .map((file) => [
      file,
      createHash('sha256')
        .update(readFileSync(resolve(root, file)))
        .digest('hex'),
    ])
);
const override = resolve(out, 'compose.override.json');
writeFileSync(
  override,
  JSON.stringify(
    {
      services: Object.fromEntries(
        Object.entries(images).map(([service, image]) => [
          service,
          {
            image,
            platform: 'linux/amd64',
            ...(service === 'cpamp-manager'
              ? { environment: { CPAMP_PUBLIC_ORIGIN: 'http://cpamp-ingress:18317' } }
              : {}),
          },
        ])
      ),
    },
    null,
    2
  )
);
const compose = [
  'docker',
  'compose',
  '--project-directory',
  root,
  '-p',
  project,
  '-f',
  resolve(root, 'docker-compose.yml'),
  '-f',
  override,
];
const dc = (...args) => run([...compose, ...args]).stdout.trim();
function pass(name) {
  report.checks.push({ name, result: 'PASS' });
  console.log('PASS ' + name);
}
async function screenshot(name, fullPage = true) {
  const overflow = await page.evaluate(() => ({
    width: innerWidth,
    scrollWidth: document.documentElement.scrollWidth,
    elements: [...document.querySelectorAll('body *')]
      .filter((element) => element.getBoundingClientRect().right > innerWidth + 1)
      .slice(0, 15)
      .map((element) => ({
        tag: element.tagName,
        class: element.getAttribute('class'),
        right: element.getBoundingClientRect().right,
      })),
  }));
  assert(
    await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth),
    'horizontal overflow: ' + name + ' ' + JSON.stringify(overflow)
  );
  await page.screenshot({
    path: resolve(out, name + '.png'),
    fullPage,
    animations: 'disabled',
  });
  report.screenshots.push({
    name,
    path: name + '.png',
    viewport: page.viewportSize(),
    theme: await page
      .locator('html')
      .evaluate((html) => (html.getAttribute('data-theme') === 'dark' ? 'dark' : 'light')),
    route: new URL(page.url()).pathname,
  });
}
async function installationScreenshots(stage) {
  for (const [name, width, light] of [
    ['desktop-dark', 1440, false],
    ['desktop-light', 1440, true],
    ['mobile-light', 390, true],
    ['mobile-dark', 390, false],
  ]) {
    await page.setViewportSize({ width, height: 1000 });
    if ((await page.locator('html').evaluate((html) => html.classList.contains('light'))) !== light)
      await page.getByRole('button', { name: '切换明暗主题', exact: true }).click();
    await screenshot('installation-' + stage + '-' + name);
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
}
async function wait(check, label, timeout = 45000) {
  const end = Date.now() + timeout;
  do {
    try {
      if (await check()) return;
    } catch {}
    await new Promise((r) => setTimeout(r, 250));
  } while (Date.now() < end);
  throw new Error('Timed out: ' + label);
}
const pythonImage = docker('image', 'inspect', 'python:3.13-alpine', '--format', '{{.Id}}');
function python(volume, program, input, readonly = false) {
  return run(
    [
      'docker',
      'run',
      '--rm',
      '-i',
      '--network',
      'none',
      '-v',
      `${volume}:/data${readonly ? ':ro' : ''}`,
      pythonImage,
      'python3',
      '-c',
      program,
    ],
    input
  ).stdout.trim();
}
const snapshotProgram = `import os,hashlib,json
v={}
for root,ds,fs in os.walk('/data'):
 for f in fs:
  p=os.path.join(root,f);s=os.stat(p);v[p]=[s.st_uid,s.st_gid,s.st_mode,hashlib.sha256(open(p,'rb').read()).hexdigest()]
print(json.dumps(v,sort_keys=True))`;
const snapshot = (volume) => python(volume, snapshotProgram, undefined, true);
const helperVolumes = [];
let taskNetwork;

try {
  dc('up', '-d', '--no-build', '--pull', 'never');
  const ingress = dc('ps', '-q', 'cpamp-ingress');
  const runtime = dc('ps', '-q', 'cpamp-runtime');
  const binding = JSON.parse(docker('inspect', ingress))[0].NetworkSettings.Ports['18317/tcp'][0];
  assert.equal(binding.HostIp, '127.0.0.1');
  const origin = `http://127.0.0.1:${binding.HostPort}`;
  report.origin = origin;
  const managerVolume = JSON.parse(
    docker('inspect', dc('ps', '-q', 'cpamp-manager'))
  )[0].Mounts.find((m) => m.Destination === '/data').Name;
  const network = Object.keys(
    JSON.parse(docker('inspect', ingress))[0].NetworkSettings.Networks
  )[0];
  taskNetwork = network;
  await wait(async () => (await fetch(origin + bootstrap)).ok, 'public Discovery');
  assert.equal((await fetch(origin + bootstrap + '/ui')).status, 200);
  for (const path of ['claim/exchange', 'admin/generate', 'admin/commit']) {
    assert.equal(
      (
        await fetch(origin + api + path, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: '{}',
        })
      ).status,
      404,
      'read-only discovery must not expose ' + path
    );
  }
  assert.equal(
    dc(
      'exec',
      '-T',
      'cpamp-manager',
      'sh',
      '-ec',
      'test ! -e /data/usage.sqlite; test ! -e /data/data.key; echo pristine'
    ),
    'pristine'
  );
  pass('first boot waits for Host initialize without creating database or data key');
  dc('stop', 'cpamp-manager');
  const issue = () => dc('run', '--rm', '--no-deps', 'cpamp-manager', 'initialize');
  let handoff = issue();
  const decode = (link) =>
    JSON.parse(Buffer.from(link.split('#handoff=')[1], 'base64url').toString());
  const fragment = (value) =>
    bootstrap + '/ui#handoff=' + Buffer.from(JSON.stringify(value)).toString('base64url');
  const expired = decode(handoff);
  secretValues.push(expired.token);
  python(
    managerVolume,
    `import sqlite3,json
c=sqlite3.connect('/data/usage.sqlite');v=json.loads(c.execute("select value from settings where key='fresh_authority_v1'").fetchone()[0]);v['claimExpiresAtMs']=1;c.execute("update settings set value=? where key='fresh_authority_v1'",(json.dumps(v),));c.commit()`
  );
  dc('up', '-d', '--no-build', 'cpamp-manager');
  await wait(async () => (await fetch(origin + bootstrap + '/ui')).ok, 'Bootstrap UI');
  browser = await chromium.launch({
    headless: true,
    ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}),
  });
  const context = await browser.newContext({
    locale: 'zh-CN',
    viewport: { width: 1440, height: 1000 },
  });
  page = await context.newPage();
  report.strategyGenerations = [];
  page.on('request', (request) => {
    if (
      new URL(request.url()).pathname === '/console/api/v2/strategy' &&
      request.method() === 'POST'
    ) {
      report.strategyGenerations.push(
        request.postData()?.match(/"expectedRuntimeGeneration":([0-9]+)/)?.[1] || 'INVALID'
      );
    }
  });
  page.on('response', async (response) => {
    const path = new URL(response.url()).pathname;
    if (path === '/console/api/v2/strategy' || path === '/console/api/v2/complete') {
      report.checks.push({
        name: response.request().method() + ' ' + path,
        status: response.status(),
      });
      if (response.status() === 409) {
        try {
          report.strategyBlocker = (await response.json()).Code;
        } catch {}
      }
    }
  });
  page.on('pageerror', () => report.pageErrors.push('Uncaught browser error'));
  await page.goto(origin + handoff);
  await page.getByTestId('bootstrap-authority-error').waitFor();
  assert((await page.getByTestId('init-error-title').innerText()).includes('401'));
  pass('expired Handoff is rejected with Host recovery guidance (offline expiry fixture)');
  dc('stop', 'cpamp-manager');
  handoff = issue();
  const claim = decode(handoff);
  secretValues.push(claim.token);
  dc('up', '-d', '--no-build', 'cpamp-manager');
  await wait(async () => (await fetch(origin + bootstrap + '/ui')).ok, 'reissued Bootstrap UI');
  await page.goto(
    origin +
      fragment({ ...claim, identity: { ...claim.identity, installationId: 'wrong-installation' } })
  );
  await page.getByTestId('bootstrap-authority-error').waitFor();
  pass('wrong Handoff installation identity is rejected without consuming the valid claim');
  await page.goto(origin + bootstrap + '/ui');
  await page.getByTestId('bootstrap-discovery-guidance').waitFor();
  await page.goto(origin + handoff);
  await page.getByTestId('bootstrap-operation-ready').waitFor();
  assert.equal(new URL(page.url()).hash, '');
  await screenshot('bootstrap-administrator-desktop-light');
  const operationId = (await page.getByTestId('op-id').textContent()).trim();
  assert(operationId, 'claimed operation identity must be present');
  report.operationId = operationId;
  const replayPage = await context.newPage();
  await replayPage.goto(origin + handoff);
  await replayPage.getByTestId('bootstrap-authority-error').waitFor();
  await replayPage.close();
  pass('real browser claims once, removes fragment and rejects consumed Handoff');
  await page.getByTestId('commit-admin-btn').click();
  await page.getByTestId('feedback-title').waitFor();
  await page.getByTestId('admin-key-input').fill('weak');
  await page.getByTestId('commit-admin-btn').click();
  assert((await page.getByTestId('feedback-title').innerText()).includes('安全校验'));
  await page.getByTestId('generate-key-btn').click();
  await wait(
    async () => (await page.getByTestId('admin-key-input').inputValue()).length >= 24,
    'generated Admin Key'
  );
  const admin = await page.getByTestId('admin-key-input').inputValue();
  secretValues.push(admin);
  await page.getByTestId('confirm-key-saved-checkbox').check();
  let commits = 0;
  await page.route('**' + api + 'admin/commit', async (route) => {
    commits++;
    const response = await route.fetch();
    assert.equal(response.status(), 200);
    await route.abort('failed'); // Real server committed; response lost to browser.
  });
  await page.route('**' + api + 'operation', (route) => route.abort('failed'), { times: 1 });
  await page.getByTestId('commit-admin-btn').click();
  await page.getByTestId('admin-commit-unresolved').waitFor();
  assert(await page.getByTestId('admin-key-input').isDisabled());
  assert(await page.getByTestId('commit-admin-btn').isDisabled());
  await page.getByTestId('refresh-operation-btn').click();
  await page.getByTestId('admin-init-complete-panel').waitFor();
  assert.equal(commits, 1);
  await page.unroute('**' + api + 'admin/commit');
  pass('missing/weak Admin Key rejected; server-generated Key commits once despite lost response');
  let activations = 0;
  await page.route('**' + api + 'routing/activate', async (route) => {
    activations++;
    if (activations === 1) await route.fulfill({ status: 503, body: '' });
    else {
      const reply = await route.fetch();
      assert.equal(reply.status(), 200);
      await route.abort('failed');
    }
  });
  await page.getByTestId('enable-console').click();
  await wait(
    async () => !(await page.getByTestId('enable-console').isDisabled()),
    'failed activation recovery'
  );
  assert.equal(await page.getByTestId('console-path').inputValue(), '/console');
  assert(await page.getByTestId('console-path').isDisabled());
  await page.getByTestId('enable-console').click();
  await page.getByTestId('continue-console').waitFor();
  await page.unroute('**' + api + 'routing/activate');
  assert.equal(activations, 2);
  pass('routing failure retains intent; lost activation reply reconciles exact public identity');
  await page.getByTestId('continue-console').click();
  await page.getByTestId('console-login').waitFor();
  assert.equal(await page.locator('.console-login-panel').count(), 0);
  await page.getByRole('checkbox', { name: '记住凭证', exact: true }).waitFor();
  for (const [name, width, light] of [
    ['desktop-light', 1440, true],
    ['desktop-dark', 1440, false],
    ['mobile-dark', 390, false],
    ['mobile-light', 390, true],
  ]) {
    await page.setViewportSize({ width, height: 1000 });
    for (let attempt = 0; attempt < 3; attempt++) {
      const dark = await page.locator('html').getAttribute('data-theme') === 'dark';
      if (dark === !light) break;
      await page.getByRole('button', { name: '主题', exact: true }).click();
    }
    assert.equal(await page.locator('html').getAttribute('data-theme') === 'dark', !light);
    assert.equal(await page.getByRole('button', { name: '主题', exact: true }).count(), 1);
    await screenshot('original-login-' + name, false);
  }
  await page.setViewportSize({ width: 1440, height: 1000 });
  pass('original CPAMP login page, language/theme tools and remember-credential control render on desktop/mobile light/dark');
  const login = async (key) => {
    await page.locator('#console-admin-key').fill(key);
    await page.getByTestId('console-login').locator('button[type=submit]').click();
  };
  assert(await page.getByTestId('console-login').locator('button[type=submit]').isDisabled());
  await login('incorrect-admin-key');
  await page.getByRole('alert').waitFor();
  assert(await page.getByTestId('console-login').isVisible());
  await login(admin);
  await page.getByTestId('fresh-managed-runtime').waitFor();
  pass('Console rejects missing/wrong Admin Key and accepts the installed Admin Key');
  const adminGet = async (path) => {
    const response = await fetch(origin + '/console/api/v2/' + path, {
      headers: { Authorization: 'Bearer ' + admin },
    });
    assert.equal(response.status, 200);
    return response.json();
  };
  for (const key of ['', 'incorrect-admin-key']) {
    const denied = await fetch(origin + '/console/api/v2/installation/runtime', {
      headers: key ? { Authorization: 'Bearer ' + key } : {},
    });
    assert.equal(denied.status, 401);
  }
  pass('deployment Runtime observation rejects missing/wrong administrator credentials');
  const runtimeToken = dc('exec', '-T', 'cpamp-runtime', 'cat', '/run/cpamp/runtime-secret/token');
  secretValues.push(runtimeToken);
  const statusProgram = `import urllib.request,json,sys
r=urllib.request.Request('http://cpamp-runtime:9081/v1/runtime/status',headers={'Authorization':'Bearer '+sys.stdin.read().strip(),'X-CPAMP-Runtime-Features':'active-gateway-artifact-v1'})
with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(r) as v:print(v.read().decode())`;
  const statusJSON = run(
    [
      'docker',
      'run',
      '--rm',
      '-i',
      '--network',
      network,
      pythonImage,
      'python3',
      '-c',
      statusProgram,
    ],
    runtimeToken
  ).stdout;
  const status = JSON.parse(statusJSON);
  status.runtimeGeneration = statusJSON.match(/"runtimeGeneration"\s*:\s*([0-9]+)/)[1];
  assert.equal(status.activeGatewayArtifact.artifactId, 'sha256:' + pin);
  assert.equal(status.activeGatewayArtifact.version, '8.0.13');
  assert.equal(
    dc('exec', '-T', 'cpamp-runtime', 'sha256sum', '/usr/local/bin/cli-proxy-api').split(/\s/)[0],
    pin
  );
  assert.equal(status.state, 'offline');
  await installationScreenshots('choice');
  assert.equal(await page.getByTestId('runtime-observation').count(), 0);
  await page.getByRole('button', { name: '下一步', exact: true }).click();
  await installationScreenshots('configure');
  await page.route(
    '**/console/api/v2/installation/runtime',
    (route) => route.fulfill({ status: 503, body: '' }),
    { times: 1 }
  );
  await page.getByTestId('inspect-installation').click();
  await page.getByRole('alert').filter({ hasText: '暂时无法读取部署环境' }).waitFor();
  assert.equal(report.strategyGenerations.length, 0);
  assert.equal((await adminGet('operation')).identity.operationId, operationId);
  pass(
    'failed deployment check preserves the original operation and performs no installation write'
  );
  await page.getByTestId('inspect-installation').click();
  await page.getByTestId('install-runtime').waitFor();
  assert(await page.getByTestId('install-runtime').isDisabled());
  await page.getByRole('checkbox').check();
  await installationScreenshots('review');
  let starts = 0;
  let strategyWrites = 0;
  let disconnected = false;
  await page.route('**/console/api/v2/operation', async (route) => {
    if (disconnected) await route.abort('failed');
    else await route.continue();
  });
  await page.route('**/console/api/v2/strategy', async (route) => {
    strategyWrites++;
    const reply = await route.fetch();
    assert.equal(reply.status(), 200);
    if (strategyWrites === 1) await route.fulfill({ response: reply });
    else {
      starts++;
      disconnected = true;
      await route.abort('failed'); // Real Start committed; subsequent status reads disconnected.
    }
  });
  await page.getByTestId('install-runtime').click();
  await wait(async () => starts === 1, 'real Runtime Start with lost browser reply');
  await wait(
    async () => (await page.getByTestId('install-stage').innerText()) === '结果待确认',
    'unconfirmed original installation'
  );
  assert(await page.getByTestId('install-runtime').isDisabled());
  assert.equal(starts, 1);
  assert.equal(strategyWrites, 2);
  assert.equal(report.strategyGenerations[0], status.runtimeGeneration);
  assert.equal(report.strategyGenerations[1], status.runtimeGeneration);
  await installationScreenshots('result-unconfirmed');
  disconnected = false;
  await page.unroute('**/console/api/v2/operation');
  await page.unroute('**/console/api/v2/strategy');
  await page.reload();
  await page.getByTestId('console-login').waitFor();
  await login(admin);
  await page.getByTestId('fresh-managed-runtime').waitFor();
  assert.equal((await adminGet('operation')).identity.operationId, operationId);
  assert.equal((await adminGet('operation')).strategy.stage, 'committed');
  await page.getByTestId('install-runtime').click();
  await wait(
    async () => (await adminGet('operation')).productComplete === true,
    'original installation recovered and completed after reload'
  );
  assert.equal(report.strategyGenerations.length, 2);
  const started = JSON.parse(
    run(
      [
        'docker',
        'run',
        '--rm',
        '-i',
        '--network',
        network,
        pythonImage,
        'python3',
        '-c',
        statusProgram,
      ],
      runtimeToken
    ).stdout
  );
  assert.equal(started.state, 'ready');
  const liveHash = dc(
    'exec',
    '-T',
    '--user',
    '10001:10001',
    'cpamp-runtime',
    'sh',
    '-ec',
    `for p in /proc/[0-9]*; do
    [ -r "$p/comm" ] || continue
    [ "$(cat "$p/comm")" = cli-proxy-api ] || continue
    sha256sum "$p/exe"; exit 0
  done; exit 1`
  );
  assert.equal(liveHash.split(/\s/)[0], pin);
  assert.equal(
    dc('exec', '-T', 'cpamp-runtime', 'sha256sum', '/runtime/gateway/config.yaml').split(/\s/)[0],
    createHash('sha256')
      .update(readFileSync(resolve(root, 'docker/runtime/config.v8.default.seed.yaml')))
      .digest('hex')
  );
  await wait(
    async () => (await page.getByTestId('install-stage').innerText()) === '安装已完成',
    'completed installation'
  );
  pass(
    'one user install submission staged and started official CPA; reload recovered the same operation after lost reply without repeating Start'
  );
  await installationScreenshots('complete');
  await page.getByRole('button', { name: '进入 Console', exact: true }).click();
  const legacyConsole = page.getByTestId('legacy-console');
  await legacyConsole.waitFor();
  await legacyConsole.locator('.app-shell').waitFor();
  assert.equal(new URL(page.url()).pathname, '/console');
  const bridgeInfo = await fetch(origin + '/console/api/v2/cpamp/usage-service/info', {
    headers: { Authorization: 'Bearer ' + admin },
  });
  assert.equal(bridgeInfo.status, 200);
  const configBefore = await fetch(origin + '/console/api/v2/cpamp/config', {
    headers: { Authorization: 'Bearer ' + admin },
  });
  assert.equal(configBefore.status, 200);
  const configPayload = await configBefore.json();
  assert(configPayload.config && typeof configPayload.config === 'object');
  const configToSave = {
    ...configPayload.config,
    // A fresh Managed install intentionally has no CPA Management Key yet.
    // Keep the existing manager-config invariant truthful in this disposable
    // probe: an empty connection may be saved only with collection disabled.
    collector: {
      ...configPayload.config.collector,
      ...(configPayload.config.cpaConnection?.cpaBaseUrl
        ? {}
        : { enabled: false }),
    },
  };
  const configSave = await fetch(origin + '/console/api/v2/cpamp/config', {
    method: 'PUT',
    headers: {
      Authorization: 'Bearer ' + admin,
      'Content-Type': 'application/json',
    },
    body: JSON.stringify({ config: configToSave }),
  });
  assert.equal(configSave.status, 200);
  const configAfter = await fetch(origin + '/console/api/v2/cpamp/config', {
    headers: { Authorization: 'Bearer ' + admin },
  });
  assert.equal(configAfter.status, 200);
  assert.equal((await adminGet('operation')).productComplete, true);
  pass(
    'Fresh New completion enters the existing CPAMP MainLayout; authenticated Config GET/PUT remains readable'
  );
  const before = await adminGet('operation');
  assert.equal(before.productComplete, true);
  assert.equal(before.gatewayPending, true);
  const normalizeConsolePath = (path) => {
    const pathname = new URL(page.url()).pathname.replace(/\/$/, '');
    return pathname || '/';
  };
  const navigate = async (path) => {
    if (page.viewportSize().width < 768 && (await page.locator('aside.sidebar.open').count()) === 0)
      await page.locator('button.hamburger-container').click();
    const href = '/console' + (path || '/');
    const link =
      path === ''
        ? page.locator('aside.sidebar a[href="/console"], aside.sidebar a[href="/console/"]')
        : page.locator(`aside.sidebar a[href="${href}"]`);
    if (!(await link.isVisible())) {
      const section = path.startsWith('/ai-providers') || ['/accounts', '/oauth'].includes(path)
        ? 'resources'
        : path === '/api-keys' ? 'clients'
          : ['/monitoring', '/usage-analytics', '/model-prices'].includes(path) ? 'observe'
            : 'system';
      await page.locator(`aside.sidebar button[aria-controls="nav-${section}"]`).click();
    }
    await link.waitFor();
    await link.click();
    await wait(
      async () => normalizeConsolePath() === href.replace(/\/$/, ''),
      'navigation ' + path
    );
    const currentLink = page.locator('a[aria-current="page"]');
    await wait(
      async () =>
        (await currentLink.count()) === 1 &&
        (await currentLink.getAttribute('href'))?.replace(/\/$/, '') === href.replace(/\/$/, ''),
      'active navigation ' + path
    );
    assert.equal(
      (await currentLink.getAttribute('href'))?.replace(/\/$/, ''),
      href.replace(/\/$/, '')
    );
    assert((await page.locator('.app-breadcrumb').innerText()).length > 0);
  };
  for (const [name, width, light] of [
    ['desktop-dark', 1440, false],
    ['desktop-light', 1440, true],
    ['mobile-light', 390, true],
    ['mobile-dark', 390, false],
  ]) {
    await page.setViewportSize({ width, height: 1000 });
    const isLight = await page
      .locator('html')
      .evaluate((html) => html.getAttribute('data-theme') !== 'dark');
    if (isLight !== light) {
      await page.getByRole('button', { name: '主题', exact: true }).click();
      await page
        .getByRole('menuitemradio', { name: light ? '明亮' : '黑夜', exact: true })
        .click();
    }
    await navigate('');
    await screenshot('legacy-home-' + name);
    await navigate('/config');
    await page.locator('[role="tablist"]').first().waitFor();
    await screenshot('legacy-config-' + name);
    if (width < 768) {
      await page.locator('button.hamburger-container').click();
      await page.locator('aside.sidebar.open').waitFor();
      await screenshot('legacy-navigation-' + name);
      await page
        .locator('.sidebar-backdrop.visible')
        .click({ position: { x: page.viewportSize().width - 20, y: 200 } });
    } else {
      await page.locator('button.hamburger-container').click();
      await page.locator('aside.sidebar.collapsed').waitFor();
      await screenshot('legacy-navigation-collapsed-' + name);
      await page.locator('button.hamburger-container').click();
    }
    await navigate('/system');
    await page.locator('.main-content').waitFor();
    await screenshot('legacy-system-' + name);
  }
  await page.goBack();
  assert.equal(normalizeConsolePath(), '/console/config');
  await page.goForward();
  assert.equal(normalizeConsolePath(), '/console/system');
  pass(
    'existing CPAMP two-level navigation, breadcrumbs, history, desktop collapse and mobile drawer work in light/dark themes'
  );
  await page.getByRole('button', { name: '语言', exact: true }).click();
  await page.getByRole('menuitemradio', { name: 'English', exact: true }).click();
  await page.getByRole('button', { name: 'Language', exact: true }).click();
  await page.getByRole('menuitemradio', { name: '简体中文', exact: true }).click();
  await page.getByRole('button', { name: '语言', exact: true }).waitFor();
  pass(
    'language selection updates the existing Console and restores Chinese'
  );
  const storedSecrets = await page.evaluate((adminKey) =>
    [...Object.values(localStorage), ...Object.values(sessionStorage)].some((value) =>
      value.includes(adminKey)
    ), admin);
  assert.equal(storedSecrets, false);
  const closed = async () => {
    for (const path of [
      '/v1/models',
      '/v1/chat/completions',
      '/v1/responses',
      '/v1/messages',
      '/v1beta/models',
    ]) {
      for (const key of ['', 'incorrect-client-key']) {
        const response = await fetch(origin + path, {
          method: path.includes('models') ? 'GET' : 'POST',
          headers: key ? { Authorization: 'Bearer ' + key } : {},
        });
        assert.equal(response.status, 503);
        assert.equal((await response.text()).trim(), 'gateway pending configuration');
      }
    }
  };
  await closed();
  const peer = run([
    'docker',
    'run',
    '--rm',
    '--network',
    network,
    pythonImage,
    'python3',
    '-c',
    `import socket
s=socket.socket();s.settimeout(3)
try:s.connect(('cpamp-runtime',8317));raise SystemExit(1)
except OSError:pass`,
  ]);
  assert.equal(peer.status, 0);
  pass('zero Client Keys: all tested Gateway paths remain Pending; peer cannot reach CPA port');
  const keyDigest = dc('exec', '-T', 'cpamp-manager', 'sha256sum', '/data/data.key');
  dc('restart', 'cpamp-manager', 'cpamp-runtime', 'cpamp-ingress');
  await wait(
    async () => (await fetch(origin + '/console', { headers: { Accept: 'text/html' } })).ok,
    'restored Console'
  );
  await page.reload();
  await page.getByTestId('console-login').waitFor();
  await login(admin);
  await page.getByTestId('legacy-console').waitFor();
  await page.locator('.app-shell').waitFor();
  const after = await adminGet('operation');
  assert.deepEqual(after, before);
  assert.equal(dc('exec', '-T', 'cpamp-manager', 'sha256sum', '/data/data.key'), keyDigest);
  assert.equal((await fetch(origin + bootstrap + '/ui')).status, 404);
  await closed();
  // Runtime process generation changes across restart. Observe it but never
  // silently bind the original completed strategy to this new generation.
  const restartedStatusJSON = run(
    [
      'docker',
      'run',
      '--rm',
      '-i',
      '--network',
      network,
      pythonImage,
      'python3',
      '-c',
      statusProgram,
    ],
    runtimeToken
  ).stdout;
  const restartedStatus = JSON.parse(restartedStatusJSON);
  restartedStatus.runtimeGeneration = restartedStatusJSON.match(
    /"runtimeGeneration"\s*:\s*([0-9]+)/
  )[1];
  report.runtimeAfterRestart = {
    state: restartedStatus.state,
    generationChanged: restartedStatus.runtimeGeneration !== status.runtimeGeneration,
  };
  assert(report.runtimeAfterRestart.generationChanged);
  pass(
    'Manager/Runtime/Ingress restart preserves identity, completion, routing, data key and saved setting; Admin relogin works'
  );
  const mismatch = await fetch(origin + '/console/api/v2/strategy', {
    method: 'POST',
    headers: { Authorization: 'Bearer ' + admin, 'Content-Type': 'application/json' },
    body: JSON.stringify({
      identity: after.identity,
      expectedRevision: after.revision,
      strategy: 'new_managed',
      expectedRuntimeIdentity: status.runtimeIdentity,
      expectedRuntimeGeneration: status.runtimeGeneration,
      expectedArtifactId: status.activeGatewayArtifact.artifactId,
    }).replace(/("expectedRuntimeGeneration":)"([0-9]+)"/, '$1$2'),
  });
  assert.equal(mismatch.status, 409);
  assert.equal((await mismatch.json()).Code, 'managed_target_identity_mismatch');
  assert.deepEqual(await adminGet('operation'), before);
  pass(
    'stale Runtime generation returns 409 managed_target_identity_mismatch without rebinding or retry'
  );
  await page.getByRole('button', { name: '登出', exact: true }).click();
  await page.getByTestId('console-login').waitFor();
  await page.getByText('记住凭证', { exact: true }).click();
  assert(await page.getByRole('checkbox', { name: '记住凭证', exact: true }).isChecked());
  await login(admin);
  await page.getByTestId('legacy-console').waitFor();
  await navigate('/config');
  await page.reload();
  await page.getByTestId('legacy-console').waitFor();
  assert.equal(new URL(page.url()).pathname, '/console/config');
  assert.equal(await page.getByTestId('console-login').count(), 0);
  assert.deepEqual(await adminGet('operation'), before);
  await screenshot('remembered-console-after-refresh');
  pass('remembered login revalidates the real Admin Key after refresh and restores the original deep route');
  await page.getByRole('button', { name: '登出', exact: true }).click();
  await page.getByTestId('console-login').waitFor();
  await page.reload();
  await page.getByTestId('console-login').waitFor();
  assert.equal(await page.getByTestId('legacy-console').count(), 0);
  if (await page.getByRole('checkbox', { name: '记住凭证', exact: true }).isChecked())
    await page.getByText('记住凭证', { exact: true }).click();
  await login(admin);
  await page.getByTestId('legacy-console').waitFor();
  pass('explicit logout clears remembered authority; refresh stays on the original login page');
  const recoverProgram = `import urllib.request,json,sys,time
token=sys.stdin.read().strip(); headers={'Authorization':'Bearer '+token,'X-CPAMP-Runtime-Features':'active-gateway-artifact-v1'}
opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
with opener.open(urllib.request.Request('http://cpamp-runtime:9081/v1/runtime/status',headers=headers),timeout=10) as r:s=json.load(r)
assert s['state']=='offline'
body={'operationId':'host-recovery:'+str(s['runtimeGeneration']),'expectedRuntimeIdentity':s['runtimeIdentity'],'expectedRuntimeGeneration':s['runtimeGeneration'],'expectedActiveArtifactId':s['activeGatewayArtifact']['artifactId']}
headers['Content-Type']='application/json'
with opener.open(urllib.request.Request('http://cpamp-runtime:9081/v1/runtime/operations/start',data=json.dumps(body).encode(),headers=headers),timeout=30) as r:result=json.load(r)
assert result['state']=='succeeded'
for _ in range(120):
 with opener.open(urllib.request.Request('http://cpamp-runtime:9081/v1/runtime/status',headers=headers),timeout=10) as r:state=json.load(r)['state']
 if state=='ready':break
 time.sleep(0.25)
assert state=='ready'
print('ready')`;
  assert.equal(
    run(
      [
        'docker',
        'run',
        '--rm',
        '-i',
        '--network',
        network,
        pythonImage,
        'python3',
        '-c',
        recoverProgram,
      ],
      runtimeToken
    ).stdout.trim(),
    'ready'
  );
  assert.deepEqual(await adminGet('operation'), before);
  await closed();
  pass(
    'explicit Host lifecycle Start restores the new Runtime generation without rewriting installation provenance'
  );
  dc('stop', 'cpamp-manager');
  const persistentIdentityFiles = () => {
    const files = JSON.parse(snapshot(managerVolume));
    delete files['/data/usage.sqlite'];
    return files;
  };
  const settingsDigest = () =>
    python(
      managerVolume,
      `import sqlite3,hashlib,json,os
# Manager is stopped and the cold snapshot has no WAL. immutable avoids creating
# SQLite WAL bookkeeping on the read-only evidence mount.
assert not os.path.exists('/data/usage.sqlite-wal')
c=sqlite3.connect('file:/data/usage.sqlite?mode=ro&immutable=1',uri=True)
rows=c.execute('select key,hex(value) from settings order by key').fetchall()
print(hashlib.sha256(json.dumps(rows,ensure_ascii=False).encode()).hexdigest())`,
      undefined,
      true
    );
  const beforeRepeat = persistentIdentityFiles();
  const beforeSettings = settingsDigest();
  const repeat = run(
    [...compose, 'run', '--rm', '--no-deps', 'cpamp-manager', 'initialize'],
    undefined,
    false
  );
  assert.notEqual(repeat.status, 0);
  assert.deepEqual(persistentIdentityFiles(), beforeRepeat);
  assert.equal(settingsDigest(), beforeSettings);
  pass(
    'completed installation rejects repeated initialize; admin/authority/settings and identity/key files unchanged'
  );
  for (const kind of ['empty-existing-db', 'unknown-db', 'legacy-settings', 'incomplete-marker']) {
    const volume = project + '-' + kind;
    docker('volume', 'create', volume);
    helperVolumes.push(volume);
    python(
      volume,
      `import sqlite3,os
kind=${JSON.stringify(kind)}
if kind=='empty-existing-db':open('/data/usage.sqlite','wb').close()
elif kind=='incomplete-marker':open('/data/usage.sqlite.manager.lock','w').write('{broken-fresh-marker')
else:
 c=sqlite3.connect('/data/usage.sqlite')
 if kind=='unknown-db':c.execute('create table unknown_product(value text)');c.execute("insert into unknown_product values('preserve')")
 else:c.execute('create table settings(key text primary key,value text not null)');c.execute("insert into settings values('setup','{}')")
 c.commit();c.close()`
    );
    const old = snapshot(volume);
    const rejected = run(
      [
        'docker',
        'run',
        '--rm',
        '--network',
        'none',
        '-v',
        volume + ':/data',
        images['cpamp-manager'],
        'initialize',
      ],
      undefined,
      false
    );
    assert.notEqual(rejected.status, 0);
    assert.equal(snapshot(volume), old);
    pass(kind + ' cannot be admitted as Fresh; source bytes and modes unchanged');
  }
  const logs = dc('logs', '--no-color');
  assert(!secretValues.some((value) => logs.includes(value)), 'secrets appeared in service logs');
  assert.equal(report.pageErrors.length, 0);
  pass('no browser storage, uncaught browser errors or secrets in service logs');
  report.result = 'PASS';
} catch (err) {
  if (taskNetwork) {
    const probe = `import urllib.request,urllib.error,json
o=urllib.request.build_opener(urllib.request.ProxyHandler({}));v={}
for u in ['http://cpamp-manager:18317/internal/routing/fresh-authority','http://cpamp-ingress:9082/internal/routing/fresh','http://cpamp-manager:18317/console','http://cpamp-ingress:18317/console']:
 try:
  try:r=o.open(urllib.request.Request(u,headers={'Accept':'text/html' if u.endswith('/console') else 'application/json'}),timeout=3)
  except urllib.error.HTTPError as e:r=e
  v[u]=[r.status,r.read(512).decode(errors='replace')]
 except Exception as e:v[u]=type(e).__name__
print(json.dumps(v))`;
    try {
      report.failureProbe = JSON.parse(
        run([
          'docker',
          'run',
          '--rm',
          '--network',
          taskNetwork,
          pythonImage,
          'python3',
          '-c',
          probe,
        ]).stdout
      );
    } catch {}
  }
  try {
    report.failureServiceLogs = secretValues.reduce(
      (logs, value) => logs.replaceAll(value, '[REDACTED]'),
      dc('logs', '--no-color', '--tail', '60')
    );
  } catch {}
  if (page && !page.isClosed()) {
    report.failurePage = await page
      .locator('body')
      .innerText()
      .catch(() => 'Page unavailable');
    await page
      .screenshot({
        path: resolve(out, 'failure.png'),
        fullPage: true,
        mask: [page.locator('input, textarea')],
      })
      .catch(() => {});
  }
  report.failure = secretValues.reduce(
    (text, value) => text.replaceAll(value, '[REDACTED]'),
    String(err.stack || err)
  );
  process.exitCode = 1;
} finally {
  if (browser) await browser.close();
  const cleanup = run([...compose, 'down', '-v', '--remove-orphans'], undefined, false);
  report.cleanup = cleanup.status === 0 ? 'PASS' : 'FAIL';
  for (const volume of helperVolumes) run(['docker', 'volume', 'rm', volume], undefined, false);
  writeFileSync(resolve(out, 'report.json'), JSON.stringify(report, null, 2), { mode: 0o600 });
  console.log(report.result + ': ' + resolve(out, 'report.json'));
  if (report.failure) console.error(report.failure);
}
