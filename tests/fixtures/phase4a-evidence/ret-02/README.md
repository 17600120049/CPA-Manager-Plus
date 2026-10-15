# RET-02: archived + no raw deletion

Only Phase4A-07 RET-02 is qualified here. The exact published
`v1.14.4@166262365d506157b99968105c890f480cc7c07c` executable collected four
unique events (five delivered payloads), completed its migration/aggregate
workers, and created/resumed/verified a real usage archive through its HTTP
retention endpoints. Automatic retention was disabled and delete was never
called. The stopped producer was reaped before checkpoint and bundle sealing.

The independent `ret-02/source-generation` records bind the producer archive,
executable, controlled input, generator commit, procedure, archive inventory,
raw inventory and generated bundle. The source bundle digest is
`3c63a99f32c5965b2b46a42e88a381d9dba6bfafe2eee954943b3d481f7560b3`.
RET-01's framework is reused; its database, key and bundle are not RET-02 inputs.

Linux and macOS runtime records show SEM-01/02/03/06 PASS, all four raw events
preserved, archive segments/manifests/refs verified, raw and retained projection
parity, stable historical prices, and no double counting. Importing the actual
archive records twice adds zero and skips four each time. Backup creates a real
bundle and RestoreNew restores it into a distinct root; SQLite checks,
authoritative digests, archive inventory and raw/aggregate/retained queries pass
again after recovery.

The frozen producer archive represents optional SQL NULL strings as empty
strings, and absent `auth_snapshot_at_ms`, `fail_status_code` and
`header_quota_recover_at_ms` as zero sentinels. The verifier normalizes only that
serialization boundary. Authoritative SQL snapshots preserve every original
NULL; nullable latency/TTFT, stream/generate and quota-used values remain unknown.
The profile exercises true, false and unknown metadata as well as explicit-zero
and positive token/cost/timing values.

`source-provenance.json#/sourceArtifact` pins the single immutable prerelease
qualification ZIP. It contains original source and runtime bytes, both real
backup bundles and separately labeled failed-attempt diagnostic logs. It was
packaged once and is independent of Actions artifact retention. Git stores
records, inventories and pins; SQLite, keys and logs remain in the sealed asset.
The qualification tag anchors the approved v2 baseline. Generator and runtime
commits are distinct identities recorded in their original execution records.

Clean-checkout reproduction (download happens outside the offline runtime):

```sh
gh release download qualification-ret02-3c63a99f32c5-20261014 \
  --repo seakee/CPA-Manager-Plus \
  --pattern ret-02-3c63a99f32c5-sealed-evidence.zip --dir "$TASK/download"
gh api repos/seakee/CPA-Manager-Plus/releases/tags/qualification-ret02-3c63a99f32c5-20261014 > "$TASK/release.json"
python bin/qualification/materialize-src11.py --case ret-02 \
  --release-metadata "$TASK/release.json" \
  --archive "$TASK/download/ret-02-3c63a99f32c5-sealed-evidence.zip" --output "$TASK/original"
python bin/qualification/run-src11.py ret02 \
  --archive "$TASK/original/producer.tar.gz" --evidence "$TASK/original/ret-02" \
  --output "$TASK/replay"
```

Use a fresh task-owned directory for `TASK`. On Linux add `--linux` and pull the
runner's pinned Debian AMD64 image first. Windows qualification is safe
materialization and compilation only; full SQLite replay remains unqualified.

Latest Plan main `7eefdbf50d3596890a75e97a400dc1f5286e6498` keeps acceptance
separate from runtime evidence: `runtimeEvidence=pass`, `casePass=false`,
`independentAcceptance=pending`, 3/49 accepted and 46 pending. Both upgrade
authority fields and both completion fields remain false. RET-03 and later
cases are outside this evidence.
