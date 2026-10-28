#!/usr/bin/env node
// PAH G2 real-browser isolation proof for CPA plugin pages.
//
// This drives a running Manager. It creates no container and installs no
// plugin: the caller supplies a deployment that already has an admin session
// and one enabled plugin contributing the fixture resources documented in
// bin/ci/README-plugin-isolation.md.
//
//   MANAGER_ORIGIN   origin of a Manager serving Full Web, e.g. http://127.0.0.1:19319
//   ADMIN_KEY        the Manager Admin Key the Console logs in with
//   PLUGIN_ID        id of the enabled fixture plugin (isolation-probe)
//   CONSOLE_BASE     verified Control base path, default /console
//   PLAYWRIGHT_MODULE (optional) path to the `playwright` package
//
// Every negative below MUST fail to escape. A single escape is a build failure.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { mkdirSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';

const origin = (process.env.MANAGER_ORIGIN || '').replace(/\/+$/, '');
const adminKey = process.env.ADMIN_KEY || '';
const pluginId = process.env.PLUGIN_ID || '';
const consoleBase = process.env.CONSOLE_BASE || '/console';
assert(origin && adminKey && pluginId, 'MANAGER_ORIGIN, ADMIN_KEY and PLUGIN_ID are required');

const { chromium, webkit } = createRequire(import.meta.url)(
  process.env.PLAYWRIGHT_MODULE || 'playwright'
);
const out = resolve(process.env.G2_OUTPUT_DIR || '/tmp/cpamp-g2-output');
mkdirSync(out, { recursive: true, mode: 0o700 });

// Full Web serves the Console under the verified Control base path and its
// public admin API at <base>/api/v2. The legacy CPAMP contracts the Console
// consumes are bridged below that same namespace (`cpamp/` prefix), so this is
// the only API base a browser request can use.
const consoleRoot = `${origin}${consoleBase}`;
const apiBase = `${consoleRoot}/api/v2`;
const resourceBase = `${origin}/v0/resource/plugins/${pluginId}`;
const themeMessageType = 'cpamp:plugin-theme';

const report = {
  result: 'FAIL',
  origin,
  consoleBase,
  pluginId,
  engines: {},
  checks: [],
  consoleErrors: [],
  notRun: [],
};

function record(engine, name, result, detail) {
  report.checks.push({ engine, name, result, ...(detail === undefined ? {} : { detail }) });
  console.log(`${result} ${engine} ${name}${detail === undefined ? '' : ' — ' + JSON.stringify(detail)}`);
}

// The admin key the run logged in with. It is memory-only in the Console, so
// the check that matters is that it never becomes readable from storage.
const adminCredential = adminKey;
const adminStorageKeys = [];
const adminCookies = [];

async function login(page) {
  // The Control base path is served at its own exact path; a trailing slash is
  // not a canonical request path and is refused, so navigation targets it bare.
  await page.goto(consoleRoot, { waitUntil: 'domcontentloaded' });
  // Full Web renders the Console login adapter with a stable test id and the
  // admin key field it declares for the console surface.
  const form = page.getByTestId('console-login');
  await form.waitFor({ state: 'visible', timeout: 60000 });
  await page.locator('#console-admin-key').fill(adminKey);
  await form.locator('button[type=submit]').click();
  await page.getByTestId('legacy-console').waitFor({ timeout: 60000 });
  await page.locator('.app-shell').first().waitFor({ timeout: 60000 });
}

// The Console's Full Web credential lives only in memory, so a top-level
// navigation reloads the document and lands back on the login form. Every admin
// page after login must therefore be entered through the app's own router: the
// sidebar links carry the real routes and keep the session alive.
//
// Some routes redirect (`/api-keys` and `/settings` both land on `/config`), so
// the resolved path is reported rather than asserted against the request.
async function expandNavGroups(page) {
  const toggles = page.locator('.app-shell button.nav-section-toggle');
  const count = await toggles.count();
  for (let index = 0; index < count; index += 1) {
    const toggle = toggles.nth(index);
    if ((await toggle.getAttribute('aria-expanded')) !== 'true') {
      await toggle.click().catch(() => {});
    }
  }
}

