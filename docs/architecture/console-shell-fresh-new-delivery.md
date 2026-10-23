# Console integration and guided Fresh New delivery — 2026-10-23

Fresh New administrators can claim a supported deployment, save the administrator
key, configure the Console address, sign in, confirm installation, observe real
execution and enter the original CPAMP Console. A lost response, disconnect or
refresh resumes the same durable installation operation.

This delivery modifies the original `MainLayout` and `MainRoutes`. It preserves
the original pages, header tools, theme, language and login page. The accepted
prototype supplies navigation ownership and user journeys; it is not a second
visual system. The duplicate Console Shell and mixed resource page were removed.

## What is integrated

| Navigation | Existing destination |
| --- | --- |
| Dashboard | Original dashboard |
| Resources | Accounts, AI providers and OAuth |
| Client access | Manager-owned API keys |
| Monitoring | Existing monitoring, usage and model-price availability gates |
| System | Original system, configuration, logs and update pages |
| Extensions | Existing plugin availability gate |

Navigation supports current leaf, nested breadcrumbs, browser history, persisted
group expansion, desktop folding with keyboard-accessible flyouts, and a mobile
drawer with focus/scroll restoration, including resize to desktop.

`/api-keys` contains the existing Manager Key workspace. `/config` preserves the
original visual/source editor and Manager connection settings, with delivered
recoverable retry controls below it. `/system/updates` uses the original update
page and provider, including the real channel preference. The original global
confirmation and notification components are mounted after installation admission.
Key/retry editors remain mounted when hidden, preserving the original pending
request during route changes. Pending operations lock both header logout and
System's clear-login action. The original Config floating directory stays inside
its workspace and cannot intercept the header's navigation control.

Login uses the original `LoginPage` and auth store. With **Remember credentials**,
refresh validates the saved credential through the canonical Fresh endpoint before
restoring the deep route. Without it, reload requires login. Logout and rejected
authentication revoke the remembered authority; temporary unavailability permits
retry. Storage failure does not prevent an in-memory login or revocation.

## Tested candidate and reproduction

- Base: merged `v2@b535fd48bd3cdfc4d38c622bc037ae4b1876ec97`.
- Tested application source: `280850e56e5bb73be25668e36cb3ae0405873627`.
  Subsequent evidence/document commits do not change the application bytes.
- Images: `cpamp-console-original-280850e5-{manager,ingress,runtime}:local`.
  The [machine report](evidence/console-original-integration/report.json) records
  exact image IDs and the same source revision on all three images.
- Supported run: disposable loopback Linux/amd64 Embedded, official
  checksum-pinned CPA **8.0.13**. No Registry push or release.

From a worktree containing that application source:

```sh
npm ci
npm --workspace apps/web run build
docker build --platform linux/amd64 -f Dockerfile.runtime \
  -t cpamp-console-original-280850e5-runtime:local .
docker build --platform linux/amd64 -f Dockerfile.ingress \
  -t cpamp-console-original-280850e5-ingress:local .
docker build --platform linux/amd64 -f Dockerfile.manager-server \
  --build-arg SOURCE_COMMIT=280850e56e5bb73be25668e36cb3ae0405873627 \
  --build-arg VERSION=2.0.0-console-candidate \
  -t cpamp-console-original-280850e5-manager:local .
PLAYWRIGHT_MODULE=/absolute/path/to/node_modules/playwright \
PLAYWRIGHT_CHANNEL=chrome \
node bin/ci/fresh-new-installation.mjs \
  cpamp-console-original-280850e5-runtime:local \
  cpamp-console-original-280850e5-manager:local \
  cpamp-console-original-280850e5-ingress:local \
  /absolute/path/to/a/new/result-directory
```

The runner creates a new random Compose project, loopback port and volumes,
exercises installation and recovery, and removes only that project. For manual
Host admission and installation steps, use the [operator guide](fresh-new-installation.md).

The actual macOS review build reused the existing local prebuilt-web packaging
approach: build Full Web/Panel, hash the Full Web manifest, then copy these into
the Go Manager image. Its task-owned Dockerfile is under
`bin/tmp/console-original-integration-20261023/`; product Dockerfiles were not
changed. Embedded package/manifest and transport-contract checks passed inside
the Manager build. The retained review candidate uses the same runner with only
task-local credential output and success cleanup adjusted; the credential is a
0600 local file and is not included in committed evidence.

## Actual results and screenshots

The [installation report](evidence/console-original-integration/report.json)
and [Console integration report](evidence/console-original-integration/integration-report.json)
record real Chrome interactions with Linux containers:

- Host-issued claim → original login → selection/configuration/confirmation →
  real execution → original Console. No manually assembled operation JSON.
