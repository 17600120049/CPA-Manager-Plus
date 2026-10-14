# Offline Manager bundles

The offline command is:

```sh
cpa-manager-plus manager-data-bundle create --db /private/source/usage.sqlite \
  --data-key /private/source/data.key --config /private/source/config.json \
  --archives /private/source/usage-archives --bundle /private/backups/bundle \
  --source-stopped --timeout 2m
cpa-manager-plus manager-data-bundle verify --bundle /private/backups/bundle
cpa-manager-plus manager-data-bundle restore-new --bundle /private/backups/bundle \
  --target /private/recovery/new-root
# Explicit hot rollback-journal recovery, on a private copy only:
cpa-manager-plus manager-data-bundle recover-create --db /private/source/usage.sqlite \
  --data-key /private/source/data.key --config /private/source/config.json \
  --archives /private/source/usage-archives --bundle /private/backups/recovered \
  --source-stopped --timeout 2m
# After an uncertain result or a crash; use the create OR restore destination:
cpa-manager-plus manager-data-bundle recover-publication --bundle /private/recovery/new-root
```

The source must have its existing Manager ownership marker; this command never
creates one. Source config and archives are required by default. Explicit
`--require-config=false` / `--require-archives=false` permits their absence when
the data owner has established they are unnecessary. Every path remains explicit.
The command reports status, cooperating fence, file count and
`upgradeAuthorized:false`; it omits paths, file hashes and file contents.
The restored root uses the canonical `BundleLayout` filenames, so a later
authorized maintenance owner must explicitly select these paths.

`maintenancebundle` is the shared Manager-owned content inventory for maintenance
and backup tooling. `Create` requires an already held `processlock.Lock` for the
same canonical database path; the caller retains that lock until it has finished
using the source. `Inspect` and `Revalidate` are content observations without a
lock claim.

Every layout explicitly supplies database, key, external config and archive
paths. Database and key are required. Missing config and archive roots are
recorded; callers set `RequireConfig`/`RequireArchives` when these assets are
required by the data source. No automatic archive discovery or inference from
database metadata is performed. Inline/environment-only keys must first be
materialized by a separately authorized owner; this slice requires a key file.

The inventory always includes database, WAL, SHM, rollback journal, data key,
config and archive root. Nested archives include every directory (even empty
directories) and every regular file. SHA-256 covers full file contents, with
explicit absence; file permissions and timestamps are not release identity.
Non-empty rollback journals fail matching evidence with
`rollback journal recovery is not qualified for maintenance bundle`. SQLite
could roll back database pages on first open, so these inputs are never
published by create/restore. Absent or empty regular journals remain supported;
ordinary create/restore never performs rollback-journal recovery. The explicit
`recover-create` primitive supports the restricted recovery contract below;
real historical recovery qualification remains a Phase4A-07 obligation.
Manifest format **version 2** requires a deterministic aggregate `sourceRevision`
across all inventory entries. Version 1 bundles from the earlier offline
primitives (including v1 manifests with an added revision) are explicitly
unsupported by verification, restoration and source revalidation. Recreate them
from the stopped/isolated original source with this command; changing only the
manifest version does not establish matching evidence or source authority.
Backup and restore use private directories (`0700`) and
files (`0600`). Sources with symlinks in any ancestor, special files, hard-linked
files or overlapping layout/destination paths are rejected. Inputs are bounded
to 10,000 archive inventory entries and a 16 MiB manifest.

Creation copies DB, WAL, SHM, key, config and archives and checks their contents
at the following observation/revalidation boundaries. The owner must establish
external writer quiescence separately:
1. Validates the held cooperating database lock.
2. Inspects source files and computes baseline entry digests and `sourceRevision`.
3. Validates source matching evidence (SQLite header magic, page size, WAL header
   and checksum, key validity, config JSON structure).
4. Materializes copies into a private temporary directory (`.manager-bundle-unpublished-*`).
5. Re-validates the database lock.
6. Revalidates the source against the baseline manifest (content differences
   observed by `Revalidate` fail closed).
