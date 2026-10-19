# Basic Fresh client key acceptance evidence

Observed on 2026-10-19 against product base
`4fd53007ecde4eab40f4890c66d46a3d6041b1ee` and this branch's implementation.
This is a private development smoke, not independent acceptance or Beta approval.

## Automated checks

- PASS: targeted Go tests for the Manager command, Fresh admission/store, SQLite
  migration, existing canonical identity repository/domain, security and new
  client key service.
- PASS: `go test -race ./internal/service/freshkeys ./cmd/cpa-manager-plus -run 'TestPersistRestart|TestFreshClientKeys' -count=1`.
- PASS: `go vet ./internal/service/freshkeys ./internal/repository/freshkeys ./cmd/cpa-manager-plus`.
- PASS: Full Web 146 tests, TypeScript, Lint (four pre-existing warnings), full
  web/panel build, module-boundary/package staging checks and `git diff --check`.

## Real Linux/amd64 process and browser smoke

Freshly cross-compiled Manager and Ingress binaries ran in an isolated Docker
Compose project, using the existing cached Alpine Manager image as a base.
This did not rebuild the standard release Dockerfile. Smoke image:
`sha256:5965e3334eef9ba499d05cba2a7ff8aa40ebf95b56e49a3ba65f23e7b25853d6`.
Only Ingress was published at `127.0.0.1:28471`; Manager had no host port.
SQLite and the protection key used this task's disposable directory.

The real host initializer, claim exchange, Admin commit and public-entry route
verification activated the Console; no routing state was injected into SQLite.

- PASS: browser rejects a wrong Admin Key, then authenticates with the initialized
  test Admin Key; the empty list comes from Manager.
- PASS: UI creates a key and displays its new plaintext once. Closing the result
  removes it from the DOM; refreshed list contains only a prefix/tail preview and
  Canonical APIKeyID. Browser local/session storage remain empty.
- PASS: after Manager commits a second creation, Playwright intentionally drops
  its real response. The UI blocks another creation and reconciles the original
  request with GET. There is no automatic POST replay or plaintext recovery.
- PASS: Manager restart and browser reload require login again and retain both
  identical Canonical IDs. Real HTTP replay returns metadata without plaintext.
- PASS: missing/wrong Admin credentials reject list/create/delete. Delete and
  replace remain unavailable. The single-key case is also covered by the Go
  HTTP test; neither a Manager count nor an internal lock claims CPA safety.
- PASS: raw SQLite inspection finds two encrypted envelopes and their canonical
  entities, zero Runtime source bindings; no full client/Admin key appears in
  Manager/Ingress logs.
- PASS: public API success/error responses use `no-store`, including through
  Full Web's outer wrapper. The original wrapper's `no-cache` override was found
  by real HTTP verification and corrected at the canonical API boundary.
- PASS: unauthenticated/invalid-key `/v1/models` stays 503 after creation and
  restart. Gateway remains Pending.
- PASS: fresh browser contexts for zh-CN/en/zh-TW/ru, 1440x1100 and 390x844,
  dark/light themes: expected translated heading, two masked rows, no horizontal
  overflow and zero application exceptions. See `layout.json`; two sanitized
  screenshots are retained here. Other screenshots were task-local scratch.

The initial locale harness incorrectly attempted repeated non-configurable
`navigator.language` overrides. Those harness errors were corrected by using
separate browser contexts; the retained evidence is from the corrected run.

## Review follow-up: F-01 and F-02

The follow-up changes only Full Web failure recovery, translations and this
contract/evidence. Manager API, SQLite and Runtime behavior are unchanged.

- PASS: Full Web 151 tests, TypeScript, Lint (zero errors, four existing warnings),
  full web/panel build, module-boundary/package staging checks and `git diff --check`.
  Regressions cover a hanging reconciliation GET, another GET after its timeout
  without another POST, same-turn logout, delayed creation response, unknown and
  reconciling results, and rejected Admin authority followed by a fresh login.
- PASS: rebuilt Full Web and cross-compiled embedded Manager on Linux/amd64 in
  the same isolated Compose environment. Follow-up image:
  `sha256:c03dfa14f4181a91ae31b94c016bb5263f3caaf2a352a0ad0e63ac4456de0623`.
- PASS: Playwright held a real committed creation response. Logout stayed disabled
  with the localized hint; releasing the response displayed the one-time secret
  and enabled logout. The screenshot was taken before releasing plaintext.
- PASS: a second real creation committed but its response was deliberately dropped.
  Logout stayed disabled while unknown. The first reconciliation GET was held at
  the browser transport boundary and timed out after 15,042 ms; the check button
  became available again. A second GET confirmed the persisted result and enabled
  logout, without another POST. Total: two deliberate creations, two checks.
- PASS: browser local/session storage empty, zero application exceptions, and
  `/v1/models` still 503. No CPA Runtime was started.
- PASS: all four translated exit hints in 390x844 and 1440x1100, dark/light;
  disabled logout and no horizontal overflow in all 16 combinations. These locale
  checks abort POST before it reaches Manager; they are UI checks, not persistence
  evidence. Sanitized screenshots: `en-mobile-pending.png` and
  `zh-CN-desktop-pending.png`.
- NOT RUN in this follow-up: local Go unit/race suites or the original complete
  restart/SQLite/security matrix; the earlier evidence above remains historical.
  No backend source changed. Browser navigation/closing the tab is not intercepted.

## NOT RUN / UNKNOWN / STOP

- NOT RUN: actual CPA Runtime, data-plane activation/auth qualification, exposed
  Gateway, standard release Dockerfile rebuild, production deployment, External,
  Upgrade, broad historical qualification matrices.
- UNKNOWN: production behavior of any future staging-to-CPA application or
  revocation path. This implementation does not provide those paths.
- STOP: the known CPA keyless authentication P0; Gateway exposure and
  revoke/replace stay disabled. Paused #1002 and CPA fork work remain untouched.
- No unresolved in-scope P0/P1 found by this self-check. Independent acceptance
  must still assess canonical staging semantics, secret protection, duplicate/
  unknown outcomes, final-key behavior and the fail-closed Gateway boundary.

## Complexity check

- Overdesign: PASS; one required metadata table shares the existing SQLite DB.
- Overdefense: PASS; guards address actual authentication, secret and duplicate
  submission risks, without claiming protection from external CPA writers.
- Overabstraction: PASS; direct repository/service/HTTP/UI composition, no CRUD
  framework or second identity/auth system.
- Scope expansion: PASS; no Policy, Quota, history, configuration, External or
  Runtime application; no Plan PR or release changes.
