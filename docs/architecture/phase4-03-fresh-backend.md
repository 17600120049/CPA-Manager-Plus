# Phase4-03 Fresh backend contract

Baseline: `63cf85c9b84de87bc469d9aab8d492c18cd7a0bf`; governing Plan:
`seakee/cpamp-plan@136e48bad66a86b85be995087f764e6dfb24ddc0`, task and prompt.

## Existing Asset Map

| Surface | Disposition |
| --- | --- |
| Bootstrap classifier, StatusSnapshot, Plan, adoption decisions, OperationID | Reuse; add positive installation/generation/operation evidence for Fresh continuation, preserving unknown native-v2 rejection |
| bootstrapdiscovery / startup listener | Reuse non-secret discovery; a held Fresh store supplies current ownership facts rather than attempting another lock |
| legacypreflight.AdmitStartup / processlock | Minimal split: same held real-store admission fence, versioned pending receipt in the existing lock inode |
| data.key / SQLite Open + Migrate | Reuse after pending receipt fsync; no temporary database |
| settings / adminauth / security | Reuse canonical credential and Protector; one transactional Fresh authority boundary |
| bootstrapservice.Run / initialize | Legacy isolation: Fresh must not call automatic admin/config/runtime/bootstrap initialization |
| setup / CPA v0 client / collector | Legacy isolation: not Fresh authority, not a v8 probe, not an adoption queue consumer |
| routing grammar / desired-effective snapshot / prefix and journal contracts | Reuse one authority; initial routing is a checkpoint of OperationInstall, never a new OperationPrefixApply |
| Ingress classifier / ControlMatcher | Reuse ownership capture and matcher seam; unknown fixed paths remain local failures |
| Full Web dist / static handler / client | Minimal split: same-build technical Bootstrap entry, bounded assets/API and memory-only handoff |
| RuntimeClient / Embedded Supervisor | Reuse typed identity/generation and lifecycle operations; never infer Gateway safety from health |
| External safe-read qualification | Reuse accepted v8 config schema and safe-read constraints; fixture runner is not a production client |
| Docker/native command dispatch | Reuse product-owned initialize command; no additional daemon |
| route-owner fixture/schema / ADR-0004 | Advance the sole fixture to v3; narrow durable namespace/classifier ADR delta |

Do not build: generic workflow/auth/session frameworks, refresh tokens, claim
files, second locks/databases/journals/route ledgers, general prefix editing,
Upgrade/history migration, product UI or provider/model/API-key pages.

## Shared ownership and authority

The product checkpoint binds admission ID, installation ID, generation, canonical
DB identity and one non-secret Fresh OperationID. Revision fences mutations.
Routing and strategy checkpoints belong to this operation. Authentication hashes
are separate from the non-secret product/routing checkpoints in the same real
Manager SQLite settings store. Neither operation references nor a pending lock
receipt authenticate a browser.

Claim lifetime is fixed at 10 minutes; Fresh Bootstrap session lifetime is fixed
at 30 minutes. Reissue invalidates prior uncommitted claim/session authority.
After admin commit, claim never reopens; a host resume can only continue the
current operation. Generated administrator plaintext is returned before commit
and never stored for redisclosure. Admin credential, consumed claim and initialized
fact commit atomically. A repeated successful initialization returns its existing
non-secret result without changing the credential.

Before canonical cutover, the fixed carrier accepts only Fresh session authority.
After verified route commit its whole namespace is generic 404. The authorized
caller knows the target before apply; subsequent authentication is Admin Key at
the canonical Control API. Product completion may leave Gateway pending and
closed. No external/source lifecycle ownership is implied by Management access.

## Real-store admission and host handoff

`cpa-manager-plus initialize` is an offline host command. It acquires the existing
process fence, admits the actual Manager database, prints a relative `/ui` URL
with its fragment to host stdout, and exits. It creates no daemon. A running
read-only missing-store listener can pick up that admission after the command
releases its lock. To issue another handoff for an already held store, the host
stops Manager, runs the same command, and restarts Manager. After Admin commit
this issues only a session for the same operation, never another claim or key.

