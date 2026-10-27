# Fresh Console basic client API keys

This slice supports private, nonproduction Fresh New installations. An
administrator signs in with the existing Admin Key, creates a client key, closes
its one-time plaintext result, and refreshes the persisted masked list. Refreshing
the browser requires Admin login again. Existing four UI languages are retained.

**Manager persistence is not CPA Runtime activation.** Keys remain staged;
Gateway remains Pending. No CPA API write, runtime acknowledgement or model
endpoint is enabled, and External and Adoption do not support these endpoints.
The Manager-side attribution binding described under "Usage and coverage" is
durable in Manager's own SQLite only; it activates nothing in CPA. An unselected
Fresh strategy may stage keys before New Runtime qualification.

## Public API

All paths are below the verified control prefix's `/api/v2` namespace, use the
existing Admin bearer verifier, and return `Cache-Control: no-store`.

| Request | Result |
| --- | --- |
| `GET api-keys` | Masked keys, canonical IDs, Manager `persisted`, Runtime `not_applied`; Gateway pending, revocation unavailable |
| `POST api-keys` with `{"requestId":"<32 lowercase hex characters>"}` | First commit: 201, key metadata and `createdSecret`; replay: 200, metadata only |
| `GET api-keys/requests/<requestId>` | Persisted metadata only; 404 means no committed result was found at that read |
| `GET api-keys/<APIKeyID>` | One persisted, masked key including its optional `name` and `nameRevision` |
| `PUT api-keys/<APIKeyID>` with `{"name":"Work client","nameRevision":1}` | Save display name only; return committed masked metadata. Stale/duplicate version: 409; unknown ID: 404; malformed ID/body/name: 400 |
| `GET api-keys/usage-summary` | Last-24h calls, last-used and coverage per Canonical Key, plus installation-wide coverage once per list |
| `GET api-keys/<APIKeyID>/usage` | Totals, request series and coverage for one key; accepts optional bounded `from_ms`/`to_ms` (400 otherwise) |

The two usage reads exist only for a completed installation: both return 409
until `ProductComplete`, and 409 for every non-New-Managed strategy, matching the
existing conflict semantics of this router.

The request ID is an independent random operation token, never an APIKeyID.
The server generates the Canonical APIKeyID using the existing identity domain.
One SQLite transaction inserts that entity in `gateway_api_key_identities`, its
protected secret/request metadata in `fresh_client_api_keys`, and — once the
installation is committed — the key's attribution window in
`gateway_api_key_source_bindings`. Staged keys created before a committed target
exists are bound at startup instead (see "Usage and coverage"). Canonical entity
lifecycle stays independent of Runtime effect, and no key operation writes CPA.

### Names and details

Detail and rename address the existing Canonical APIKeyID, never a creation
request ID, list position, name or hash. Names are optional, trimmed, at most
120 Unicode code points, and cannot contain control characters. Empty names
display as unnamed. Names are display metadata, not credentials or identity.

The additive migration puts `name` (default empty) and `name_revision` (default
1) on the existing `fresh_client_api_keys` table. It preserves existing staged
records. A single conditional SQLite UPDATE changes only those two columns;
the observed `nameRevision` must match. Each successful submission increments
the name version. Two writers using one observed version cannot both succeed;
repeating a committed submission returns 409. This is a metadata version,
independent of Canonical Identity revision, RuntimeGeneration and routing.
Canonical ID/lifecycle/revision/timestamps, ciphertext, secret hash, creation
request ID, source bindings and historical data are unchanged by rename.
The legacy hash-keyed alias authority is not used for Fresh staged keys, which
are identified by their own Canonical ID. No CPA write or second Key authority
is added by rename.

The Console opens one inline detail form at a time, with masked metadata only.
It blocks conflicting list operations while that form is open, confirms
discarding a draft on close/reload, and updates its list after an acknowledged
save or a valid detail read. It scopes replies to the mounted canonical record
and authenticated view; an old reply/401 cannot change a new login's data.

