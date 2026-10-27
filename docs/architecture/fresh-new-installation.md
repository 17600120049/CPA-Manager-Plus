# Private Fresh New installation and recovery

This is the bounded Linux/amd64 Fresh New journey on `v2`, using the normal
official CPA v8 Runtime. It uses the existing Host initialization, Bootstrap,
routing and Runtime lifecycle contracts. Ordinary Compose model Gateway stays
Pending, including with zero CPA Client Keys. This is not a public deployment,
Provider setup, Upgrade or five-path Beta qualification.

Use a disposable host/project and **new volumes**. Never attach an existing
installation, Agent B's volumes/network, or a production database. Preserve both
the SQLite database and `data.key` when retaining an installation.

## Operator journey

Run these commands from the selected product worktree. Keep the same project
name and environment for every Compose invocation:

```sh
export COMPOSE_PROJECT_NAME=cpamp-fresh-new-local
export CPAMP_PUBLIC_PORT=127.0.0.1:18317
export CPAMP_PUBLIC_ORIGIN=http://cpamp-ingress:18317
docker compose build
docker compose up -d --no-build
```

The image build uses the repository's normal Runtime target and its frozen
official CPA pin. Use a Linux/amd64 Docker engine for this acceptance profile.
Only the Ingress port is published, to host loopback. The explicitly configured
origin above lets Manager verify the same Ingress routes inside this private
Compose deployment; it is not a public-host or TLS qualification.

Before initialization the service waits for Host admission. Discovery is
read-only and does not authorize initialization or create an administrator.
Before Host admission, the bounded Bootstrap UI may show read-only source guidance if the startup inspection listener is active. This UI never grants a Fresh claim, Upgrade/Adopt permission or writer; unknown/blocked sources remain fail-closed.

```sh
docker compose stop cpamp-manager
docker compose run --rm --no-deps cpamp-manager initialize
docker compose up -d --no-build cpamp-manager
```

`initialize` must hold the existing database fence exclusively; do not run it
against a running Manager. It prints one relative Handoff URL. Open that path
and fragment under `http://127.0.0.1:18317` in your browser. Treat the output as a
one-time credential: do not paste it into issues, service logs or evidence.

1. The page removes the fragment before exchanging the claim. A consumed,
   expired or wrong-identity Handoff cannot initialize the installation.
2. Generate an Admin Key on the page, or enter a manually chosen key with
   24–256 UTF-8 bytes, no whitespace/control characters, at least three character
   classes and twelve distinct characters. Save it in a password manager and
   check the saved-key confirmation before submitting.
3. Submit administrator initialization. If the reply is lost, the page reads
   the actual Operation. If that read also fails, writes and key editing stay
   locked; use **同步服务端状态**. Do not refresh or replace the candidate key
   while the result is unknown.
4. Keep `/console` or choose an allowed Console path, then activate it. The
   existing routing contract persists the intent and verifies the real Ingress
   route, hashed assets and installation identity. A failed prepare/read cannot
   authorize a different target. The continue link appears only after committed
   route verification.
5. Follow the link and use the existing CPAMP login page with the saved Admin
   Key. Select **Remember credentials** to reuse the existing browser-local
   credential storage. Refresh revalidates that credential against the canonical
   Fresh operation before restoring the current Console route. Without this
   option, refresh requires another login. Logout or rejected authentication
   clears the saved credential; another deployment prefix cannot reuse it.

After signing in, the installation wizard continues the same durable operation:

1. Choose **New embedded installation**. Adopt cannot execute; External remains
   a Host-authorized connection checkpoint and cannot finish installation. The
   Fresh flow does not upgrade existing product data.
2. Review the Console address and packaged official CPA version, then select
   **Check deployment**. Manager reads the Runtime through its existing
   Host-configured authenticated transport. There is no Runtime JSON input,
   browser-selected target URL or browser-granted Host permission.
3. Review the destination, acknowledge the installation and select **Confirm
   and install** once. The wizard prepares the target, starts CPA through the
   existing idempotent operation, checks readiness and saves the result. Every
   transition reads the persisted operation before advancing.
4. An interrupted or ambiguous reply shows **Result needs confirmation** and
   locks writes. Use **Check original operation**, or refresh, sign in with the
   same Admin Key and **Continue original installation**. A conflicting Runtime
   identity/generation requires Host reconciliation; it is never silently rebound.