Immediately after exclusive new-marker acquisition, `pending_fresh_admission`
version 1 is written and fsynced in the held `.manager.lock` inode, including
admission/installation/generation/canonical DB path/OperationID. The parent
directory is synced before creating `data.key` or opening/migrating SQLite.
The first product authority is the matching `fresh_operation_v1` and
`fresh_authority_v1` transaction in the real settings store. Finalization writes
the retained `manager` receipt into that same inode. No rename, second marker,
temporary DB, claim sidecar, or new journal is involved.

| Kill/restart window | Required and tested result |
| --- | --- |
| A: pending receipt only | Resume original identity; no pre-receipt key/DB creation |
| B: receipt + data.key, no DB | Resume original identity and key |
| C: receipt + migrated DB, no Fresh checkpoint | Require pristine product/history state; write original ownership |
| D: Fresh checkpoint, marker still pending | Match checkpoint and finalize same inode |
| Malformed/wrong admission, installation, generation, operation or path | Fail closed |
| Ordinary marker, unknown native-v2, archive/history/Admin/Runtime/Bootstrap conflicts | Fail closed; never mint replacement ownership |
| Fresh checkpoint with deleted/empty marker | Reject both Fresh and historical writable startup |

The tests kill actual subprocesses at A/B/C/D and compare the original complete
identity and lock inode after restart. They do not model power-loss durability or
perform platform qualification. Torn/invalid receipts remain closed. If a
config-only initializer crashes while its receipt is still pending, normal
service startup stays closed; the host reruns `initialize` to recover the same
admission. Custom key/config paths have not received a separate kill matrix.

## Admin and transport boundary

Only administrator keys are supported. Generated keys reuse the existing secure
generator. Manual and host keys must be 24–256 bytes, contain no whitespace or
control characters, include at least three character classes and twelve distinct
characters. Only the existing salt/HMAC credential representation is durable.
Host/env authority is materialized with initial Fresh ownership and permanently
closes claim eligibility; a different/unverifiable canonical key is rejected.
An explicitly configured empty/unreadable host secret is also rejected rather
than becoming claim-eligible. Fresh routes have no env-auth fallback.

Claim exchange is single-use. Reissue advances claim/session authority generation
while keeping the installation generation and OperationID unchanged. Session
hashes and fixed expiries are durable; plaintext claim/session tokens are never
journaled or put into query strings or browser storage. Admin commit transaction
rechecks current session, installation, generation and initialization state.
Concurrent attempts have at most one winner. Retrying the same committed
`commitId` reconciles the existing non-secret result, without redisclosing or
replacing the key. Admin generation and initialization capabilities are then gone;
same-operation routing and strategy continuation remain until route cutover.

The exact discovery root retains GET/OPTIONS. `/ui`, bounded hashed loading
assets and the explicit `/api/v2/` resource allowlist use the existing Full Web
build. Unknown/malformed/encoded aliases stay ownership-captured. Bootstrap
HTML/assets/API are `no-store`, never redirect or disclose the prefix publicly,
and accept only the distinct `Fresh` session scheme. Normal Admin/CPA credentials
cannot authenticate this carrier. After Admin initialization, unauthenticated
discovery returns the existing 503 `unavailable` envelope. Verified route commit
retires the entire fixed namespace to generic 404. The authorized caller already
has the target; subsequent canonical requests use normal Bearer Admin Key.

## Initial routing and strategies

