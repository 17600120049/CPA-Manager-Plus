# Embedded candidate build, load and Fresh New smoke — 2026-10-27

Outcome: **PASS** for the local `linux/amd64` candidate build, its structural
verification, the local load identity check and the disposable Fresh New smoke
on exactly those images. The CPA data layer is reported as
`DEFAULT AUTHORITY MISSING`: a labelled known gap (lane P), not a pass and not a
tool failure.

This is local-only evidence. It is **not** a signed production receipt, not a
registry readback, and not a Beta/Release or public-Gateway qualification. No
push, tag, GitHub Release, channel or `update-index` write happened.

## Source identity

| Item | Value |
| --- | --- |
| Branch | `build/v2-candidate-smoke-refresh` |
| Base | `v2@49579583b719f9b5058ca2ffa73b752ca6787de1` |
| Tested commit | `4eee135cb23579f15a22b8d9ab15e1e2f4ccdaa9` (clean tree) |
| Smoke `diffSha256` | `e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855` (SHA-256 of empty input → no uncommitted bytes) |
| Candidate version claim | `2.0.0-r1candidate` |
| Candidate build event | `local-r1-0e0a88a447d54ff0809e7b3a011e013d` |
| Platform | `linux/amd64` |
| Builder | dedicated `docker-container` builder, `CPAMP_CANDIDATE_BUILDER` |

Tooling commits on this branch:

- `ed3f2e05` — dual-format export (OCI evidence + docker load transport) and the
  opt-in load/identity check.
- `8fa4d955` — `/health` 503→200 assertions, CPA data-layer assertions with the
  `DEFAULT AUTHORITY MISSING` label, configurable model target.
- `4eee135c` — smoke aligned with the shipped Fresh setup wizard, plus an offline
  contract that fails when the smoke drives a test id the Full Web no longer
  renders.

## Candidate artifacts

| Role | `imageConfigDigest` = loaded image Id | Platform manifest | OCI layout index | OCI archive SHA256 |
| --- | --- | --- | --- | --- |
| manager | `sha256:44773a389436dff8bbfc3bd34ed22e3a01ce9d44a28942b92548140f038b3917` | `sha256:36e22f1970c68a7b2dea7b2a42b42ef6915b5da0f3917ea35698920cca30c075` | `sha256:644392554ee6a11a30a059f56c976ed531aaad6074c045a6f1cde047dc1c72ef` | `sha256:4dab73e01b5811ae4aa380b2888278eaf6ebb25cb8892762525f4653ff8dc798` |
| ingress | `sha256:013f5d7b8de1375ec1c66b875221b970b52af88aa51622d1d9f993812d1ad26a` | `sha256:b46e7e61f5ae372290ee56e2bf856b6b3177166d03ce336a0bc8502f0875740c` | `sha256:facc3508f2567449108f16fddbe2bc14eaadb3da6bf9bb577c20019406520ffb` | `sha256:78ae0e1dafb8e98adc38d9a448d94a4cd3243b1bc4c4308dfee16ad5b399e060` |
| runtime | `sha256:65628b2e1201699732dca7154d58e2a7ec4618bf4b91e555fadf78ff00028e9b` | `sha256:c0f610a968c44e2f040ff6c2a08a938123ac34c5d0a752823cc13296fc8ffd0f` | `sha256:9a3050d5d25f4f1536c02779a14991176f369d111675d2a41bdb39035e20318f` | `sha256:2f853748af75a07840e32d55ef7b611634bd3c87a381a181588cb5ede2fc71db` |

`verify-embedded-candidate.py` ran on the three OCI archives and recorded
`STRUCTURAL_CANDIDATE_EVIDENCE_ONLY_NOT_RELEASE_QUALIFIED`. The runtime image
still carries the pinned official CPA `8.0.13` executable
`sha256:b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41`, which
the running process matched after installation.

### Load transport

The local engine rejects an OCI-layout archive, so the **same** buildx
invocation exported a docker-format tar per role. `docker load` of that tar
produced an image Id equal to the verified `imageConfigDigest`, with the config
blob and every layer digest equal to the OCI archive:

