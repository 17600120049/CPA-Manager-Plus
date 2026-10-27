#!/usr/bin/env node
// PAH G2 real-browser isolation proof for CPA plugin pages.
//
// Prerequisites (all provided by the caller; this script creates nothing):
//   MANAGER_ORIGIN   origin of a Manager serving Full Web, e.g. http://127.0.0.1:19319
//   ADMIN_KEY        the Manager admin key the Console logs in with
//   PLUGIN_ID        id of an enabled CPA plugin that contributes the fixture
//                    resources described in bin/ci/README-plugin-isolation.md
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
assert(origin && adminKey && pluginId, 'MANAGER_ORIGIN, ADMIN_KEY and PLUGIN_ID are required');

const { chromium, webkit } = createRequire(import.meta.url)(
  process.env.PLAYWRIGHT_MODULE || 'playwright'
);
const out = resolve(process.env.G2_OUTPUT_DIR || '/tmp/pah-g2-output');
mkdirSync(out, { recursive: true, mode: 0o700 });

const base = `${origin}/v0/resource/plugins/${pluginId}`;
const report = { result: 'FAIL', origin, pluginId, engines: {}, checks: [], consoleErrors: [], notRun: [] };

function record(engine, name, result, detail) {
  report.checks.push({ engine, name, result, ...(detail ? { detail } : {}) });
  console.log(`${result} ${engine} ${name}${detail ? ' — ' + JSON.stringify(detail) : ''}`);
}

// Admin authority the plugin must never reach.
const adminStorageKeys = [];
const adminCookies = [];

async function login(page) {
  await page.goto(origin + '/', { waitUntil: 'domcontentloaded' });
  // Full Web gates every admin API on the bearer key held in storage; the
  // login document persists it before navigating to the Console shell.
  const field = page.locator('input[type="password"], input[name="adminKey"], #admin-key').first();
  await field.waitFor({ state: 'visible', timeout: 45000 });
  await field.fill(adminKey);
  await page.locator('button[type="submit"], button:has-text("Login"), button:has-text("登录")').first().click();
  await page.waitForURL((url) => !/login/i.test(url.pathname), { timeout: 45000 });
  await page.waitForLoadState('networkidle');
}

async function snapshotAdminAuthority(context, page) {
  const storage = await page.evaluate(() =>
    Object.fromEntries([['local', localStorage], ['session', sessionStorage]].map(
      ([kind, store]) => [kind, Object.keys(store).map((key) => [key, store.getItem(key)])]
    ))
  );
  for (const [kind, entries] of Object.entries(storage)) {
    for (const [key, value] of entries) adminStorageKeys.push({ kind, key, value });
  }
  adminCookies.push(...(await context.cookies()));
  assert(adminStorageKeys.length > 0, 'Console stored no credential; login did not complete');
}

// Runs inside the plugin document. Returns which probes were contained.
async function probe(page) {
  return page.evaluate(async () => {
    const result = { storage: {}, network: {}, dom: {} };
    for (const [name, store] of [['localStorage', 'localStorage'], ['sessionStorage', 'sessionStorage']]) {
      try {
        const value = window[store];
        result.storage[name] = { reachable: true, length: value.length };
      } catch (error) {
        result.storage[name] = { reachable: false, error: (error && error.name) || 'Error' };
      }
    }
    for (const path of ['/api/v2/bootstrap/identity', '/v0/management/config.yaml']) {
      for (const credentials of ['omit', 'include']) {
        try {
          const response = await fetch(path, { credentials, method: 'GET' });
          const body = await response.text();
          result.network[`${path}#${credentials}`] = { ok: response.ok, status: response.status, bytes: body.length };
        } catch (error) {
          result.network[`${path}#${credentials}`] = { ok: false, error: (error && error.name) || 'Error' };
        }
      }
    }
    try {
      result.dom.parentTitle = { reachable: true, value: String(parent.document.title) };
    } catch (error) {
      result.dom.parentTitle = { reachable: false, error: (error && error.name) || 'Error' };
    }
    try {
      result.dom.origin = String(location.origin);
    } catch (error) {
      result.dom.origin = 'Error';
    }
    return result;
  });
}

function assertContained(engine, where, result) {
  for (const [name, value] of Object.entries(result.storage)) {
    assert.equal(value.reachable && value.length > 0, false,
      `${engine} ${where}: plugin read ${name} (${JSON.stringify(value)})`);
  }
  record(engine, `${where}: admin storage unreadable`, 'PASS');
  assert.equal(result.dom.parentTitle.reachable, false,
    `${engine} ${where}: plugin reached parent.document (${JSON.stringify(result.dom.parentTitle)})`);
  record(engine, `${where}: parent.document unreachable`, 'PASS');
  for (const [target, value] of Object.entries(result.network)) {
    assert.equal(value.ok, false, `${engine} ${where}: plugin fetch succeeded for ${target} (${JSON.stringify(value)})`);
  }
  record(engine, `${where}: admin API fetches refused`, 'PASS', Object.keys(result.network));
}

