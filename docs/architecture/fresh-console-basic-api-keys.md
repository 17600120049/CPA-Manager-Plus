# Fresh Console basic client API keys

This slice supports private, nonproduction Fresh New installations. An
administrator signs in with the existing Admin Key, creates a client key, closes
its one-time plaintext result, and refreshes the persisted masked list. Refreshing
the browser requires Admin login again. Existing four UI languages are retained.

**Manager persistence is not CPA Runtime activation.** Keys remain staged;
Gateway remains Pending. No CPA API, source binding, runtime acknowledgement or
model endpoint is enabled. External and Adoption do not support these endpoints.
An unselected Fresh strategy may stage keys before New Runtime qualification.

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

The request ID is an independent random operation token, never an APIKeyID.
The server generates the Canonical APIKeyID using the existing identity domain.
One SQLite transaction inserts that entity in `gateway_api_key_identities` and
its protected secret/request metadata in `fresh_client_api_keys`. It creates no
CPA source binding; canonical entity lifecycle is independent of Runtime effect.
No usage or retained history is modified.

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
have no Runtime source binding. No CPA write or second Key authority is added.

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

## Focused acceptance

Service tests use real temporary SQLite files: canonical repository lookup,
atomic persistence, close/reopen, concurrent duplicate requests, independent
identities, wrong protection keys and malformed envelopes. HTTP tests cover the
real Admin verifier, masked reads/reconciliation, replay, negative credentials,
unsupported mutations, invalid payloads and unchanged Gateway state. Web tests
cover the Public API transport, timeout, duplicate click, unknown-result
reconciliation, same-request retry, authentication failure and one-time display.

Linux/amd64 Manager/Ingress/SQLite and browser smoke results are reported in the
Draft PR. They are not CPA data-plane authentication qualification, production
deployment acceptance, or permission to publish a Beta.

For the follow-on detail/rename slice, see the separate
[acceptance record](evidence/fresh-console-key-rename/README.md), including its
native loopback process scope and remaining NOT RUN/STOP boundaries.