| Role | Layers | Config blob matches OCI | Layer digests match OCI | Reference |
| --- | --- | --- | --- | --- |
| manager | 8 | yes | yes | `cpamp-r1-manager:candidate` |
| ingress | 5 | yes | yes | `cpamp-r1-ingress:candidate` |
| runtime | 11 | yes | yes | `cpamp-r1-runtime:candidate` |

`load-evidence.json` records the same result with status
`LOCAL_LOAD_IDENTITY_ONLY_NOT_RELEASE_QUALIFIED`. The docker tar is a load
transport for the same build result — not a second candidate and not a rebuild.
The reference comes from the buildx `--tag` of that invocation, so no separate
`docker tag` step exists. Any identity or digest mismatch stops the run with no
fallback build; skopeo was not used.

## Smoke run

Disposable Compose project `cpamp-fresh-new-506e0e77cd50`, random loopback port,
`python:3.13-alpine` helper container, Chrome driven by Playwright. The script
pinned the three supplied images by local Id, and the report records the same
digests as the candidate evidence. Cleanup of its own project, volumes and
helper volumes: PASS. Uncaught browser errors: none. No Handoff, Admin or
Runtime token appears in the service logs or the report.

| Check | Result |
| --- | --- |
| first boot waits for Host initialize without creating database or data key | PASS |
| unclaimed Manager `/health` is 503 before Host initialize | PASS |
| expired Handoff is rejected with Host recovery guidance (offline expiry fixture) | PASS |
| wrong Handoff installation identity is rejected without consuming the valid claim | PASS |
| real browser claims once, removes fragment and rejects consumed Handoff | PASS |
| Fresh takeover `/health` is 200 with the Manager service identity | PASS |
| weak Admin Key rejected; server-generated Key commits once despite lost response | PASS |
| POST `/.well-known/cpamp-bootstrap/api/v2/strategy` | HTTP 200 |
| POST `/.well-known/cpamp-bootstrap/api/v2/strategy` | HTTP 200 |
| POST `/console/api/v2/complete` | HTTP 200 |
| one installation submission committed official CPA and the exact public route; controlled activation failure and lost replies recovered without replay | PASS |
| original CPAMP login page, language/theme tools and remember-credential control render on desktop/mobile light/dark | PASS |
| Console rejects missing/wrong Admin Key and accepts the installed Admin Key | PASS |
| deployment Runtime observation rejects missing/wrong administrator credentials | PASS |
| one user installation started the pinned official CPA; running executable and saved seed match | PASS |
| Fresh New completion enters the existing CPAMP MainLayout; authenticated Config GET/PUT remains readable | PASS |
| managed retry configuration panel layout (hidden: gateway retry fields not exposed on this base) | NOT RUN |
| existing CPAMP two-level navigation, breadcrumbs, history, desktop collapse and mobile drawer work in light/dark themes | PASS |
| language selection updates the existing Console and restores Chinese | PASS |
| original global confirmation can be canceled without logout; original update provider loads and persists channel | PASS |
| zero Client Keys: all tested Gateway paths remain Pending; anonymous peer cannot read CPA | PASS |
| Manager/Runtime/Ingress restart preserves identity, completion, routing, data key and saved setting; Admin relogin works | PASS |
| stale Runtime generation returns `409 managed_target_identity_mismatch` without rebinding or retry | PASS |
| remembered login revalidates the real Admin Key after refresh and restores the original deep route | PASS |
| explicit logout clears remembered authority; refresh stays on the original login page | PASS |
| explicit Host lifecycle Start restores the new Runtime generation without rewriting installation provenance | PASS |
| completed installation rejects repeated initialize; admin/authority/settings and identity/key files unchanged | PASS |
| empty-existing-db cannot be admitted as Fresh; source bytes and modes unchanged | PASS |
| unknown-db cannot be admitted as Fresh; source bytes and modes unchanged | PASS |
| legacy-settings cannot be admitted as Fresh; source bytes and modes unchanged | PASS |
| incomplete-marker cannot be admitted as Fresh; source bytes and modes unchanged | PASS |
| no browser storage, uncaught browser errors or secrets in service logs | PASS |

