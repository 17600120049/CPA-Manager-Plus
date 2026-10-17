# Fresh production consumer record

`consumer.json` is the sole build-bound Fresh qualification authority. It is
needed because the original immutable AQ manifests describe evidence and
fixture profiles, not independently accepted Fresh production consumers. The
record references those original hashes/runner HEADs; it does not replace their
reports, create a second AQ ledger, or grant whole-profile/Beta qualification.

**All four consumers are pending. No production qualification is granted.**
The empty case sets, acceptance hashes, limitations and implementation hash
are deliberate missing acceptance facts, not placeholders interpreted as PASS.
The current record preserves the candidate and evidence identities from the
Phase4-03 audit. No fixture, CI job, request, environment variable or Runtime
observation can change it in a running Manager.

To accept a consumer, independent review must first identify the exact original
case subset and explicitly accept its applicability to that consumer's actual
implementation and supported target/transport/config/mode combinations. Accepting
an observation or an AQ profile name alone is insufficient. Any accepted limits
must be consistent with the implementation; text cannot impose an unenforced
runtime restriction. Review must separately accept legacy staging transition.

Only then may the reviewed record name `status: accepted`, its original profile/
case/report/manifest/runner-HEAD bindings, independent review SHA-256, explicit
accepted limitations and invalidation conditions. `implementationSha256` binds
the compiled Fresh strategy/loader/adoption source bytes and the v8 safe-read/
destination verification source
digest; changing them requires renewed acceptance. It is independent of the
original evidence execution HEADs and avoids a commit containing its own SHA.

Referenced original manifests, reports, selected sanitized traces and the
independent review must be included unchanged under
`evidence/<sha256>.json` or `evidence/<sha256>.md`. The loader verifies the
actual bytes, report-to-manifest binding, observed archive/binary hashes,
resulting Runtime ArtifactID, exact runner HEAD, profile/case/run/trace bindings
and completed passing evidence. It does not rerun AQ or infer acceptance from
those observations. Evidence unavailable in the build, malformed canonical
record bytes or any binding mismatch grants no qualification to any consumer.
Git attributes preserve these bytes on Windows.

The four consumers are independent: New Managed, External, network adoption and
legacy adoption. Accepted source qualification still grants no resource replay,
destination ownership, Gateway activation or product completion. Both adoption
consumers additionally reference the existing immutable `CPA8-STAGE-r1` manifest.
Their optional `adoptionEvidence` names three distinct accepted cases in that
profile: `readCaseRef`, `replayCaseRef` and `verifyCaseRef`. Independent review must
accept those exact cases for the bounded `client-api-keys` resource and this
implementation. Only that explicit mapping yields its `Migrate` decision and
exact destination ArtifactID. An External safe-read profile cannot supply it.
Legacy transition acceptance remains separate from network adoption acceptance.

All current entries still omit `adoptionEvidence` and remain pending. Missing
destination capability retains `adoption_destination_staging_required`; accepted
source-only consumers with an available destination return
`adoption_resource_qualification_required` before source discovery. Test-only
synthetic acceptance is constructed in memory and is never part of this directory
or the production binary. Controlled real-CPA CI observations are review inputs,
not authority files and not automatic production acceptance.


## Current acceptance-repair inputs

The bounded destination execution chain is pinned by
`runtime_destination_implementation.sha256`. The regression test recomputes
that digest over the Manager Runtime destination client/model and the Supervisor
destination/start/restart/recovery/private-protocol source files. The pin is part
of `consumerImplementationSHA256`, so changing that execution chain invalidates
any later accepted consumer record.

Controlled real-process CI for `legacy_managed` must use a pinned real pre-v8
CPA source that remains live and byte-preserved while the exact v8 candidate
runs only against an isolated staging copy. Merely labelling a v8 process as
`legacy_managed` is not transition evidence.

All consumers remain pending until the exact-head observations from these
repairs are independently reviewed. CI success alone never writes acceptance.