async function navigateInApp(page, path) {
  // The dashboard is the Control base itself, not a child route.
  const target = path === '/dashboard' ? consoleBase : `${consoleBase}${path}`;
  let link = page.locator(`.app-shell a.nav-item[href="${target}"]`).first();
  if ((await link.count()) === 0) {
    // Sidebar children only render once their group is open.
    await expandNavGroups(page);
    link = page.locator(`.app-shell a.nav-item[href="${target}"]`).first();
  }
  assert((await link.count()) > 0, `${path} is not reachable from the Console sidebar`);
  await link.click();
  await page.waitForFunction(
    (expected) => window.location.pathname === expected,
    target,
    { timeout: 60000 }
  );
  const settled = new URL(page.url()).pathname;
  assert.equal(settled, target, `in-app navigation settled on ${settled}`);
  return { path, settled };
}

// The plugin page's sidebar entry is contributed by the plugin itself, so its
// label and href come from the plugin list rather than a fixed route table. It
// sits inside the "Plugins" nav group, whose children only render once the
// group is open, so the group is opened the way an operator opens it.
async function openPluginPageFromSidebar(page, pluginId, menuIndex, expectedLabel) {
  const route = `/plugin-pages/${encodeURIComponent(pluginId)}/${menuIndex}`;
  const href = `${consoleBase}${route}`;
  let link = page.locator(`.app-shell a.nav-item[href="${href}"]`).first();
  if ((await link.count()) === 0) {
    const group = page
      .locator('.app-shell .nav-item[aria-expanded], .app-shell .nav-item')
      .filter({ hasText: /Plugins|插件/i })
      .first();
    await group.waitFor({ state: 'visible', timeout: 60000 });
    await group.click();
    link = page.locator(`.app-shell a.nav-item[href="${href}"]`).first();
  }
  await link.waitFor({ state: 'visible', timeout: 60000 });
  const label = (await link.innerText()).trim();
  assert(
    label.includes(expectedLabel),
    `plugin sidebar entry label ${JSON.stringify(label)} does not name ${expectedLabel}`
  );
  await link.click();
  await page.waitForURL((url) => url.pathname === href, { timeout: 60000 });
  return { href, label };
}

async function snapshotAdminAuthority(context, page) {
  // The Console keeps its session under the real storage key. The value is
  // reversible obfuscation, which is exactly why the sandbox must make it
  // unreachable: this proof asserts reachability, not secrecy of the bytes.
  const storage = await page.evaluate(() =>
    Object.fromEntries(
      [['local', localStorage], ['session', sessionStorage]].map(([kind, store]) => [
        kind,
        Object.keys(store).map((key) => [key, store.getItem(key)]),
      ])
    )
  );
  for (const [kind, entries] of Object.entries(storage)) {
    for (const [key, value] of entries) adminStorageKeys.push({ kind, key, value });
  }
  adminCookies.push(...(await context.cookies()));
  assert(
    adminStorageKeys.some((entry) => entry.key === 'cli-proxy-auth'),
    'Console did not persist cli-proxy-auth; login did not complete'
  );
}

// Runs inside the plugin document.
async function probe(target) {
  return target.evaluate(async () => {
    const result = { storage: {}, network: {}, dom: {} };
    for (const name of ['localStorage', 'sessionStorage']) {
      try {
        const value = window[name];
        result.storage[name] = { reachable: true, length: value.length };
      } catch (error) {
        result.storage[name] = { reachable: false, error: (error && error.name) || 'Error' };
      }
    }
    // A top-level plugin document has no parent, so `parent === window` and
    // reading its title says nothing about containment. Only a framed document
    // reaches across an origin boundary, so the check is scoped to that case.
    result.dom.framed = window.parent !== window;
    try {
      result.dom.parentTitle = { reachable: true, value: String(parent.document.title) };
    } catch (error) {
      result.dom.parentTitle = { reachable: false, error: (error && error.name) || 'Error' };
    }
    try {
      result.dom.cookie = { reachable: true, value: String(document.cookie) };
    } catch (error) {
      result.dom.cookie = { reachable: false, error: (error && error.name) || 'Error' };
    }
    result.dom.origin = String(location.origin);
    // Any admin API path, with and without credentials.
    for (const path of ['/v0/management/plugins', '/v0/management/config.yaml']) {
      for (const credentials of ['omit', 'include']) {
        try {
          const response = await fetch(path, { credentials, method: 'GET' });
          const body = await response.text();
          result.network[`${path}#${credentials}`] = {
            status: response.status,
            ok: response.ok,
            bytes: body.length,
          };
        } catch (error) {
          result.network[`${path}#${credentials}`] = {
            ok: false,
            error: (error && error.name) || 'Error',
          };
        }
      }
    }
    return result;
  });
}

