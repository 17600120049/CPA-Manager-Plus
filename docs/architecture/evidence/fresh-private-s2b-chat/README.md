# Private S2-B source and offline verification

**Overall: Draft / LIVE NOT RUN / HOLD. Not S2-B PASS. Not merge-ready.**

Implementation baseline: `v2@5ae3ea874fe241cda093402ca7aefa99ca516b8a`. Merged dependencies #1017, #1019 and #1022 were verified against live GitHub metadata and current source. The source checkout's unrelated Compose changes and other worktrees were preserved. All implementation and disposable verification used `feat/v2-s2b-private-real-model-chat` in its own worktree.

The maintainer explicitly moved real Provider/network acceptance to the accumulated pre-first-Beta live queue. The approved S2-B decision and Plan #62 still apply; **the Plan's existing real-LIVE-before-merge gate has not been removed**. No Plan files, UI, wizard, normal Compose, Runtime source/seed, release artifacts or CPA sources were edited.

## Verification results

| Check | Result / scope |
| --- | --- |
| Manager `go test ./...` | PASS, 100 tested packages; subsequent affected-package checks repeated after self-review fixes |
| Manager `go test -race ./internal/service/cpa ./internal/service/freshkeyapply ./internal/service/freshkeys ./cmd/cpa-manager-plus` | PASS on final product code |
| Additional CPA seed/source fallback regressions under race | PASS |
| Manager `go vet ./...` and command build | PASS on final product code |
| Ingress `go test -race ./...`, `go vet ./...`, command build | PASS on final product code |
| Runtime `go test ./...`, `go vet ./...`, command build | PASS; Runtime source unchanged |
| Manager/Ingress/private Runtime Linux/amd64 Docker builds | PASS; unchanged Dockerfiles and checksum-pinned official CPA |
| Host namespace-reference unit regression | PASS |
| S2-B offline Docker + natural permit expiry | PASS_OFFLINE_S2B_ONLY_LIVE_HOLD / PASS_OFFLINE_S2B_EXPIRY_ONLY_LIVE_HOLD |
| S1 and S2-A Docker regressions, including natural expiry | PASS_PRIVATE_S1_GATEWAY_STOP / PASS_PRIVATE_S2A_CHAT / PASS_PRIVATE_S2A_EXPIRED_PERMIT_DENIED |
| Source-content manifests and credential/evidence checks | Checked before commit; no real credentials were provided |
| Agent A pages/Console/wizard and unrelated checkout preservation | PASS, no edits |
| Real Provider response / approved model / credential | LIVE NOT RUN / HOLD |
| Controlled fixed-target external egress and its new inbound/denied-target matrix | NOT IMPLEMENTED / NOT RUN / HOLD; no new network topology was created |
| Real-environment failure/recovery/negative matrix | LIVE NOT RUN / HOLD |
| Public Gateway, ordinary Docker, External/Upgrade, Config B1, Key K/U, Beta/Release | STOP |
| Independent exact-HEAD review and Required CI | Pending; this record is developer verification |

Local command output remains in the task-owned ignored `bin/tmp/s2b-20261020/`. Final JSON records retain per-case timestamps/statuses, independently measured CPA Chat arrivals, offline stub arrivals where available, permit/generation/process/namespace observations, local immutable image IDs and relevant source SHA256 manifests. Image builds precede the commit; matching source-content manifests bind the tested source. These local images are not release provenance.

- [Final offline S2-B Docker record](docker-offline.json): 40 named scenarios, including one successful TLS stub call and one upstream 503 attempt; all admission denials have zero CPA Chat arrivals.
- [Final offline S2-B natural-expiry record](permit-expiry-offline.json): original permit expires with the same Runtime generation still ready; denied request has zero CPA Chat arrivals.
- [S1/S2-A regression records](regressions.json): all substantive case/arrival records, source manifests, permit/generation and shared immutable image identities; routine intermediate HTTP polling is omitted. Full raw runs remain in the ignored task directory.

All five disposable runs exited successfully; task containers, volumes and networks were removed. All retained source manifests match the final source. Credential-pattern scan and local documentation-link checks passed. The normal private CPA/Runtime ports remain loopback-only with no published ports in these **internal-network** runs. This does not establish real outbound-network isolation.

## Coverage and limits

