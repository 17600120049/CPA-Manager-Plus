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

Unit/integration regressions cover zero Key, correct/wrong Key, `missing`/`superseded` canonical Key state, corrupted DB or encryption key, disabled endpoint, method, encoded path and Ingress deny. The private Manager handler is also tested in a separate child process over a real IPv4 loopback TCP socket (enabled/disabled and positive/negative requests); a public Ingress TCP listener verifies that neither Fresh nor Legacy forwards the private path to Manager or CPA. Nonloopback Manager peer rejection is tested with synthetic request peers only. The original #1010 test suite does not itself claim an external-observer, Docker-network, live CPA, or Runtime-effective proof. The later independent #1008 + #1010 joint run on v2@5de8b272223d59b78f56e899c9fe4aa161e2f37f did prove the limited opt-in Docker IPv4 topology, Manager probe, and Pending non-forwarding. To roll back the opt-in diagnostic, leave the variable unset (default). The source experiment is merged, but Gateway authorization remains STOP.

Overdesign, overdefense, overabstraction and scope expansion: PASS — no new persistence, authentication platform, model proxy, CPA fork or release behavior.


## Pinned CPA v8.0.13 Runtime-effective Client Key contract audit (read-only, 2026-10-19)

**Decision: NO-GO for model forwarding.** The Manager's canonical staged keys and the CPA process's actual request-authentication providers are distinct authorities. A positive private probe (`managerStagedKeyMatched=true`) is not an effective-runtime or Gateway grant.

### Confirmed contracts from the exact upstream tag

