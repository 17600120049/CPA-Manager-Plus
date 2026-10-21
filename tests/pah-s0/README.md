# PAH-S0 private resource-guard proof (NOT RELEASE QUALIFIED)

The only allowed profile is the disposable Linux/amd64 Docker Embedded Fresh New
fixture with official CPA v8.0.13. **Do not deploy the default Compose** as P-S0.
The all-deny policy intentionally disables legacy executable plugin pages and
plugin-owned image/font resources; it does not sandbox native CPA plugins.

Build the profile with `docker compose -f docker-compose.pah-s0-proof.yml build`
and start only on a private, disposable Host. The one host-loopback-published port
is `127.0.0.1:28491` unless deliberately changed in the proof environment.
Ingress and Manager must use the same fixed profile; the Manager image's Web
bundle is compiled with `VITE_CPAMP_PAH_S0=1` and the Ingress image with
`CPAMP_PAH_S0_BUILD=1`; both images retain `CPAMP_PAH_S0_BUILD=1` as
an image-stamped runtime marker. Both services must opt in with
`CPAMP_PRIVATE_PAH_S0=1`, and a missing/mismatched marker refuses startup. Absence of a responsive Manager guard **never**
authorizes a plugin resource request.

Still required before any P-S0 deployment qualification:
- Verify published port matrix, live process images and environment, IPv4/IPv6,
  loopback, independent external vantage, untrusted same-host process and
  adjacent container; test proxy aliases, cache and rewrite bypasses.
- Prove canonical GET/HEAD traffic takes Ingress -> Manager denial without
  upstream CPA calls, while malformed/encoded methods never enter Gateway.
- On a disposable profile only, test Ingress-only, Manager-only, one-side
  restart, wrong/absent flag and legacy passthrough; any bypass is STOP.
- With a real browser check old plugin menu URL types (same origin, external,
  data, blob, javascript, unknown), iframe, new tab, refresh, worker/import,
  redirects, cookies, storage and CSP. Preserve Admin/health/brand assets.
- Attach actual Docker/browser/Host logs with exact build SHA; do not infer
  a PASS from source or a loopback-only proof. Keep G1/G2/G3 and public Gateway
  STOP independent.

Do not run this fixture against a production CPA/Manager or trusted existing
volumes. Remove the disposable resources after evidence capture.
