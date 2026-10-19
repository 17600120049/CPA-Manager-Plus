# Private Fresh update-channel development acceptance

Date: 2026-10-19. Base: `d5bece3b39c150739da28d177ee53c84856720ac`.
Plan #54: `439b23f1c988168ed0bd7748f83204b81f3c26dc`; merged #53/#46
also checked live. This is local development verification, not independent PR
acceptance or Beta/release qualification.

## Real user operation

An isolated Linux/amd64 Docker Compose project ran freshly compiled embedded
Manager and Ingress binaries, a cached official-v8 private Runtime image,
task-specific volumes, and a new SQLite directory. Only Ingress was published
at `127.0.0.1:28683`. No existing environment or data volume was reused.
Cached Alpine image content supplied the binary host; this did not rebuild the
standard release Dockerfile. See `artifact.json` for source and binary hashes.

The actual host `cpa-manager-plus initialize` issued a one-time handoff.
Playwright exchanged it, committed Admin initialization, and activated routing
using real Manager/Ingress public-entry verification. No routing or Admin state
was injected into SQLite. Fresh remains pre-CPA-staging, Gateway Pending and
`productComplete=false`; completing CPA provisioning is outside this slice.

Observed:

- Wrong Admin Key rejected; the real initialized Admin Key opened Console.
- Read `auto`; explicitly saved `stable`, HTTP 200,
  `{"channel_preference":"stable"}`, `Cache-Control: no-store`.
- Browser reload required login and restored `stable`.
- Raw SQLite inspection confirmed `stable` in `manager_update_check_v1`.
- Restarted the real Manager container, preserving its original SQLite
  directory; reloaded the browser, logged in and read `stable`.
- Explicitly saved back to `auto`; HTTP 200 and raw SQLite readback agreed.
- All six unrelated settings rows remained byte-for-byte unchanged.
- The real background checker persisted a successful GitHub index check,
  revision 15; its timestamp survived preference saves.
- Zero staged Client Keys. Missing, wrong and synthetic invalid model credentials
  all received 503 from public `/v1/models`. Ingress regression tests also
  assert zero upstream hits while Pending. No CPA model request or Key write
  was made by this acceptance.
- No Admin plaintext in Manager/Ingress/Runtime logs. Browser local/session
  storage remained empty.

## Failure and browser evidence

- Real PUT reached Manager and committed; Playwright deliberately dropped only
  its reply. The editor issued exactly one PUT and one GET, displayed the
  actual saved `stable`, and did not claim an acknowledged save.
- With that real save committed and the first reconciliation GET stalled,
  timeout released recovery after 15,138 ms. Editing remained locked; an
  explicit second GET recovered `stable`. Total writes: one.
- Held a real successful save reply, logged out and logged back in before
  releasing it. The old reply produced no success message in the new session;
  the authoritative saved value remained correct.
- Four languages (zh-CN/en/zh-TW/ru), 1440x1100 and 390x844, dark/light:
  16 combinations had translated headings, an operable selector, no horizontal
  overflow, empty browser storage and no application exceptions.
  `layout.json` records the final matrix; two sanitized screenshots are retained.

Harness corrections: initial Bootstrap/Console locators assumed an English
label on a Chinese page; one response-body inspection ran after navigation.
Both were corrected and the successful operation was repeated. An initial
new-context layout navigation was blank, and one later pass had an aborted CSS
request while the mounted test binary was being rebuilt. The final binary was
built to a separate file, remounted in a recreated Manager using the same data,
and checked for matching host/container SHA-256. The retained complete matrix
waits for navigation/network and loaded CSS; it has no application or request
failures. The transient cause was not established. Incomplete attempts are not
counted as acceptance passes.

## Automated verification

- PASS: Manager `go test ./...`.
- PASS: `go test -race ./internal/service/updatecheck ./internal/repository/setting ./internal/service/fresh ./internal/repository/freshstore ./cmd/cpa-manager-plus`.
- PASS: targeted post-review race regression for preference/save/uncertain-store
  acknowledgement and real SQLite/Admin/restart/failure cases.
