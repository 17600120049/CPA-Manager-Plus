# Phase4-03 production prerequisite audit — 2026-10-17

Product: #995, audited implementation `20d00aaa232c32758484230c5968290e2dde6129`.
Plan: [seakee/cpamp-plan#23](https://github.com/seakee/cpamp-plan/pull/23),
merged plan tree `5558a10f68cce29a1f77873b2120b92bf518a2f4`,
`active/task-v2-phase4-03-production-prerequisite-closure.zh-CN.md`.

**Qualification Gate: BLOCKED. Adopt destination Gate: BLOCKED.**
This is an evidence audit, not a qualification authority or an acceptance record.
The plan's independent acceptance approves implementation scope; it does not
accept the product's CPA artifact/profile or any resource replay capability.

## Evidence and independent acceptance are separate

| Consumer fact | Existing evidence | Independently accepted scope | Unaccepted or missing prerequisite |
| --- | --- | --- | --- |
| New Managed exact v8 artifact | r2 normal/expiry and r3/r4 reports observe the same Linux amd64 plugin archive and executable | Historical independent review accepts exact locally verified bytes and their recorded official metadata binding | No accepted New Managed production-consumer qualification, profile/limitations/invalidation binding, or build-bound authority |
| Connect External required v8 safe-read profile | r3 control run, `CPA8-EXTERNAL-SAFETY-r1`; real auth/config leaves exist | Historical review accepts bounded real config/auth and harness/denial engineering facts | No independently accepted production safe-read subset for the actual supported target/transport/config/mode combinations; deployment/proxy applicability remains unaccepted |
| Adopt `network_existing` | Same safe-read facts; existing domain resource-decision contract | No independent acceptance of native-resource read/replay/verify for this Fresh consumer | Required profile acceptance plus destination resource replay/readback/effective-state evidence and operation-owned destination execution are missing |
| Adopt `legacy_managed` | r4 `CPA8-STAGE-r1`; synthetic snapshots, startup writes and conditional restore observations | Historical review accepts only those bounded synthetic engineering facts | Required profile **and** separately accepted staging-v8 transition are missing; real legacy CPA source-to-destination transition/replay/verify is not established |

The historical independent review is
`bin/tmp/pre4a-exit/20261009-entry-review-3B89KD/independent-review.zh-CN.md`
in the main checkout, SHA-256
`ae378022a8d32455c9a3b36f2b5ddb8e1eb18e9d1ee9072171429cdf11706da5`.
It identifies its independent reviewer, exact baseline and original reports;
its X04/X06/X08 conclusions distinguish accepted engineering observations from
unaccepted profiles, actual External applicability and actual migration.
It explicitly records CPA qualification and public Beta as No-Go. Later limited
engineering-entry permission does not turn those facts into qualified profiles.

The live Phase4A compatibility ledger was also read at plan tree `5558a10f...`:
`active/issue-v2-phase4a-01-compatibility-ledger.zh-CN.md`. Its independent source
assessment concerns capability integration and explicitly retains CPA/Beta
No-Go. The archived #944 acceptance concerns the legacy schema classifier and
Bridge observations; it does not accept the full Phase4A-03 exit or a CPA v8
transition. The fixed v1.14.4 writer archive pins in `legacyartifact` identify a
**Manager writer**, not a CPA executable or a Fresh adoption profile.

## Exact original bindings

Current checked-in r5 catalogue:
`tests/fixtures/cpa-v8-qualification/manifest.json`, SHA-256
`47154799753813e47b0a159685bb3ede25b250de40dafe7991a18bb96e0f9980`.
Its candidate is declared, and its schema contains operations, profiles, fixtures
and cases, not production acceptance. Its adjacent README explicitly says
`qualification: not accepted` and `acceptedProductionConsumer: pending`.

All four accessible original reports below observe archive SHA-256
`50ecffb47fdd81c8c5a9825a73a7a905ab66342337e274f39c4276b92d3533f3`
and executable SHA-256
`b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41`.
Candidate: `CPA8-8.0.13-linux-amd64-plugin`, source
`d7914afdedca7af95ee974a42453dc49fc1388ce`.
Runtime's content-derived ID for those executable bytes would be
`sha256:b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41`;
this derivation grants no qualification. No actual destination Runtime was
observed or accepted in this audit, and the candidate archive was not rerun.

Original directories are relative to the main checkout's
`bin/tmp/pre4a-exit/20261008-1c3l3os7/`. These local artifacts are not included
in this product commit and must remain accessible for any future acceptance.

| Evidence | Original directory | Exact execution HEAD | Report SHA-256 | Manifest SHA-256 |
| --- | --- | --- | --- | --- |
| r2 normal **rerun** | `aq02-rerun/normal/output/run-VeocP0` | `27f4bf80dc7cbbca90c07cc2d3853be7c5ee9902` | `5481b5ca7ce53175bc5199f32cd6a68f5a1f29c302c9cbf3465a1a62ccfe0d92` | `758fafa41bbbf6ce58961c0a3ac3beedab2af29b18631bef3ffb4064dd3955f2` |
| r2 expiry **rerun** | `aq02-rerun/expiry/output/run-haHpzr` | `27f4bf80dc7cbbca90c07cc2d3853be7c5ee9902` | `c0cef73919b9072e76072a837603044e6381d17d9bf70fb07fdd24a09ddb0ac3` | `758fafa41bbbf6ce58961c0a3ac3beedab2af29b18631bef3ffb4064dd3955f2` |
| r3 recovered **control run** | `originals/r3-control` | `7d8c98cb93a4ca9d405f2f91504348d439655f59` | `ce1e806cf8f5a676c97c7337bef3618b2cdf1213b9b33827a90d2e3a0141e779` | `75f79bc124a5a712ac74f162b0ab8d434438863b295a79de60e92a8d7c5523ea` |
| r4 original recovered run | `originals/r4-usage-stage` | `7d8c98cb93a4ca9d405f2f91504348d439655f59` | `6add816401dad04a7e29285eece43e30abcef6625313a8e492ae24b4ef079a7c` | `3c21eb7db0d45638b9be81891277329b0c04ea941588dc90213d6d864f34896f` |

This audit recalculated these hashes, validated every report and referenced
trace with the existing `validateEvidence`, and compared recorded tool source
hashes with `git show <execution HEAD>:<source path>`. All bindings match.
This verifies retained evidence integrity, not its acceptance or applicability.
The older r2 normal/expiry and original r3 report locators in the fixture README
were not found in the inspected local evidence directories. The reruns/control
run have their own identities and must never be presented as those older runs.

r3 has two supported leaves, including `AQ03-E01-real-auth` and
`AQ03-E01-real-config`; the `AQ03-E02-*` stronger coverage is limited and the
root/prefix/private/remote/IPv6/mediation applicability leaves remain unknown.
r4 stage `AQ04-S02`–`S10` and `S11-reconcile` are limited; snapshot, lost-reply,
precommit, restore, fence, tamper and crash leaves remain unknown. These facts
cannot qualify a resource as `Migrate`. Only the minimum consumer-specific
subset needs later reconciliation; this audit does not demand a whole AQ/Beta
rerun as a substitute for identifying that subset.

## Manifest reuse decision

Reuse original r2/r3/r4 immutable manifest/report hashes and implementation
heads as evidence references, preserving original identities and limitations.
The r5 catalogue can locate profile declarations but cannot carry accepted
production facts without changing its fixture-only trust semantics.

Runtime artifact observation/selection/staging manifests bind executable bytes
and, when present, source archive digests. They contain no independently accepted
management, External or transition profile and cannot supply `CPAQualification`.
Phase4A evidence manifests and the immutable SRC-11 sealed artifact bind Manager
history/bundle evidence; they do not qualify CPA resources or a CPA executable.
Their acceptance is scoped to their own case, even when a later planning record
accepts that case. Immutability alone does not widen that scope.

**No inspected existing manifest is an accepted Fresh production qualification
authority.** No consumer record or loader is added while its required accepted
facts are missing. Once the minimum evidence subset is independently accepted,
the approved plan permits one minimal build-bound consumer record referencing
the original hashes/heads/profiles/limitations if no existing authoritative
manifest can carry them. It must remain the single effective source of truth.
This document is never loaded as authority, and tests inject only synthetic
qualification to exercise existing contracts.

## Safely deliverable change and retained blockers

`legacy_managed` now requires both the required-v8 profile and staging-v8
transition. A transition flag alone previously passed the domain gate; a negative
test reproduced that gap before the fix. `network_existing` retains its separate
required-profile gate. No Phase4-04 behavior is added.

Production application wiring retains unqualified server-owned defaults and
these exact blockers before source access or destination effects:

- New Managed: `exact_v8_artifact_qualification_required`.
- Connect External: `required_v8_profile_qualification_required`.
- Both Adopt source classes: `adoption_source_qualification_required`.
- Synthetic qualified Adopt tests: `adoption_destination_staging_required`.

New regression coverage exercises real Fresh store reopen, the authenticated
strategy handler, rejected HTTP qualification fields, unchanged durable records,
zero source requests for both classes and three strategies, repeated/reconstructed
blocked adoption, and withdrawal/mismatch of qualification for a staged managed
target despite its ready state and v8 version. Existing Fresh admission, claim,
Admin, routing, exact Runtime identity/generation/artifact and retry tests remain.
The existing Windows preflight job now includes the Bootstrap/Fresh strategy
packages and the new HTTP/reopen test in its startup filter; no new job or
qualification workflow is introduced.

Destination discovery, resource decisions, replay, semantic verification and
destination failure/restart/timeout/lost-reply acceptance **remain unimplemented
and unverified**. There is no accepted resource set with which to implement and
accept them safely in this batch. Do not equate zero-effect blocker regression
with successful destination idempotency/verification. Final independent #995
acceptance is not possible until both gates close for both source classes.

Scope check: no second qualification authority, Runtime authority, routing or
state ledger, workflow/migration framework, signatures/remote trust, history
transfer, Upgrade/rollback or product UI is introduced. Source lifecycle,
configuration, credentials, history and queues are untouched. Product behavior
changes are limited to the qualification and checkpoint/replay boundaries here.

## State-machine review follow-up

The two P0 production prerequisites above remain blocked. The subsequent review
identified two independent P1 defects, both reproduced by failing regressions:
incomplete durable strategy shapes could pass `FreshOperation.Validate`, and
the HTTP committed-replay shortcut returned success without adapter validation.

Strategy validation now recognizes only the empty initial checkpoint and the
complete shapes emitted by New Managed and Connect External. Managed records
require exact artifact identity and a positive Runtime generation; External
records require their URL fingerprint and existing generation sentinel. Unknown,
partial and currently unimplemented Adopt checkpoints fail closed. The existing
repository validator applies this on both load and update, so corrupt records
cannot reach `/complete` or be admitted after reopen. This validates structure,
not production qualification or proof of actual CPA state.

Committed HTTP retries now pass through the same strategy adapter. Managed
requests must match the observed Runtime identity/generation/artifact at every
durable stage. External URL conflicts are rejected before any source probe;
matching retries retain the bounded safe-read check. Successful committed replay
does not rewrite checkpoints, connection ciphertext or revisions, including after
product completion and real close/reopen. No second persistence authority or
general state-machine framework is added. Both new HTTP regressions and the
Fresh repository tests run in the existing Windows preflight job.

## Local verification

- Manager `go test ./...`, affected Bootstrap/Fresh/CPA/controller/processlock
  race suites (including `cmd/cpa-manager-plus`), and `go vet ./...`: PASS.
- Supervisor and Ingress `go test -race ./...` and `go vet ./...`: PASS.
- Web: 3953 tests; repository: 734 tests; workflow/classifier final check: 55
  tests; type-check, lint, build and demo isolation: PASS.
- Embedded Full Web packaged-dist test, Runtime architecture boundary and both
  Compose configurations: PASS.
- Docker daemon is unavailable locally; Docker build/E2E and Windows execution
  must be checked in the existing CI on the final pushed head. Cross-compilation
  and the previous head's green CI cannot substitute for that execution.

These checks verify the safe subset and retained blockers. They do not accept
CPA profiles, the missing loader or successful destination replay/verification.

### Windows CI follow-up

At pushed head `3149fb24781cd32e73336a7a37690814729e68cf`, the added
Fresh strategy qualification tests passed on Windows. The expanded Bootstrap
suite exposed a pre-existing negative-fixture construction defect:
`TestStrictFixtureRejectsUnknownDuplicateAndMissing` removed a member by matching
an LF-terminated line. With CRLF checkout bytes the replacement was a no-op,
so it incorrectly tested the original valid fixture as a missing-field case.
The job failed and the subsequent Fresh HTTP/reopen step was not executed.

The follow-up test change removes the JSON member independently of line endings,
asserts that removal actually changed the bytes, and exercises both LF and CRLF
for missing/unknown/duplicate rejection. Both variants and the full Bootstrap
package pass locally, including race/vet. The parser, schema, production gates
and original fixture are unchanged; no test is skipped or weakened. Windows
execution of the corrected suite and the HTTP/reopen test still requires CI on
the follow-up head.

At follow-up head `6fd49ff85fbe3eba10f05b8e593bcf24565dd380`, Windows
[job 117303368962](https://github.com/seakee/CPA-Manager-Plus/actions/runs/39000801694/job/117303368962)
passed both LF/CRLF controls and the full preflight ownership/qualification step.
The newly executed HTTP/reopen test then failed at the real store reopen with
`SQL logic error: out of memory (1)`. Fresh's checkpoint reader built
`file://C:/...`, treating the Windows drive as a URI host. A platform-independent
regression reproduced that incorrect local path before the fix. The checkpoint
reader now uses the same leading-slash handling as the existing SQLite URI
builders, retaining `mode=ro`, `query_only(1)` and its busy timeout. No fallback,
test skip, writable inspection or qualification change is introduced. Windows
execution on the corrected implementation remains required before CI acceptance.
The corrected implementation passes local Manager `go test -count=1 ./...` and
`go vet ./...`, plus Fresh service/command-entry `go test -race -count=1`.

At `1545565b116f83d92f574e9cd75f0921eed2cfd6`,
[Windows job 117309262171](https://github.com/seakee/CPA-Manager-Plus/actions/runs/39002747923/job/117309262171)
then passed the complete suite, including LF/CRLF controls and the real
HTTP/reopen test. All 22 checks on that head passed. The subsequent P1
state-machine follow-up above requires verification on its own implementation
head; the earlier green CI does not qualify that new change or either P0 gate.
