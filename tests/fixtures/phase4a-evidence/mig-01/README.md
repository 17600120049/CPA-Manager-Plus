# MIG-01: completed required migrations

Only Phase4A-07 / MIG-01 is qualified. Plan main is pinned to
`7eefdbf50d3596890a75e97a400dc1f5286e6498`; product base is
`v2@3f65813749a89371a3005b4a710836df587a7582`.

The independent producer is the published Linux AMD64 executable from exact
`v1.14.4@166262365d506157b99968105c890f480cc7c07c`, verified by archive,
executable, release-info and executable build metadata. Generator checkpoint
`fe5f11349d876b57c7cb6929bb270bce3a869fb6` launched and reaped that process
inside the existing network-isolated container. The producer created its own
schema and processed controlled collector, price and quota HTTP inputs. Its
persisted cache migration was naturally `completed` at revision **2**, and
quota migration was naturally `completed`. No SQL inserted or repaired migration
states. The stopped source was checkpointed and sealed through the existing
ownership/maintenancebundle contract. `source-generation/` is independent of
SRC-11 and RET-01; only their accepted exact producer identity is reused.

`runtime/` (macOS), `platform-replay/linux/` and `clean-checkout/` contain actual
invocations of qualification-only `inspectDirectCandidateReadiness` in
`apps/manager-server/tests/qualification/src11/mig01_readiness_test.go`. It executes the
maintained source inspector, schema classifier, exact artifact verifier and
matching-evidence validator. Results explicitly report
`controlledDirectCandidateReadiness=ready` for **controlled direct-candidate data
and compatibility eligibility**, and `productionBridgeReadiness=unknown`.
The default embedded preflight still reports
exact release and Bridge obligations as `unknown`; its production authority is
fail-closed. Caller-supplied qualification pins cannot authorize production.
No Upgrade API, receipt, cutover, bootstrap, UI or startup authorization was
implemented or called.

The final PR has zero product-code changes versus the frozen v2 base.
Production `LegacyInspection`, inspector calls and CLI JSON remain unchanged.
Migration details are queried by `_test.go` harness code with `immutable=1 +
query_only` and serialized separately from the existing preflight report.
`product-scope.json` pins the unchanged product trees, checked against HEAD.

The source is opened read-only. Matching validation uses its existing private,
disposable copies; v2 migrations never run on the source. Full bundle inventory,
authoritative snapshots and migration rows match before/after. SEM-01 preserves
event identity, every historical source field, NULLs, token/cost and metadata;
SEM-07 preserves quota observations/windows/cycles/snapshots, banked resets and
cooldowns. Historical Devin revision 2 is retained. The missing exact-generation
evidence guard rejects the same schema candidate without qualifying another
matrix case.

The original ZIP is hosted as an **immutable qualification prerelease**, not a
product release, and includes DB/key/config, complete generation evidence and
the exact producer archive. Pins and every member digest are in
`source-provenance.json`. Release `412855420`, asset `640737774`, tag
`qualification-mig01-d337ad0eeef2-20261014` points to the generator checkpoint.
Release API `target_commitish=main` is separately recorded as default metadata;
the actual Git tag ref is independently verified. ZIP SHA256:
`bca2e3d802b9c48e76e895418a7280e666915bebe3cd353bef17c45a3021607b`.
The existing downloaded ZIP digest was rechecked and its unchanged materialized
source replayed from a clean clone at the readiness-naming verifier checkpoint
in `clean-checkout/checkout-record.json`.

`result.json` sets `casePass=true` for the 13 completed delivery criteria, including
`controlledDirectCandidateReadinessPass`. This never records a production Bridge
readiness pass; production obligations remain `unknown`.
Each preserved runtime `result.json` keeps `casePass=false` because a runtime
invocation alone cannot establish durable delivery or independent acceptance.
**independentAcceptance=pending** everywhere. Accepted cases remain SRC-11,
RET-01 and AUT-03 (3/49), with 46 awaiting independent acceptance. MIG-02–09
remain pending. No other case or lane is advanced.

`UpgradeAuthorized=false`, `TrustedHopReceipt=false`,
`Phase4A07Complete=false`, `Phase4AComplete=false`. Lane B/E remain blocked,
Lane C retains qualification gaps, #870 remains paused and Phase4-01 inactive.

## Clean checkout replay

From a clean checkout of this branch, materialize the durable original bytes
into a fresh task-owned directory. Internet is needed only for download and
metadata; the qualification itself uses local evidence. No historical local
`/tmp` directory or expiring Actions artifact is required.

```sh
mkdir -p bin/tmp/mig01-replay/download
TAG=qualification-mig01-d337ad0eeef2-20261014
gh release download "$TAG" --repo seakee/CPA-Manager-Plus \
  --pattern mig-01-d337ad0eeef2-sealed-evidence.zip \
  --dir bin/tmp/mig01-replay/download
gh api repos/seakee/CPA-Manager-Plus/releases/412855420 > bin/tmp/mig01-replay/release.json
gh api "repos/seakee/CPA-Manager-Plus/git/ref/tags/$TAG" > bin/tmp/mig01-replay/tag.json
python3 bin/qualification/materialize-src11.py --case mig-01 \
  --release-metadata bin/tmp/mig01-replay/release.json \
  --tag-metadata bin/tmp/mig01-replay/tag.json \
  --archive bin/tmp/mig01-replay/download/mig-01-d337ad0eeef2-sealed-evidence.zip \
  --output bin/tmp/mig01-replay/original
PYTHONDONTWRITEBYTECODE=1 CPAMP_MIG01_REQUIRED=1 \
  CPAMP_MIG01_FULL_EVIDENCE="$PWD/bin/tmp/mig01-replay/original/mig-01" \
  python3 -m unittest discover -s bin/qualification -p test_mig01.py -v
python3 bin/qualification/run-src11.py mig01 \
  --archive bin/tmp/mig01-replay/original/producer.tar.gz \
  --evidence bin/tmp/mig01-replay/original/mig-01 \
  --output bin/tmp/mig01-replay/runtime
```

For Linux isolation add `--linux` and use the pinned Debian image already
required by the runner. CI extends the existing path-aware qualification
workflow: Linux/macOS execute readiness; Windows verifies materialization,
actual persisted SQLite source and compiles the harness, without claiming
Windows runtime qualification. Required PR checks remain in place.

`evidence-manifest.json` authenticates all retained evidence files except itself
and this README. Source-generation and runner JSON are byte-identical records.
Runner log files are retained as `<original-name>.txt`; generation logs have
an additional `generation-` prefix. Tests check those bytes against the original
runner's log digests. Historical absolute runner paths describe original
execution locations; replay does not depend on those paths. `verification/`
retains historical unchanged-product Manager tests/vet and preflight race logs
(bound separately in `check-record.json`), focused logs and refreshed actual
race qualification
outputs. No runtime database, key or executable is committed to Git.
