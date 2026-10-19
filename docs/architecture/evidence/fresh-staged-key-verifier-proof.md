# Private Fresh staged-key verifier proof — NOT Runtime authorization

This narrowly scoped opt-in diagnostic is an intermediate Lane B consumer of the existing Manager-owned canonical Fresh Client API Key records from product #1007. It **does not qualify, configure or activate a Gateway**, and never declares a key effective in CPA.

## Default behavior / access boundary

- The exact `POST /internal/fresh-client-key-stage/probe` route is absent unless the **Manager** process explicitly sets `CPAMP_PRIVATE_CLIENT_AUTH_PROOF=1`. The variable is not set in the default Compose or the separate #1008 experiment.
- The Manager handler checks the actual TCP peer remote address for IPv4/IPv6 loopback, requires the exact unescaped, queryless path, and does not accept browser Admin credentials as Client Keys. The public Ingress classifier always denies the corresponding private namespace locally, including encoded variants, so credentials cannot fall through to CPA in Legacy or Fresh mode.
- `Authorization: Bearer <fresh-client-key>` is checked directly against the current Manager-owned staged hashes and protected records. Missing/invalid -> 401; database or protection authority failure -> 503; wrong method -> 405; disabled, nonloopback or noncanonical path -> 404.
- A matching credential returns `200 {"managerStagedKeyMatched":true,"runtimeEffective":false,"gatewayAuthorized":false}`. This is **diagnostic** and cannot authorize a model request. No other public authority, key store or CPA provider configuration is created.
- This is not a second Key authority; no secrets are returned, logged or copied into Ingress storage.

## STOP and prerequisites for a real Ingress consumer

An eventual private Ingress gate MUST separately prove canonical Key identity/state and exact CPA Runtime-effective authorization, including zero Key, bad/missing Key, last Key removal, timeout/reload/restart, and no raw-CPA path bypass. A successful staged-key proof response alone MUST never unlock forwarding. Until that qualified consumer exists, Fresh Gateway remains 503 Pending.

Cross-generation CPA reactivation and post-restart live isolation from #1008 remain BLOCKED. This PR does not loosen the Fresh Strategy identity/generation fence, modify Compose, or attempt Runtime recovery.

## Test/rollback

Unit/integration regressions cover zero Key, correct/wrong Key, `missing`/`superseded` canonical Key state, corrupted DB or encryption key, disabled endpoint, method, encoded path and Ingress deny. The private Manager handler is also tested in a separate child process over a real IPv4 loopback TCP socket (enabled/disabled and positive/negative requests); a public Ingress TCP listener verifies that neither Fresh nor Legacy forwards the private path to Manager or CPA. Nonloopback Manager peer rejection is tested with synthetic request peers only. No external-observer, Docker-network, live CPA, or Runtime-effective proof is claimed. To roll back, leave the opt-in variable unset (default), or revert the isolated files. Keep this Draft pending independent security acceptance.

Overdesign, overdefense, overabstraction and scope expansion: PASS — no new persistence, authentication platform, model proxy, CPA fork or release behavior.