Detail reads and writes use the existing 15-second timeout. No write is
automatically retried. A rejected 400/404/409 requires reading saved details
before editing again. Network errors, 503 and malformed acknowledgements are
unknown outcomes: save, close and ordinary logout remain blocked until a valid
detail GET reads the current persisted state. That GET does **not** prove the
original request's final outcome or exclude another writer; its message says
the save was not acknowledged and asks the user to review the current state.
Another explicit save uses the newly read version, so an older in-flight write
cannot overwrite a newer committed version. There is no rename operation ledger.
Browser navigation/reload may discard in-memory drafts; after login the saved
name is read from Manager. No browser journal or secret persistence is added.

Fresh Console uses the existing persistent Admin Key verifier, not an expiring
Admin session/token scheme. Missing/invalid credentials and Fresh Bootstrap
session tokens cannot authorize detail or rename. This slice adds no auth model.

Client secrets contain 32 cryptographically random bytes. The existing
`security.Protector` stores an authenticated AES-GCM envelope; SQLite never stores
plaintext. The list validates decryption and the secret hash before returning
only a prefix/tail preview. A missing, wrong or damaged protection key cannot
silently become plaintext or admit mixed-key writes through this API. Preserve
the original Manager SQLite data and `data.key` together.

The browser holds Admin authority, creation request ID and new plaintext in
memory only. Closing the result, logout or unmount removes plaintext from the
page; no reveal endpoint exists. A lost creation response may permanently lose
the administrator's copy of that secret, while its persisted identity remains
verifiable. It must not be reported as a usable Runtime key.

Submission is guarded in memory and by the unique persisted request ID.
Creation and original-request checks have a 15-second transport timeout and are
never automatically retried. A timed-out check releases the UI for another check.
On an unknown result the UI blocks new creation, checks the original operation,
and permits an explicit retry with **the same request ID** only after a 404.
Even if the original request subsequently commits, that retry cannot create a
second identity or disclose the old plaintext. Do not leave an unresolved
operation to start another one; browser journals are intentionally not persisted.
Ordinary Console logout is blocked from submission until the original creation
is confirmed, including while its result is unknown or being checked. Rejected
Admin authority still clears the login state. Browser navigation is not blocked.

Delete, replace, reveal, bulk operations and Runtime application are unavailable
server-side, including deletion of the last client key. The known CPA keyless
authentication P0 and external-writer risks remain STOP conditions for exposure
and revocation. No change to CPA upstream, paused #1002, v7 defaults or official
v8 opt-in packaging is part of this slice.

## Usage and coverage

Fresh Key usage is read per Canonical Key, never per hash and never from a CPA
summary.

- **Attribution:** the create transaction — or the startup backfill for keys
  staged before a committed target existed — writes one
  `gateway_api_key_source_bindings` row with
  `api_key_hash = fresh_client_api_keys.secret_hash` and the committed
  installation's runtime identity. The window opens at the key's own creation
  instant. One secret hash has exactly one active binding, so passive identity
  synchronization binds the staged Canonical Key instead of minting a second
  one. A hash that belongs to a revoked or replaced (superseded) staged key is
  skipped for that hash alone: no second canonical ID is minted, no binding is
  written, and the rest of the snapshot still applies. Lagging inventory
  evidence therefore can never split a key's recorded history. A missing staged
  key is recoverable: its returning source is recovered and bound to the same
  canonical ID. A hash actively bound to another identity fails closed too
  (`binding_conflict`).
- **Collection:** after `ProductComplete` the Fresh startup binds staged keys,
  starts the Collector with the install-derived CPA authority, feeds its events
  to the canonical identity projection worker, and records the first proven
  collection instant (earliest wins). Usage statistics must already be enabled in
  CPA by the destination template; this slice writes no CPA configuration.
- **Reads:** `identityusage` aggregates `usage_events` joined to
  `gateway_usage_identity_projection_v1` where `api_key_state = 'mapped'`,
  reusing the existing token and failure columns. Batches larger than one
  statement's placeholder limit are split and merged, never truncated. Only
  counts and token sums leave the adapter: no hash, secret, masking preview or
  management credential appears in a response, a log or the journal.
