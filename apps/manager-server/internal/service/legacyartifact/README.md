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

Under Phase4A-03B-2b, `ExecuteHop` and `ValidateHop` implement the Manager-owned
controlled forward-hop execution and verification pipeline:
- Executable extraction strictly derives authentic binary bytes from verified immutable release archives.
- Process execution runs in an isolated output workspace with parent environment pollution stripped, binding directly to isolated `USAGE_DB_PATH`, `USAGE_DATA_DIR`, and ephemeral loopback `HTTP_ADDR=127.0.0.1:0`.
- Staging enforces double quiescence / consistency revalidation against input manifest digest before and after copying.
- Controlled runner polls SQLite migration checkpoints (`usage_cache_accounting_v2` and `quota_snapshot_lifecycle_v1`) and gracefully interrupts and reaps the child process upon completion.
- Post-condition inspection confirms required migration checkpoints, absence of maintenance locks, absence of pending archives, and zero inspection issues.
- Atomic signing uses a Manager-owned private HMAC authority (`0600` key file in `0700` dir) recorded into an inter-process file-locked anti-replay journal.
- `legacypreflight` consumes non-forgeable `ValidatedHopEvidence` strictly bound to the exact input manifest digest of the inspected source, rejecting cross-source mismatch.
Supervisor does not own Manager data, exact source release strictly remains `"unknown"`, and `UpgradeAuthorized` strictly remains false.

For actual published-byte qualification, download an immutable asset separately
and run the package tests with `CPAMP_LEGACY_ARTIFACT_ARCHIVE` and
`CPAMP_LEGACY_ARTIFACT_ASSET` set. This opt-in test is skipped without those
inputs; synthetic parser fixtures do not prove historical forward-hop safety.