function assertContained(engine, where, result) {
  for (const [name, value] of Object.entries(result.storage)) {
    assert.equal(
      value.reachable && value.length > 0,
      false,
      `${engine} ${where}: plugin read ${name} (${JSON.stringify(value)})`
    );
  }
  record(engine, `${where}: storage unreachable`, 'PASS');
  if (result.dom.framed) {
    assert.equal(
      result.dom.parentTitle.reachable,
      false,
      `${engine} ${where}: plugin reached parent.document (${JSON.stringify(result.dom.parentTitle)})`
    );
    record(engine, `${where}: parent.document unreachable`, 'PASS');
  } else {
    // Nothing embeds this document, so there is no parent to reach. The opaque
    // origin is still proven by the empty storage and cookie jar above.
    record(engine, `${where}: top level, no parent document to reach`, 'PASS');
  }
  assert.equal(
    Boolean(result.dom.cookie.reachable && result.dom.cookie.value),
    false,
    `${engine} ${where}: plugin read a cookie (${JSON.stringify(result.dom.cookie)})`
  );
  record(engine, `${where}: cookie jar empty`, 'PASS');
  for (const [target, value] of Object.entries(result.network)) {
    assert.equal(
      value.ok,
      false,
      `${engine} ${where}: plugin fetch succeeded for ${target} (${JSON.stringify(value)})`
    );
  }
  record(engine, `${where}: admin API fetches refused`, 'PASS', Object.keys(result.network));
}

async function adminList(page) {
  // Confirm the plugin is enabled and read the menu the Console routes to,
  // using the Console's own credential against the real bridge path.
  const response = await page.evaluate(
    async ({ base, key }) => {
      const call = await fetch(`${base}/cpamp/v0/management/plugins`, {
        headers: { Authorization: `Bearer ${key}` },
      });
      return { status: call.status, body: await call.text() };
    },
    { base: apiBase, key: adminKey }
  );
  assert.equal(response.status, 200, 'plugin list through the admin session failed');
  const parsed = JSON.parse(response.body);
  report.pluginsEnabled = parsed.plugins_enabled;
  assert.equal(parsed.plugins_enabled, true, 'plugins.enabled is false on this deployment');
  const entry = (parsed.plugins || []).find((item) => item.id === pluginId);
  assert(entry, `plugin ${pluginId} is not registered`);
  assert.equal(entry.effective_enabled, true, `plugin ${pluginId} is not effectively enabled`);
  const menuIndex = (entry.menus || []).findIndex((menu) => menu.path.includes('/probe'));
  assert(menuIndex >= 0, `plugin ${pluginId} exposes no probe menu`);
  report.pluginLabel = (entry.menus[menuIndex].menu || '').trim();
  assert(report.pluginLabel, `plugin ${pluginId} menu has no label to find in the sidebar`);
  return { entry, menuIndex };
}

