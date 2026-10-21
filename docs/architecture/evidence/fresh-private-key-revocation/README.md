# Private Key revocation / necessary replacement evidence

The browser proof correction and its new disposable official CPA runs are
recorded in [browser-fix/acceptance.json](browser-fix/acceptance.json). Product
source remains **a20a7fe83483d51a7ca29c70d94bb4df75e35dfe**, unchanged in
the starting checkout **b0d2432b44449d581aa3c35e2c46e8503d9d18d6**.
The manifest binds the tested tool bytes and unchanged image/build inputs;
the PR description names the exact delivery/review HEAD.

## Cause and bounded correction

The original runner counted every requestfailed event as an unexpected failure.
Its intentional missing-permit POST had already returned HTTP 409 and displayed
the refusal. During re-login, Chromium cancelled that unconsumed error response
with net::ERR_ABORTED. The passive [original trace](browser-network.jsonl)
shows the authorized add and revoke responses returned 200 and finished normally.
The API client throws for this POST error without consuming its response body;
this explains the observed response lifecycle, without implying a failed CPA write.

The correction identifies that specific Playwright Request object, requires its
observed 409 and visible UI refusal, and accepts only net::ERR_ABORTED during the
proof's explicit re-login. It retains the cancellation in the report. Another
request to the same URL, another status/error, missing UI refusal or cancellation
outside re-login still fails the proof. The zero-unexpected-network-failures
assertion remains. No product behavior or CPA writer changed.

## New proof and regression scope

Both browser runs use official CPA **v8.0.13**, Linux/amd64, new internal networks
and disposable volumes. Host ownership and fresh step-bound permits are proved
by the existing serial launcher. Images are reused from the verified unchanged
a20a7fe product source; the changed host-side tools are hashed separately.
Environment acknowledgement alone is not writer authority.

The new runs include the browser operation, real Manager restart, original
operation recovery, canonical history and final Ingress matrix. Their actual
results, timestamps, source hashes and resource cleanup are in the manifest:

- [Bundled Chromium browser](browser-fix/chromium-browser.json) and
  [complete Docker proof](browser-fix/chromium-docker.json).
- [Chrome browser](browser-fix/chrome-browser.json) and
  [complete Docker proof](browser-fix/chrome-docker.json).
- [Focused policy, syntax and namespace tests](browser-fix/checks.txt).

The final positive control requires replacement Chat HTTP 200, one CPA Chat
arrival and one synthetic upstream arrival. Old, wrong and absent Keys must
each return 503 with zero CPA Chat and upstream arrivals. Offline synthetic
Chat is not a real Provider or target-network qualification.

Three self-review rounds examine request identity/response/UI binding and the
complete operation; error/status/concurrent-request exclusion and writer/history
boundaries; then source hashes, retained failures, unchanged product scope and
regression reuse. The focused negative tests are classifier tests, not CPA proof.

The [previous acceptance report](acceptance-a20a7fe.md), its manifest, all four
stopped browser runs and original screenshots remain retained. They are not
relabeled PASS. [KU API](ku-api.json), [S1](s1.json) and [S2-A](s2a.json) remain
actual a20a7fe results; they were not rerun for this host-only browser correction.
The launcher's only change outside the browser is adding the imported helper to
the KU source hash list. The S1/S2-A execution paths are unchanged. Historical
[S2-B](s2b.json) reuse and its limits remain as documented in the previous report;
no new S2-B Docker run is claimed. Existing Go/Web tests/builds belong to their
recorded source HEADs; exact delivery-HEAD Required CI must be read from GitHub.

## Layout and reproduction

Inspected bundled Chromium screenshots from the new run:

- [Desktop dark English selection](browser-fix/desktop-dark-en-selection.png).
- [Desktop light English committed history](browser-fix/desktop-light-en.png).
- [Mobile dark Simplified Chinese committed history](browser-fix/mobile-dark-zh-CN.png).

The runner also checks no horizontal overflow and empty persistent browser
storage. These three variants do not qualify every locale/theme combination.

With the existing explicit private disposable-write approval and verified
unchanged local images:

~~~sh
node --test bin/ci/fresh-ku-network.test.mjs
CPAMP_PROOF_IMAGE_PREFIX=cpamp-ku-acceptance \
CPAMP_PROOF_IMAGE_TAG=a20a7fe \
CPAMP_PROOF_KEY_WRITE_APPROVED=plan64-private \
CPAMP_PROOF_KU_BROWSER=1 PLAYWRIGHT_MODULE=/absolute/path/to/playwright \
  python3 bin/ci/fresh-s1-docker-proof.py <new-unique-output> --ku
# Set PLAYWRIGHT_CHANNEL=chrome for the Chrome run; unset for bundled Chromium.
~~~

## Remaining qualification

**LIVE NOT RUN / BETA HOLD** remains under Plan #63/#64. Real Provider, controlled
external egress, target Host/network and human end-to-end acceptance are deferred
to pre-Beta. Ordinary Compose, External, Upgrade, production volumes, arm64 and
public Gateway are outside this private profile. Keep PR #1031 Draft; no merge
or release. A passing offline browser proof permits independent source acceptance
only after exact-HEAD Required CI passes.
