# Console Shell and guided Fresh New delivery — 2026-10-23

An administrator can now claim a new installation, configure the administrator
key and Console address, sign in, review the embedded destination, submit
installation once, follow real execution/recovery and enter Console. The
Console reuses the original CPAMP layout and business pages, with grouped
navigation, breadcrumbs, desktop folding and a mobile drawer. This remains an
in-progress integration: menu ownership still needs to match the accepted
prototype, Fresh Key/retry/update-channel controls remain combined, and a
second Shell implementation remains in the source. The installation evidence
below does not establish completion of those integration requirements.

## Login and refresh correction

The Console now renders the original `features/login/LoginPage` with its existing
brand, theme, language and remember-credential controls. A narrow adapter verifies
the Admin Key through the canonical Fresh operation endpoint; it does not probe
or fall back to the old CPA credential path. Remembered credentials use the
existing auth store and must match the current origin, API base and deployment
mode. Refresh revalidates before mounting the original business pages and keeps
the deep route. Explicit logout and rejected authentication clear the credential.
Without **Remember credentials**, reload requires signing in again, consistent
with the existing login choice. The historical candidate below predates this fix.

The correction was verified at application source
`e610e3c485b9cbfac0d9d518bf29a079daa745e9` on a new Linux/amd64 Embedded
Manager + Ingress + official CPA 8.0.13 Runtime installation. The
[new report](evidence/console-login-refresh/report.json) records the real Fresh
New journey, original login page across desktop/mobile light/dark, remembered
login restoring `/console/config` after reload, and explicit logout preventing
restoration. Its five new screenshots are retained here; the other journey
screenshots remain in the task-owned local run. There were no uncaught browser
errors. Targeted tests also verify rejected/stale-scope credentials, temporary
503 recovery, and in-memory login/revocation when browser persistence throws.
The focused independent review's confirmed storage-error regression was fixed.

| Original login | Desktop | Mobile |
| --- | --- | --- |
| Light | [Screenshot](evidence/console-login-refresh/original-login-desktop-light.png) | [Screenshot](evidence/console-login-refresh/original-login-mobile-light.png) |
| Dark | [Screenshot](evidence/console-login-refresh/original-login-desktop-dark.png) | [Screenshot](evidence/console-login-refresh/original-login-mobile-dark.png) |

[Console after remembered refresh](evidence/console-login-refresh/remembered-console-after-refresh.png)
shows the restored original Config page. Candidate images are
`cpamp-console-auth-e610e3c4-{manager,ingress,runtime}:local`; exact IDs are in
the new report. Reproduce using the existing build and acceptance commands below,
substituting source `e610e3c4`, these image names and a new output directory.
The local build used the existing prebuilt-web packaging approach described below
and passed the embedded file/manifest and transport-contract checks. No Registry
push or release was performed. The older `18357` review environment was preserved;
the new report's loopback origin is a separate task-owned installed candidate.

## Candidate and reproduction

- Base: merged `v2@b535fd48bd3cdfc4d38c622bc037ae4b1876ec97`.
- **Tested application source:** `7dd532e61ff91206bba6c9ded7f8e8f0be1b8d27`.
  The Manager, Ingress and Runtime images were built from this final
  application source. The machine report records the same worktree `gitHead`
  and the exact candidate image IDs.
- Local image tags: `cpamp-console-v2-7dd532e6-manager:local`,
  `cpamp-console-v2-7dd532e6-ingress:local`,
  `cpamp-console-v2-7dd532e6-runtime:local`.
- Candidate image IDs: Manager
  `sha256:041933850b9cabf8b60a7761053ce83c9db8d2d4b78d052392235d23c5d04de4`,
  Ingress `sha256:07b665ec58af9c5dd3fe1a0eeed9d68d4e9309c845da65ddb58da906ae791992`,
  Runtime `sha256:ef3ba5168a3b76d75e14e6182f6e5ac4b5784a867530eb4c8ffaf6ffb46d2702`.
- Scope: disposable, loopback-published **Linux/amd64 Embedded** installation,
  official checksum-pinned CPA **8.0.13**, ordinary isolated Compose services.
  No Registry push, production data, shared queue, merge, tag or publication.
- Exact image IDs, the final worktree `gitHead` and observed generations are in
  the [machine report](evidence/console-shell-fresh-new/report.json). The
  report's image-source revision fields are unavailable for this disposable
  local build; the build command above pins the application source explicitly.

Use the [operator guide](fresh-new-installation.md) for manual Host admission
and UI steps. To rebuild from the source worktree:

```sh
git rev-parse HEAD
# The tested application bytes are 7dd532e61ff91206bba6c9ded7f8e8f0be1b8d27.
docker build --platform linux/amd64 -f Dockerfile.runtime \
  -t cpamp-console-v2-7dd532e6-runtime:local .
docker build --platform linux/amd64 -f Dockerfile.ingress \
  -t cpamp-console-v2-7dd532e6-ingress:local .
docker build --platform linux/amd64 -f Dockerfile.manager-server \
  --build-arg SOURCE_COMMIT=7dd532e61ff91206bba6c9ded7f8e8f0be1b8d27 \
  --build-arg VERSION=2.0.0-console-candidate \
  -t cpamp-console-v2-7dd532e6-manager:local .
PLAYWRIGHT_MODULE=/absolute/path/to/node_modules/playwright \
PLAYWRIGHT_CHANNEL=chrome \
node bin/ci/fresh-new-installation.mjs \
  cpamp-console-v2-7dd532e6-runtime:local \
  cpamp-console-v2-7dd532e6-manager:local \
  cpamp-console-v2-7dd532e6-ingress:local \
  /absolute/path/to/a/new/disposable-result
```