1. **Persistent config is not active provider state.** [CPA v8.0.13 ConfigV8](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/api/handlers/management/config_v8.go) serves `GET /v8/management/config/access/api-keys` from the saved YAML tree. A successful write returns `200` after `WriteConfig`, but calls `reloadConfigAfterManagementSaveAsync` before returning. [Its reload handler](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/api/handlers/management/handler.go) launches the application callback in another goroutine. Neither write success nor subsequent disk readback is an acknowledgment that the request-authentication manager has applied the keys.
2. **Effective auth resides behind a separate live provider manager.** [CPA access reconciliation](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/access/reconcile.go) can replace its provider list after reload. [The config key provider](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/access/config_access/provider.go) unregisters itself when no configured keys remain. [CPA's access manager](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/sdk/access/manager.go) returns a nil result/error when it has no providers; [the HTTP middleware](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/internal/api/server_middleware.go) then continues the request. This is the confirmed zero-key bypass risk; it must not be used as CPAMP security behavior.
3. **No sufficient effective-key attestation was identified in the reviewed v8.0.13 management contract.** The documented config endpoint observes a persisted view, not the current request-auth provider set or an applied revision bound to the running process and Runtime generation. A later process restart can change this state again. This is a bounded conclusion about the inspected interfaces, not a claim that every internal SDK path was exhaustively surveyed.
4. **Present Embedded Fresh has no Key application owner.** The opt-in [loopback CPA seed](../../../docker/runtime/config.v8.loopback-proof.yaml) starts with `access.api-keys: []` and an empty `management.secret-key` (Management API unavailable without a separately configured password). [Fresh Manager Key CRUD](../../../apps/manager-server/cmd/cpa-manager-plus/fresh_keys.go) only creates/lists/looks up encrypted staged records, reports `runtimeState=not_applied` and `revocationAvailable=false`, and intentionally has no delete/replace or CPA config writer. [Existing read-only v8 adapter](../../../apps/manager-server/internal/service/cpa/v8.go) returns `ClientKeyCount` from a management config view and expressly does not qualify Runtime or Gateway. The private probe never writes CPA.

### Minimum exit criteria before proposing any enabled model consumer

- **Explicit owner and authorization first:** identify the one approved CPAMP-owned consumer for staging-to-official-CPA configuration application and its management credential/lifecycle protection. Existing B1 CPA config-write STOP remains in force. Do not use the private staged-key probe, create a second durable Key authority, or silently enable CPA Management API in the experimental seed.
- **Live effective evidence:** in one disposable, generation-bound, trusted CPA process with a synthetic upstream, demonstrate actual request-auth decisions **at the CPA data-plane** after any proposed key application, not just `PUT 200`, `GET` equality or a nonempty list. Observe absent/wrong/valid keys and prove which requests reach the upstream. Where active provider state cannot be demonstrated reliably, leave Ingress Pending.
- **Revocation and state changes:** do not claim effective authorization on a partial CRUD surface. Independently prove last-key denial, key replacement/revocation, async reload and stale-provider races, Manager/Runtime authority loss, process restart and cross-generation fencing. The current Fresh API does not yet expose an approved revoke/apply contract; record these as **NOT IMPLEMENTED / NOT RUN**, not implicit success.
- **No bypass at the actual boundary:** retain the bounded #1008 loopback namespace model; revalidate live CPA reachability for any new deployment/restart profile, and inspect *all* supported model/streaming/upgrade ingress routes before admitting requests. Preserve `409 managed_target_identity_mismatch`; post-restart live CPA remains **BLOCKED**.
- **Fail closed until complete:** Manager-staged match, CPA config readback, ConfigV8 write success or live child health can be supporting diagnostics, but none is sufficient by itself to set `runtimeEffective=true` or `gatewayAuthorized=true`. Public Gateway/Beta/External/Upgrade/Release remain **STOP**.

This is a source-only feasibility finding. No new Management write, runtime process experiment, release qualification, client-Key revocation implementation, Gateway admission rule or recovery path was performed or authorized by this audit. Overdesign, overdefense, overabstraction and scope expansion: PASS — reuse the current authority and private topology; do not add an attestation framework without a qualifying consumer.


## Official CPA v8.0.13 private key-effect test (Plan #52)

**Latest bounded development result:** the closed-loop run below passed the private experiment, including Supervisor-managed CPA, real key changes and a separate synthetic upstream control. The earlier STOP report remains historical. Keep the PR Draft for distinct independent acceptance; Gateway/Beta and post-restart qualification remain STOP.

The test-only [runner](../../../bin/ci/fresh-v8-private-key-effective-probe.py) makes **destructive client-key configuration writes**. It must run exclusively in a fresh, disposable, private #1008 Compose profile, never against a user's running CPA, existing volumes or actual credentials. The merged [Plan #52](https://github.com/seakee/cpamp-plan/pull/52) authorizes only this bounded experiment, **not** a Manager configuration writer or Gateway enablement.

**Preflight and invocation:**

1. Create a unique disposable Compose project with empty synthetic volumes. Use only the existing pinned official v8.0.13 loopback-proof image; independently verify the live CPA executable SHA-256 `b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41` and record the exact source, image and runtime generation.
2. Before *first* Runtime start, make an untracked, private, temporary copy of `docker/runtime/config.v8.loopback-proof.yaml` with a fresh strong synthetic `management.secret-key`. Mount it read-only at `/usr/local/share/cpamp/runtime/config.seed.yaml` in an isolated, untracked Compose override. The entrypoint copies that seed only if `/runtime/gateway/config.yaml` is absent. Do not change tracked seeds or start with any existing volume. Keep Management, CPA and Runtime control ports loopback-only.
3. Complete authorized Fresh bootstrap/start using the existing operation and generation contracts. Confirm live CPA, no user/provider/OAuth credentials or real upstream configuration, and initially empty `access.api-keys`.
4. Use the one-shot Python container below to join the *trusted Runtime network namespace*. Runtime has neither Python nor this script; do not install either in the product image. Only `127.0.0.1:8317` and `127.0.0.1:<ingress-port>` are addressed. The opt-in flags **do not** replace explicit verification of new empty volumes.
5. The runner checks private CPA model auth before and after official `PUT /v8/management/config/access/api-keys` install/replace/clear operations and demands public Ingress `503` throughout. Each phase needs **at least 2 seconds and 5 consecutive matching sweeps**, within the total `--reload-seconds` budget (default 15; allowed 3–60). Old-to-new transient decisions are recorded; an unexpected HTTP status, per-credential regression after first reaching its target, unreachable service or timeout stops immediately. Each HTTP result and sweep has UTC and monotonic elapsed timestamps. Sequential sweeps are sampled evidence, **not an atomic observation or a reload acknowledgment**. Reports never contain plaintext keys. If the last CPA Key removal restores anonymous access, record this as official CPA behavior, not CPAMP authorization.
6. A separate independent observer must still verify whether real synthetic model requests reach a synthetic upstream; the script's `/v1/models` request decisions do **not** prove upstream nonarrival/arrival, streaming or all supported model routes. On any unconfirmed write, unexpected response, topology change, security discrepancy or preserved `409` fence, STOP and destroy only the new disposable experiment containers/network/volumes. Do not retry uncertain writes, leak raw synthetic secrets into public issues, or bypass the generation fence.

This experiment does **not** implement automatic CPA config application, effective-Key attestation, revocation, reload completion acknowledgment, Gateway admission, or cross-generation restart recovery. Only the previously demonstrated Pending Ingress behavior is allowed. Gateway/Beta/External/Upgrade/Release continue STOP.

Overdesign, overdefense, overabstraction and scope expansion: PASS; one test-only runner, focused offline regressions and this existing evidence document, no new product owner or storage authority.

### Reproducible one-shot Python execution

Run from the exact reviewed worktree. Docker Engine must be Linux/amd64; Compose must support the existing namespace-sharing proof profile. Prepare a **unique** project and a private ignored scratch directory; check the intended public loopback port is free. Do not enable shell tracing. Never print the seed, environment file, full container environment, Management config body, initialization handoff or bootstrap responses.

```sh
umask 077
mkdir -p bin/tmp
proof_dir=$(mktemp -d "$PWD/bin/tmp/pr1012-private.XXXXXX")
export proof_dir
python3 - <<'PY'
import json, os, pathlib, secrets
p = pathlib.Path(os.environ['proof_dir'])
key = secrets.token_hex(32)  # 64 ASCII bytes; official CPA bcrypt limit is 72 bytes.
assert 32 <= len(key.encode('utf-8')) <= 72
seed = pathlib.Path('docker/runtime/config.v8.loopback-proof.yaml').read_text()
assert seed.count("secret-key: ''") == 1
(p / 'seed.yaml').write_text(seed.replace("secret-key: ''", 'secret-key: ' + json.dumps(key)))
(p / 'probe.env').write_text('CPAMP_PRIVATE_CPA_KEY_PROOF=1\n'
    'CPAMP_PROOF_DISPOSABLE_VOLUME=1\nCPAMP_PROOF_CPA_MANAGEMENT_KEY=' + key + '\n')
PY
```

Use an **untracked** override to bind `$proof_dir/seed.yaml` read-only at `/usr/local/share/cpamp/runtime/config.seed.yaml` on `cpamp-runtime`, before its first start. Keep the existing `docker-compose.fresh-auth-proof.yml` network topology, volume declarations and port bindings; no Docker socket, extra published ports, external volumes or user paths. Build reviewed source with task-specific image tags. Complete the existing Fresh initialize/claim/Admin/new-managed strategy workflow. The existing 409 generation fence is a STOP, never an invitation to reset the database or call a different Start path.

Before any Client Key write, verify and privately record:

- Project/container/network/volume creation times and Compose labels; names must not have existed before this run. Inspect the resolved Compose config privately and assert every mount belongs to this new project except the single synthetic seed. Confirm initially empty volume contents **before starting services**, and no OAuth/auth/plugin files before running the probe.
- Container IDs, image IDs, Linux/amd64, actual process executable/arguments/UID, `/proc/<cpa-pid>/exe` SHA-256, current Runtime identity/generation/active artifact and network namespace IDs. Recheck the same identity after execution; no service restart or other writer may overlap the run. Do not use stale image tags or a saved checksum alone.
- CPA 8317, Runtime 9081, Manager 18319 and Ingress control 9082 listen only on loopback. Only Ingress is published at host `127.0.0.1:$CPAMP_PROOF_PUBLIC_PORT`. An ordinary same-bridge participant must fail to connect to private ports, with a reachable Ingress/503 positive control.
- Full persisted seed/config contains only the constrained synthetic settings; no Provider, OAuth, existing Client Key, environment credentials, plugin or real upstream. Management credential comes from the private seed above. Runtime image remains without Python (`docker exec "$runtime_id" sh -c '! command -v python3'`). Read-only Management config/key-list checks must agree before writes.

The runner accepts only this seed's config shape, including disabled panel updates/log persistence/usage collection. Additional roots (including `requests.proxy-url`), OAuth options, TLS/bind changes and plugin settings are rejected before writes. It rechecks the previous Client Key list immediately before each of its three writes and stops on ownership drift. This is not a compare-and-swap API: the verified disposable topology **must have no concurrent writer**. The runner does not inspect Docker or `/proc`; those identity/storage checks remain explicit operator prerequisites, not self-attestation through opt-in environment flags. Synthetic upstream execution is **NOT RUN by this runner**; adding a Provider is outside its fixed empty-seed contract.

After these checks, set `runtime_id` from the **exact** project's `cpamp-runtime` container, `probe_image` to a locally inspected Linux/amd64 Python image ID (for example `python:3.13-alpine` resolved with `docker image inspect --format '{{.Id}}'`), and `CPAMP_PROOF_PUBLIC_PORT` to the profile's actual port. Then:

```sh
test -n "$runtime_id" && test -n "$probe_image"
docker run --rm --pull=never --platform linux/amd64 \
  --network "container:$runtime_id" --read-only --cap-drop ALL \
  --security-opt no-new-privileges --user 65534:65534 \
  --env-file "$proof_dir/probe.env" \
  --mount "type=bind,src=$PWD/bin/ci/fresh-v8-private-key-effective-probe.py,dst=/probe.py,readonly" \
  "$probe_image" python3 -B /probe.py --ingress-port "$CPAMP_PROOF_PUBLIC_PORT" \
  >"$proof_dir/probe.json" 2>"$proof_dir/probe.stderr"
```

No production image change, extra port or Docker socket is required. Read the exit status and both evidence files even on failure; STOP means no further config write, retry or automatic rollback. Keep the private evidence directory mode 0700/files 0600. Only publish a reviewed, secret-scanned summary. Destroy only the task-owned project containers/network/new volumes after collecting evidence; never run a global prune. Protect/remove private seed material under the same task-owned cleanup scope.

Offline checks (safe without Docker or credentials):

```sh
python3 -m py_compile bin/ci/fresh-v8-private-key-effective-probe.py bin/ci/fresh-v8-private-key-effective-probe-test.py
python3 bin/ci/fresh-v8-private-key-effective-probe-test.py
```

### Independent acceptance, 2026-10-19 — STOP / NOT MERGE-READY

**Tested implementation:** product `16dfbdca955bb371242c3f0dbf18b50521a87a08`, based on `v2@22b6f786415ba227e9cb64e350237906d98ec06f`. The subsequent report-only commit does not change the tested runner, test cases, Docker inputs or product code. Official source was inspected at tag v8.0.13, commit [`d7914afdedca7af95ee974a42453dc49fc1388ce`](https://github.com/router-for-me/CLIProxyAPI/tree/d7914afdedca7af95ee974a42453dc49fc1388ce), after inspecting the local CPA checkout and establishing that it did not contain the fixed tag. Do not substitute that checkout's newer source for the pin.

**Round 1 — confirmed P2 fixes:**

- **P2-1, fixed:** Runtime contains neither Python nor the script. The documented one-shot Linux/amd64 Python container joins the verified Runtime namespace, uses a read-only script mount and private environment file, and requires no product image change, published port or Docker socket. The actual container ran `--help` and all offline regressions successfully as UID/GID 65534 with all capabilities dropped. The destructive live path was **not executed**, because CPA failed its prerequisite below.
- **P2-2, fixed in code / live validation BLOCKED:** a single successful sweep no longer passes. Every phase needs the bounded 2-second/5-sweep minimum and records request timestamps, old/new/absent/wrong statuses, transitional sweeps and the final sampled window. Unexpected statuses, per-credential regression, transport uncertainty and timeouts STOP. Management PUT/readback cannot produce an effective-auth PASS. No generic retry engine, persistent state or automatic rollback was added.
- Python compilation, `git diff --check` and **13 offline regression tests PASS** on the host and in the one-shot Python container. Cases cover one-match insufficiency, old-to-new convergence, full/partial regression, absent-key fail-open during replacement, unexpected HTTP status, timeout, Ingress non-503, uncertain writes, ownership drift, malformed/unsafe seed projections and sanitized preflight evidence. These are simulated negatives, not live CPA authentication results.

**Round 2 — complete static review and re-review:**

- **Additional P2, fixed:** the reviewed `02a4c95b` preflight could accept settings outside the fixed proof seed, including the officially supported `requests.proxy-url` (which can carry non-synthetic proxy credentials). A mocked source-level reproduction showed the old runner reaching PUT with that extra field; the fixed runner rejected the same projection before any write. Restricting the writable projection to the fixed empty seed closes that target-validation gap. This defect **already existed at the previous reviewed HEAD**; it was not introduced by the first repair. **Review correction:** Management GET normalizes historical Provider key-family aliases; the earlier proposed alias-bypass explanation is not established and is not a finding. Unknown/retired configuration test fixtures are rejection tests, not proof that official CPA executes those fields. Each later write also rereads the expected prior Key list and stops on foreign changes. This does not claim atomic exclusion of a concurrent writer; operator-verified exclusive ownership is required.
- All **3 changed files** were reviewed, with no skipped changed files. Cross-checks covered official ConfigV8 persistence/async callback, v8 legacy aliases, access-provider reconciliation, empty-provider behavior, real request middleware, CPAMP staged-key handler, Manager Fresh strategy/generation checks, Ingress Pending branch, Runtime child start/readiness and proof Compose/entrypoint. No remaining confirmed in-scope static blocker was found at `16dfbdca`; real execution remains a separate blocking acceptance gate.
- The runner uses fixed loopback TCP addresses and direct stdlib HTTP, follows no redirects, never logs response bodies or credentials, performs no uncertain-write retry/rollback and keeps `gatewayAuthorized=false` / `runtimeEffective=false`. Docker identity, empty storage/auth directories and single-writer checks are explicit external prerequisites, not proven by the runner's opt-in flags. No default Compose, binary, staged-key authority, generation fence or product configuration writer was changed.

**Round 3 — actual disposable Docker evidence:**

Docker Desktop Engine 27.3.1 Linux/amd64 and Compose 2.30.3 ran project `cpamp-pr1012-61f154ff`. All project resources were absent before creation; all **3 newly created volumes were inspected empty before any service start**. Only a synthetic private seed was bind-mounted, read-only. The only published port was host `127.0.0.1:38471`. No real account, credential, Provider, OAuth or existing volume was used.

| Identity | Observed value |
| --- | --- |
| Runtime container | `d33e6a5c31c2157e098304a18531295f4c4f422ec002a96475d082b49f2c1d8b` |
| Manager container | `d63c1ada5bec0b0504ebf0dd87bddd17ca3c02d2df24f2c56c5337c876451c36` |
| Ingress container | `3afdeb9d7c80121e8a710634fd63ae0ab45a3a9f327687ede82ae1f020c34c4a` |
| Runtime image | `sha256:075b6822a17a4cee76904055a1c684ee0e321256960a035ddfa27afefb257314` |
| Manager image | `sha256:34d0c09631af5eb9ab1cd3e6ad8cafa29228d24317fca0670a0240806e3b22c2` |
| Ingress image | `sha256:418e55792cb2827c0c50dfd05109c10cb9949b9d4d1deee765b7ab76e6c385c0` |
| One-shot Python image | `sha256:050f6d1f4e5f89af62e03f6fcd1c06be2a401339b3694adc4c0a2850d9d8b1e7` |
| Shared exec network namespace | `net:[4026532699]` in all three containers; Docker also reported Manager/Ingress sharing the exact Runtime container |
| Runtime identity / generation | `embedded-auth-proof` / `13920945367327046344` (unchanged); all container restart counts 0 |
| Official file / artifact identity | v8.0.13, Linux x86-64 ELF; SHA-256 `b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41`; same active artifact reported by authenticated Runtime status |
| **Live CPA process identity** | **BLOCKED:** child exited before `/proc/<pid>/exe`, PID/start-time and live listener attestation could be completed |

The original Fresh initializer handoff was used without a database reset. Known auxiliary-command errors were preserved: the first read-only Runtime query omitted its artifact-feature header; an immediate Bootstrap read returned 503 before the Fresh listener was admitted; one claim request incorrectly included handoff-only `resume` metadata and was explicitly rejected with 400. After correcting that request shape, the **same issued handoff** exchanged successfully (200), Admin commit returned 200, and the two distinct canonical strategy steps returned 200 with staged then committed checkpoints. No uncertain mutation was retried. The accepted Start journal row nevertheless did **not** establish CPA health.

Runtime observation immediately after the committed strategy was `offline`; independent `/proc` inspection subsequently found **no CPA child**. At **13:12:25.722276 UTC**, authenticated `GET /v1/runtime/status` returned **200**, still `offline`, with recovery `manual_intervention` and 0 attempts remaining. The existing Supervisor journal recorded one `start` and three `auto_recovery` operations as `succeeded` (13:10:43.019, 13:10:44.185, 13:10:45.351 and 13:10:46.516 UTC), while the child had exited. Those three attempts were existing Supervisor behavior, not retries or a recovery feature introduced by this PR. The experiment issued **no** additional Start/restart/restore or generation override.

The child exit cause is **undetermined**: the existing child launcher discards stdout/stderr, and no child log was available. A successful spawn journal entry, configured version or active artifact is not live process proof. No production-image/debugging/lifecycle repair was attempted within this experiment.

| Required live case | Actual result / raw observation |
| --- | --- |
| Initial zero-Key raw CPA authentication | **BLOCKED**: no live CPA. Persisted config still byte-for-byte equals the initial synthetic empty seed; no runtime-auth inference from that file. |
| Install one Key; PUT/readback/effectiveness | **NOT RUN**: 0 Client Key configuration writes. |
| Missing / wrong / valid Key at CPA data-plane | **BLOCKED**: no raw CPA HTTP authentication statuses available. |
| Replace Key; old invalidation/new activation/async transition | **NOT RUN**: no measured transition or activation latency. Offline convergence tests do not substitute for this case. |
| Clear final Key; official anonymous behavior | **NOT RUN**: source confirms the possible empty-provider bypass; this run did not reproduce it. |
| Public Ingress while CPA unavailable | **Observed 503**, missing and synthetic-wrong credentials, at **13:12:25.723835** / **13:12:25.724867 UTC**. This is limited offline Pending evidence, not live CPA isolation or proof of packet nonarrival. |
| Private Management HTTP | At **13:12:25.722496 UTC**, loopback 8317 connection failed: **no HTTP response**, not 401/503. |
| Untrusted bridge participant vs live CPA/Management | **BLOCKED / NOT RUN after STOP**: an offline port refusal cannot prove live process isolation. Private control binds and the sole host-loopback publication were inspected; no claim of a live CPA listener. |
| CPA packet nonarrival / synthetic upstream arrival | **NOT RUN**: no live CPA calibration, no synthetic Provider and no upstream. No forwarded-request or successful-model inference from `/v1/models` or Ingress 503. |
| Restart / concurrent writer / routed IPv6 / remote host / streaming | **NOT RUN / existing lifecycle BLOCKED**; no generation-fence bypass. |

**STOP was honored before invoking the destructive probe.** All new resources were collected privately and destroyed by **13:15:11.019724 UTC**: exactly 3 containers, 3 synthetic volumes and the project's single network. Label- and ID-scoped post-cleanup checks found **0 remaining project resources**; no global prune or unrelated-resource cleanup was used.

**Evidence / CI:** raw timestamps, HTTP results (including the auxiliary 503/400), journal/WAL snapshot, process/socket inspections, image/container identities, resolved private override, setup commands, synthetic seed and disposable data snapshots are retained in a local private evidence package. Raw seed/environment/session/Admin/Runtime credentials are **not public artifacts**. Only this reviewed summary may be published. Implementation HEAD `16dfbdca` passed PR Scope, Required checks and PR Template (Actions **39257549072 / 39257549103**); existing path classification skipped product build/test jobs. This is not CI coverage of Python or proof of live CPA acceptance. The final report-only HEAD must receive its own applicable check results before closeout.

**Round 4 — scope self-check:** Overdesign **PASS** (no test platform or product feature); Overdefense **PASS** (fixed-seed and ownership checks protect the destructive operation); Overabstraction **PASS** (one bounded observation loop and status-event helper); Scope Expansion **PASS** (runner, focused tests, existing evidence document only). No Gateway, Beta, release, formal Manager-to-CPA writer, revocation or recovery capability is granted.

**Conclusion: STOP; keep #1012 Draft and do not merge.** The two original P2 repairs and the additional bounded preflight repair are delivered, but the required real official CPA authentication/transition evidence and live process isolation are absent. A separately authorized investigation must establish why the disposable official child exits; only a fresh, fully attested run can satisfy this experiment's remaining acceptance gate. Successful static review, offline tests or CI cannot replace it.

### Closed-loop development verification, 2026-10-19 — bounded experiment PASS

This is **Codex development self-check, not ChatGPT's final independent acceptance**. It consumes merged [Plan #52](https://github.com/seakee/cpamp-plan/pull/52), [Plan #53 / engineering policy §11.8](https://github.com/seakee/cpamp-plan/pull/53) and the maintainer's explicit complete-slice authorization. Tested implementation: `63260846812bdf240927c675d431888dd5626ffd`, based on `v2@22b6f786415ba227e9cb64e350237906d98ec06f`. The following report-only commit changes no runner, tests, Docker inputs or product code. The exact final HEAD and its applicable CI are recorded in the PR handoff.

**Delivered correction and internal iteration.** The already committed 64-byte `secrets.token_hex(32)` Management Secret fixes the previous 87-byte/bcrypt failure; the documented preparation snippet was executed unchanged and produced three correct environment-file entries. No shared Supervisor defect or production repair was required. One supplemental synthetic-provider attempt stopped before any Client Key write because its temporary comparison omitted official ConfigV8's injected `auth_index`. Exact upstream `internal/api/handlers/management/config_v8.go:55` confirms this read-only metadata. The temporary client now checks its 16-hex-digit shape and excludes only that field from the exact synthetic-provider comparison. That first environment was destroyed; the corrected test ran in an entirely new environment. The maintained runner's strict empty-provider preflight was not relaxed.

#### Supervisor-managed identity and storage

Docker Engine 27.3.1 Linux/amd64, Compose 2.30.3-desktop.1. Every project/container/network name was absent before creation; each of the **nine volumes across three attempts** was independently inspected empty before service start. Only private synthetic seeds were mounted, read-only; no Docker socket, real Provider/OAuth/user credential, existing volume or Runtime generation was used. The two successful projects were:

| Fact | Maintained empty-provider runner | Separate synthetic-upstream control |
| --- | --- | --- |
| Project | `cpamp-keyaccept-0d441826` | `cpamp-upstream-f2198421` |
| Runtime container | `9bbf21daa6cca0755712439297521d1b86fd238b93290401a9fa2d0decf8448c` | `639d7e55db244fda03d9f0ce2a636fe245075f764515227b8cad727dbe733062` |
| Runtime identity / generation | `embedded-auth-proof` / `17188170161380247809` | `embedded-auth-proof` / `3140510193300187722` |
| CPA PID / start ticks | `301` / `1124`, unchanged before/after experiment | `294` / `33104`, unchanged before/after experiment |
| Identity observation interval, UTC | 14:26:41.369075–14:26:57.738892 | 14:31:23.492564–14:31:41.652108 |
| Sole published endpoint | host `127.0.0.1:51509` Ingress | host `127.0.0.1:51965` Ingress |
| CPA executable / argv / cwd | `/usr/local/bin/cli-proxy-api`, no arguments, `/runtime/gateway` | same |
| Actual process UID/GID/groups | 10001 / 10001 / 10001 | same |
| Effective config | `/runtime/gateway/config.yaml`, 0600, 10001:10001 | same |

Actual `/proc/<pid>/exe` hashes match **`b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41`**, official CPA v8.0.13 Linux/amd64, upstream tag commit `d7914afdedca7af95ee974a42453dc49fc1388ce`. Immutable Runtime image `sha256:075b6822a17a4cee76904055a1c684ee0e321256960a035ddfa27afefb257314`, Manager `sha256:34d0c09631af5eb9ab1cd3e6ad8cafa29228d24317fca0670a0240806e3b22c2`, Ingress `sha256:418e55792cb2827c0c50dfd05109c10cb9949b9d4d1deee765b7ab76e6c385c0` were reused after verifying their product/build inputs have no changes from image-build implementation `16dfbdca`. Python observer image: `sha256:050f6d1f4e5f89af62e03f6fcd1c06be2a401339b3694adc4c0a2850d9d8b1e7`.

Both CPAs started through canonical Fresh initialize/claim/Admin/new_managed staged -> committed, without direct Start substitution, checkpoint reset or generation-fence bypass. Repeated authenticated Runtime status returned **ready**, `/healthz` returned **200**, authenticated Management key-list GET returned **200 / []** before and after the experiments. Each copied journal contains exactly **one succeeded Start and zero auto_recovery**; recovery stayed armed with all three attempts remaining. All container restart counts were zero. Actual `/proc` identity, repeated HTTP, unchanged PID/start ticks/generation and live socket tables establish the bounded steady-state evidence; journal success or an artifact declaration alone does not.

`/runtime/supervisor/destination` was absent. No destination stage or `-config` path was used. This matters because `lifecycle/destination.go:217` can generate a wildcard listener for a different, unexercised adoption path: if that path appears in this loopback experiment, STOP instead of assuming the seed remains effective.

#### Actual key-write and authentication matrix

The maintained runner executed as UID/GID 65534 in the existing trusted Runtime namespace with a read-only script mount, all capabilities dropped and no extra port. Exit code **0**, result **PASS_WITH_EXPLICIT_STOPS**, `gatewayAuthorized=false`, `runtimeEffective=false`. Exactly three official Management PUTs, no retries or rollback; each returned 200 and was followed by matching persisted readback. The separate request-auth results below establish live effect independently of those write/readback signals.

| Phase | PUT completion UTC / status | Private `/v1/models` statuses in every sampled stable sweep | Stable observation | Public Ingress |
| --- | --- | --- | --- | --- |
| Initial zero keys | no write | absent 200, wrong 200, generated key 200 | 9 sweeps / 2.052491 s | 27 requests, all 503 |
| Install first | 14:26:50.509668 / 200 | absent 401, wrong 401, first 200 | 9 / 2.058114 s | 27, all 503 |
| Replace first with second | 14:26:52.718265 / 200 | old 401, new 200, absent 401, wrong 401 | 9 / 2.058016 s | 27, all 503 |
| Clear last | 14:26:54.927563 / 200 | old 200, new 200, absent 200, wrong 200 | 9 / 2.055994 s | 27, all 503 |

There were **108 Ingress 503 checks** in these four phases. No transient state was sampled after the maintained runner's write/readback sequence; its first post-readback sweep already matched each target. This is not evidence of synchronous reload or zero latency. Its loop still required the full bounded stability window; timeout, regression and uncertainty negatives remain tested.

The supplemental control's independent read-only observer spanned **before PUT through convergence**, collecting **4,148 individual model-list HTTP observations**. It observed the old and new states during install/replace/clear; each credential's compressed sequence had at most one status change, with no observed regression or unexpected status. During replacement, absent/wrong stayed 401; old changed 200 -> 401 and new 401 -> 200. The replacement PUT completed at **14:31:35.595054**; first observed target requests were old/401 at **14:31:35.602576**, new/200 at **14:31:35.602992**. These are request-start timestamps from sequential requests, not an atomic key-swap measurement or exact activation latency. All four supplemental phases also satisfied 9-sweep, >2-second stable windows. Raw observations preserve the time order rather than attributing effect to PUT 200 or config readback.

**Zero-key 200 and anonymous forwarding are official CPA data-plane behavior, never CPAMP authorization.** CPAMP public Ingress remained Pending in every sampled request, including after removing the last key. No Manager-staged key result was promoted into a Runtime-effective grant.

#### Independent synthetic upstream and network boundaries

The maintained runner continues to reject any Provider. An actual negative run in the separate provider environment returned **STOP before any PUT attempt**, proving that protection remains in force. A different, temporary one-shot control then validated its exact known synthetic configuration before writes; it did not claim to be the maintained empty-seed runner.

Its only extra seed resource was one `api-keys.openai-compatibility` provider: name `proof-upstream`, URL **`http://127.0.0.1:18080/v1`**, `request-retry: 0`, a newly generated synthetic credential and explicit model/alias `proof-model`. No proxy, real account or external upstream was configured. A separate unprivileged, capability-free Python container shared only the trusted Runtime network namespace and served a fixed non-streaming `/v1/chat/completions` response on loopback 18080. It had no published ports or product-volume access. The observer recorded only timestamps, synthetic request IDs, path/model and a boolean confirming the synthetic upstream credential; it did not log authorization values.

| Phase | Actual private chat statuses: absent / wrong / old / new | Independently observed upstream arrivals | Public Ingress POST |
| --- | --- | --- | --- |
| Zero keys | 200 / 200 / 200 / 200 | 4 matching synthetic IDs | four 503, zero matching extra arrivals |
| First key installed | 401 / 401 / 200 / 401 | only old ID, 1 arrival | four 503, zero extra arrivals |
| Key replaced | 401 / 401 / 401 / 200 | only new ID, 1 arrival | four 503, zero extra arrivals |
| Last key cleared | 200 / 200 / 200 / 200 | 4 matching synthetic IDs | four 503, zero extra arrivals |

Every allowed private POST returned the fixed synthetic completion. Exactly **10 arrivals**, **6 rejected private requests without arrivals**, and **16 public Ingress requests without arrivals** were correlated against the independent upstream's raw console observations, including a post-request drain interval. Successful requests before/after the negative cases calibrated upstream reachability. This proves only those tagged, non-streaming requests in the measured window; it does not prove every protocol or permanent packet nonarrival.

Live socket tables showed CPA 8317, Runtime 9081, Manager 18319 and Ingress control 9082 on **127.0.0.1**; Manager/Ingress shared the exact Runtime namespace. Before/after each successful experiment, ordinary same-bridge observer containers made three rounds against all four private ports: **48 total refused connections**, while **12 direct bridge Ingress positive controls returned 503**. These negatives were performed with the matching CPA process alive. Only host-loopback Ingress was published. No Management/CPA/control port, Docker socket or additional host listener was exposed.

#### Final regressions, evidence and scope

| Check | Result |
| --- | --- |
| Python syntax; 13 offline positive/negative regressions | PASS on host; all 13 also PASS in unprivileged read-only Linux/amd64 Python container |
| Corrected Secret on original Supervisor-managed Fresh consumer | PASS; live identity, repeated Ready/health/Management, no exit/recovery churn |
| Install / replace / clear; sampled async stability | PASS, actual official CPA, three writes per successful project |
| Extra Provider rejection before write | PASS, actual Management view in the separate lab |
| Synthetic non-streaming forwarding / rejection / Ingress nonarrival | PASS for the exact tagged requests above |
| Full changed-file self-review | 3/3 files reviewed, none skipped; no remaining confirmed in-scope blocking defect |
| Product rebuild | Not rerun: only documentation changed after tested implementation; immutable image inputs verified identical. Applicable final-HEAD CI is required separately. |
| Restart / cross-generation reactivation | NOT RUN / existing BLOCKED gate preserved; no bypass of 409 |
| Streaming, Responses, WebSocket/upgrade, routed IPv6, independent remote host | NOT RUN; no broader qualification claim |
| Formal Manager-to-CPA writer / enabled Gateway / External / Upgrade / Beta / Release | STOP / not authorized by this private experiment |

Final cleanup removed exactly the three task-owned Compose projects, nine new volumes, three project networks and two synthetic upstream containers, plus their ephemeral observer containers. Primary cleanup completed **14:30:41.776003 UTC**; corrected upstream project **14:32:17.403651 UTC**. The first failed supplemental project's evidence and zero-write facts were retained before its separate cleanup. Scoped post-cleanup checks found zero project resources; no global prune or unrelated-worktree cleanup occurred.

Local evidence directories are `bin/tmp/pr1012-complete.1S09dY`, `bin/tmp/pr1012-upstream.FaiR2n` (pre-write comparison failure), and `bin/tmp/pr1012-upstream-fixed.9zDrPZ`. The handoff supplies the local-only raw archive, hashes and sanitized event package. Raw seed/env/handoff/session/Runtime tokens and full Docker/config snapshots remain private; public report/event files are checked against known credentials before publication. No plaintext synthetic key is included here.

Overdesign **PASS** (no product test platform); Overdefense **PASS** (exact target checks retained); Overabstraction **PASS** (existing bounded observer reused, temporary control only); Scope Expansion **PASS** (no lifecycle/auth framework, formal writer, default image/Compose or official binary change). Merge-value self-assessment: **15/18** — importance 3, scope 1, fit 3, evidence 3, implementation 3, risk cost 2. These scores cannot grant independent acceptance.

**Conclusion: bounded private-experiment development PASS; submit the stable exact HEAD for independent acceptance.** Recommend source-only merge only after that review and applicable exact-HEAD CI. Keep #1012 Draft and do not auto-merge. This evidence grants no Gateway/Beta operation, generation recovery or release eligibility.
