# Lane B: private Embedded Fresh CPA loopback isolation candidate

Baseline: product `v2@60f4ca76b0b78e7bbc6c2f8461dbc400afa74b37` (merged #1007); merged CPAMP Plan #47/#48/#49/#50. Evidence checked against experimental HEAD `6d83b9e413b90d20d47d0803b53d5838138b6ac3`.

**Status: LIMITED PRIVATE TOPOLOGY PROOF PASSED; end-to-end Client Key / post-restart live CPA qualification BLOCKED.** An independent disposable Docker Desktop linux/amd64 run verified live official CPA v8.0.13 steady-state IPv4 bridge isolation, fail-closed Gateway Pending and explicit Compose restart namespace reattachment. This is an opt-in, nonproduction experiment only. Do not publish a Gateway, treat it as production-ready, or forward real model traffic.

## Observed source-level risk

The current default Compose sends Ingress traffic to `cpamp-runtime:8317`, but both existing CPA seeds listen on `0.0.0.0:8317` (`host` in v7, `server.host` in the official v8 opt-in seed). The Supervisor's `CPAMP_RUNTIME_CPA_ADDR=127.0.0.1:8317` is a local readiness target, **not** the CPA bind configuration. No host `ports` mapping for 8317 does not prevent a peer on the Docker network from connecting directly to a running CPA process and bypassing Ingress.

## Bounded candidate

The standalone `docker-compose.fresh-auth-proof.yml` uses the same official pinned CPA v8.0.13 Linux/amd64 binary without modifying its source. A separate seed makes CPA listen on **127.0.0.1:8317**. Trusted Manager and Ingress both use `network_mode: service:cpamp-runtime` to share Runtime's network namespace. CPA (8317), Supervisor control (9081), Manager (18319) and Ingress control (9082) bind **loopback only**; Ingress alone binds the public listener inside that namespace. Only its port `${CPAMP_PROOF_PUBLIC_PORT:-28471}` is published on **host 127.0.0.1**, with the same internal and external port so Manager can verify the actual public origin.

Manager retains its original SQLite, protection key and Runtime token authority. Its loopback upstream and runtime URLs reach the shared namespace without exposing them to untrusted Docker network peers. The original Manager public-origin verification is preserved: both its configured URL and the Ingress socket resolve to `127.0.0.1:${CPAMP_PROOF_PUBLIC_PORT:-28471}` within the proof namespace. This is a topology candidate, **not** verification of Manager-to-CPA management/config writes or any Client Key authority. The normal Compose, default v7 runtime build, official v8 opt-in candidate, Legacy, External, Upgrade and release profiles are unchanged.

Current Fresh Ingress still returns **503** for model requests. Manager-staged Key records from #1007 do not prove effective Runtime keys. This network topology experiment intentionally does **not** create a Key verifier, runtime binding or an enabled Gateway.

## Explicit Compose restart — namespace recovery verified, CPA restart still blocked

Independent disposable Docker Desktop evidence for HEAD `3f53958a1b73e7867d663dfa42c9422718842326` revealed a real P2: a standalone `docker compose restart cpamp-runtime` moved Runtime to a new network namespace while Manager/Ingress retained the old namespace. All containers showed Up but the host Ingress endpoint returned an empty reply. Restarting the two consumers restored a shared namespace and Gateway Pending (503), but CPA was offline after its owning Runtime restarted. A stopped CPA is not a positive isolation proof.

At HEAD `6d83b9e413b90d20d47d0803b53d5838138b6ac3`, independent local regression **PASS** demonstrated that **Compose-native `depends_on.restart: true`** restarted both trusted consumers automatically after an explicit `docker compose restart cpamp-runtime` (Compose v2.30.3). Docker events, `StartedAt` timestamps, real namespace IDs and socket/HTTP observations confirmed all three containers rejoined the new namespace without manual consumer restarts; the public Ingress again returned 503 and Manager's origin was reachable. This is not proof of interruption-free recovery. `docker restart`, `docker compose restart --no-deps`, out-of-Compose service restarts, Docker daemon restarts and container-runtime automatic restarts were NOT RUN and are not in this profile's supported restart contract. Each service remains configured with `restart: 'no'`.

The independent regression executed the following command against disposable synthetic data after starting CPA through the existing authorized Fresh Runtime lifecycle:

```sh
docker compose -p cpamp-fresh-auth-proof -f docker-compose.fresh-auth-proof.yml config
docker compose -p cpamp-fresh-auth-proof -f docker-compose.fresh-auth-proof.yml restart cpamp-runtime
```

**Separate, unresolved lifecycle gate:** after this restart Runtime generation changed and CPA was offline; the canonical, authorized Fresh recovery attempt returned `409 managed_target_identity_mismatch` because its stored checkpoint targeted the old generation. The existing identity/generation fence must not be relaxed or bypassed. As a result, **post-restart live CPA isolation and live CPA packet nonarrival are BLOCKED**, not PASS. A later, separately authorized Fresh Strategy/Runtime Lifecycle recovery consumer must establish these claims before any profile claims automatic CPA recovery or private end-to-end integration readiness.

**Evidence limits:** no truly remote network observer was available (NOT RUN); the tested bridge had routed IPv6 disabled (routed IPv6 NOT RUN). Live local `::1`/host-loopback checks were recorded, but are not substitutes for routed IPv6 or an external independent host. Do not generalize this proof beyond the tested host-loopback/IPv4-bridge profile or change the Docker architecture solely to force irrelevant claims.

## Real-process evidence — independently run

The first disposable local acceptance used HEAD `3f53958a1b73e7867d663dfa42c9422718842326` and identified a real namespace-split P2 after a Runtime-only restart. A second independent regression used HEAD `6d83b9e413b90d20d47d0803b53d5838138b6ac3`; its exact supported restart path passed. The original acceptance artifacts are retained outside this repository; this README summarizes their observed results, not a new run by the document author.

1. **Build / exact binary — PASS:** `docker compose ... config` and `up -d --build`, real packaged Manager/Ingress/Runtime, official pinned CPA v8.0.13 binary SHA256 `b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41`. Final Dockerfile runtime target remains default v7.
2. **Live CPA isolation — PASS in tested steady-state profile:** approved Fresh initialize/claim/Admin/strategy Start produced a live CPA child, with 127.0.0.1:8317 listener. Trusted Ingress-to-CPA connection succeeded; untrusted same-bridge peer could not connect to CPA 8317, Runtime 9081, Manager 18319 or Ingress control 9082 (12 refusals with reachable Ingress/503 positive controls). Real Manager Public Origin HTTPDriver route activation succeeded.
3. **Host/public route — PASS in tested local profile:** host `127.0.0.1:28471` reached Ingress; no private port was host-published. The local-host and IPv6 loopback probes do not qualify remote-network or routed-IPv6 behavior.
4. **Gateway Pending — PASS before Runtime restart:** model, streaming/upgrade and lookalike requests returned 503; the calibrated CPA listener packet monitor observed no forwarded requests while CPA was live. Explicit Compose Runtime restart automatically reattached both consumers; Ingress stayed 503 and Manager origin reachable. **After Runtime restart**, CPA was offline and canonical recovery was rejected with 409; repeat live CPA isolation/nonarrival tests remain **BLOCKED**.
5. **Client Key data-plane authorization — NOT IMPLEMENTED / STOP:** Manager-only staged keys do not prove Runtime-effective keys or per-request verification. Zero/missing/wrong/valid Key, last-Key revoke and authority/reload fail-closed admission for enabled Gateway require a separate authenticated consumer and live-process acceptance before any model forwarding.

The separately merged CPAMP Plan #50 explicitly permits **source-only merge of this opt-in network experiment** on the proven limited scope, without implying private integration eligibility. Docker Engine control/host root or containers explicitly admitted into the trusted network namespace are not modeled as ordinary untrusted bridge peers. Every unsupported deployment mode must be independently qualified before it is advertised.

## Gate and complexity

Gate: **P0/STOP** for public Gateway, public Beta, production exposure, External, Upgrade and release. No second identity store, general proxy framework, CPA fork or unrequested Runtime feature. Overdesign, overdefense, overabstraction and scope expansion: **PASS for this narrowly scoped candidate**. Steady-state and explicit Compose restart network experiments have been independently accepted within their stated scope; cross-generation live CPA reactivation and complete authenticated model data-plane integration are not accepted. Source merge of this opt-in profile does not change these gates.