async function runEngine(engineName, launcher) {
  const browser = await launcher.launch({ headless: true });
  const engineReport = { checks: [] };
  report.engines[engineName] = engineReport;
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    page.on('console', (message) => {
      if (message.type() === 'error')
        report.consoleErrors.push({ engine: engineName, text: message.text() });
    });
    await login(page);
    await snapshotAdminAuthority(context, page);
    record(engineName, 'Console login with cli-proxy-auth persisted', 'PASS');

    const { menuIndex } = await adminList(page);
    record(engineName, 'plugin list through the admin bridge', 'PASS', { pluginId, menuIndex });

    // ---- Positive: the Console renders the plugin menu in a sandbox -------
    // Clicked from the sidebar the plugin contributes, not by loading the URL:
    // that is how an operator reaches it and it keeps the session alive.
    const sidebarEntry = await openPluginPageFromSidebar(
      page,
      pluginId,
      menuIndex,
      report.pluginLabel || pluginId
    );
    record(engineName, 'plugin page opened from the in-app sidebar entry', 'PASS', sidebarEntry);
    const iframe = page.locator('iframe').first();
    await iframe.waitFor({ timeout: 60000 });
    const sandboxAttr = (await iframe.getAttribute('sandbox')) || '';
    assert.equal(sandboxAttr.trim(), 'allow-scripts allow-forms', `unexpected iframe sandbox: ${sandboxAttr}`);
    const frameSrc = (await iframe.getAttribute('src')) || '';
    assert(
      frameSrc.startsWith(resourceBase + '/'),
      `iframe src is outside the plugin prefix: ${frameSrc}`
    );
    record(engineName, 'Console frames the plugin page with allow-scripts allow-forms', 'PASS', {
      src: frameSrc,
    });

    const pluginFrame = () =>
      page.frames().find((frame) => frame.url().startsWith(resourceBase));
    const frame = pluginFrame();
    assert(frame, 'plugin document never loaded');
    await frame.locator('#probe-ready').waitFor({ timeout: 60000 });
    record(engineName, 'plugin page renders inside the Console', 'PASS');

    // ---- Positive: the plugin reads its own resource ----------------------
    const ownFetch = await frame.evaluate(async (url) => {
      const response = await fetch(url, { credentials: 'omit' });
      return { status: response.status, ok: response.ok, bytes: (await response.text()).length };
    }, `${resourceBase}/probe.js`);
    assert(ownFetch.ok && ownFetch.bytes > 0, `plugin could not read its own resource: ${JSON.stringify(ownFetch)}`);
    record(engineName, 'plugin fetches its own resource', 'PASS', ownFetch);

    // ---- Positive: the host theme arrives over postMessage ----------------
    // The host posts the theme as soon as the frame finishes a navigation, so a
    // listener attached after the fact misses it. Record every message from
    // document start instead, then reload the frame to make the host post again.
    // Only the host can produce this message.
    await page.addInitScript((type) => {
      window.__cpampHostThemeMessages = [];
      addEventListener('message', (event) => {
        const data = event.data;
        if (data && data.type === type) window.__cpampHostThemeMessages.push(data);
      });
    }, themeMessageType);
    const readThemes = () =>
      frame.evaluate(() =>
        (window.__cpampHostThemeMessages || []).map((data) => ({
          version: data.version,
          themeAttribute: data.themeAttribute,
          variables: Object.keys(data.variables || {}).length,
        }))
      );
    const waitForTheme = async () => {
      for (let attempt = 0; attempt < 40; attempt += 1) {
        const seen = await readThemes();
        if (seen.length > 0) return seen[seen.length - 1];
        await page.waitForTimeout(250);
      }
      return null;
    };
    await frame.goto(`${resourceBase}/probe`, { waitUntil: 'domcontentloaded' });
    await frame.locator('#probe-ready').waitFor({ timeout: 60000 });
    const theme = await waitForTheme();
    assert(theme, 'plugin never received the host theme postMessage');
    assert(
      theme.themeAttribute === 'dark' || theme.themeAttribute === 'white',
      `host theme message has no resolved attribute: ${JSON.stringify(theme)}`
    );
    assert(theme.variables > 0, `host theme message carried no variables: ${JSON.stringify(theme)}`);
    record(engineName, 'plugin receives the host theme postMessage', 'PASS', theme);

    // ---- Negatives inside the Console iframe ------------------------------
    assertContained(engineName, 'iframe', await probe(frame));

    // ---- Negatives after a plugin refresh --------------------------------
    await frame.goto(`${resourceBase}/probe`, { waitUntil: 'domcontentloaded' });
    assertContained(engineName, 'iframe-after-refresh', await probe(frame));

    // ---- Negatives on a direct open and in a new tab ----------------------
    const direct = await context.newPage();
    await direct.goto(`${resourceBase}/probe`, { waitUntil: 'domcontentloaded' });
    assertContained(engineName, 'direct', await probe(direct));
    const tab = await context.newPage();
    await tab.goto(`${resourceBase}/probe`, { waitUntil: 'domcontentloaded' });
    assertContained(engineName, 'new-tab', await probe(tab));

    // ---- Negative: a Control document cannot load plugin code ------------
    await navigateInApp(page, '/dashboard');
    await page.waitForLoadState('networkidle');
    const injections = await page.evaluate(async (src) => {
      const results = {};
      const settle = (name, promise) =>
        promise.then(
          () => {
            results[name] = { loaded: true };
          },
          (error) => {
            results[name] = { loaded: false, error: (error && error.name) || 'Error' };
          }
        );
      await settle(
        'script',
        new Promise((ok, fail) => {
          const element = document.createElement('script');
          element.src = src + '.js';
          element.onload = ok;
          element.onerror = fail;
          document.head.appendChild(element);
        })
      );
      await settle('import', import(src + '.js'));
      await settle(
        'worker',
        new Promise((ok, fail) => {
          const worker = new Worker(src + '.js');
          worker.onerror = fail;
          setTimeout(ok, 1500);
        })
      );
      await settle(
        'svg-use',
        new Promise((ok, fail) => {
          const svg = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
          const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
          use.setAttribute('href', src + '.svg#shape');
          use.onload = ok;
          use.onerror = fail;
          svg.appendChild(use);
          document.body.appendChild(svg);
          setTimeout(ok, 1500);
        })
      );
      await settle(
        'svg-image',
        new Promise((ok, fail) => {
          const image = document.createElementNS('http://www.w3.org/2000/svg', 'image');
          image.setAttribute('href', src + '.svg');
          image.onload = ok;
          image.onerror = fail;
          document.body.appendChild(image);
          setTimeout(ok, 1500);
        })
      );
      return results;
    }, resourceBase);
    for (const [kind, value] of Object.entries(injections)) {
      assert.equal(
        value.loaded,
        false,
        `${engineName}: the admin page loaded a plugin resource as ${kind} (${JSON.stringify(value)})`
      );
    }
    record(
      engineName,
      'admin page cannot load plugin paths (script/import/Worker/SVG use+image)',
      'PASS',
      Object.keys(injections)
    );

    // ---- Negative: redirects and cookies, asserted on raw responses ------
    // A plugin page's own requests arrive cross-site from the opaque origin,
    // so the sandbox shape is what the Fetch Metadata policy admits.
    const sandboxHeaders = { 'Sec-Fetch-Site': 'cross-site', 'Sec-Fetch-Dest': 'empty' };
    const escaped = await context.request.get(`${resourceBase}/redirect-admin`, {
      maxRedirects: 0,
      headers: sandboxHeaders,
    });
    assert(
      escaped.status() !== 302 && escaped.status() !== 307 && escaped.status() !== 308,
      `plugin escaped its prefix via ${escaped.status()}`
    );
    record(engineName, 'out-of-prefix redirect refused', 'PASS', { status: escaped.status() });

    const inside = await context.request.get(`${resourceBase}/redirect-self`, {
      maxRedirects: 0,
      headers: sandboxHeaders,
    });
    // The plugin answers a relative target and the proxy preserves it as sent,
    // so the confinement check is on where the browser resolves it: a relative
    // Location keeps its own directory, an absolute one must already be inside
    // the prefix.
    const location = inside.headers()['location'] || '';
    const resolved = new URL(location, `${resourceBase}/redirect-self`).href;
    assert(
      resolved.startsWith(resourceBase + '/'),
      `in-prefix redirect resolved outside the prefix: ${location} -> ${resolved}`
    );
    record(engineName, 'in-prefix redirect preserved', 'PASS', { status: inside.status(), location });

    // ---- The resource namespace is read-only -----------------------------
    // CPA serves a pluginapi.ResourceRoute only for GET (it is the plugin's own
    // page), and a plugin that needs input posts to a Management route instead.
    // Both edges must therefore refuse a write with 405 rather than forwarding
    // it, and the refusal must not leak whether the path exists.
    const writeBody = JSON.stringify({ refresh: true, from: 'g2' });
    for (const method of ['POST', 'PUT', 'PATCH', 'DELETE']) {
      const refused = await context.request.fetch(`${resourceBase}/probe`, {
        method,
        headers: { ...sandboxHeaders, 'Content-Type': 'application/json' },
        data: writeBody,
        failOnStatusCode: false,
      });
      assert.equal(refused.status(), 405, `${method} on a plugin resource returned ${refused.status()}`);
      // The refusal comes before any upstream call, so it carries no document.
      const body = await refused.text();
      assert.equal(body, '', `${method} refusal leaked a body: ${body.slice(0, 80)}`);
      record(engineName, `${method} on a plugin resource is 405`, 'PASS', { status: refused.status() });
    }

    // A write with no Fetch Metadata is refused earlier still: the method guard
    // must not become a way around the metadata policy.
    const writeWithoutMetadata = await context.request.fetch(`${resourceBase}/probe`, {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      data: writeBody,
      failOnStatusCode: false,
    });
    assert(
      writeWithoutMetadata.status() === 405 || writeWithoutMetadata.status() === 403,
      `write without Fetch Metadata returned ${writeWithoutMetadata.status()}`
    );
    record(engineName, 'write without Fetch Metadata is refused', 'PASS', {
      status: writeWithoutMetadata.status(),
    });

    // ---- Redirect targets a browser resolves differently than Go -----------
    for (const [name, path] of [
      ['backslash traversal', 'redirect-backslash'],
      ['backslash protocol-relative', 'redirect-protocol-relative'],
    ]) {
      const response = await context.request.get(`${resourceBase}/${path}`, {
        maxRedirects: 0,
        headers: sandboxHeaders,
      });
      assert(
        response.status() !== 302 && response.status() !== 307 && response.status() !== 308,
        `${name} redirect was admitted with ${response.status()}`
      );
      const location = response.headers()['location'] || '';
      assert(!location.includes('\\'), `${name} redirect kept its backslash target: ${location}`);
      record(engineName, `${name} redirect refused`, 'PASS', { status: response.status() });
    }

    // ---- Origin-wide response headers the plugin must not set --------------
    const hostile = await context.request.get(`${resourceBase}/hostile-headers`, {
      headers: sandboxHeaders,
    });
    const hostileHeaders = hostile.headers();
    const dropped = [
      'clear-site-data',
      'nel',
      'report-to',
      'reporting-endpoints',
      'strict-transport-security',
      'alt-svc',
      'access-control-allow-credentials',
    ].filter((name) => hostileHeaders[name] !== undefined);
    assert.equal(dropped.length, 0, `origin-wide headers survived: ${dropped.join(', ')}`);
    assert.equal(hostileHeaders['content-type'], 'text/plain; charset=utf-8', 'representation header was dropped');
    record(engineName, 'origin-wide plugin response headers dropped', 'PASS', { checked: 7 });

    const cookieResponse = await context.request.get(`${resourceBase}/cookie`, {
      headers: sandboxHeaders,
    });
    assert.equal(cookieResponse.headers()['set-cookie'], undefined, 'plugin Set-Cookie survived');
    assert.equal(
      (await context.cookies()).some((cookie) => cookie.name.startsWith('plugin_probe')),
      false,
      'plugin cookie was planted in the admin context'
    );
    record(engineName, 'plugin Set-Cookie stripped', 'PASS');

    // ---- Acceptance 5: a client without Fetch Metadata is refused --------
    const noMetadata = await context.request.get(`${resourceBase}/probe`, { headers: {} });
    assert.equal(noMetadata.status(), 403, `plugin resource without Fetch Metadata returned ${noMetadata.status()}`);
    record(engineName, 'plugin resource without Fetch Metadata is 403', 'PASS');

    // ---- The plugin must not have replaced the admin credential ----------
    // The Console keeps the admin key in memory only; its storage entry holds
    // server metadata (`apiBase`, `serverVersion`) that it rewrites as the page
    // resolves the server, so byte equality says nothing and changes with or
    // without a plugin. The invariant that matters is that the key itself never
    // lands in storage where the sandboxed plugin could have influenced it, so
    // assert exactly that, plus that nothing else stored the key.
    assert(adminCredential, 'admin credential was never captured');
    const state = await context.storageState();
    const local = state.origins.find((item) => item.origin === origin)?.localStorage ?? [];
    for (const entry of local) {
      assert(
        !entry.value.includes(adminCredential),
        `admin credential appeared in localStorage key ${entry.name}`
      );
    }
    assert.equal(
      state.cookies.filter((cookie) => cookie.value.includes(adminCredential)).length,
      0,
      'admin credential appeared in a cookie'
    );
    record(engineName, 'admin credential unchanged after plugin interaction', 'PASS');

    await page.screenshot({ path: resolve(out, engineName + '-console.png'), fullPage: true });
    await context.close();
  } finally {
    await browser.close();
  }
}

