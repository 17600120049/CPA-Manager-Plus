# Private S2-A retained evidence — 2026-10-20

The maintainer authorized implementation of [Plan #58](https://github.com/seakee/cpamp-plan/pull/58).
Base and live remote `v2` at final preflight:
`f278dd2d6e718b393a93fa23b7a99acc87bb83a6`.
The PR's exact HEAD is the independent acceptance target. These are developer
observations, not independent acceptance or release qualification.

- [Full private Docker matrix](docker.json): `PASS_PRIVATE_S2A_CHAT`.
- [Natural permit expiry](permit-expiry.json): `PASS_PRIVATE_S2A_EXPIRED_PERMIT_DENIED`.
- [Boundary and reproduction](../../fresh-private-s2a-chat-admission.md).

Each record binds 24 executable/harness source files by SHA256 and records
immutable local image IDs. Both manifests were checked against the final source
with **zero mismatches** before commit. Images were built from that worktree
before commit; this is source-content binding, not a release build/provenance
claim. No changed executable source is omitted from the manifests.

## Observed requests

All rows below are real fresh disposable Linux/amd64 Docker observations. Counts
are deltas per scenario, not inferences from HTTP status. CPA Chat requests and
synthetic Provider arrivals are independently measured. Successful admission also
makes fresh CPA configuration/auth/readiness probes, recorded separately as
`cpaTotalArrivals`; those are not client model forwarding.

| Scenario | HTTP | CPA Chat arrivals | Synthetic upstream arrivals |
| --- | --- | --- | --- |
| Installed Canonical Key through Ingress | 200 | 1 | 1 |
| Zero configured CPA Keys, public Ingress (absent/wrong/staged Bearer) | 503 | 0 | 0 |
| Installed CPA, absent/wrong Bearer | 503 | 0 | 0 |
| Other canonical staged but uninstalled Key | 503 | 0 | 0 |
| Manager unavailable | 503 | 0 | 0 |
| CPA child stopped while Runtime endpoint remains live | 503 | 0 | 0 |
| Runtime/CPA paused | 503 | 0 | 0 |
| Original permit naturally expired, same ready Runtime generation | 503 | 0 | 0 |
| Real Manager verdict delayed 1.1 seconds before Ingress forwarding | 503 | 0 | 0 |
| New request after expired verdict | 200 | 1 | 1 |
| Missing/duplicate stream, duplicate model, stream true, other model | 503 | 0 | 0 |
| Trailing JSON, over 64 KiB, compression, chunking | 503 | 0 | 0 |
| Query, encoded path, wrong method, HTTP upgrade | 503 | 0 | 0 |
| Invalid HTTP Content-Length | 400 | 0 | 0 |
| Other model APIs / model list | 503 | 0 | 0 |
| Public internal-verdict raw/encoded/normalized paths | 404 | 0 | 0 |
| Internal path after Ingress restart | 404 | 0 | 0 |
| Wrong Key after Ingress restart | 503 | 0 | 0 |
| New valid request after Ingress restart | 200 | 1 | 1 |
| Untrusted same-bridge peer, private ports / wrong Key at Ingress | refused / 503 | 0 | 0 |
| Runtime generation changed and CPA offline | 503 | 0 | 0 |

Public verdict-path cases additionally measured **zero private Manager verdict
arrivals and zero total CPA arrivals**. Fresh and Legacy TCP tests cover both
classifiers, encoded delimiters and restart classification; the real Docker
matrix uses the Fresh application. Same-bridge peers could not connect to
8317/9081/9082/18319/18320/18321. No port was published to the host.

The zero-Key direct-CPA positive control returned 200 and reached the synthetic
upstream once per request, demonstrating the official CPA fail-open and
calibrating the passive counter. Those explicit private controls are not Ingress
admissions. After S1 installation, direct CPA absent/wrong/valid Key returned
401/401/200 and reached upstream 0/0/1 times.

The full run's Runtime identity was `embedded-auth-proof`, generation
`1114768582050550091`; after restart it changed to `5522457491549209267`, with CPA
offline. Initial-install GET/POST and the committed Strategy replay preserved
their existing **409** generation fences. The expiry run kept ready generation
`4867047546273678925`; the permit expired without renewal or any Key mutation.
JSON records retain permit/observation/request timestamps and per-scenario
arrival deltas. Host-issued permits, Management/Admin/Client credentials and
verdict bodies are never included.

Official CPA v8.0.13 executable SHA256:
`b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41`.
Runtime image: `sha256:e2320792c6f646e61999af742fd93738a686e15e0eb01623bbae3b16973a041f`.
Manager image: `sha256:38ba12fb72e5a8b594eb263187250e5cb9d2b06d7768949222407b1e3deb778e`.
Ingress image: `sha256:ec013170b93e0bbf778ee3b3898bfcc7c8289aca014d033cc118f628dfb2446a`.
The official CPA executable was neither rebuilt nor modified.

## Focused regression and affected modules

Passed:

```text
apps/manager-server: go test ./...
apps/manager-server: go vet ./...
apps/manager-server: go test -race ./internal/service/freshkeyapply ./internal/service/freshkeys ./cmd/cpa-manager-plus
apps/ingress: go test -race ./...
apps/ingress: go vet ./...
apps/runtime-supervisor: go test ./...
apps/runtime-supervisor: go vet ./...
python3 bin/ci/test_fresh_s1_docker_proof.py
git diff --check
Linux/amd64 builds of the three task-owned Docker images
Two fresh real Docker proofs above; source manifests match final executable source
```

Manager tests exercise installed-ID matching, zero/wrong/other staged Key,
changed generation/permit during observation, expired permit, missing runtime,
closed SQLite, absent Protector, failed/slow CPA auth and cancellation. HTTP
tests exercise opt-in, non-loopback rejection and uncommitted Fresh denial.
Ingress tests cover complete byte preservation, expired/replayed verdicts,
concurrent requests, restart, first-write deadline and upstream failure **after
an actual attempt** (502, one arrival, no replay). Unicode field aliases,
duplicate names, invalid encodings and exact URLs are covered. Web source and
UI are unchanged; no additional Web test or browser acceptance was claimed.

## Three self-review passes

1. **Business/request flow:** corrected the cross-process deadline comparison;
   request-bound verdicts require new live authority and preserve the original
   Client Bearer and exact body. Real 200/1/1 and negative 503/0/0 observations
   now establish the end-to-end result.
2. **Security/expiry/concurrency:** fixed Unicode case-alias ambiguity and denied
   encoded private-path delimiters; checked first-write expiry, old-verdict
   rejection, actual-attempt failure reporting, zero Key, authority outages,
   peer isolation, process restarts and identity/generation fences. Regressions
   and final Docker replays passed.
3. **Scope/compatibility/complexity:** re-read the final diff and failure paths;
   no new confirmed blocking defect after fixes. No schema/dependency/CPA writer,
   second Key ledger, background watcher, auth framework, persistent verdict,
   token issuer, Web feature, Runtime source or default Compose change. Product
   additions are the existing S1 service's admission consumer, its private HTTP
   entry and one limited Ingress path. Relay/meter complexity is test-only and
   exists to measure the explicitly requested live security claims.

## Resolved diagnostic STOPs and limits

Earlier runs deliberately remained STOP and were not counted as PASS:

| Local run | Confirmed cause | Resolution |
| --- | --- | --- |
| `a-first` | CPA stdout is discarded; Docker-log count was invalid | Passive packet counter plus direct positive calibration |
| `a-second` | Unfiltered counter dropped packets during Bootstrap | Kernel destination-port filter and zero-drop requirement |
| `a-third` | Test relay did not restart with probe namespace | Reattach/restart trusted meter and relay before testing real 409 |
| `a-complete` | Minimal Runtime image has no standalone `kill` executable | Use the existing shell builtin for child STOP/CONT |
| `a-expiry` | 12-second test permit elapsed before positive sample | New environment with one 60-second permit, then natural expiry |

Original sanitized diagnostic JSON remains under the task-owned
`bin/tmp/s2a/<run>/evidence.json`. No source boundary was weakened to pass a
proof; all resources created by every run were removed by its launcher.

**NOT RUN / STOP:** routed IPv6, external host, cross-generation recovery,
arbitrary/production Docker networks, public Gateway, default v7 Compose,
streaming, multiple installed Keys, External/Upgrade, Beta/Release. Host root,
Docker daemon and the inspected serial launcher are trusted for the bounded
proof; no general host attestation or continuous state guarantee is claimed.
The one-second observation deadline is checked before first write and cannot
retroactively cancel or erase an actual CPA arrival. Independent acceptance of
the PR's exact HEAD remains pending.
