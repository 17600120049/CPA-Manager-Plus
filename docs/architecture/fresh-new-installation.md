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
and official artifact. It does not open the model Gateway: `gatewayPending`
remains true, including with zero Client Keys. Diagnostics are available in
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
Handoff or Admin/Runtime tokens, and injects no Provider or Client Key.

The expiry case explicitly edits only the claim expiry timestamp in this
disposable, stopped Manager fixture; natural ten-minute wall-clock expiry is
reported NOT RUN. Response-loss cases let real server writes complete before
aborting browser delivery. Source tests, real Docker execution and interactive
browser results must be reported separately. See the
[acceptance record](fresh-new-installation-acceptance.md) for actual observed
outcomes and the remaining limits.

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
`GET /v8/management/config`. Only after that read succeeds does the CAS store
the protected Manager connection. Completing the Fresh routing/product CAS
also mirrors that connection to `manager_config_v1` and the compatibility
`setup` row; both rows contain the protected envelope and are decrypted only by
Manager-owned repositories.

Key install, revoke, replace and retry configuration use this connection as
the default authority. `DefaultInstallCPAAuthorityProven` is the boolean
admission predicate at
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
the identical destination, and only with terminal failure evidence for the
whole previous attempt: a failed stage published nothing usable, and a stage
whose Start is terminally failed never owned a running child. A missing,
accepted, running or successful Start, a content change, or a running CPA child
stays a conflict. The superseded directory is renamed to a content-addressed
`.destination-retired-<digest>` sibling, never deleted and never left as the
live destination; a retry that fails to publish restores it, so a retry can
neither orphan nor displace the previous attempt.

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
2. Create a Client Key, send a real model request through Ingress and observe
   CPA acceptance; revoke the key and observe the request rejected. Replace a
   key and repeat the positive/negative checks.
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