Routing is a sub-checkpoint of the original Fresh OperationID, with its own
revision, intent, apply, public verification and commit stages. No top-level
`OperationPrefixApply` is created. Verification uses the actual Manager/Ingress
public entry, HTML/static/API identity and negative Gateway ownership. The private
Ingress control listener has no public host port and stores only an in-memory
projection. It holds no product/Runtime secret: each private routing mutation is
accepted only when it exactly matches the Manager's current read-only, non-secret
Fresh routing authority. Restore redelivers the original DB checkpoint after
Ingress restart; it cannot reopen committed Bootstrap routes. Foreign routing
owners are rejected. `CPAMP_PUBLIC_ORIGIN` and the private
`CPAMP_INGRESS_CONTROL_URL` are required for this verification.
The sole route-owner fixture/schema is version 3,
`cpamp-v2-phase4-03-routing-ownership-v3`; its predecessor assertions remain
intact. ADR-0004 records only this bounded durable namespace/classifier delta.

New Managed uses existing typed RuntimeClient/Supervisor ownership. It persists
the exact target identity/generation/artifact before Start, uses a Fresh-owned
child operation ID, and reconciles Supervisor retries. Start fences the artifact
inside the lifecycle lock, including after lost responses. No new lifecycle
client is introduced. Product completion requires committed routing and strategy;
Gateway remains pending and closed regardless of process health or empty keys.

Adopt keeps the source usable: no stop/delete/replace, no usage/history/log
migration and no usage queue consumption. External validation performs one bounded
GET of the v8 config contract, with no redirect, v0 fallback, OAuth or destructive
probe. A v0-only source returns `owner_upgrade_required`. Supported future External
connection persistence uses the existing Protector and canonical encrypted
connection representation in the same checkpoint transaction; collector is off.
External lifecycle, ingress and client-auth policy stay with the operator.

### Explicit production prerequisite gaps

1. There is no accepted production v8 artifact/profile evidence loader. The
   qualification manifest/README and CI fixtures are contract/test references,
   not production authority (`qualification: not accepted`, production consumer
   pending). Production New Managed/External qualification therefore remains
   typed blocked. Tests injecting qualified exact targets exercise the bounded
   implementation; they do not establish release qualification.
2. There is no accepted Adopt destination staging/replay/verify primitive.
   The existing adoption contract stays fail closed and returns
   `adoption_destination_staging_required`, explicitly listing unsupported native
   resources. Synthetic staging experiments are not used in product code.

These are remaining Phase4-03 acceptance blockers, explicitly confirmed by the
maintainer. This change does not claim end-to-end strategy qualification or full
Phase4-03 acceptance. No generic qualification service, second evidence schema
or staging framework was added to work around them.

## Verification and scope closure

Coverage includes real admission kill windows; malformed/source conflict negative
controls; claim exchange/reissue/expiry/revoke/concurrency; atomic generated/manual
Admin initialization and response-loss reconciliation; host materialization;
restart-safe continuation; same-operation routing and foreign-owner rejection;
real Ingress subprocess apply/verify/cutover/kill/restart; full fixed transport
capture and retirement; strategy ownership/source preservation/safe-read limits;
and the Supervisor artifact race and replay boundary.

Local verification also covers Manager full tests/vet and affected race tests,
Ingress and Supervisor full race/vet, web type check/lint/tests/build, sole route
fixture v3, Panel single-file/demo isolation, packaged Full Web embed smoke,
Docker builds/compose validation and Playwright against the compiled embedded
Shell. The browser proved fragment removal, successful exchange, no Console lazy
chunk, empty browser storage, secret-free request URLs and no console errors.
The first unrestricted web run hit one existing 5s AccountsPage timeout; the
complete run with four workers passed all 3953 tests. Existing lint has four
warnings and no errors.

Over-design/scope review: retained one real DB, one held lock inode, existing
Runtime transport and one routing checkpoint authority. Initial routing guards
are limited to this operation. No refresh token, general auth/workflow/prefix
editor, second route ledger, v0 adapter, source lifecycle takeover, usage migration,
Provider/API-key/model pages or product Bootstrap UI were added. Phase4-04 Upgrade,
Phase4-05/09 follow-up work, full login/Console,
post-completion prefix changes and Native/Release/platform qualification remain
deferred. Residual risks are the two explicit prerequisites above and unqualified
platform/power-loss behavior; Gateway stays closed.
