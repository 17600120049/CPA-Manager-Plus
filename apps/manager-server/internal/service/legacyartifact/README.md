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
The archive digests match the GitHub asset digests. Executable digests were
independently computed by reading each of the six immutable published archives,
without executing their binaries, and fixed alongside archive digests in deployed
code. Both identities are compared exactly during verification and extraction;
operators cannot provide or override pins.

The JSON status is `verified-artifact-only`. `writerEvidence` and
`upgradeAuthorized` remain false. This diagnostic JSON is not a trusted
execution handle, receipt, database release identity or support allowlist.
Nothing here changes legacy preflight or writable startup admission.

## Phase4A-03B-2b: producer/validator foundation

This slice is a foundation, not a qualified execution closed-loop.
`ExecuteHop`, `DefaultCommandRunner` and CLI `--execute-hop` return
`ErrExecutionNotQualified` on every platform before opening source data,
creating authority files, staging output or starting a process.

`ValidateHop` and CLI `--verify-receipt` likewise return
`ErrValidationNotQualified` before reading any receipt, bundle, key or journal.
Public options accept no authority, journal or owner directory; CLI `--data-dir`
is removed. Key creation/loading, signing and journal operations are package-private.
A caller-owned HMAC key and a private journal cannot establish Manager ownership.
There is no qualified owner bootstrap yet, so production cannot issue trusted
`ValidatedHopEvidence`, even for an otherwise consistent receipt.

Exact v1.14.4 has no qualified offline writer entry point. Its full server
normalizes `USAGE_COLLECTOR_MODE=none` to `auto`, can restore CPA connections
from the copied database, and starts automation and unrelated maintenance.
Changing environment variables or binding HTTP to loopback does not isolate
these effects. Windows graceful shutdown/reaping is also unqualified.
No successful real published-artifact execution is claimed here.

The retained foundation provides:

- Authenticated pinned archive extraction and canonical input/output digests.
- Package-private simulated producer operations exercised directly by package
  tests, using the same strict historical capability classifier as preflight.
  No exported test runner API or production `testing` dependency is needed.
  Their receipts are fixtures, never real writer qualification.
- Absolute bundle paths, input revalidation and staged-copy equivalence checks.
- Private authority creation serialized across processes, with locked recheck,
  unique temporary files and `0600` key files in `0700` directories.
- Dedicated `<data-dir>/.legacy-hop/` private storage: `authority.key`,
  `journal.json` and their `.lock` files require `0600` on Unix; the private
  directory requires `0700`. The Manager data directory may retain `0755`.
  Existing permissive files or directories are rejected, never chmod-repaired.
- Existing-journal-only validation. Missing, corrupt, unreadable or redirected
  runtime journals fail closed for reads, writes and revocations; stale memory
  never restores lost authority. New journal initialization is explicit.
- Version 2 receipts authenticate PID, exit code, start/finish timestamps and
  duration. Producer and validator require a positive PID and start time,
  ordered timestamps, exit zero and positive duration consistent with elapsed
  milliseconds (at most one millisecond rounding difference). Issuance must be
  at or after process finish/reap; no TTL is imposed. Version 1 is
  rejected because its signature did not cover PID. These consistency checks
  do not substitute for qualified launch/exit/reap observations.
- Package-private receipt authenticity, operation record and output digest checks followed
  by current schema re-inspection. The output must still be recognized with
  the signed family and identical checkpoint evidence; bundle contents are
  revalidated after inspection. Every operation record binds receipt version and
  the complete canonical receipt SHA256, covering process, families and checkpoints
  as well as artifact and bundle identities. Delimiters inside canonical fields
  are rejected. Old journal records are rejected without fallback or regeneration.
  A successful internal result is `verified-receipt-only` with no trusted evidence.
- Reserved in-process preflight projection bound to the **output candidate** digest.
  The input digest describes where an operation began and cannot prove that
  the current candidate was written by v1.14.4.

`ValidatedHopEvidence` is process-local and excluded from JSON. The only valid
instances in this slice are created in `_test.go` projection fixtures, not by a
production validator or exported test API. There is no
production `ValidateHop -> preflight` consumer yet; diagnostic JSON must never
be accepted as a trusted handle. Original historical release remains unknown,
`UpgradeAuthorized` remains false, and Supervisor has no Manager DB/key access.

Full 03B-2 and Phase4A-07 remain pending until an exact offline execution
strategy, real published writer and historical matrix, platform-specific
shutdown/reap behavior, Manager-owned authority bootstrap, and a production
consumer have independent evidence. Full Phase4A-03 is not accepted by this slice.
The default private paths replace the previous foundation-only paths
`<data-dir>/.legacy-authority.key` and `<data-dir>/legacy-hop-journal.json`.
There is no automatic fallback, migration or regeneration during verification.
Windows ACL protection remains unqualified; cross-builds prove compilation only.

For published **artifact-byte verification only**, download an immutable asset
separately and run the package tests with `CPAMP_LEGACY_ARTIFACT_ARCHIVE` and
`CPAMP_LEGACY_ARTIFACT_ASSET` set. That opt-in test calls `Verify` and is skipped without those inputs.
`CPAMP_LEGACY_ARTIFACT_DIR` enables verification and extraction of all six pinned
archives. Neither opt-in test executes any legacy binary or qualifies a real writer.