5. **Enter Console** opens the original CPAMP layout, toolbar and business
   pages with two-level navigation. Client access contains API keys; System
   management contains the original configuration and update pages. Recoverable
   retry controls appear on the configuration page. Key and retry operations
   retain their pending state when navigating away and returning. Unsupported
   entries are hidden. Existing Key/Config writer permissions remain unchanged:
   a normal deployment cannot enable private writers through navigation.

Installation completion requires a committed Fresh New checkpoint and a real
Runtime observation of **ready**, matching the original identity, generation
and official artifact. It does not open the model Gateway by itself:
`gatewayPending` remains true and Ingress keeps answering 503 until a Client
Key is installed and Manager proves the admission posture in
[Gateway admission](#gateway-admission-ingress). Diagnostics are available in
expandable details instead of the normal user workflow.

## Restart and recovery

```sh
docker compose restart cpamp-manager cpamp-runtime cpamp-ingress
```

Reopen the saved Console path and log in with the same Admin Key. Manager restores
the same operation, completed routing and installation state; the saved setting
and `data.key` survive. Do not rerun `initialize` on a completed installation.
The fixed Bootstrap UI is retired and returns 404 after routing commits.

An independent Supervisor container restart creates a new Runtime generation and
currently leaves CPA offline. Console access still recovers. A replay of the
old installation target must return `409 managed_target_identity_mismatch` and
must not update the saved target. Host-controlled Runtime Start/Restart remains
the existing separate lifecycle operation: read the current Supervisor status
and reconcile its operation journal before any lifecycle retry. This UI does
not introduce automatic restart policy or silently rebind installation identity.

| Failure | Safe next operation |
| --- | --- |
| No Handoff / expired / consumed claim / lost exchange reply | Stop only this Manager, run Host `initialize` against the same owned store, restart Manager and use the new Handoff. Before routing commits, an initialized admin gets a resume session rather than a second initialization. |
| Missing, weak or incorrect Admin Key | Follow the displayed validation; Console rejects a wrong key. No CPA Management Key substitution. Preserve the saved original key after an unknown commit. |
| Unknown admin/Runtime/completion write result | Read server state. Keep the original key/commit identity and target. If state cannot be read, keep writes locked. Never delete volumes to turn uncertainty into Fresh. |
| Routing activation failure | Retry the original persisted intent after checking state; successful public identity verification is required before continuing. A lost commit reply may retire the fixed Bootstrap transport; the exact public identity leaf resolves that case. |
| HTTP 409 / target or generation conflict | STOP that write and let the Host owner reconcile actual state. No automatic rebind, skip or restart. Refresh reads state but does not clear the conflict write lock. |
| Existing database, legacy settings, unknown source or incomplete receipt | Stop Fresh admission and preserve the source. This flow does not migrate, repair or classify it as a new empty installation. |
| Admin Key lost after installation | Stop and use a separately approved administrator recovery procedure; this flow cannot reveal or overwrite the stored credential. |

## Repeatable automated acceptance

Build task-specific image tags from the selected source, without changing Runtime
targets, seeds, networks or another task's state:

```sh
docker build --platform linux/amd64 -f Dockerfile.runtime -t cpamp-fresh-e2e-runtime:local .
docker build --platform linux/amd64 -f Dockerfile.ingress -t cpamp-fresh-e2e-ingress:local .
docker build --platform linux/amd64 -f Dockerfile.manager-server -t cpamp-fresh-e2e-manager:local .
PLAYWRIGHT_MODULE=/absolute/path/to/node_modules/playwright \
PLAYWRIGHT_CHANNEL=chrome \
node bin/ci/fresh-new-installation.mjs \
  cpamp-fresh-e2e-runtime:local cpamp-fresh-e2e-manager:local \
  cpamp-fresh-e2e-ingress:local /tmp/cpamp-fresh-new-unique-result
```

Prerequisites: Docker Compose, local `python:3.13-alpine`, Node, a callable
Playwright module and installed browser. Omit `PLAYWRIGHT_CHANNEL` for an
installed Playwright Chromium. Use a new output directory per run. The script
pins the supplied images by local image ID, creates a random Compose project,
binds a random loopback port and removes only its own project/volumes afterward.
It retains a secret-free JSON report and masked screenshots. It never logs
Handoff or Admin/Runtime tokens. The data-layer step creates one Client Key and
sends one model request inside the disposable installation.

The script asserts `/health` is `503` before Host initialize and `200` with the
Manager service identity after the Fresh store takeover, then drives the whole
setup wizard (administrator key, one installation submission, public route) and
checks the CPA data layer through the `cpamp/` resource bridge: one CPA
management read, one Client Key creation plus initial install, and one model
request that must authenticate the installed Client Key (non-401 — it proves
authentication, not a real Provider response; real Provider model success is
reported NOT RUN because no Provider fixture exists in the disposable install).
`CPAMP_SMOKE_MODEL_TARGET` selects where that request is sent: the default
`http://cpamp-runtime:8317` reaches CPA on the Compose network, and
`http://cpamp-ingress:18317` reaches it through the public Ingress listener. The
data layer has no tolerance: the committed default New Managed destination is the
installation-derived CPA management authority, so a bridge or install status
other than the expected one fails the run. Before the first Client Key exists
every tested Gateway path answers the Pending `503`; once one is installed the
Ingress admits it, and any status other than Pending on that path means admission
was granted. A restarted Runtime reports a new generation, which withdraws
admission until a new completion binds it, so the post-restart Gateway path is
asserted closed again. The managed retry layout check runs when the config
catalog exposes a retry count or interval field, and is reported `NOT RUN` only
when the catalog omits both.

The expiry case explicitly edits only the claim expiry timestamp in this
disposable, stopped Manager fixture; natural ten-minute wall-clock expiry is
reported NOT RUN. Response-loss cases let real server writes complete before
aborting browser delivery. Source tests, real Docker execution and interactive
browser results must be reported separately. See the
[acceptance record](fresh-new-installation-acceptance.md) for actual observed
outcomes and the remaining limits.

## Candidate build, load and smoke

The release lane builds the three `linux/amd64` candidate images once. The OCI
archives stay the verified evidence artifact, and the verifier still runs on
them. The local engine cannot load an OCI layout archive, so the **same** buildx
invocation also exports a docker-format tar per role. That tar is a load
transport for the very same build result — not a second candidate and not a
rebuild.

```sh
export CPAMP_CANDIDATE_BUILDER=cpamp-r1-local
docker buildx create --name "$CPAMP_CANDIDATE_BUILDER" --driver docker-container --bootstrap
CPAMP_CANDIDATE_LOAD=1 bash bin/release/build-embedded-candidate.sh /tmp/cpamp-r1-unique

PLAYWRIGHT_MODULE=/absolute/path/to/node_modules/playwright \
PLAYWRIGHT_CHANNEL=chrome \
node bin/ci/fresh-new-installation.mjs \
  cpamp-r1-runtime:candidate cpamp-r1-manager:candidate cpamp-r1-ingress:candidate \
  /tmp/cpamp-fresh-new-unique-result
```

A container-driver builder is required: the `docker` driver cannot export an OCI
layout, so the builder script refuses it and asks for
`CPAMP_CANDIDATE_BUILDER=<name>` instead of silently producing another format.
`CPAMP_CANDIDATE_LOAD=1` loads each transport archive and asserts the loaded
image ID equals the verified `imageConfigDigest`, plus config and layer blob
equality with the OCI archive. Any mismatch stops the run with no fallback build.
The reference `cpamp-r1-<role>:candidate` comes from the same buildx `--tag`, so
no separate `docker tag` step exists. `load-evidence.json` records the loaded IDs
and digest comparisons next to `candidate-evidence.json`; the transport note is
repeated in both files.

The three images are local-only evidence: they are not signed producer receipts,
not a registry readback and not a Beta/Release qualification. The candidate
inventory still records `NOTICE` as `NOT_VERIFIED` (third-party LICENSE/NOTICE
legal sufficiency is out of scope).
## Default New Managed CPA authority

The default Compose installation uses the Runtime `new_managed` destination.
`freshstrategy.newManaged` generates a random 256-bit management key, stores
the protected destination snapshot in the Fresh CAS checkpoint, stages and
starts the destination, then requires an authenticated CPA v8 config read-back
before committing `manager_config_v1`. The runtime template binds CPA to
`0.0.0.0`, enables remote management and usage statistics, and disables the
control panel. The key is never returned to the browser, Runtime journal,
operation response, or logs.

After commit, `freshkeyapply.Service.DefaultInstallCPAAuthorityProven` in
`apps/manager-server/internal/service/freshkeyapply/config_target.go` is the
Manager-internal predicate exposed for Gateway admission. It requires the
committed New Managed connection and an authenticated Runtime status matching
the recorded identity, generation, and artifact. Private attested installs
continue to use their explicit host-permit profile.

### Trust boundary and durable authority

The managed profile is selected by the server-owned Fresh checkpoint, not by a
browser field or an environment variable. A `new_managed` checkpoint is bound
to the authenticated Runtime identity, generation and active CPA artifact. Its
destination identity is `managed:sha256:<sha256(normalized CPA base URL)>`.
The checkpoint's protected snapshot contains the Supervisor operation ID,
expected Runtime facts, source discriminator and randomly generated 256-bit
management secret. The plaintext secret is used only while staging and reading
the destination; it is never serialized to a response, journal, log or browser
state.

The first strategy call only returns this protected snapshot. The caller writes
it through the Fresh CAS before `StageDestination` or `Start` is attempted.
The next call reuses the same child operation ID, verifies the Runtime facts,
stages the `new_managed` destination, starts it and authenticates
`GET /v8/management/config`. A Supervisor `Start` result only publishes the
spawn, so the install consumer's Runtime client first waits for the Supervisor's
own readiness observation of that exact identity and generation (bounded to
20 s); a readiness timeout is not a failed spawn, and it leaves the typed,
resumable `managed_destination_verification_required` blocker instead of an
unconfirmed write. Only after that read succeeds does the CAS store
the protected Manager connection. Completing the Fresh routing/product CAS
also mirrors that connection to `manager_config_v1` and the compatibility
`setup` row; both rows contain the protected envelope and are decrypted only by
Manager-owned repositories.

Key install, revoke, replace and retry configuration use this connection as
the default authority. One profile discriminator decides that ownership:
`freshkeyapply.Service.Targets` compares the committed checkpoint with the
profile the Manager constructed — the managed destination for the default
Compose install, the attested private target for the host-permit profile.
Initial install, key revoke/replace, the private chat consumer and the gateway
configuration page all ask that one function, so no surface can accept a
checkpoint another refuses, and an adoption, External or uncommitted
checkpoint is refused everywhere. `DefaultInstallCPAAuthorityProven` is the
boolean admission predicate at
`apps/manager-server/internal/service/freshkeyapply/config_target.go`; it
requires the exact committed Runtime facts, managed CPA posture and at least
one installed Client Key, so a CPA with client authentication off is not
admitted. The private profile remains separately discriminated and continues
to require the short-lived host permit.

### Failed stage or start

The Supervisor journal never resumes a terminal operation, so a stage or start
recorded as `failed` can only ever be replayed. The checkpoint therefore
carries a durable `destinationAttempt`: stage/start operation IDs are derived
from it (`<base>` for attempt zero, `<base>:attempt-<n>` afterwards) and it
advances by exactly one, in the same revision-guarded Fresh CAS, only after the
Supervisor reports a terminal `failed`. An unresolved outcome (transport
error, `accepted`, `running`) keeps the attempt, so a retry replays the
operation that may still be running rather than racing a second one, and two
browser retries at the same revision cannot fork attempts. The legacy
migration uses the same counter.

The Runtime admits a replacement stage only under a new operation ID, only for
the identical destination, and only with terminal evidence that the superseded
attempt never owned a child. Its stage operation must be terminal, and its Start
operation must be absent or terminally failed; an accepted, running or
successful Start, a content change, or a live owned child stays a conflict. A
stage that succeeded always needs its Start terminally failed, because that
attempt may own the child the retry would replant. A stage may publish its live
root and still be recorded as failed, because the final directory sync runs
after the rename; that is why `Start` refuses any destination whose stage
operation is not `succeeded`, and why absence of Start evidence is the only
proof that such a root is free. The superseded directory is renamed to a
content-addressed `.destination-retired-<digest>` sibling, never deleted and
never left as the live destination; a retry that fails to publish restores it,
so a retry can neither orphan nor displace the previous attempt.

### Gateway admission (Ingress)

Ingress keeps every Fresh client Gateway route closed with
`503 gateway pending configuration` until Manager answers the private
`/internal/routing/fresh-authority` read with an admission posture that is
consistent with the installation:

- the Fresh install is committed (`committed: true`) and the reply repeats the
  installation, generation, operation, revision and target that Ingress
  already holds in its routing projection;
- `gatewayAdmission.installationId` and `gatewayAdmission.generation` are the
  same installation generation;
- `gatewayAdmission.admitted` is true, which Manager sets only while
  `DefaultInstallCPAAuthorityProven` is true for the default New Managed
  profile (committed destination, authenticated Runtime status, managed CPA
  posture and at least one installed Client Key).

Manager computes the posture on every read and never persists it; the reply
carries no key, management secret or destination address, and a Manager that
does not send the field (an older Manager) never admits. Ingress caches one
positive answer for 15s and one negative answer for 1s, re-reads Manager after
that TTL, and withdraws a cached positive answer immediately when its routing
projection changes or disappears. A restarted Ingress has no cached answer, so
it stays closed until Manager re-publishes the projection (the existing
`restoreFreshRouting` loop runs every 2s) and admits again. Any Manager error,
timeout, non-200 reply, legacy `204`, unknown field, trailing data or mismatch
keeps the Gateway closed.

Client authentication is enforced by requiring at least one installed Client
Key, not by reacting to its removal. Three facts form the safety chain:

1. admission requires at least one installed key, so Manager never admits a
   destination whose client set is empty;
2. the product cannot empty that set: a managed revoke of the last effective
   key is refused with `ErrLastKey` (HTTP 409 `last_effective_key`), and
   replace installs the successor before the old key is dropped;
3. if the destination's key set or managed posture is changed out of band
   (outside Manager), the predicate returns false on the next read and Ingress
   withdraws admission at that read.

"Revoke every Client Key" is therefore not a reachable product state and does
not itself withdraw admission. The residual window is out-of-band only: a
positive answer already held by Ingress stays effective for at most 15 s, and
CPA v8 with zero configured client keys does not enforce client
authentication, so anonymous Gateway requests during that window are not
rejected by CPA. With at least one installed key, a missing or wrong client
credential is forwarded to CPA, which answers `401`.

The public Ingress listener never proxies CPA management paths:
`/v8/management` is denied at the transport edge, `/v0/management` keeps its
Manager ownership, and the private `/internal/...` namespaces are denied before
either upstream. Legacy deployments keep the existing `204` authority contract
unchanged.

### Existing-install migration

On Manager startup, a completed legacy default record with a committed
`new_managed` checkpoint but no destination identity or connection enters one
one-time migration. Manager requires the Runtime to be authenticated and CPA
offline, CAS-persists a protected migration snapshot, then reconciles the
stable `:managed-migration-destination` Supervisor journal through stage,
start and config read-back. A generation/checkpoint mismatch is a conflict and
never silently rebinds. If `/runtime/gateway/auth` is non-empty, the Runtime
returns `managed_gateway_auth_present`; Manager leaves the durable snapshot in
place and fails closed. A completed connection makes subsequent startup passes
no-ops.

### Beta acceptance (D)

After the candidate is re-accepted and the three qualification pins are
updated by the authorized maintainer, D runs the default Compose acceptance in
this document and verifies, in order:

1. Fresh New Managed completes with the destination identity, generation and
   artifact recorded; `manager_config_v1` and `setup` resolve to the same base
   URL and management authority without exposing it.
2. Create a Client Key and send a real model request through the public
   Ingress listener by setting
   `CPAMP_SMOKE_MODEL_TARGET=http://cpamp-ingress:18317` for the smoke run;
   observe CPA acceptance. Replace the key and observe the old key rejected
   while the new key works; revoking the last effective key must return 409
   `last_effective_key`. Before the first Client Key exists the same request
   returns 503; with a key installed, a wrong or missing credential returns
   401 from CPA. A restart of Ingress must not forward before it has
   reconfirmed admission.
3. Read and write each accepted Gateway retry/streaming field, then read it
   back and compare the CPA v8 config. Interrupt one write, query its operation
   and resume it; the journal must not duplicate the PUT.
4. Read `/auth-files` and the resource bridge through `cpamp/`; verify the
   collector/identity consumers resolve the managed base URL and credential
   from the completion mirrors.
5. Repeat after Manager restart. For a legacy fixture, verify migration is
   idempotent and refuses a non-empty bootstrap auth directory or generation
   drift. Capture only secret-free evidence.

Before re-acceptance, any pin-gated step is `NOT RUN`. The two build-bound
pins and the observation bytes they bind were recomputed/re-emitted on
maintainer decision, ahead of independent re-acceptance; writing an acceptance
record is not part of this implementation lane, and the retained independent
review is unchanged.
