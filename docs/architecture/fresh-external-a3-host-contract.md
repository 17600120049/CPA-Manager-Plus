# Fresh External A3 — private Host admission and recovery

Authorization: merged private CPAMP Plan #77. This document describes a **default-off**, bounded source-code consumer, not a general installation or public deployment procedure. Source-merging does not grant live Host or Beta eligibility.

## Ownership and authorization

Only a separately approved trusted Host/launcher for the single disposable private Fresh External profile may issue a permit at `/run/cpamp-a3/permit.json`. The Host must own the exact Manager installation, control its process and mount namespaces, hold the exclusive original operation record, and verify ingress/CPA source isolation. Ordinary Docker, arbitrary public External installations, browser-provided identity, S1/H1 grants, or `CPAMP_PRIVATE_A3_PROFILE` alone confer **no** authority.

The Manager requires **both** `CPAMP_PRIVATE_A3_PROFILE=plan77-private-fresh-external` and a separate root-owned regular 0600 A3 permit (Linux/amd64, no symlink, strict JSON). The private Host must issue all fields from the *observed original installation*; never trust the browser for values. `runId` is a Host-owned private run identifier at least 16 characters long. The exact permit contract is:

```json
{
  "scope": "plan77-private-fresh-external",
  "runId": "trusted-host-run-id-at-least-16",
  "admissionId": "from-original-fresh-identity",
  "installationId": "from-original-fresh-identity",
  "generation": "from-original-fresh-identity",
  "databasePath": "from-original-fresh-identity",
  "operationId": "from-original-fresh-identity",
  "issuedAtMs": 0,
  "expiresAtMs": 0,
  "expectedRevision": 0,
  "intentDigest": "64-lowercase-hex-host-intent-commitment"
}
```

The Host must bind the original *complete* request **before enabling the browser POST**. `expectedRevision` is the original Fresh revision. `attemptId` is a fresh, randomly generated 128-bit lowercase hexadecimal value, created only in browser memory when the user clicks once and sent in that `POST strategy`. The Host's `intentDigest` is the lowercase SHA-256 of the UTF-8 bytes `"cpamp-a3-intent-v2\\0" + runId + "\\0" + decimal(expectedRevision) + "\\0" + normalizedBaseURL + "\\0" + managementKey + "\\0" + attemptId` (each `\\0` represents one NUL byte). The source URL must already follow the exact CPA v8 normalization rules.

The Host commitment binds the **specific attempt**, not just URL and Key: after a browser refresh a different random attempt ID cannot consume the first grant even with otherwise identical input. The attempt ID is never stored in browser storage, GET Operation, Manager checkpoints, or logs. Its content alone conveys no authority; only a trusted Host-issued, installation-bound permit for the original intent can authorize a submission. A verbatim replay carrying the *same* attempt ID cannot be distinguished before the first CAS, but the durable exclusive CAS still prevents a second CPA read or connection write. The permit file has **no cryptographic signature**; root ownership, file permissions, strict contents and the private profile gate are its source-level checks, not proof of issuer provenance. A trusted Host pre-dispatch handoff and issuance flow that can bind the browser's original attempt ID is **not yet implemented or accepted**. Until that exists, keep A3 default-off and STOP; do not replace the missing Host witness with an untrusted browser claim.

Both timestamps are Host-provided Unix milliseconds: issued in the past, expires in the future, lifetime no more than ten minutes. Zero timestamps in this schema are **illustrative invalid placeholders**, not issuance guidance. Issuance, evidence of exclusive Host ownership, scoped read-only mounting, and revocation are **not** automated by this product PR. Do not enable the profile without a verified and independently accepted Host owner. Remove/unmount the grant to close admission; no fallback to S1/H1.

## One attempt and recovery boundary

Before accessing the external CPA or persisting a Management Key, Manager checks the original attempt ID format, canonical/fixed session authority, the compiled production consumer, static Management Key/URL shape and original-intent-bound A3 Host admission, then CAS-reserves the single Fresh strategy as `connect_external / validated` with the normalized URL SHA256 target. This is a durable **attempt fence**, not successful proof of an External connection. An external source read failure, timeout, process death, permit loss, or unknown response leaves that operation STOP and blocks any new POST, including with a changed target/key. No automatic rollback, replay, or reset is authorized.

Successful bounded CPA v8 direct GET, Protector encryption, and final CAS transition the same operation to `committed`. For browser confirmation, a secure context with Web Crypto SHA-256 and cryptographically secure random bytes is required; the Console holds its shared submission lock from the original click through the async digest and POST. A failed local preparation that never sent a POST releases that lock; once a POST may have been sent, UNKNOWN remains terminal for this browser. Unsupported or insecure origins refuse the initial POST rather than guessing a target digest. Only an acknowledged POST *and* a subsequent matching authenticated GET during the same browser attempt may be displayed as Manager encrypted persistence confirmation. A GET-only `committed` record can be displayed but does not identify the submitted Management Key or resolve an earlier lost POST; the original Host must reconcile the record before any separate authorized follow-up. HTTP 401/403 retires browser Admin authority; other 409/503/transport errors never initiate a second write.

External never calls ProductComplete and the canonical server `POST complete` refuses External with 409. Fresh New preserves its existing complete contract. Gateway remains Pending. No CPA upstream change, Adopt/Upgrade, public Docker/Gateway, or client-key installation is enabled.

## Acceptance gates

Before first Beta, independently verify exact Host permit issuer/mount provenance and revocation, isolation against other Docker/container/Ingress surfaces including external-network/IPv6 view, real official CPA v8 read, isolated credential persistence, browser refresh/logout/UNKNOWN/re-entry behavior, server reboot and CAS recovery. These are **LIVE NOT RUN / BETA HOLD** until documented. A source test with a synthetic permit predicate is not proof of those Host facts. Known P0/P1 flaws must be fixed before approval; a stopped attempt must not be reset without new explicit authorization.