- **Coverage** (one vocabulary for list and detail): `complete` only when
  collection is proven, the Collector is running, the projection checkpoint is
  `ready` with its last processed position at or beyond the target and the
  current event frontier, and no event of that key was left unattributed or
  archived out of the window. A `pending` or lagging checkpoint is never
  complete, even when its frontier is still empty. The provable window starts at
  `max(collection start, binding start)`: a requested window that starts earlier
  is `partial` (`window_partial`), and a requested window with no provable
  interval publishes no totals (`window_unobserved`). `partial` also covers any
  other failed proof, `unknown` means collection or attribution cannot be proven
  yet, and `unavailable` means the key has no binding, its hash belongs to
  another identity, or collection is stopped or failing. Unknown and unavailable
  publish no totals at all, and partial prints a lower bound. The list header's
  aggregate is the weakest per-key coverage, so one incomplete key makes the
  summed total a lower bound or hides it. Reason codes are stable and localized
  by the UI.
- **Lifecycle:** rename never touches attribution. Revocation or replacement
  closes the superseded key's window (`retired_at_ms`) and keeps its projected
  history readable under its own Canonical Key; the replacement starts from a new
  identity with a new hash.

## Focused acceptance

Service tests use real temporary SQLite files: canonical repository lookup,
atomic persistence, close/reopen, concurrent duplicate requests, independent
identities, wrong protection keys and malformed envelopes. HTTP tests cover the
real Admin verifier, masked reads/reconciliation, replay, negative credentials,
unsupported mutations, invalid payloads and unchanged Gateway state. Web tests
cover the Public API transport, timeout, duplicate click, unknown-result
reconciliation, same-request retry, authentication failure and one-time display.

Usage tests use real temporary SQLite too: same-transaction binding, replay and
backfill idempotency, completion inside the running process starting the chain
exactly once, superseded-window closure, staged-identity reuse in passive
synchronization with per-key skipping of terminal staged hashes and recovery of
missing ones, per-key aggregation with a fake Collector phase, batching beyond
one statement, every coverage state, and responses free of hashes, secrets and
credentials. Web tests
cover the usage column, the detail section, unknown/partial/lower-bound rendering
and a failed usage read. Real numbers on a built default candidate remain a NOT
RUN item until the
D acceptance run below.

Linux/amd64 Manager/Ingress/SQLite and browser smoke results are reported in the
Draft PR. They are not CPA data-plane authentication qualification, production
deployment acceptance, or permission to publish a Beta.

For the follow-on detail/rename slice, see the separate
[acceptance record](evidence/fresh-console-key-rename/README.md), including its
native loopback process scope and remaining NOT RUN/STOP boundaries.

### Acceptance steps (D)

Run on the batch's default candidate after the pin gates are re-accepted, with no
manual UI walkthrough required.

1. Fresh New + default managed install; wait for `ProductComplete` and confirm
   the destination CPA reports `observability.usage.usage-statistics-enabled:
   true` and that Manager reads `GET /v0/management/usage-queue`.
2. Create one client key through the Admin-authenticated API, install it in CPA
   (existing key-install operation), and send `N` model requests through Ingress
   `:18317` with that key.
3. `GET api-keys/usage-summary` → the item for that Canonical Key reports
   `calls >= N`, a non-null `lastUsedAtMs` and `coverage.state = "complete"` with
   reason `observed`.
4. `GET api-keys/<APIKeyID>/usage` → totals show at least `N` calls, the failure
   count, token sums, and a series whose bucket calls sum to the totals.
5. Revoke/replace the key, send traffic with the replacement, then re-read both
   endpoints: the old Canonical Key keeps its history and the replacement starts
   from its own numbers.
6. Fail-closed checks: stop the Collector (coverage `unavailable` /
   `collection_stopped`, no totals), and read both endpoints before completion in
   a fresh candidate (409).
7. Confirm the responses, Manager logs and journal contain no key hash, secret,
   masking preview or CPA management credential.