- PASS: Manager `go vet ./internal/service/updatecheck ./cmd/cpa-manager-plus`.
- PASS: Ingress `go test ./...`, including unchanged Pending/no-forwarding.
- PASS: full Web suite, 247 files / 4,025 tests; after adding the Console
  authentication integration regression, Full Web 8 files / 168 tests.
- PASS: repository suite, 24 files / 734 tests.
- PASS: TypeScript, Lint (zero errors; four existing unrelated warnings), standard
  Full Web/Panel build, module boundary/package staging checks, demo isolation,
  and `git diff --check`.
- Backend coverage includes default/saved reads, all enums, invalid/malformed
  requests, wrong/missing/non-Admin credentials, SQLite trigger-aborted writes,
  close/reopen recovery, offline remote discovery after successful persistence,
  preserved metadata/settings, concurrent checker/saves, and lost store ACK.
- Web coverage includes explicit save, authoritative GET-only reconciliation,
  malformed replies, duplicate/same-turn submissions, refresh/save races,
  timeout, unmount/late responses, and rejected authentication on reads, saves
  and reconciliation.

Task-local logs are under `bin/tmp/fresh-update-channel/20261019/` in the task
worktree. They and test SQLite/key/claim files are deliberately untracked.

## Required local review rounds

All rounds read current implementation and consumers, rather than only diff
formatting. They are local self-review and do not replace independent acceptance.

| Round | Scope and evidence | Confirmed issues and disposition |
| --- | --- | --- |
| 1: full implementation | Changed Go/Web/i18n/tests; Fresh admission/canonical auth and Full Web wrapper; service, repository, Legacy consumers; real login/save/reload/restart/restore | No remaining confirmed in-scope defect. Save/check separation and direct SQLite reconciliation verified. |
| 2: security and boundaries | Admin-only canonical API, Fresh/Legacy isolation, secrets/cache headers, SQLite failures and lost ACK, repeat/late replies; Ingress Pending tests and unchanged generation mismatch fences | No new confirmed in-scope defect. Added explicit lost-store-ACK and Console-auth integration regressions; real dropped-response/timeout/session checks passed. |
| 3: regression and complexity | Re-read final code/tests and direct consumers; targeted race/Web regressions, final packaged binary restart replay, language/layout evidence and scope inventory | PASS: final binary replayed auto → stable → browser reload/login → Manager restart/login → stable → auto. SQLite confirmed both values; unrelated rows unchanged; model requests remained 503. No new confirmed in-scope findings. |

Resolved implementation concerns: Fresh no longer inherits the ambiguous
save-then-check outcome; durable reads do not trust an old service cache; failed
preference writes cannot let a later checker overwrite a committed but
unacknowledged preference; unknown HTTP outcomes never replay PUT; obsolete
response IDs cannot update a new session. No confirmed in-scope issue remains
open at handoff; this does not claim the absence of unknown defects.

## Scope and remaining owners

Overdesign: PASS. Overdefense: PASS. Overabstraction: PASS. Scope expansion: PASS.
One setting, one existing service/store/lock, a bounded API, and a small Full Web
editor; no CAS platform, schema migration, second authority, RBAC or CPA writer.

BLOCKED outside scope: official CPA zero-effective-Key authentication P0,
production Manager-to-CPA Key apply/revoke and per-request Ingress authorization
(security consumer-contract owner); real Upgrade/External/Adoption and Plugin
Authority qualification (their existing owners); Beta/Release exact-artifact
promotion and supported-deployment matrices (Release/Readiness owner).

NOT RUN: CPA staging/installation, model forwarding, public Gateway, real
Upgrade/External/Adoption, Client Key replace/revoke, production support matrices,
standard release Dockerfile rebuild or publication. Existing v7 default and v8
opt-in image definitions were not changed. The private Runtime image was only
an isolated environment component; its use grants no new production support.

Draft / DO NOT MERGE. Independent exact-HEAD acceptance is still required.
