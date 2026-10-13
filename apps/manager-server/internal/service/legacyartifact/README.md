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
controlled forward-hop execution and verification pipeline. The executor verifies
the executable bytes, executes in an isolated output workspace, cleanly waits and
reaps the process, verifies required migration checkpoints, seals the output bundle,
and signs an authenticated `ExecutionReceipt` with anti-replay journal protection.
Supervisor does not own Manager data, and `UpgradeAuthorized` strictly remains false.

For actual published-byte qualification, download an immutable asset separately
and run the package tests with `CPAMP_LEGACY_ARTIFACT_ARCHIVE` and
`CPAMP_LEGACY_ARTIFACT_ASSET` set. This opt-in test is skipped without those
inputs; synthetic parser fixtures do not prove historical forward-hop safety.
