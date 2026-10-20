# Fresh canonical key detail / rename acceptance

Observed 2026-10-20. Product base:
`2ac66daa25e98d88330b472fba30ebfc89b91f89` (live `v2`, rechecked before delivery).
Plan baseline: `seakee/cpamp-plan@1aba3b1330768fcb6ea2e42ad10dcc83358b8515`
(merged Plan #56). This is developer verification for a private, nonproduction
Fresh New slice, not independent acceptance or Beta qualification.

## Completed user operation

Log in with the initialized Admin Key, select a masked Key by Canonical APIKeyID,
read its persisted details, set or clear its name, save and reread. Browser
reload/relogin and Manager restart retain the same canonical ID and saved name.
Manager persistence never represents Runtime activation.

## Automated checks — PASS

```text
cd apps/manager-server
go test ./internal/repository/sqlite ./internal/adapters/sqlite/identitystore ./internal/domain/identity ./internal/service/freshkeys ./internal/service/fresh ./cmd/cpa-manager-plus
go test -race ./internal/service/freshkeys ./cmd/cpa-manager-plus -run 'TestRename|TestPersistRestart|TestFreshClientKeys' -count=1
go test ./internal/repository/sqlite -run TestFreshKeyNamesUpgradeExistingStagedRecords -count=1
go vet ./internal/service/freshkeys ./internal/repository/freshkeys ./cmd/cpa-manager-plus
go build -o <task-scratch>/manager ./cmd/cpa-manager-plus

npm --workspace apps/web run test -- src/fullweb
npm run type-check
npm run lint
npm run build
npm --workspace apps/web run build:fullweb
node bin/build/stage-manager-web.mjs
node bin/build/check-fullweb-build.mjs
git diff --check
```

Final Full Web result: **180 tests passed**. Lint: zero errors, four existing
warnings in AccountsPage tests / AccountHealthBadge. Full build covers Panel Lite
and Full Web separation; the final Full Web build includes the long-name wrap fix.
Generated web artifacts are not hand-edited or committed.

Tests cover pre-rename table upgrade/reopen; immutable identity/secret snapshots;
set/clear; wrong IDs including a creation-request ID; invalid names/payloads;
wrong protector; canceled calls; actual SQLite trigger-aborted writes; competing
same-version writers; duplicate submission; close/reopen; create/list/original
request replay; Admin rejection; unchanged Gateway Pending and existing command
regressions. UI tests cover unknown outcomes, 400/404/409/503, hanging GET timeout,
manual readback without another PUT, duplicate clicks, logout guards, mismatched
canonical replies and delayed success/401 from an unmounted record/session.

During development, TypeScript rejected test-only Array.at/act return usage and
Go vet rejected test-only WaitGroup.Go against the module's Go 1.24 floor. Those
were corrected using the repository's existing compatible patterns; final checks
above passed. No compiler target or dependency versions changed.

## Real process and browser evidence — PASS

Freshly built native darwin/amd64 Manager and Ingress processes ran on loopback
ports 29861–29863 with a unique task-owned SQLite/data-key directory. No CPA
Runtime, provider, shared database or queue consumer was started. The real host
initializer, claim exchange, Admin commit and public Ingress route verification
enabled `/console`; no routing checkpoint was injected into SQLite.

Final native executable SHA-256 values (test binaries, not release artifacts):

```text
Manager fe8ce9f16ccba9e2766ca6ddef31aa90fccde374536888c4824746e0baed02dd
Ingress 2036aed4db8e9212ebdf36ec518e81293237ee90532626163d97232d621499e7
```

- Browser rejects wrong Admin credentials, then logs in, creates a Key, closes
  its one-time plaintext, opens canonical details, saves a name and rereads it.
  The detail/list contain only masked preview and nonsecret metadata.
- After a real Manager commit, the browser deliberately aborts the PUT reply.
  UI shows unknown, disables ordinary logout, and resolves by one detail GET:
  **one PUT total**, no automatic retry, saved name recovered.
- A SQLite trigger deliberately aborts a real name UPDATE. HTTP 503 enters
  unknown; detail GET shows the previous saved name and version. The task-owned
  trigger is removed after the check.
- Another authenticated writer commits a new name while the form holds the
  older version. The stale UI save receives 409 and requires a detail read;
  readback shows the other writer's name without overwriting it.
- A real authorized detail response is held across logout. Another request
  saves a newer name, then a fresh login reads it. Delivering the old response
  does not change the new session's name.
- Actual Manager process stop/start and browser reload/relogin preserve the
  identical masked records, Canonical ID and saved names. Original-request GET
  and create replay return current metadata without `createdSecret`.
- Missing/wrong Admin credentials and the issued Fresh Bootstrap session are
  rejected on detail/rename; using a creation-request ID as the canonical ID is
  404. Admin Keys have no TTL in the existing verifier, so an expiring Admin
  session acceptance case is not applicable; no new session model is claimed.
- Direct SQLite inspection: protected envelopes, zero Runtime source bindings,
  zero usage rows in this fresh fixture. Manager/Ingress log scan finds no full
  test client secret or Admin Key. Browser localStorage/sessionStorage stay empty.
- `/v1/models` remains **503** before/after key changes and Manager restart.
- zh-CN/en/zh-TW/ru × 390x844/1440x1100 × dark/light: translated detail headings,
  zero application exceptions, empty persistent browser storage and no page
  horizontal overflow. A 120-character unbroken name exposed button-internal
  clipping (scrollWidth 695 vs clientWidth 281); after the focused wrap fix both
  widths are 281 at mobile size. HTTP errors from deliberate negative cases are
  expected console network entries, not application exceptions.

Sanitized screenshots: [Chinese desktop dark](zh-CN-desktop-dark.png),
[English mobile light](en-mobile-light.png). They contain only a masked test Key,
nonsecret Canonical ID and display name.

The initial process harness checked listener reachability before the Fresh
handler was ready, then incorrectly expected the routing-authority endpoint to
exist before route preparation. The harness now waits for the real Bootstrap UI;
no product change was made for those harness failures.

## Self-review and boundaries

1. **Function/data flow:** isolated detail editing from list refresh/create so a
   delayed list read cannot replace newly saved display metadata; confirmed draft
   discard on close/detail reload, persistence, rejection and unknown-result recovery.
2. **Security/concurrency:** canonical IDs and versions fence targets/writers;
   the keyed authenticated view retires old replies. Added success/401 isolation
   and same-turn logout regressions; real competing-writer and old-session
   response checks passed. No new in-scope P0/P1 found on recheck.
3. **Scope/maintenance:** removed the component import cycle by sharing the small
   metadata validator; fixed the reproduced long-name clipping and test-runtime
   compatibility. Final diff adds two display columns to one existing table and
   direct service/HTTP/UI code. No generic CRUD, auth, operation-journal, Policy,
   Quota, history or Runtime subsystem. No new confirmed in-scope defect remains.

Overdesign / overdefense / overabstraction / scope expansion: **PASS**.

**NOT RUN:** new Linux/Docker deployment pairing, CPA Runtime/data-plane auth
qualification, Runtime apply/replace/revoke, production, External, Upgrade,
unrelated historical test matrices. The old #1007 Linux evidence is historical
and is not substituted for this slice's native process evidence.

**P0 / STOP:** the known CPA keyless authentication boundary remains unresolved;
public Gateway exposure and apply/replace/revoke remain blocked. **P1:** no new
confirmed in-scope P1. **UNKNOWN:** future staged-to-Runtime writer/revocation
safety and public Beta readiness. Independent exact-HEAD acceptance remains due.

Rollback: revert the feature while preserving the existing SQLite and data.key.
The two added columns may remain unused; do not delete staged Keys, identities or
history to roll back. No Plan files or other worktrees were modified.