7. Revalidates the staged copy against the baseline manifest.
8. Validates matching evidence on the staged copy in a temporary inspection sandbox
   (SQLite driver `PRAGMA quick_check(1)` and `PRAGMA foreign_key_check`, production
   CPA connection JSON settings decryption with the data key, config validity,
   DB-recorded archive hashes/sizes, run contracts and maintenance locks). A
   separate writable copy must also pass the real production SQLite open/migration
   and archive reader queries. This migrated copy is discarded, never published.
9. Writes `manifest.json` with `sourceRevision` and fence `cooperating-manager-lock`.
10. Records the expected manifest in a fenced per-destination operation journal;
    flushes every staged file and, on POSIX, all staged directories from leaves
    to root. Independently verifies the complete staged manifest and inventory.
11. Revalidates the original source again AFTER the expensive matching checks,
    manifest write and staging flush, and rechecks its Manager lock. Any observed
    drift invalidates this operation's content evidence and aborts publication.
12. Verifies destination is absent and renames the complete temporary directory
    to the destination. Flushes the old and new parent metadata on POSIX, then
    durably records `committed`. Windows uses `MoveFileEx(WRITE_THROUGH)` without
    replacement; it has no portable directory fsync guarantee.

An external writer that ignores the lock can change and revert files between
observations or write after the final source observation and before publication;
matching hashes do not prove its absence. The manifest explicitly
labels the fence `cooperating-manager-lock`. Neither an operator assertion that
a source is stopped nor this label grants exact-source, forward-hop or upgrade
authority.

Destination parents must already exist and be exclusively controlled by the
caller, without concurrent destination publishers. The checks are not a
sandbox against a hostile same-user process replacing directory ancestors.
The owner must actually stop/isolate external writers before treating a bundle
as a consistent migration input. Other owners must not continue writing while
the bundle is validated or restored. This package does not provide that process
lifecycle proof.

`Verify` rejects changed, missing, extra, and unpublished temporary entries, as
well as manifests with mismatched `sourceRevision`. An incomplete bundle directory
cannot pass verification.

`RestoreNew` first verifies the bundle, stages the entire file set into a private
temporary directory, revalidates file digests and `sourceRevision`, and executes
`ValidateMatchingEvidence` on the staged copy:
- Detects and rejects corrupt SQLite databases (fails SQLite magic header,
  `PRAGMA quick_check(1)` or `PRAGMA foreign_key_check`). Any foreign key violation
  fails even when quick_check and all recorded file digests pass; inspection
  uses the foreign keys declared in the database schema.
- Requires real production SQLite open/migration on a second private writable
  copy of DB + WAL/SHM, followed by foreign-key inspection and the production
  archive run/segment readers: every persisted row is streamed through the full
  production `scanRun`/`scanSegment`, without paging or an empty-ID shortcut.
  Also compiles shared Records, RecordSegment and DeleteBatch persistence SQL
  with `EXPLAIN`, including event refs/identity ledger, delete candidates,
  raw deletion, ledger detach, ref updates and deleted daily coverage upsert.
  No event payloads are read or underlying statements executed by this
  compilation; it does not check coverage or ledger contents.
  Same-name incomplete archive tables cannot
  pass merely because the matching validator's smaller query succeeds. This
  reuses maintained migrations/adapters without comparing full schema definitions.
  Original evidence is inspected on the read-only copy; migrations never change
  the source, bundle or published restored snapshot. A legacy snapshot that
  production migration can open remains eligible for restoration.
- Detects and rejects wrong data keys when encrypted CPA connection settings
  provide evidence: reads `setup.managementKey` and
  `manager_config_v1.cpaConnection.managementKey` through the production setting
  repository and attempts authenticated decryption. The existing raw-storage
  guard rejects malformed/non-encrypted non-empty keys after connection-storage
  migration v2, while retaining pre-migration plaintext compatibility. A database
  without encrypted connection fields cannot establish key correspondence.
- Detects and rejects malformed config (invalid JSON).
- Reads every archive segment and rejects any status other than `published` or
  `verified`; an invalid status cannot filter a record out of matching evidence.
  Published segments must have no verification timestamp; verified segments
  require positive `verified_at_ms`, as written atomically by `MarkVerified`.
  For supported segments, rechecks the recorded
  `run_id/file_name` invariant and root containment, rejects non-regular files,
  and compares full-file SHA-256 and size with `content_sha256` and
  `compressed_bytes` in SQLite. Missing segments and same-name foreign content
  fail even if the manifest was recomputed. Optional archive absence cannot
  bypass a segment reference.
