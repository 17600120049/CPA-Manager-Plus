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
   (SQLite driver `PRAGMA quick_check(1)`, production CPA connection JSON settings
   decryption with the data key, config validity, DB-recorded archive hashes/sizes).
9. Writes `manifest.json` with `sourceRevision` and fence `cooperating-manager-lock`.
10. Verifies destination is absent and atomically renames the temporary directory to
    the destination.

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
- Detects and rejects corrupt SQLite databases (fails SQLite magic header or
  `PRAGMA quick_check(1)`).
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
  For supported segments, rechecks the recorded
  `run_id/file_name` invariant and root containment, rejects non-regular files,
  and compares full-file SHA-256 and size with `content_sha256` and
  `compressed_bytes` in SQLite. Missing segments and same-name foreign content
  fail even if the manifest was recomputed. Optional archive absence cannot
  bypass a segment reference.
- Independently reads all `usage_archive_runs` manifest references, including
  when the segment table is absent. If either `manifest_file` or `manifest_sha256`
  is recorded, both are required. The manifest path must remain inside its run
  directory under the archive root, and its file must be regular, single-linked,
  non-empty and at most 4 MiB, with full-file SHA-256 matching the database.
  Paired NULL/empty fields remain optional before publication. This check does
  not decode the archive manifest or validate event/run metadata semantics.
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
Restoration does not retain writer authority or authenticate a historical source.

Files and the manifest are synced before publication. Final directory metadata
fsync, in-place live-file replacement, power-loss recovery, real historical
SQLite/archive qualification and non-cooperating writer fencing remain later
Phase4A-04/07 obligations. Abrupt termination before rename may leave a private
`*-unpublished-*` directory, which is not a published bundle. Cancellation,
disk-full (ENOSPC), and ordinary failures remove the temporary directory and
never publish an incomplete bundle.

Windows amd64 cross-compilation establishes build compatibility only. The
existing `Manager Server SQLite (Windows)` CI job exercises SQLite/preflight
paths, not `maintenancebundle` or `managerdatabundle`; bundle create/restore,
SQLite URI and archive path checks still lack Windows runtime qualification.