The offline profile installs its single Client Key through the original Canonical/S1 writer into official CPA v8.0.13, then performs the unchanged absent/wrong/valid `/v1/models` AuthSweep. A TLS server at `https://s2b-provider.invalid/v1` checks the injected **fake** credential, exact model/path and nonstreaming request. The server returns an explicitly labelled local-stub response. This validates CPA/provider integration and authentication mechanics, **not a real model**.

The passive loopback meter sees actual CPA HTTP request starts; it retains only counters and refuses a run with packet drops. All applicable offline admission negatives require CPA Chat arrival delta **0**, including absent/wrong/canonical-shaped-wrong Key, Manager-only staged Key, zero-CPA-Key preinstallation, changed generation, Manager unavailable, CPA unresponsive with Runtime live, Runtime/CPA unavailable, expired Host permit, delayed/replayed/cross-body verdicts, wrong model, stream true, duplicate/escaped/Unicode-folded/nested fields, invalid UTF-16, trailing/oversized/compressed/chunked JSON, wrong method/query/encoded path/Upgrade/length, public private-verdict routes and untrusted network access. A bad Runtime token returns 401.

The admitted stub request has **1 CPA / 1 stub arrival**. The injected upstream 503 also has **1 CPA / 1 stub arrival**, verifying that neither Ingress nor CPA replays it. A sent request's 503 is an upstream attempt, not a pre-admission denial. Natural expiry is exercised with the same authenticated Runtime still ready. Restart changes generation and keeps CPA offline; the old install/status/Strategy calls retain 409 while Chat has zero CPA arrivals. No generation recovery or reinstall is attempted.

Unit/race tests separately cover Provider/target/model/credential drift, extra fields/Providers/models/credentials, nonempty initial Client Keys, missing Host source, blank/synthetic fallback refusal, same-run fixture retargeting across Manager restart, conflicting Manager/Ingress model projection, expired/replaced permits during observation, corrupt/unavailable Protector or SQLite, single install under concurrency, unknown write/no retry and short verdict expiry after dial/before first CPA byte. These are explicitly unit-test observations, not real-environment acceptance.

The CPA-zero-Key Docker cases run **before installation**. Post-install zero/unknown Key state is covered by service-level negative tests; the Docker proof deliberately does not remove the installed last Key or add a second writer just to create that state. Real-LIVE negative qualification must name each actual setup/result and retain its pre-first-byte counter evidence.

## Three developer self-review rounds

1. **Function / seed / response:** traced the original S1 writer, current official CPA projection and AuthSweep into S2-B. Confirmed exact Provider credential/target/model fields, empty initial Client Keys, successful original installation, TLS stub response and disabled CPA retry. Real response and actual external target remain NOT RUN.
2. **Authentication / security / concurrency / failure:** fixed missing persistent fixture binding by extending the existing journal with a nonsecret hash; no second ledger/schema. Fixed missing-source fallback, bounded duplicate-field scanning, and Manager/Ingress model-conflict refusal before CPA. Added same-run/restart retargeting and model-projection regressions. Checked per-request read/auth/recheck, source/key/permit/generation fences, replay/expiry, private-route denial, packet-count evidence and upstream 503 no-retry.
3. **Complexity / compatibility / ownership:** reused the same parser, transport, journal, Host permit, Admission and Docker launcher. Shared raw-request tests take the single body explicitly; replay controls exist only in disposable loopback instrumentation. No second writer, general proxy/config/auth platform, new dependency, UI edit, CPA fork or new external-network service. Rechecked S1/S2-A and final affected-package checks. No new confirmed defect remains in the implemented source/offline scope; live/network gaps remain explicit blockers.

Earlier diagnostic `offline-docker-01` stopped at its final restart measurement because the local TLS stub had intentionally stopped with its network-namespace consumer. The instrument tried to read that stopped stub. The fixture was corrected to check the restarted independent CPA meter after the generation fence instead. Its earlier assertions were not relabelled a completed PASS. Subsequent full runs use the corrected harness; final image/source changes are rechecked with their own manifests.

## Remaining gate

Before S2-B can pass, choose and lock the actual Provider/model/HTTPS target, inject its credential only from the trusted Host before CPA start, implement and verify the temporary fixed-destination outbound arrangement, and complete the real response plus applicable real-process security negatives with independent arrival evidence. No offline/internal-network result in this directory substitutes for that work. Keep Draft/HOLD until the Plan gate, latest-HEAD independent acceptance and Required CI are satisfied. No merge or release was performed.
