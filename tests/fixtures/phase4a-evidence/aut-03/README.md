# AUT-03 artifact-integrity evidence

AUT-03 is the Phase4A-07 Lane A direct-candidate case from Plan main
`2719d8466951da1ea457bd1a00dca3fa6f09dc43`:

- exact candidate: `v1.14.4@166262365d506157b99968105c890f480cc7c07c`;
- trusted input: the published Linux AMD64 producer member in the immutable
  SRC-11 qualification archive;
- outer sealed input pin: release `412319673`, asset `639129253`, archive
  SHA-256 `c6abbb0594a874708b41795de6d1035fd4263fec1c067496a8b0bb1d26fec6d2`
  (also recorded in `src-11/sealed-artifact.json`);
- contract: a wrong archive or identity is rejected with `ErrUnverified`
  before extraction/execution; an altered staged executable is rejected with
  `ErrExecutableMismatch` before sealed launch;
- authority: this is diagnostic qualification evidence only. It never creates a
  trusted hop receipt or `UpgradeAuthorized=true`.

The executable harness is
`apps/manager-server/internal/service/legacyartifact/aut03_qualification_test.go`.
It consumes the existing deployment-owned artifact pins and existing offline
preflight. It does not add a production API, authority bootstrap, signature
mode, or trusted validator.

The runtime matrix records the original and mutated SHA-256 values, rejection
category/stage, actual typed error, executable-start observation, source tree
digest before/after, output/workspace existence, operation/receipt/trusted
evidence state, and final authority fields. `trustedEvidenceMinted=false` means
that no trusted operational evidence was minted; the diagnostic AUT-03 record
itself is deliberately separate and carries its own `recordSHA256` for
record-byte consistency checking:

| Mutation | Runtime assertion |
| --- | --- |
| archive byte flip | archive pin rejects before inspection/extraction |
| extracted executable byte flip | executable pin rejects before sealed launch |
| v1.14.4 archive claimed as another pinned platform asset | archive identity rejects |
| v1.14.4 archive claimed as v1.14.5 | release/source identity rejects |
| sealed input manifest digest replacement | existing offline input preflight rejects before workspace creation; this is a boundary control, not AUT-05 qualification |

The manifest/inventory, missing/extra file, unsafe path, symlink/hardlink and
duplicate-member cases remain owned by the existing sealed materializer and
artifact-parser tests. They are referenced by the AUT-03 runtime evidence and
are not copied or reclassified as AUT-03 authority qualification.

## Re-run from a clean checkout

The workflow downloads the existing immutable SRC-11 evidence archive outside
Go tests, runs the existing materializer/adversarial suite, and then runs the
focused Linux AMD64 test against the real producer archive and sealed bundle:

```sh
gh release download qualification-src11-0e106589baef-20261014 \
  --repo seakee/CPA-Manager-Plus \
  --pattern src-11-0e106589baef-sealed-evidence.tar.gz --dir <scratch>
export CPAMP_SRC11_SEALED_ARCHIVE=<scratch>/src-11-0e106589baef-sealed-evidence.tar.gz
python3 -m unittest discover -s bin/qualification -p 'test_*.py' -v
python3 bin/qualification/materialize-src11.py \
  --archive <scratch>/src-11-0e106589baef-sealed-evidence.tar.gz \
  --output <scratch>/src11-original

mkdir -p <scratch>/aut-03
(
  cd apps/manager-server
  CPAMP_AUT03_ARCHIVE=<scratch>/src11-original/producer/cpa-manager-plus_v1.14.4_linux_amd64.tar.gz \
  CPAMP_AUT03_ASSET=cpa-manager-plus_v1.14.4_linux_amd64.tar.gz \
  CPAMP_AUT03_INPUT_BUNDLE=<scratch>/src11-original/src-11/bundle \
  CPAMP_AUT03_CODE_COMMIT="$(git rev-parse HEAD)" \
  CPAMP_AUT03_EVIDENCE=<scratch>/aut-03/evidence.json \
  go test ./internal/service/legacyartifact -run '^TestAUT03Qualification$' -count=1 -timeout 10m
)
```

The generated `evidence.json` has `runtimeAssertions=pass`,
`casePass=true`, and `independentAcceptance=pending` when all runtime
assertions pass. The workflow uploads it as the actual run artifact; it is not
a receipt, upgrade permission, or completion of Phase4A-07/Phase4A.
