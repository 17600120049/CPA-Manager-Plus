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
and backup tooling. It does not open SQLite or acquire a Supervisor authority.
`Create` requires an already held `processlock.Lock` for the same canonical
database path; the caller retains that lock until it has finished using the
source. `Inspect` and `Revalidate` are content observations without a lock claim.

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
Backup and restore use private directories (`0700`) and files (`0600`). Sources
with symlinks in any ancestor, special files, hard-linked files or overlapping
layout/destination paths are rejected. Inputs are bounded to 10,000 archive
inventory entries and a 16 MiB manifest.

Creation hashes the source, copies it, rehashes both source and copy and validates
the held lock before publication. Detected source/lock changes, cancellation and
copy failures prevent publication. This establishes consistency only under the
cooperating Manager lock and the owner's offline source isolation. An external
writer that ignores that lock can change and revert files between observations;
matching hashes do not prove its absence. The manifest explicitly labels the
fence `cooperating-manager-lock`. Neither an operator assertion that a source
is stopped nor this label grants exact-source, forward-hop or upgrade authority.

Destination parents must already exist and be exclusively controlled by the
caller, without concurrent destination publishers. The checks are not a
sandbox against a hostile same-user process replacing directory ancestors.
The owner must actually stop/isolate external writers before treating a bundle
as a consistent migration input. Other owners must not continue writing while
the bundle is validated or restored. This package does not provide that process
lifecycle proof.

`Verify` rejects changed, missing and extra entries. Its manifest is editable
integrity metadata, not an authenticated execution receipt. `RestoreNew` first
verifies every file, stages and revalidates the entire DB/sidecar/key/config/
archive set, then publishes it by one directory rename into a previously absent
root. It refuses to replace a live or existing root. Missing sidecars and
optional roots remain absent, and all present roots come from the same bundle.
Restoration does not retain writer authority or authenticate a historical source.

Files and the manifest are synced before publication. Final directory metadata
fsync, in-place live-file replacement, power-loss recovery, real historical
SQLite/archive qualification and non-cooperating writer fencing remain later
Phase4A-04/07 obligations. Abrupt termination before rename may leave a private
`*-unpublished-*` directory, which is not a published bundle. Cancellation and
ordinary failures remove only the temporary directory created by this call.