- Independently reads all `usage_archive_runs` manifest references, including
  when the segment table is absent, and binds required evidence to the production
  run contract and lifecycle. IDs must be 32 lowercase hexadecimal characters,
  mode must be `manual` or `retention`, and schema version/format must equal the
  production archive constants. Unknown `requested_stage` values fail closed;
  known queued stages may be satisfied/stale and handled by the production runner.
  Cross-stage runner self-healing and the broader active-run uniqueness predicate
  remain separate hardening work; this slice does not qualify those behaviors.
  `archived`/`verifying` require a manifest and at least one
  corresponding segment, all `published`; `verified`/`deleting`/`completed`
  require a manifest and corresponding segments, all `verified`. Failed runs
  apply these requirements according to their `verifying`/`deleting` resume
  stage. Missing segment tables or rows cannot bypass post-publication evidence.
  Publication requires a non-blank `archive_digest` and positive
  `archived_at_ms`. Verified stages additionally require positive run
  `verified_at_ms`; deleting stages (including failed resuming deletion) require
  positive `delete_started_at_ms`, and completed runs require positive
  `completed_at_ms`. These checks bind the snapshot to the fields written by
  the production transitions; they do not recompute archive coverage or digests.
  Unknown run/resume states, missing failed resume stages, and resume stages on
  non-failed runs fail closed.
  `previewed` forbids segment records and manifest metadata. `archiving` and
  failed runs resuming `archiving` allow optional segments, all `published`,
  and forbid manifest metadata. `cancelled` permits optional `published`
  segments and optional publication metadata because cancellation can retain
  published files. Any recorded publication field requires the complete
  manifest pair, archive digest and positive publication timestamp. Run
  verification timestamps are forbidden before verification or on cancelled
  runs. When no publication evidence is recorded, paired NULL/empty manifest
  fields remain absent. The path must remain inside its run
  directory under the archive root, and its file must be regular, single-linked,
  non-empty and at most 4 MiB, with full-file SHA-256 matching the database.
  This check does not decode the archive manifest or validate event contents,
  hash chains or complete historical coverage.
- Checks irreversible run deletion evidence: positive `deleted_event_count`
  or `last_deleted_event_id`, non-NULL `delete_started_at_ms` or
  `completed_at_ms` requires `deleting`/`completed` or failed resuming `deleting`.
  `completed_at_ms` additionally requires the actual `completed` state. Negative
  deletion counters fail closed. Changing only a completed run's status to a
  pre-publication/cancelled state cannot discard this evidence.
- Binds every `usage_maintenance_locks` row to an existing active run:
  name must be `usage_archive`, operation must match the run's actual
  `archiving`/`verifying`/`deleting` status, and lock timestamps must be positive.
  This global lock permits only one owner even if its schema PK was removed.
  Every active run requires its matching lock, even when the lock table is
  absent. Failed, previewed, archived, verified, completed and cancelled runs
  must not retain locks. Legal interrupted active stages remain resumable;
  stale locks cannot permanently block future archive or compact operations.
- Inspection query, scan, row-iteration, filesystem and context cancellation
  errors fail closed; an absent optional table is distinct from inspection failure.
- Detects asset mismatches supported by these checks and bundle modifications
  observed during restoration. Matching metadata is integrity evidence, not
  provenance/authentication; a coherently replaced database and assets cannot
  authenticate a historical instance. Full archive decoding/event validation
  remains outside this slice.

Only after matching evidence passes and the source bundle is re-verified does
`RestoreNew` publish the restored file set via atomic rename into a previously
absent root. It refuses to replace a live or existing root. Missing sidecars and
optional roots remain absent, and all present roots come from the same bundle.
The restored root now also carries `manifest.json`, so it can be verified before
an authorized owner adopts/opens it. A target inside, containing, or aliasing the
source bundle root is rejected before any journal or staging write. Restoration
does not retain writer authority or authenticate a historical source. Starting a
writer on a restored root changes its content and may invalidate this offline
manifest; do not reconcile publication concurrently with writer adoption.

