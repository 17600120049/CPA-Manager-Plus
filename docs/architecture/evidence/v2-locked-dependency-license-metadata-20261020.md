# CPAMP v2 — Locked dependency license metadata baseline (2026-10-20)

**Scope:** read-only incremental source inventory for the existing [Release and NOTICE delta](v2-release-candidate-and-licensing-delta-20261020.md). Product source **[`v2@9af6e1faae5b1a24c0ade509354fec1ae3a03a5b`](https://github.com/seakee/CPA-Manager-Plus/commit/9af6e1faae5b1a24c0ade509354fec1ae3a03a5b)**. This report resolves only a *locked dependency-metadata enumeration* gap. It does **not** attest copyright/redistribution compliance, actual shipped contents, a software bill of materials, or Beta/Release readiness.

## 1. npm lockfile census (metadata, not shipped bundle)

Source: [`package-lock.json`](../../../package-lock.json), lockfileVersion `3`. The lock spans Web **and Docs** workspaces; it includes build/test tools, transitive packages and optional platform dependencies. Count **distinct `node_modules/` lockfile entries, not unique projects**; exclude the two workspace links and three workspace/root entries. No npm packages were installed or fetched for this analysis.

| Lockfile `license` metadata value | Package entries |
| --- | ---: |
| `MIT` | 456 |
| `Apache-2.0` | 18 |
| `ISC` | 15 |
| `MPL-2.0` | 12 |
| `BSD-2-Clause` | 7 |
| `BSD-3-Clause` | 4 |
| `0BSD` | 3 |
| `Python-2.0` | 1 |
| `CC0-1.0` | 1 |
| `BlueOak-1.0.0` | 1 |
| `CC-BY-4.0` | 1 |
| **Total** | **519** |

Of the 519 package entries, **448** have `dev: true` and **71** do not. All 519 have a `license` field in this lockfile snapshot. **Do not equate `dev: false` with actual runtime redistribution, or `dev: true` with exemption from distribution/notice review.** These flags are lockfile metadata, not a determination of final Docker / Full Web binary contents.

### Exact package/version records for focused attribution review

These entries merit **explicit source-license/asset/output verification**, not an assumption of noncompliance. In particular, many `lightningcss` rows are mutually exclusive optional platform builds, not twelve necessarily distributed copies.

| Lock path suffix (`node_modules/` omitted) | Locked version | Declared license | `dev` |
| --- | --- | --- | --- |
| `echarts/node_modules/tslib` | `2.3.0` | `0BSD` | absent |
| `tslib` | `2.8.1` | `0BSD` | absent |
| `zrender/node_modules/tslib` | `2.3.0` | `0BSD` | absent |
| `@typescript-eslint/typescript-estree/node_modules/minimatch` | `10.2.5` | `BlueOak-1.0.0` | true |
| `browserslist/node_modules/caniuse-lite` | `1.0.30001759` | `CC-BY-4.0` | true |
| `@iconify-json/simple-icons` | `1.2.88` | `CC0-1.0` | true |
| `lightningcss` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-android-arm64` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-darwin-arm64` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-darwin-x64` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-freebsd-x64` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-linux-arm-gnueabihf` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-linux-arm64-gnu` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-linux-arm64-musl` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-linux-x64-gnu` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-linux-x64-musl` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-win32-arm64-msvc` | `1.32.0` | `MPL-2.0` | true |
| `lightningcss-win32-x64-msvc` | `1.32.0` | `MPL-2.0` | true |
| `@eslint/eslintrc/node_modules/argparse` | `2.0.1` | `Python-2.0` | true |

## 2. Go module *require* inventory (licenses not inferred)

Inputs: [Manager](../../../apps/manager-server/go.mod), [Runtime Supervisor](../../../apps/runtime-supervisor/go.mod), [Ingress](../../../apps/ingress/go.mod), from the same exact commit. Declared `require` counts: Manager **12**, Runtime **10**, Ingress **0**. De-duplicated union **12 module paths**, including direct and indirect requirements; the two nonempty modules agree on shared pinned versions.

| Required module | Version |
| --- | --- |
| `github.com/dustin/go-humanize` | `v1.0.1` |
| `github.com/google/uuid` | `v1.6.0` |
| `github.com/mattn/go-isatty` | `v0.0.20` |
| `github.com/ncruces/go-strftime` | `v1.0.0` |
| `github.com/remyoudompheng/bigfft` | `v0.0.0-20230129092748-24d4a6f8daec` |
| `golang.org/x/exp` | `v0.0.0-20251023183803-a4bb9ffd2546` |
| `golang.org/x/sys` | `v0.37.0` |
| `modernc.org/fileutil` | `v1.3.40` |
| `modernc.org/libc` | `v1.67.6` |
| `modernc.org/mathutil` | `v1.7.1` |
| `modernc.org/memory` | `v1.11.0` |
| `modernc.org/sqlite` | `v1.46.1` |

No license identities are asserted from module names or Go `require` entries. Exact module archive/source license texts, transitive build graph, linked code and notices in final images remain **NOT RUN**. This source list cannot establish which portions of dependencies are shipped.

## 3. Upstream executable and packaged notices

- CPAMP [root `LICENSE`](../../../LICENSE) currently declares **MIT**. The approved future CPAMP-owned v2 BSL policy is separate and **not yet implemented**; do not relabel today's artifacts as BSL.
- Official embedded CPA **v8.0.13** declares **MIT** in [upstream LICENSE](https://github.com/router-for-me/CLIProxyAPI/blob/v8.0.13/LICENSE), with its own copyright/attribution. Its actual upstream notice bytes are **not explicitly copied by the current `Dockerfile.runtime` final-stage recipe**; that is a source-level packaging gap, not proof that no license text exists elsewhere in image layers. CPA must not be represented as CPAMP-owned BSL code.
- The final-stage [Manager](../../../Dockerfile.manager-server), [Ingress](../../../Dockerfile.ingress) and [Runtime](../../../Dockerfile.runtime) recipes do not explicitly copy a comprehensive `THIRD_PARTY_LICENSES` or CPAMP/CPA notice bundle. Actual final-image file/layer inspection is **NOT RUN**.

## 4. Remaining verification — bounded to the Release owner

1. At an **exact rebuilt product HEAD**, inspect final Manager/Ingress/Runtime image layers and extracted Full Web output; determine **which of the above locked dependencies and assets are actually shipped**. Include fonts/icons/assets and base-image package notices. Do not treat lockfile presence as proof of distribution.
2. For shipped components only, fetch and preserve **actual matching-version LICENSE/NOTICE texts and attribution** (not solely `package-lock.json` metadata); verify that the generated notices remain present in source, native, OCI and other supported delivery artifacts as applicable.
3. Verify the **CPA v8 upstream notice** separately and without relicensing it, plus eventual CPAMP license/README/CONTRIBUTING consistency under the independently approved legal/governance process. No unilateral BSL switch or legal classification in this slice.
4. Retain the previous Release identity, actual Docker topology, provenance, readability, migration, readiness and public Beta STOPs. This report is not an independent Docker replay or a public deployment permit.

### Repeatable read-only source count

```python
import collections, json
with open("package-lock.json", encoding="utf-8") as f:
    packages = json.load(f)["packages"]
locked = [v for p, v in packages.items() if p.startswith("node_modules/") and not v.get("link")]
print("entries:", len(locked), "dev:", sum(v.get("dev") is True for v in locked))
print("license metadata:", dict(sorted(collections.Counter(v.get("license", "NOT_DECLARED") for v in locked).items())))
```

**Evidence limits:** source-only, no Docker daemon, network package install, running services, published assets, secret material or user data. This report is a targeted continuation of #1018, not a replacement for its binary-checker evidence, nor authorization to edit runtime packaging (owned by the parallel default-v8 slice).