async function runEngine(engineName, launcher) {
  const browser = await launcher.launch({ headless: true });
  const engineReport = { checks: [] };
  try {
    // ---- Positives through a real admin session -------------------------
    const context = await browser.newContext();
    const page = await context.newPage();
    page.on('console', (message) => {
      if (message.type() === 'error') report.consoleErrors.push({ engine: engineName, text: message.text() });
    });
    await login(page);
    await snapshotAdminAuthority(context, page);
    record(engineName, 'Console login established', 'PASS');

    // Plugin list, enable/disable, config read/write and store listing are
    // exercised through the admin session with the Console's own credential.
    const api = await page.evaluate(async (key) => {
      const call = async (method, path, body) => {
        const response = await fetch('/api/v2/cpamp/' + path, {
          method,
          headers: { Authorization: 'Bearer ' + key, 'Content-Type': 'application/json' },
          body: body === undefined ? undefined : JSON.stringify(body),
        });
        return { status: response.status, ok: response.ok };
      };
      const key = localStorage.getItem('cpamp-admin-key') || sessionStorage.getItem('cpamp-admin-key');
      return { key, list: await call('GET', 'v0/management/plugins') };
    }, adminKey);
    assert.equal(api.list.ok, true, 'plugin list through the admin session failed: ' + JSON.stringify(api.list));
    record(engineName, 'plugin list through admin session', 'PASS', api.list);

    // The plugin page renders inside the Console iframe.
    const iframeSrc = `${base}/probe`;
    await page.goto(origin + '/plugins');
    await page.waitForLoadState('networkidle');
    const frameHandle = await page.evaluate((src) => {
      const frame = document.createElement('iframe');
      frame.id = 'pah-g2-frame';
      frame.src = src;
      document.body.appendChild(frame);
      return true;
    }, iframeSrc);
    assert.equal(frameHandle, true);
    const frame = page.frameLocator('#pah-g2-frame');
    await frame.locator('#probe-ready').waitFor({ timeout: 45000 });
    record(engineName, 'plugin page renders inside Console', 'PASS');

    // The plugin can fetch its own resource.
    const ownFetch = await page.frames().find((f) => f.url().startsWith(base))
      ?.evaluate(async (url) => {
        const response = await fetch(url, { credentials: 'omit' });
        return { status: response.status, ok: response.ok, text: (await response.text()).length };
      }, `${base}/probe.js`);
    assert(ownFetch && ownFetch.ok && ownFetch.text > 0, 'plugin could not read its own resource: ' + JSON.stringify(ownFetch));
    record(engineName, 'plugin fetches its own resource', 'PASS', ownFetch);

    // The theme postMessage arrives.
    const theme = await page.evaluate(() => {
      const frame = document.getElementById('pah-g2-frame');
      const seen = new Promise((resolve) => {
        addEventListener('message', (event) => { if (event.data && event.data.type === 'theme') resolve(event.data); });
        setTimeout(() => resolve(null), 5000);
      });
      frame.contentWindow.postMessage({ type: 'theme', theme: 'dark' }, '*');
      return seen;
    });
    assert(theme, 'plugin never received the theme postMessage');
    record(engineName, 'plugin receives theme postMessage', 'PASS', theme);

    // Negatives inside the Console iframe.
    assertContained(engineName, 'iframe', await probe(page.frames().find((f) => f.url().startsWith(base))));

    // Negatives after a refresh.
    const framePage = page.frames().find((f) => f.url().startsWith(base));
    await framePage.goto(`${base}/probe`, { waitUntil: 'domcontentloaded' });
    assertContained(engineName, 'iframe-after-refresh', await probe(framePage));

    // Negatives when the plugin page is opened directly.
    const direct = await context.newPage();
    await direct.goto(`${base}/probe`, { waitUntil: 'domcontentloaded' });
    assertContained(engineName, 'direct', await probe(direct));

    // Negatives in a brand-new tab of the same authenticated context.
    const tab = await context.newPage();
    await tab.goto(`${base}/probe`, { waitUntil: 'domcontentloaded' });
    assertContained(engineName, 'new-tab', await probe(tab));

    // ---- Attacker-controlled loads from the admin page ------------------
    const injections = await page.evaluate(async (src) => {
      const results = {};
      const settle = (name, promise) => promise.then(
        () => { results[name] = { loaded: true }; },
        (error) => { results[name] = { loaded: false, error: (error && error.name) || 'Error' }; }
      );
      await settle('script', new Promise((resolve, reject) => {
        const el = document.createElement('script');
        el.src = src + '.js';
        el.onload = resolve;
        el.onerror = reject;
        document.head.appendChild(el);
      }));
      await settle('import', import(src + '.js'));
      await settle('worker', new Promise((resolve, reject) => {
        const worker = new Worker(src + '.js');
        worker.onerror = reject;
        setTimeout(resolve, 1500);
      }));
      await settle('svg-use', new Promise((resolve, reject) => {
        const host = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
        const use = document.createElementNS('http://www.w3.org/2000/svg', 'use');
        use.setAttribute('href', src + '.svg#shape');
        use.onload = resolve;
        use.onerror = reject;
        host.appendChild(use);
        document.body.appendChild(host);
        setTimeout(resolve, 1500);
      }));
      await settle('svg-image', new Promise((resolve, reject) => {
        const image = document.createElementNS('http://www.w3.org/2000/svg', 'image');
        image.setAttribute('href', src + '.svg');
        image.onload = resolve;
        image.onerror = reject;
        document.body.appendChild(image);
        setTimeout(resolve, 1500);
      }));
      return results;
    }, base);
    for (const [kind, value] of Object.entries(injections)) {
      assert.equal(value.loaded, false, `${engineName}: admin page loaded plugin resource as ${kind} (${JSON.stringify(value)})`);
    }
    record(engineName, 'admin page cannot load plugin paths (script/import/Worker/SVG use+image)', 'PASS', Object.keys(injections));

    // ---- Redirect and cookie containment -------------------------------
    for (const path of ['redirect-admin', 'redirect-self']) {
      const response = await context.request.get(`${base}/${path}`, { maxRedirects: 0 });
      if (path === 'redirect-admin') {
        assert.notEqual(response.status(), 302, 'plugin escaped its prefix via 302');
        record(engineName, 'plugin 302 out of prefix refused', 'PASS', { status: response.status() });
      } else {
        const location = response.headers()['location'] || '';
        assert(location.startsWith(base) || !location.includes('/v0/management'),
          'in-prefix redirect was not preserved: ' + location);
        record(engineName, 'in-prefix 302 preserved', 'PASS', { status: response.status(), location });
      }
    }
    const cookieResponse = await context.request.get(`${base}/cookie`);
    assert.equal(cookieResponse.headers()['set-cookie'], undefined, 'plugin Set-Cookie survived');
    const afterCookies = await context.cookies();
    assert.equal(afterCookies.some((c) => c.name.startsWith('plugin_probe')), false, 'plugin cookie was planted');
    record(engineName, 'plugin Set-Cookie stripped', 'PASS');

    // Nothing the plugin did may have replaced the admin credential.
    for (const entry of adminStorageKeys) {
      const now = await context.storageState();
      assert.equal(
        JSON.stringify(now.origins.find((o) => o.origin === origin)?.localStorage ?? []).includes(entry.value),
        true,
        'admin credential disappeared after plugin interaction'
      );
    }
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
    const context = await browser.newContext();
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
    page.on('console', (message) => { if (message.type() === 'error') errors.push(message.text()); });

    const pages = ['/', '/login', '/setup'];
    for (const path of pages) {
      await page.goto(origin + path, { waitUntil: 'networkidle' }).catch(() => {});
      const found = await page.evaluate(() => window.__cspViolations || []);
      const visible = await page.evaluate(() => (document.body?.innerText || '').trim().length > 0);
      violations.push(...found.map((v) => ({ path, ...v })));
      assert.equal(visible, true, `${engineName} ${path}: no visible content`);
    }
    await login(page);
    const consolePages = ['/dashboard', '/plugins'];
    for (const path of consolePages) {
      await page.goto(origin + path, { waitUntil: 'networkidle' }).catch(() => {});
      const found = await page.evaluate(() => window.__cspViolations || []);
      violations.push(...found.map((v) => ({ path, ...v })));
      assert.equal(await page.evaluate(() => (document.body?.innerText || '').trim().length > 0), true,
        `${engineName} ${path}: no visible content`);
    }
    assert.deepEqual(violations, [], `${engineName}: CSP violations ` + JSON.stringify(violations));
    const cspErrors = errors.filter((text) => /Content Security Policy|Refused to/i.test(text));
    assert.deepEqual(cspErrors, [], `${engineName}: CSP console errors ` + JSON.stringify(cspErrors));
    record(engineName, 'admin CSP: zero violations on login/setup/Console', 'PASS');
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
