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
5. **Enter Console** opens the two-level navigation. API keys, gateway retry
   settings, system settings (update-channel preference) and installation overview
   have real routes under the configured Console address. Unsupported entries
   are hidden. Existing Key/Config writer permissions remain unchanged: a normal
   deployment cannot enable private writers through navigation.

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
