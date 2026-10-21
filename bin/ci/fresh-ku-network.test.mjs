import assert from 'node:assert/strict';
import test from 'node:test';
import { isExpectedDeniedNavigationAbort } from './fresh-ku-network.mjs';

const request = (method = 'POST', errorText = 'net::ERR_ABORTED') => ({
  method: () => method, failure: () => ({ errorText }),
});

test('only the observed deliberate 409 may be cancelled during re-login', () => {
  const denied = request();
  const denial = { request: denied, status: 409, uiConfirmed: true };
  assert.equal(isExpectedDeniedNavigationAbort(denied, denial, true), true);
  // A later authorized request has the same URL, but a different identity.
  assert.equal(isExpectedDeniedNavigationAbort(request(), denial, true), false);
  assert.equal(isExpectedDeniedNavigationAbort(denied, denial, false), false);
  assert.equal(isExpectedDeniedNavigationAbort(denied, undefined, true), false);
  assert.equal(isExpectedDeniedNavigationAbort(denied, { ...denial, uiConfirmed: false }, true), false);
  for (const status of [200, 401, 403, 500, 503]) {
    assert.equal(isExpectedDeniedNavigationAbort(denied, { ...denial, status }, true), false);
  }
  for (const [method, error] of [['GET', 'net::ERR_ABORTED'], ['POST', 'net::ERR_FAILED'], ['POST', 'net::ERR_TIMED_OUT']]) {
    const other = request(method, error);
    assert.equal(isExpectedDeniedNavigationAbort(other, { ...denial, request: other }, true), false);
  }
});
