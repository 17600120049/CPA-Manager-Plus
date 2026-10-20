# Private Key revocation / necessary replacement evidence

Disposition: **PRIVATE SOURCE + OFFLINE CPA VERIFIED / LIVE NOT RUN / BETA HOLD**.
These are developer observations, not independent acceptance or release/image
qualification. The Draft PR's exact final HEAD is the independent review target.

Product base: `v2@bbf504557ff2e5828933f2855d94abee947dbc94`.
Authority: maintainer's explicit private implementation/write instruction and
[Plan #64](https://github.com/seakee/cpamp-plan/pull/64), merge
`ac4600cec887a937dbf6f52ea0bd50dff2971548`. Latest Plan main was also checked at
`6c217236b62e2c76558108859887ecd15d4a540d`; its separate configuration lane is
not implemented here. No configuration/wizard owner files were changed.

## Retained real observations

| Record | Result / scope |
| --- | --- |
| [KU browser + CPA](ku-browser.json) | Official v8.0.13 replacement then revocation, original operation recovery after Manager restart, final Ingress positive/negatives |
| [Browser assertions](browser.json) | Canonical selection, preparation, two authorized UI steps, reload/login recovery, list refresh; zero page errors and failed network requests; expected missing-permit 409 recorded |
| [S1 regression](s1.json) | Original private one-time installation and generation fences; Gateway remains closed |
| [S2-A regression](s2a.json) | Existing synthetic nonstreaming Chat positive/negative matrix, isolation, restart and request-time fences |
| [S2-B regression](s2b.json) | Existing fixed Provider/model admission and offline TLS fixture; real Provider/network NOT RUN |

All four Docker records use the same final Manager image:
`sha256:b1b76861d5d2bb9d85a772aa1035231485c8a51b7656ccf8682a38bb24f9416a`.
Runtime is `sha256:3fc9b926a8af2742cda13656e589dbb067c3aa3984438ea742b88c0158d686e2`;
Ingress is `sha256:2b46fb49a9fafc3e4aa7d7e0419c0b0d628e60231469090229fd15563e9b7a48`.
Each manifest was checked against final executable/harness sources with **zero
hash mismatches**. The KU manifest binds 38 source files, including new writer,
journal, UI, CSS and locales. Images were built from the worktree before commit;
this is source-content binding, not signed build/release provenance.
The official CPA executable SHA256 is
`b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41`.

The existing Key API created the independent replacement. Browser actions then
selected both canonical IDs, prepared the operation, observed missing-permit
refusal, continued `add`, reloaded/re-authenticated after an actual Manager
restart, and continued `revoke` under a fresh dedicated grant. Host inspected
all reachable source/namespace consumers before each grant. The browser never
received a CPA Management credential or supplied writer/proof flags.

| Final request through Ingress | HTTP | CPA Chat arrivals | Synthetic upstream arrivals |
| --- | --- | --- | --- |
| Replacement | 200 | 1 | 1 |
| Revoked old Key | 503 | 0 | 0 |
| Wrong Key | 503 | 0 | 0 |
| Missing Key | 503 | 0 | 0 |

Counts are measured deltas using the existing passive CPA meter and independent
synthetic upstream log, not inferred from status. The API-mode development run
also observed both Keys refused by Ingress during the two-Key transition. No
empty Key set was written. Final CPA configuration contained only replacement;
old canonical identity was `superseded`, replacement remained `active`.
The original S1 journal and encrypted canonical records were retained.

## Source verification and self-review

- Manager `go test ./...`, `go vet ./...`, `go build ./...`: passed. Final changed
  Key/CPA/HTTP paths passed race tests, including multi-consumer journal claims.
  Expanded full Manager race initially timed out in untouched `maintenancebundle`
  at ten minutes; all other packages passed. Its isolated 20-minute recheck
  passed in 812.717 seconds. [Raw focused/recheck output](race-recheck.txt).
- Ingress and Runtime: full `go test -race ./...`, vet and build passed.
- Full Web: 233 focused tests; full Web suite **4,091** tests and repository
  tooling **734** tests passed. Type check, lint and build passed. Lint retains
  four existing Accounts warnings, zero errors. Packaged Full Web Docker check
  passed. Proof helper namespace tests and Python compilation passed.
- One S2-A development run refused a positive control beyond the existing
  one-second admission window while builds/tests ran concurrently. Subsequent
  isolated and final-image matrices passed; the deadline was not relaxed.
- Required CI must be read from the Draft PR at its exact final HEAD. Local
  results do not substitute for that gate.

Three self-review rounds covered user closure; authentication, last-Key,
concurrency and unknown-result recovery; then boundaries, regressions and scope.
Confirmed issues were fixed and rechecked: submitted canonical selection and
monotonic UI recovery, retained revoked detail/install state, correction of a
rejected new selection after a completed operation, mobile form layout, and
retained history status after later operations. No confirmed unresolved blocking
finding remained at handoff.

Fault/state-machine tests use synthetic transports to exercise pre/post-intent
failure, lost replies, delayed reload, no-resend recovery, conflicting operations,
source/permit/generation mismatch, and later history. Those unit results are
**not** claimed as real CPA effect evidence; the Docker records above supply the
actual official CPA positive/negative and Manager restart observations.

## Browser layout evidence

- [Desktop dark English selection](desktop-dark-en-selection.png)
- [Desktop light English completed operation](desktop-light-en.png)
- [Mobile dark Simplified Chinese completed operation](mobile-dark-zh-CN.png)

Rendered layout was inspected. The runner checks visible operation state, no
horizontal page overflow and empty persistent browser storage. Other locale/
theme combinations have automated i18n/type coverage, not visual acceptance.

## Limits and remaining gate

Only the default-off disposable Linux/amd64 profile, fixed seed and one/two
canonical-Key configurations are qualified here. KU's final Chat uses the
existing S2-A synthetic Provider. S2-B's original offline behavior is a regression
check, not a real-Provider KU acceptance claim. Last-Key removal is refused;
direct two-Key mode has service tests and shares the real-CPA revoke step used
after replacement. This does not qualify arbitrary existing deployments.

Real Provider traffic, target network/Host isolation and human end-to-end
acceptance remain **LIVE NOT RUN / BETA HOLD**, scheduled together before the
first Beta under Plan #63. Ordinary Compose, External, Upgrade, arm64, public
Gateway and generation recovery are outside this private write authorization.
No automatic merge or Beta/release publication is performed.

See [operation contract and reproduction](../../fresh-private-key-revocation.md).
