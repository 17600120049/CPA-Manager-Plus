// Only the disposable Host-loopback test harness exposes __ku endpoints.
import assert from 'node:assert/strict';
import { createRequire } from 'node:module';
import { readFileSync, writeFileSync } from 'node:fs';
import { resolve } from 'node:path';
import { isExpectedDeniedNavigationAbort } from './fresh-ku-network.mjs';
const { chromium } = createRequire(import.meta.url)(process.env.PLAYWRIGHT_MODULE || 'playwright');
const [origin, output] = process.argv.slice(2);
const text = JSON.parse(readFileSync(new URL('../../apps/web/src/i18n/locales/en.json', import.meta.url))).fullweb;
const zh = JSON.parse(readFileSync(new URL('../../apps/web/src/i18n/locales/zh-CN.json', import.meta.url))).fullweb;
const report = { result: 'STOP', checks: [], pageErrors: [], layouts: [], apiFailures: [], networkFailures: [], expectedDenialCancellations: [] };
const browser = await chromium.launch({ headless: true, ...(process.env.PLAYWRIGHT_CHANNEL ? { channel: process.env.PLAYWRIGHT_CHANNEL } : {}) });
let secret = '';
try {
  const context = await browser.newContext({ locale: 'en-US', viewport: { width: 1440, height: 1100 } });
  const page = await context.newPage();
  let denial;
  let navigating = false;
  page.on('pageerror', () => report.pageErrors.push('uncaught page error'));
  page.on('requestfailed', request => {
    const failure = { path: new URL(request.url()).pathname, method: request.method(), errorText: request.failure()?.errorText };
    if (isExpectedDeniedNavigationAbort(request, denial, navigating)) report.expectedDenialCancellations.push({ ...failure, status: denial.status });
    else report.networkFailures.push(failure);
  });
  page.on('response', response => {
    if (response.status() >= 400 && new URL(response.url()).pathname.includes('/api/v2/')) report.apiFailures.push({ path: new URL(response.url()).pathname, status: response.status() });
  });
  page.on('dialog', dialog => dialog.accept());
  const fixture = await (await context.request.get(origin + '/__ku/fixture')).json();
  secret = fixture.admin;
  async function login() {
    await page.goto(origin + '/control');
    await page.locator('#console-admin-key').fill(fixture.admin);
    await page.getByTestId('console-login').locator('button[type=submit]').click();
    await page.getByRole('heading', { name: text.keysRevokeTitle }).waitFor();
    await page.getByRole('button', { name: text.keysOperationPrepare, exact: true }).waitFor();
  }
  const section = () => page.getByRole('heading', { name: text.keysRevokeTitle }).locator('..');
  const check = () => section().getByRole('button', { name: text.keysOperationCheck, exact: true });
  const proceed = () => section().getByRole('button', { name: text.keysOperationContinue, exact: true });
  async function waitState(word) {
    for (let i = 0; i < 15; i++) {
      if (await section().getByText(word, { exact: true }).isVisible()) return;
      await check().click();
      await page.waitForTimeout(150);
    }
    throw new Error('expected operation state missing');
  }
  await login();
  await section().locator('select').nth(0).selectOption(fixture.target);
  await section().locator('select').nth(1).selectOption(fixture.keeper);
  await section().locator('select').nth(2).selectOption('replace');
  await layout(page, 'desktop-dark-en-selection', text.keysRevokeTitle);
  await section().getByRole('button', { name: text.keysOperationPrepare, exact: true }).click();
  await waitState(text.keysOperationAddNext);
  const operation = (await section().locator('code').allTextContents())[0];
  assert.match(operation, /^[a-f0-9]{32}$/);
  const deniedResponse = page.waitForResponse(response => response.request().method() === 'POST' &&
    response.url() === origin + '/control/api/v2/api-keys/operations/' + operation + '/continue');
  await proceed().click(); // An old S1 permit must not authorize KU.
  const response = await deniedResponse;
  assert.equal(response.status(), 409);
  denial = { request: response.request(), status: response.status(), uiConfirmed: false };
  await section().getByRole('alert').waitFor();
  denial.uiConfirmed = true;
  report.checks.push('Missing dedicated permit returns HTTP 409 and a visible UI refusal');
  await check().click();
  assert.equal((await context.request.post(origin + '/__ku/grant', { data: {} })).status(), 200);
  await proceed().click();
  await waitState(text.keysOperationRevokeNext);
  report.checks.push('Browser canonical selection, durable preparation, dedicated add permit and observed replacement');
  assert.equal((await context.request.post(origin + '/__ku/restart', { data: {} })).status(), 200);
  navigating = true;
  await login();
  navigating = false;
  await waitState(text.keysOperationRevokeNext);
  assert.equal((await section().locator('code').allTextContents())[0], operation);
  report.checks.push('Manager restart and browser reload recover the original operation');
  assert.equal((await context.request.post(origin + '/__ku/grant', { data: {} })).status(), 200);
  await proceed().click();
  await waitState(text.keysOperationCommitted);
  // Completion refreshes the list asynchronously before creation is unlocked.
  for (let i = 0; i < 30 && !(await page.getByRole('button', { name: text.keysCreate, exact: true }).isEnabled()); i++) await page.waitForTimeout(100);
  assert(await page.getByRole('button', { name: text.keysCreate, exact: true }).isEnabled());
  report.checks.push('Browser continues only original revoke step and refreshes retained canonical history');
  async function layout(p, label, heading) {
    const box = p.getByRole('heading', { name: heading }).locator('..');
    await box.scrollIntoViewIfNeeded();
    assert(await box.isVisible());
    assert(await p.evaluate(() => document.documentElement.scrollWidth <= innerWidth + 1), 'horizontal overflow');
    assert.equal(await p.evaluate(() => localStorage.length + sessionStorage.length), 0);
    await p.screenshot({ path: resolve(output, label + '.png'), fullPage: true });
    report.layouts.push(label);
  }
  await page.getByRole('button', { name: text.theme, exact: true }).click();
  await layout(page, 'desktop-light-en', text.keysRevokeTitle);
  const mobile = await browser.newContext({ locale: 'zh-CN', viewport: { width: 390, height: 844 }, isMobile: true });
  const p2 = await mobile.newPage();
  p2.on('pageerror', () => report.pageErrors.push('uncaught mobile page error'));
  await p2.goto(origin + '/control');
  await p2.locator('#console-admin-key').fill(fixture.admin);
  await p2.getByTestId('console-login').locator('button[type=submit]').click();
  await p2.getByText(zh.keysOperationCommitted, { exact: true }).waitFor();
  await layout(p2, 'mobile-dark-zh-CN', zh.keysRevokeTitle);
  assert.equal(report.pageErrors.length, 0);
  assert.equal(report.networkFailures.length, 0);
  report.operation = operation;
  report.result = 'PASS_BROWSER_PRIVATE_REPLACEMENT';
} catch (error) {
  report.error = String(error.message).replaceAll(secret || 'unused-secret', '[REDACTED]');
  process.exitCode = 1;
} finally {
  writeFileSync(resolve(output, 'browser.json'), JSON.stringify(report, null, 2) + '\n');
  await browser.close();
}
