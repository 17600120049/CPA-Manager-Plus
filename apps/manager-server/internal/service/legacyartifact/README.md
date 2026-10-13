# Fixed legacy writer artifact verification

`cpa-manager-plus legacy-writer-artifact --archive PATH --asset NAME` verifies
a locally downloaded native archive from immutable `v1.14.4`, without extraction,
network access, config discovery, database access or execution. All six published
native assets are pinned in deployed code. The archive is bounded, read once and
hashed before inspecting its exact executable bytes; unsafe, duplicate, missing,
oversized and corrupt entries fail closed. Cancellation produces no evidence.

Pins were checked against GitHub release `404941931` and its immutable
`release-info.json` digest
`b8f7b97cd242c11d95b905e2b402275fc5d664025bc829773a260d1da17d18b5`,
which names `v1.14.4@166262365d506157b99968105c890f480cc7c07c`.
The exact archive digests are the GitHub asset digests; they are not supplied
by the operator or inferred from an archive's name or executable `--version`.

The JSON status is `verified-artifact-only`. `writerEvidence` and
`upgradeAuthorized` remain false. This diagnostic JSON is not a trusted
execution handle, receipt, database release identity or support allowlist.
Nothing here changes legacy preflight or writable startup admission.

## Phase4A-03B-2b: producer/validator foundation

This slice is a foundation, not a qualified execution closed-loop.
`ExecuteHop`, `DefaultCommandRunner` and CLI `--execute-hop` return
`ErrExecutionNotQualified` on every platform before opening source data,
creating authority files, staging output or starting a process.

Exact v1.14.4 has no qualified offline writer entry point. Its full server
normalizes `USAGE_COLLECTOR_MODE=none` to `auto`, can restore CPA connections
from the copied database, and starts automation and unrelated maintenance.
Changing environment variables or binding HTTP to loopback does not isolate
these effects. Windows graceful shutdown/reaping is also unqualified.
No successful real published-artifact execution is claimed here.

The retained foundation provides:

- Authenticated pinned archive extraction and canonical input/output digests.
- Test-only simulated producer operations guarded by `testing.TB`, using the
  same strict historical capability classifier as preflight. Their receipts
  are fixtures, never real writer qualification.
- Absolute bundle paths, input revalidation and staged-copy equivalence checks.
- Private authority creation serialized across processes, with locked recheck,
  unique temporary files and `0600` key files in `0700` directories.
- Existing-journal-only validation. Missing, corrupt, unreadable or redirected
  runtime journals fail closed for reads, writes and revocations; stale memory
  never restores lost authority. New journal initialization is explicit.
- Receipt authenticity, operation record, output digest and required checkpoint
  validation. CLI verification reads an existing authority and never generates
  a replacement key.
- In-process preflight projection bound to the **output candidate** digest.
  The input digest describes where an operation began and cannot prove that
  the current candidate was written by v1.14.4.

`ValidatedHopEvidence` is process-local and excluded from JSON. There is no
production `ValidateHop -> preflight` consumer yet; diagnostic JSON must never
be accepted as a trusted handle. Original historical release remains unknown,
`UpgradeAuthorized` remains false, and Supervisor has no Manager DB/key access.

Full 03B-2 and Phase4A-07 remain pending until an exact offline execution
strategy, real published writer and historical matrix, platform-specific
shutdown/reap behavior, and a production consumer have independent evidence.

For published **artifact-byte verification only**, download an immutable asset
separately and run the package tests with `CPAMP_LEGACY_ARTIFACT_ARCHIVE` and
`CPAMP_LEGACY_ARTIFACT_ASSET` set. That opt-in test calls `Verify`, not
`ExecuteHop`, and is skipped without those inputs.