Writes observed: 2 strategy submissions (start, then the committed reconcile),
2 routing activations (one controlled 503, then the real activation), 1
administrator commit. Every strategy submission used the observed Runtime
generation. The installation reached `productComplete`, the Runtime reported
`ready`, and a restart changed the Runtime generation while leaving identity,
completion, routing, `data.key` and the saved setting intact.

### Data layer — expected gap on this base

| Check | Result | Observed |
| --- | --- | --- |
| `cpamp/` resource bridge reads CPA `/auth-files` | DEFAULT AUTHORITY MISSING | HTTP 428 |
| Client Key installed into CPA | DEFAULT AUTHORITY MISSING | HTTP 409 |
| CPA model API accepts the installed Client Key | DEFAULT AUTHORITY MISSING | not attempted |
| CPA rejects an unknown Client Key | DEFAULT AUTHORITY MISSING | not attempted |
| CPA accepts one real model request with the installed Client Key | DEFAULT AUTHORITY MISSING | not attempted |
| Ingress Gateway stays Pending with an installed Client Key | DEFAULT AUTHORITY MISSING | not attempted |

The default Fresh New Managed installation still has no installation-derived CPA
management authority (lane P), so the bridge answers `428` and the Client Key
install answers `409`. These rows must become PASS after P merges; they are
labelled, recorded in `report.authority` / `report.expectedGaps`, and never
counted as a pass. The model request target is configurable
(`CPAMP_SMOKE_MODEL_TARGET`); this run used the default
`http://cpamp-runtime:8317` on the Compose network, where CPA stays on Runtime
loopback, so an anonymous peer is `unreachable` rather than readable.

## NOT VERIFIED

- **NOTICE NOT VERIFIED** — the candidate third-party inventory is still
  `NOT_VERIFIED`; LICENSE/NOTICE legal sufficiency is out of scope.
- Real Provider/model traffic, public Gateway admission, arm64, Native/External/
  Adopt/Upgrade journeys, power-loss and production rollback.
- Natural ten-minute claim expiry (the fixture injects an expired server
  timestamp into a stopped, disposable Manager).
- Managed retry configuration layout (reported NOT RUN while the gateway
  allowlist exposes no retry field; it returns with lane C2).
- Non-Chinese locale layouts, other browser engines, manual UI walkthrough.
- Registry readback, signed producer identity, Beta/Release qualification.
- Data-layer success on the default installation (blocked on lane P).

## Re-run on a new HEAD

From a clean worktree of the new HEAD (P and G stacked branch later):

```sh
git status --porcelain            # must be empty: the builder refuses a dirty tree
export CPAMP_CANDIDATE_BUILDER=cpamp-r1-local
docker buildx create --name "$CPAMP_CANDIDATE_BUILDER" --driver docker-container --bootstrap
CPAMP_CANDIDATE_LOAD=1 bash bin/release/build-embedded-candidate.sh /tmp/cpamp-r1-$(date +%s)

PLAYWRIGHT_MODULE=/absolute/path/to/node_modules/playwright \
PLAYWRIGHT_CHANNEL=chrome \
CPAMP_SMOKE_MODEL_TARGET=http://cpamp-runtime:8317 \
node bin/ci/fresh-new-installation.mjs \
  cpamp-r1-runtime:candidate cpamp-r1-manager:candidate cpamp-r1-ingress:candidate \
  /tmp/cpamp-fresh-new-$(date +%s)
```

Expect `PASS` with the three `LOAD PASS` lines and the same three digests in
`candidate-evidence.json`, `load-evidence.json` and `report.json`. After P
merges, the six `DEFAULT AUTHORITY MISSING` rows must become PASS; point
`CPAMP_SMOKE_MODEL_TARGET` at the published Ingress Gateway once lane G admits
model traffic there. `npm run test:repo` and
`python3 tests/embedded_candidate_contract_test.py` stay offline and must pass
before any build.

## Limits of this record

Local Docker Desktop daemon, `linux/amd64`, host loopback publication only. The
candidate archives, `candidate-evidence.json`, `load-evidence.json` and the
smoke report stayed in `/tmp` scratch directories created for this run; the
durable record is this document plus the recorded digests. Machine reports can be
regenerated with the commands above.