For this macOS review run, the web bundle was built first with
`npm --workspace apps/web run build`; the Manager image used the task-owned
prebuilt packaging Dockerfile under `bin/tmp/cpamp-console-v2-7dd532e6/`
because the product Manager Dockerfile's Alpine `npm ci` step could not resolve
the dependency cache in that environment. No product Dockerfile was changed.

The runner launches its own random Compose project and loopback port, initializes
only new volumes, exercises the browser and removes only that project's
containers/volumes. The tested environment used an installed Playwright module
and Chrome on macOS against real Linux/amd64 Docker containers. All task
containers and volumes were removed successfully; the local candidate images
remain available. This is a runnable review candidate, not a published release.

## Actual administrator journey

**PASS:** Host-issued claim → saved administrator key → verified Console address
→ login → choose new installation → check the deployment → review and confirm
→ prepare/start/verify/save → enter Console.

Runtime identity comes from Manager's existing Host-configured authenticated
transport. The browser no longer pastes operation/Runtime JSON or invokes
stage/start individually. Completion requires the original committed target
to be observed ready with the same identity, exact uint64 generation and
artifact. Every successful transition reads the durable operation.

**PASS:** the actual Start request completed, but delivery of its response and
subsequent reads was interrupted. The UI showed an unconfirmed result and
blocked another write. Browser refresh, administrator relogin and continuation
recovered the same operation and completed it without a second Start. A failed
deployment read made no installation write.

**PASS:** four real routes, current leaf, breadcrumbs, browser Back/Forward,
desktop folding, mobile drawer, theme variants and language switching. The
update-channel change persisted. Manager/Runtime/Ingress restart preserved the
operation, routing, installation result, data key and setting.

**PASS:** missing/wrong administrator credentials, stale target generation,
consumed/wrong/expired handoffs, repeated initialization and non-Fresh sources
were rejected. The expiry case uses an offline disposable expiry fixture;
natural ten-minute expiry was not rerun. No browser storage, uncaught browser
errors or secret-bearing service logs were observed.

## Screenshots

All screenshots below come from the final real candidate, not prototype data.
The complete desktop/mobile × light/dark evidence is indexed by the machine
report in the same directory.

| View | Desktop | Mobile |
| --- | --- | --- |
| Existing CPAMP Console, light | [Home](evidence/console-shell-fresh-new/legacy-home-desktop-light.png) | [Home](evidence/console-shell-fresh-new/legacy-home-mobile-light.png) |
| Existing CPAMP Console, dark | [Config](evidence/console-shell-fresh-new/legacy-config-desktop-dark.png) | [Config](evidence/console-shell-fresh-new/legacy-config-mobile-dark.png) |
| Navigation | [Folded](evidence/console-shell-fresh-new/legacy-navigation-collapsed-desktop-dark.png) | [Drawer](evidence/console-shell-fresh-new/legacy-navigation-mobile-light.png) |
| Installation review | [Desktop](evidence/console-shell-fresh-new/installation-review-desktop-light.png) | [Mobile](evidence/console-shell-fresh-new/installation-review-mobile-light.png) |
| Unconfirmed result | [Desktop](evidence/console-shell-fresh-new/installation-result-unconfirmed-desktop-dark.png) | [Mobile](evidence/console-shell-fresh-new/installation-result-unconfirmed-mobile-dark.png) |
| Installation complete | [Desktop](evidence/console-shell-fresh-new/installation-complete-desktop-light.png) | [Mobile](evidence/console-shell-fresh-new/installation-complete-mobile-light.png) |

## Verification and remaining scope

Passed: Manager complete Go tests and vet; race checks for affected Fresh
admission/store/routing/strategy paths; full Web tests and repository contracts;
TypeScript; ESLint (no errors, six warnings); Full Web and single-file Panel
builds and demo-isolation guard. The final Shell delta passed its focused
regressions and type/lint checks. The final same-source disposable candidate
report records 25 checks, 37 screenshots and no page errors. Independent
review of the tested application source found no confirmed High/Medium defects.
Applicable remote Required CI is reported on the Draft PR; local checks do not
stand in for it.

The accepted prototype is implemented for this delivery slice, not for every
prototype page. Full Key/Config workspace modernization, models, Runtime status
modernization, policy, quota and pinned menus remain outside this slice.
Ordinary Fresh keeps private H1 retry writers and CPA key installation/revocation
unavailable unless their existing Host authority is present; navigation grants
no new writer. The actual candidate verified the truthful read-only state.

Gateway model traffic remains Pending and zero-key requests stay blocked.
Adopt stays closed; External is a checkpoint, not ProductComplete; both Upgrade
journeys remain unavailable. The five-path Beta obligation remains open.
An independent Supervisor restart changes generation and currently leaves CPA
offline: original installation provenance is retained and old-target writes
are refused. The existing explicit Host lifecycle recovery was exercised;
automatic browser recovery across that identity change is not claimed.

Non-Chinese layout matrices, other browser engines, arm64, real Provider/model
traffic, power-loss and production rollback were not run. One minor visual
difference remains: the mobile dark unconfirmed-result alert heading wraps
into short lines; its explanation and recovery controls remain readable.