- Failed deployment read makes no installation write. Lost Start response and
  disconnected reads show an unconfirmed result; refresh/relogin recovers the
  same operation without another Start. Service restarts preserve the journal.
- Missing/wrong authentication, stale generation, consumed/wrong/expired handoff,
  repeated initialization and non-Fresh data are rejected. Expiry uses an offline
  disposable timestamp fixture rather than a natural ten-minute wait.
- Desktop/mobile × light/dark navigation, Key/Config/update ownership, original
  toolbar, remembered deep-route refresh, logout rejection, keyboard flyouts and
  drawer resize work. Key POST executes with its reply aborted; switching pages
  and checking the original request recovers it with only one POST. System's
  clear-login action remains disabled while it is unresolved.
- Original update provider loads and persists the channel; original clear-login
  confirmation can be canceled without signing out. Config directory positioning
  and actual header click hit-testing pass. No uncaught browser errors.

| View | Desktop | Mobile |
| --- | --- | --- |
| API keys, light | [Desktop](evidence/console-original-integration/integrated-keys-desktop-light.png) | [Mobile](evidence/console-original-integration/integrated-keys-mobile-light.png) |
| API keys, dark | [Desktop](evidence/console-original-integration/integrated-keys-desktop-dark.png) | [Mobile](evidence/console-original-integration/integrated-keys-mobile-dark.png) |
| Original configuration, light | [Desktop](evidence/console-original-integration/integrated-config-desktop-light.png) | [Mobile](evidence/console-original-integration/integrated-config-mobile-light.png) |
| Retry controls, dark | [Desktop](evidence/console-original-integration/integrated-retry-desktop-dark.png) | [Mobile](evidence/console-original-integration/integrated-retry-mobile-dark.png) |
| Original updates, light | [Desktop](evidence/console-original-integration/original-updates-desktop-light.png) | [Mobile](evidence/console-original-integration/original-updates-mobile-light.png) |
| Installation confirmation | [Desktop](evidence/console-original-integration/installation-review-desktop-light.png) | [Mobile](evidence/console-original-integration/installation-review-mobile-light.png) |
| Installation complete | [Desktop](evidence/console-original-integration/installation-complete-desktop-light.png) | [Mobile](evidence/console-original-integration/installation-complete-mobile-light.png) |

[Saved original update channel](evidence/console-original-integration/original-updates-working.png) and [keyboard flyout](evidence/console-original-integration/integrated-navigation-flyout-desktop-dark.png) provide additional interaction evidence.

Selected screenshots are committed; the reports index the complete task-local
capture set. Intermediate candidates and earlier successful CI are historical,
not substitutes for the current application evidence.

The earlier [original-login screenshots](evidence/console-login-refresh/report.json)
remain historical authentication evidence; final authentication behavior is
rechecked in the current candidate reports above.

## Verification and remaining scope

Passed locally: affected Full Web/login/layout/Config/Key/Retry regressions,
repository contracts, TypeScript, ESLint (zero errors, four existing Accounts
warnings), Full Web and single-file Panel builds and isolation checks. Earlier
affected Go tests, vet and Fresh race checks remain applicable; the final delta
is frontend-only and the Manager packaging tests were rerun. A focused independent
review's confirmed drawer-resize and trailing-slash logout-guard defects were fixed. Browser checks then found and
closed the Config portal overlap and missing original update/dialog lifecycle.
Latest-head remote CI is reported separately on Draft PR #1068; historical CI
does not establish the new head's status.

Full Key/Config modernization, models, Runtime status modernization, policy,
quota and pinned menus are not included. Ordinary deployments keep private
retry writers and CPA Key installation/revocation unavailable without existing
Host authority. Manager Key persistence does not imply enabled Gateway traffic.
Gateway remains Pending; zero-key traffic remains blocked. Adopt is fail-closed,
External is checkpoint-only, both Upgrade paths remain unavailable, and the
five-path Beta remains open. An independent Supervisor restart changes generation;
recovery requires the existing explicit Host lifecycle rather than silent
browser rebinding. The disposable candidate shows an upstream Manager update-check
failure and unavailable CPA update/status observation; channel persistence passed,
but successful version discovery or an actual product upgrade is not claimed.
Fresh intentionally has no CPA Management Key yet: the original CPA visual/source
configuration reports HTTP 428 until its existing Manager-to-CPA connection is
authorized and configured. The tested configuration GET/PUT is Manager configuration,
not a claim that this CPA configuration source is connected. No private writer or
management credential was fabricated to remove this limit.
No arm64, real Provider traffic, other browser engines,
non-Chinese layout matrix, power-loss or production rollback qualification is claimed.