async function adminCspCheck(engineName, launcher) {
  const browser = await launcher.launch({ headless: true });
  try {
    const context = await browser.newContext({ viewport: { width: 1440, height: 1000 } });
    const page = await context.newPage();
    const violations = [];
    await page.addInitScript(() => {
      addEventListener('securitypolicyviolation', (event) => {
        (window.__cspViolations = window.__cspViolations || []).push({
          directive: event.violatedDirective,
          blocked: event.blockedURI,
        });
      });
    });
    const errors = [];
    page.on('console', (message) => {
      if (message.type() === 'error') errors.push(message.text());
    });

    // The admin document policy is nonce-based; a served document must carry it.
    const documentResponse = await page.goto(consoleRoot, { waitUntil: 'networkidle' });
    const policy = (await documentResponse.allHeaders())['content-security-policy'] || '';
    assert(/script-src 'nonce-/.test(policy), `admin document CSP is not nonce-scoped: ${policy}`);
    assert(/frame-src 'self'/.test(policy), `admin document CSP does not confine frames: ${policy}`);
    record(engineName, 'admin document carries the nonce CSP', 'PASS', { policy });

    await login(page);
    // Every page the Console routes to. The list is the real route table, not a
    // sample: a CSP regression on one page is the same defect wherever it lands.
    // These are the routes the Console sidebar exposes; groups are expanded as
    // each route is visited, so the sweep walks what an operator can open.
    const adminPages = [
      '/dashboard',
      '/accounts',
      '/ai-providers',
      '/oauth',
      '/api-keys',
      '/model-prices',
      '/system',
      '/system/runtime',
      '/config',
      '/logs',
      '/system/updates',
      '/plugins',
    ];
    const visited = [];
    for (const path of adminPages) {
      const landed = await navigateInApp(page, path);
      await page.waitForLoadState('networkidle');
      // A sweep that silently stayed on one page would prove nothing, so each
      // step asserts it really landed where it asked to go.
      const expected = path === '/dashboard' ? consoleBase : `${consoleBase}${path}`;
      assert.equal(landed.settled, expected, `sweep did not reach ${path}`);
      visited.push(landed.settled);
      violations.push(
        ...(await page.evaluate(() => {
          const found = window.__cspViolations || [];
          // In-app routing keeps one document, so the buffer is drained per
          // page: an entry then names the page it happened on.
          window.__cspViolations = [];
          return found;
        })).map((item) => ({ path, ...item }))
      );
      assert.equal(
        (await page.evaluate(() => (document.body?.innerText || '').trim().length > 0)),
        true,
        `${engineName} ${path}: no visible content under CSP`
      );
    }
    assert.deepEqual(violations, [], `${engineName}: CSP violations ${JSON.stringify(violations)}`);
    const cspErrors = errors.filter((text) => /Content Security Policy|Refused to/i.test(text));
    assert.deepEqual(cspErrors, [], `${engineName}: CSP console errors ${JSON.stringify(cspErrors)}`);
    record(engineName, 'admin CSP: zero violations across Console pages', 'PASS', {
      pages: visited,
    });
    await context.close();
  } finally {
    await browser.close();
  }
}

try {
  for (const [engineName, launcher] of [['chromium', chromium], ['webkit', webkit]]) {
    await runEngine(engineName, launcher);
    await adminCspCheck(engineName, launcher);
  }
  report.result = 'PASS';
} catch (error) {
  report.error = String(error && error.message ? error.message : error);
  console.error('FAIL ' + report.error);
} finally {
  report.summary = report.checks.reduce((acc, check) => {
    acc[check.result] = (acc[check.result] || 0) + 1;
    return acc;
  }, {});
  writeFileSync(resolve(out, 'report.json'), JSON.stringify(report, null, 2));
  console.log(JSON.stringify(report.summary));
}
process.exitCode = report.result === 'PASS' ? 0 : 1;
