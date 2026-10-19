# Opt-in strict-auth Runtime candidate

This is a private, nonproduction Linux/amd64 integration candidate. The default
`runtime` target remains CPA v7.3.3; `v8-amd64-candidate` remains the unmodified
official v8.0.13 target. Neither target needs this candidate's local input.
Compose, Installer, CI defaults and supported deployment claims are unchanged.

## Controlled build input

Obtain the reviewed S1-A executable through the restricted workspace. Keep it
outside the public checkout and name it `cli-proxy-api` in a dedicated directory.
Do not put private source, executable bytes, credentials or reproduction logs in
this repository, a public build context, registry or CI artifact.

The only accepted input for this target is:

- Candidate source: `1fbb56551218bc7eadad1598d6009e9248447cf6`.
- Official parent: `d7914afdedca7af95ee974a42453dc49fc1388ce`.
- Executable SHA256: `0adcfa2283438cdb041e78074eae89321ca5986e5dc5d1da399b8def3ca22150`.
- Version: `8.0.13-cpamp-strict-auth.1`, built using Go 1.26.9,
  CGO enabled, Linux/amd64. This is not the official release executable.

```sh
docker buildx build --platform linux/amd64 \
  --target v8-amd64-strict-auth-candidate \
  --build-context strict-auth-input=/controlled/local/cpa-input \
  -f Dockerfile.runtime --load -t cpamp-runtime-strict-auth:local .
python3 bin/ci/runtime-strict-auth-image-check.py cpamp-runtime-strict-auth:local
```

The explicit target verifies the input's fixed digest during build. It copies
the bytes without rebuilding or stripping them, generates a separate
`sha256:<executable digest>` artifactId, and retains the existing four-field
`cpa-artifact.json` contract. Source/parent/input-mode metadata is stored in a
separate root-owned read-only `cpa-provenance.json`; Supervisor parsing is not
extended. Supervisor, entrypoint and the existing v8 seed are reused unchanged.
The seed's empty-key warning still applies to the official target; the strict
candidate supplies different authentication behavior with identical seed bytes.

A rebuilt executable with a different digest is a different candidate and must
have its provenance and focused verification reviewed before changing this pin.
Do not override the digest or reuse the official binary's artifactId.

## Verification boundary

The image check verifies actual packaged executable bytes, runtime-loadable
version/source identity, manifest/provenance, immutable metadata permissions,
seed identity and existing Supervisor/entrypoint selection. It uses a disposable
container without network or persistent mounts. A mismatched local input must
also fail the build's checksum check.

The separate restricted joint smoke runs real Manager, Ingress, Supervisor and
CPA processes with synthetic credentials and an upstream stub on an internal
Docker network without published ports or user-data mounts. It checks initial
empty-key rejection, valid/invalid/missing credentials, final-key removal,
upstream request counts, management authentication, health and process restart.
The existing `runtime-lifecycle-manager-baseline.sh` supplies an explicitly
synthetic historical Manager fixture. This is not a real Upgrade or a Fresh New
journey; no Fresh production authority or qualification record is modified.
Detailed mutation transcripts and the security fixture stay in restricted local
evidence, outside this public repository.

Do not infer External, plugin, real-provider, public Beta or production support
from this candidate. Runtime integration acceptance does not change the
default target, deployment policy or release gates.

## Observed integration result (2026-10-19)

Product input: `v2@b3287b5fbd665c789c0405ba87bc15f2e408fd5d` plus this
packaging change. The verified executable was reused byte-for-byte; no CPA
rebuild was performed. Runtime image ID/config digest:
`sha256:7d8813f9fcc47498a1dd5209693bfad7c26f9f1a9eaa1948fc387ae299e87009`.
This is a local image identity, not a published registry digest.

| Focused check | Observed result |
| --- | --- |
| Candidate build and packaged identity check | Exit 0; exact input digest, manifest/provenance, seed and executable identity match |
| Incorrect local executable | Build exits 1 at checksum verification |
| Default v7 and official v8 builds without local context | Exit 0; original image IDs, executable digests and seeds preserved |
| Supervisor artifact/selection tests | Both packages pass, exit 0 |
| Real joint smoke | One isolated profile passes, 113 HTTP status assertions; no published ports or host-data mounts |
| Empty seed and configured data plane | Missing/invalid credentials rejected; valid models and chat reach the controlled fixture |
| Final-key removal, restart, recovery | Rejection survives reload/restart; restoring a valid key restores legitimate traffic |
| Upstream effects | Zero unauthorized requests; exactly two expected authorized chat requests |
| Management and health | Real Manager Admin proxy and CPA Management authentication work; Manager health and CPA HEAD health pass |

The joint smoke retained exact live executable hashes/UID, container/image
identities, network isolation and cleanup records. Raw security evidence is in
the restricted local S1-B workspace and is intentionally not published here.
The standalone S1-A 189-assertion suite was not rerun. Real accounts/providers,
plugins, other architectures, Fresh New/real Upgrade, External and public Beta
qualification remain untested by this slice. Existing asynchronous reload
timing is retained; this change does not promise immediate credential revocation.
