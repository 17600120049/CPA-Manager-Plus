# Private Key revocation / necessary replacement evidence

Disposition: **KU BROWSER STOP / ACCEPTANCE HOLD / LIVE NOT RUN / BETA HOLD**.
Current source passes KU API, S1 and S2-A offline Docker proofs. The unchanged KU
Browser runner fails its final network assertion. Successful operation assertions
and CI do not turn a stopped browser run into PASS.

Tested product source: **a20a7fe83483d51a7ca29c70d94bb4df75e35dfe**.
The delivery commit changes evidence/docs only. Its exact HEAD is the Draft PR
review target; executable source trees and supplemental hashes are recorded in
[the acceptance manifest](acceptance-a20a7fe.json). No rebase, product change or
modification of another worktree was performed during this acceptance.

Authority: maintainer's explicit disposable private proof instruction and
[Plan #64](https://github.com/seakee/cpamp-plan/pull/64), merge
ac4600cec887a937dbf6f52ea0bd50dff2971548.
[Plan #63](https://github.com/seakee/cpamp-plan/pull/63) defers real-environment
manual acceptance to pre-Beta; it does not waive failed source checks or grant
production writes. Configuration/wizard owner files remain outside scope.

## Current-source observations

All new runs used official CPA **v8.0.13**, Linux/amd64, a fresh internal network
and newly created disposable volumes. The existing serial launcher proved
source/namespace ownership and issued dedicated per-step permits. No ordinary
Compose, External, Upgrade, real user volume or public Gateway was used. All
task-owned containers, volumes and networks were confirmed destroyed.

| Record | Current-source result / scope |
| --- | --- |
| [KU Browser Docker](ku-browser.json) / [browser report](browser.json) | **STOP**, vanilla Playwright 1.58.0 bundled Chromium revision 1208; three functional assertions and layouts completed, final zero-network-failures assertion failed |
| [Chrome attempts](chrome-browser-stops.json) | Two original runs and one passive diagnostic run with Chrome 155.0.8059.85 also **STOP**; not replaced by API PASS |
| [Passive network events](browser-network.jsonl) | Expected missing-permit POST returned 409, later reported net::ERR_ABORTED during re-login; both authorized POST responses were 200 and finished normally |
| [KU API Docker](ku-api.json) | **PASS**, actual replacement/revocation, original operation recovery after Manager restart, final CPA/Ingress positive and negative controls |
| [S1 regression](s1.json) | **PASS**, original installation, duplicate-attempt and generation fences; Gateway remains closed |
| [S2-A regression](s2a.json) | **PASS**, synthetic Chat matrix, malformed requests, expiry, isolation, restart, CPA unresponsiveness and generation fences |
| [S2-B historical record](s2b.json) | Historical PASS at 95d885f…, retained unchanged with the reuse analysis below; **not a new-head Docker run** |

Current Manager image:
sha256:cd44c1a1568d5c7633b5a26e5d47592f5a7c5fcf20211c7d0aa551a5b6c5fc06.
Runtime: sha256:3fc9b926a8af2742cda13656e589dbb067c3aa3984438ea742b88c0158d686e2.
Ingress: sha256:2b46fb49a9fafc3e4aa7d7e0419c0b0d628e60231469090229fd15563e9b7a48.
All three were built from clean tested source. Runtime/Ingress reused identical
cached layers; Manager was rebuilt with Full Web, packaged-web checks and
SOURCE_COMMIT=a20a7fe…. This is development-image source binding, not signed
release provenance.

Every current Docker record matches its listed source hashes with **zero
mismatches**. KU binds 38 files. The supplemental manifest also binds all five
files changed since 95d885f…, including publicApi.ts, plus Dockerfiles, package/
lock files and Git source-tree IDs. The runner's 38-file list alone does not cover
every build input. Official CPA executable SHA256:
b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41.

The separate KU API run measured final deltas using the passive CPA packet meter
and independent synthetic upstream log:

| Request through Ingress | HTTP | CPA Chat arrivals | Synthetic upstream arrivals |
| --- | --- | --- | --- |
| Replacement | 200 | 1 | 1 |
| Revoked old Key | 503 | 0 | 0 |
| Wrong Key | 503 | 0 | 0 |
| Missing Key | 503 | 0 | 0 |

Browser runs stop before the launcher's final Ingress matrix. These API-run
measurements must not be attributed to a stopped browser run. No empty Key set
was written. API proof retained the original S1 journal and canonical history:
old superseded, independent replacement active.

## Browser STOP and smallest next step

The existing browser runner completes canonical selection, preparation,
missing-permit refusal, authorized add, actual Manager restart, re-login,
authorized revoke and history refresh. All four attempts report zero page errors
and one failed /continue request. **No browser run is PASS.**

The Chrome diagnostic ties the event to the already returned intentional 409:
the two authorized 200 requests have matching normal completion events. Its
[task-only observer](browser-observer.cjs.txt) is retained as text evidence.
It adds event listeners only, with no request interception or failure filtering.

**HOLD:** narrowly correct the bounded browser proof's handling of an already
received expected 409 response cancelled during navigation, then rerun KU Browser
on the same product source. Keep failures of authorized writes, unexpected
statuses and unknown outcomes visible. Do not remove the network assertion or
whitelist all /continue failures. This task permits evidence/docs commits only;
no product or executable proof-tool fix is included. Further unchanged browser
retries add no useful evidence. Real Provider acceptance is not this unlock.

## S2-B historical reuse

The original [S2-B artifact at its source HEAD](https://github.com/seakee/CPA-Manager-Plus/blob/95d885f60e600bf8c8adae8d1e163454b5e4fd46/docs/architecture/evidence/fresh-private-key-revocation/s2b.json)
is preserved byte-for-byte (artifact SHA256 in the manifest). All 27 source
hashes match 95d885f60e600bf8c8adae8d1e163454b5e4fd46; 26 also match current
source. The sole mismatch is freshkeyapply/key_operation.go.

That delta changes checkOperation historical committed reads and latest-operation
ordering in ContinueOperation. The S2-B launcher does not enable
CPAMP_PRIVATE_KEY_OPERATIONS or call either method. Shared installedKey, Admit,
permit/service wiring, fixed Provider transport, Ingress, Runtime, seed and S2-B
harness are unchanged. Other changes are KU UI/API-client handling and tests,
unused by that proof. Current freshkeyapply race tests include S2-B fixture/
journal/permit regressions; current S2-A Docker covers shared request admission.

Historical fixed-Provider/model, TLS-stub, replay and denial observations remain
useful within that unchanged scope. They do not qualify the new Manager image as
a newly executed S2-B Docker run, KU-with-S2-B, real Provider, external egress or
target Host/network. No full S2-B matrix rerun was needed for this delta.

## Source checks and review

[Current command output](acceptance-checks.txt):

- Targeted Manager race: freshkeyapply, freshkeys, cpa and cmd/cpa-manager-plus
  passed; repository/freshkeys has no test files. Includes historical-result,
  no-retry and S2-B regressions.
- Targeted Manager vet, Full Web **235 tests**, type check and Lint passed.
  Lint retains four existing Accounts warnings, zero errors. Proof namespace
  test passed. Full Web/panel build and packaged-web check passed inside build.
- Source-HEAD PR Check, Frontend, Manager Server, Windows SQLite, Docker Build,
  AUT-03 and Required checks passed; snapshot/URLs are in the manifest.
  Delivery-HEAD checks must be read from the PR, not inferred from that snapshot.
- Original full Go/Web/Ingress/Runtime checks, 4,091 Web tests and
  [maintenancebundle race timeout/recheck](race-recheck.txt) belong to 95d885f…,
  not this rerun. Current targeted checks and CI supplement them.

Three acceptance self-review passes check source/run/image identity; authority,
secret exclusion, failed-run preservation and history boundaries; then
evidence-only scope, links and delivery HEAD/CI reporting. Stale all-PASS and
all-current-image claims are removed. Browser STOP remains explicit; completed
acceptance or independent human review is not claimed.

Fault/state-machine tests use synthetic transports and are not real CPA effect
evidence. Actual CPA observations come only from the retained Docker records.

## Layouts and reproduction

Current-source screenshots were inspected. They are from the **stopped**
bundled Chromium run and do not change its disposition:

- [Desktop dark English selection](desktop-dark-en-selection.png)
- [Desktop light English committed result](desktop-light-en.png)
- [Mobile dark Simplified Chinese committed result](mobile-dark-zh-CN.png)

The runner checked visible state, no horizontal overflow and empty persistent
browser storage. Other locale/theme variants are not visual acceptance claims.

Build images per [the existing instructions](../../fresh-private-key-revocation.md)
with prefix cpamp-ku-acceptance and tag a20a7fe. Run serially with unique output
directories and existing explicit disposable private-write approval:

~~~sh
export CPAMP_PROOF_IMAGE_PREFIX=cpamp-ku-acceptance
export CPAMP_PROOF_IMAGE_TAG=a20a7fe
export CPAMP_PROOF_KEY_WRITE_APPROVED=plan64-private
# Bundled Chromium; this browser run currently STOPs.
unset PLAYWRIGHT_CHANNEL
CPAMP_PROOF_KU_BROWSER=1 PLAYWRIGHT_MODULE=/absolute/path/to/playwright \
  python3 bin/ci/fresh-s1-docker-proof.py <unique-browser-output> --ku
env -u CPAMP_PROOF_KU_BROWSER \
  python3 bin/ci/fresh-s1-docker-proof.py <unique-api-output> --ku
python3 bin/ci/fresh-s1-docker-proof.py <unique-s1-output>
python3 bin/ci/fresh-s1-docker-proof.py <unique-s2a-output> --s2a
~~~

Acknowledgement variables are not authorization. No new test framework, product
feature, CPA writer, dependency or acceptance gate was introduced.

## Remaining qualification

Overall acceptance is **HOLD** until the existing KU Browser proof completes.
Real Provider traffic, controlled external egress, target network/Host isolation
and human end-to-end acceptance remain **LIVE NOT RUN / BETA HOLD**, scheduled
before first Beta under Plan #63. Offline synthetic traffic is not LIVE evidence.
Ordinary Compose, External, Upgrade, arm64, public Gateway and generation
recovery remain outside this private scope. The PR stays Draft; no merge/release.
