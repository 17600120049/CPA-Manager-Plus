# PAH-S0 private resource-guard proof (NOT RELEASE QUALIFIED)

The only allowed profile is the disposable Linux/amd64 Docker Embedded Fresh New
fixture with official CPA v8.0.13. **Do not deploy the default Compose** as P-S0.
The all-deny policy intentionally disables legacy executable plugin pages and
plugin-owned image/font resources; it does not sandbox native CPA plugins.

Build the profile with `docker compose -f docker-compose.pah-s0-proof.yml build`
and start only on a private, disposable Host. The one host-loopback-published port
is `127.0.0.1:28491` unless deliberately changed in the proof environment.
Ingress and Manager must use the same fixed profile; the Manager image's Web
bundle is compiled with `VITE_CPAMP_PAH_S0=1` and the Manager Go binary
is compiled with the **same** immutable argument. Ingress is compiled with
`CPAMP_PAH_S0_BUILD=1` into its Go binary. Runtime `ENV` values cannot
change these build identities. Both services must opt in with
`CPAMP_PRIVATE_PAH_S0=1`; missing or mismatched compiled identities
refuse startup. A Legacy/unknown existing Manager store cannot join P-S0. Absence of a responsive Manager guard **never**
authorizes a plugin resource request.

Still required before any P-S0 deployment qualification:
- Verify published port matrix, live process images and environment, IPv4/IPv6,
  loopback, independent external vantage, untrusted same-host process and
  adjacent container; test proxy aliases, cache and rewrite bypasses.
- Prove canonical GET/HEAD traffic takes Ingress -> Manager denial without
  upstream CPA calls, while malformed/encoded methods never enter Gateway.
- On a disposable profile only, test Ingress-only, Manager-only, one-side
  restart, wrong/absent flag, **including swapped/off-profile Ingress** and
  legacy passthrough; any bypass is STOP. Source binaries cannot prove a
  mismatched Host public listener safe. Do not waive this test based on the
  ordinary Legacy fallback behavior.
- Reject persisted External/AdoptExisting Fresh checkpoints during private
  Manager admission and reject strategy requests other than NewManaged. A
  missing, not-yet-selected Fresh checkpoint remains available for setup.
- With a real browser check old plugin menu URL types (same origin, external,
  data, blob, javascript, unknown), iframe, new tab, refresh, worker/import,
  redirects, cookies, storage and CSP. Preserve Admin/health/brand assets.
- Attach actual Docker/browser/Host logs with exact build SHA; do not infer
  a PASS from source or a loopback-only proof. Keep G1/G2/G3 and public Gateway
  STOP independent.

Do not run this fixture against a production CPA/Manager or trusted existing
volumes. Remove the disposable resources after evidence capture.

## Evidence limitation and concrete denial witness

The official v8 loopback seed currently has `plugins.enabled: false`.
Consequently, a 404 from that CPA fixture alone **does not prove** active
plugin resource interception or plugin inventory compatibility. Go HTTP
negative tests instead mount an instrumented fake upstream that increments a
hit count and returns executable plugin bytes; all canonical, encoded, method
and direct Manager resource requests must cause **zero upstream hits**, even
with an Admin header. Non-plugin Admin/health traffic remains independently
routable.

This stub contract is **not** an official CPA plugin execution proof. If an
actual enabled official-CPA plugin route is required for Host acceptance,
a separately controlled disposable native plugin fixture and real network
witness must be provided under the existing Host authorization; do not enable
untrusted native plugins or claim that a disabled-plugin 404 closes G2.

## Host acceptance remains STOP

Source/CI proof and a green private P-S0 image build do not qualify external
network, IPv4/IPv6, reverse proxy/cache, active plugin route, real browser,
single-side restart, or a published Beta. Record every tested exact image
digest and observer origin; never promote the private Compose to release.

## Trusted, disposable proof launcher (not a production deployment)

The only approved launcher entry is `bin/ci/pah_s0_host_admission.py`
from a trusted Docker Host with exclusive operator control. It is **not**
invoked by CI, and requires exact opt-in:

```sh
export CPAMP_PAH_S0_TRUSTED_HOST=I_ACCEPT_DISPOSABLE_PAH_S0
python3 bin/ci/pah_s0_host_admission.py --launch
```

This checks a fixed three-service Linux/amd64 Compose profile, strict build
switches, actual Docker image IDs, runtime/shared network namespace, port
bindings, label ownership and unexpected same-namespace/port peers before
starting the Ingress process. Existing containers are refused, never adopted
or overwritten. The ingress is started last; a detected error stops only
the private project containers. Use the same tool without `--launch` for a
read-only *current-graph audit*, not an autonomous restart. Do not use
`docker compose up` directly to claim proof qualification.

**STOP remains** if another proxy, old binary or untrusted peer can publish
a second entry, if automatic/one-sided restarts occurred, or after any
unobserved Host topology mutation. This script is a bounded startup preflight
only; it is not continuously enforcing Docker ownership, cannot constrain
a trusted Docker-root operator, and has not been executed on a live Host.
An independent external observer must still verify IPv4/IPv6/ports and all
resource/legacy bypass paths before claiming even private P-S0 safety.

### Pre-public launch requirements

The disposable launcher refuses any pre-existing P-S0 named volume even if
its old containers were deleted. It checks **actual** local Docker volume
ownership, each Manager/Runtime mount and read-only secret role, and unrelated
containers' volume or bind-path aliases. It also requires exact loopback
`CPAMP_RUNTIME_ADDR=127.0.0.1:9081` and
`CPAMP_RUNTIME_CPA_ADDR=127.0.0.1:8317` in both resolved Compose and
created Runtime container. A foreign peer/credential mount, retained volume,
wrong-side image or listener is a hard STOP, never an ignored warning.

Before it starts Ingress, the launcher probes the **Manager's real HTTP
resource rejection** on the private shared namespace: an unauthenticated
request for `/v0/resource/plugins/pah-s0-proof.js` must return 404 and
`Cache-Control: no-store`. On an initial Fresh New install there is no
chosen strategy or attached Fresh handler yet, so
`/internal/pah-s0/resource-guard` cannot return 204 before setup.
The P-S0 Ingress continues to deny plugin resources until that guard
returns 204 after Fresh admission. Do not misinterpret a pre-setup 404 guard
as a reason to enable normal Legacy Gateway fallback.

These are **point-in-time admission checks**, not a daemon or continuously
enforced Docker-root exclusion. They do not replace independent Host witness,
external network/IPv4/IPv6, cache/proxy, one-sided restart or browser tests.