### Publication, cancellation and restart

Each destination has a private sibling `.manager-bundle-operation-<digest>/`
containing a `processlock` fence, bounded `state.json`, and its recorded staging
directory. These journals contain content hashes and must receive the same
private placement/ACL protection as backups. They are integrity/operation state,
never trusted upgrade receipts. The parent remains exclusively owner-controlled;
the fence is not a hostile same-user directory-replacement sandbox. Destination
publishers must use one canonical path and must not publish concurrently through
case aliases. Journal directories are retained for explicit restart inspection.

Cancellation and ordinary failures BEFORE rename remove only the recorded
staging directory and record `aborted`. Cancellation AFTER rename finishes the
commit; it does not roll back a complete published target. Flush/journal errors
after rename return `ErrPublicationUncertain`, and the CLI explicitly requests
`recover-publication`. A target may already exist: never infer that any error
means publication did not happen, and never delete it merely to retry.

`recover-publication --bundle DESTINATION` acquires that operation's fence and:
- With no target and a valid pending/aborted record, removes only that exact
  recorded staging directory and records `aborted`. Create/restore can retry the
  same target; they also reconcile a valid interrupted absent-target operation.
- With a target, requires the complete manifest to equal the recorded expected
  manifest, validates matching evidence, flushes and rechecks it, then records
  `committed`. It never replaces a target or reruns database migrations on it.
- Refuses unknown/corrupt journals, missing committed targets, mismatched or
  partial targets, symlink ancestors and unexpected targets for aborted records.
  Such failures require owner inspection; no broad cleanup or adoption occurs.

Public `Verify`/restore refuse unpublished/operation roots and their descendants,
even if staging already contains a fully written manifest. Filesystem failures
can leave recoverable staging; unknown unrecorded artifacts are quarantined by
refusal, never deleted by age or pattern. Simulated process crashes and flush
errors establish these software boundaries. They do not establish physical
power-loss durability across all disks, filesystems, VMs or storage controllers.

### Explicit rollback-journal recovery

`RecoverCreate`/`recover-create` requires the existing matching Manager fence,
explicit stopped/isolated source, nonempty rollback journal, and no nonempty
WAL/SHM. It inventories and copies the complete file set, revalidates both copies,
and opens ONLY the private copy with SQLite `mode=rw`, `synchronous=FULL`.
Recovery does not run production migrations or delete an input journal by hand.

Before writable open, a streaming format gate checks synced headers, consistent
page/sector geometry, declared record bounds, page numbers and SQLite's native
page checksums. An unsynced final header is supported only for complete frames
whose full page preimages still equal the on-disk database pages. No-sync counts,
partial/truncated records, unsupported headers, mixed WAL and super-journals are
refused. This avoids treating SQLite's silent discard of invalid journal records
as successful recovery. Native journal checksums are not authentication or a
complete corruption detector; exact historical/producer qualification is separate.

SQLite must consume the journal itself. The recovered copy must pass full
`integrity_check` and the existing key/config/archive/foreign-key/production-open
matching gates before sealing. The original dirty database and journal remain
unchanged. The resulting format-v2 manifest adds optional `recovery.method` and
`recovery.sourceRevision`; `sourceRevision` describes recovered bytes. Older
readers reject the added recovery field; ordinary v2 bundles remain readable by
both versions. The raw pre-recovery revision is transformation metadata, not
provenance. `RevalidateSource` with the recovered manifest cannot authorize the
different dirty source. Consumers needing that source must retain and revalidate
its independently captured original inventory and ownership contract.

Linux and Windows CI execute the bundle service and CLI, including real WAL,
hot-journal recovery, external mutation/reap, cancellation and process-kill tests.
Windows additionally exercises non-delete-sharing handles against rename,
replacement and delete. Runtime evidence must come from the actual successful
job's tested SHA/environment and JSON artifact; workflow presence and successful
cross-compilation alone confer no qualification. See [BOUNDARIES.md](BOUNDARIES.md)
for Phase4A-04 and Phase4A-07 Lane C dependency status and remaining gates.
