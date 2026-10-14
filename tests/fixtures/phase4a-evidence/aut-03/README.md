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

Plan's frozen `reject / ErrUnverified` is the case-level artifact-integrity
rejection description. This harness records the production stage-specific
typed errors without changing them: archive rejection is `ErrUnverified`,
executable rejection is `ErrExecutableMismatch`. These are not Go error aliases
(`errors.Is(ErrExecutableMismatch, ErrUnverified)` is not asserted). This typed
refinement is explicit for the subsequent independent acceptance/Plan closeout;
no independent acceptance or Plan amendment is performed by this PR.

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

The materializer verifies the entire inventory on the real trusted input.
Its existing adversarial tests exercise outer-digest mismatch, unsafe paths,
links, duplicates and preserving existing output; they do not separately mutate
every missing/extra/replaced inventory member. Those categories are not claimed
as additional AUT-03 runtime mutations or authority qualification.

The workspace parent uses the existing `testTempDir` private-storage helper.
The real trusted archive verifies and extracts successfully; its executable
also passes sealed memfd preparation, which is closed without launching it.
A `RunOfflineCleanup` precondition control uses the real SRC-11 sealed bundle,
the same private parent, manifest digest and timeout as the altered-archive run.
It must pass artifact/bundle validation and reach real sealed launch, child
exit zero and reap, proving that the same parameters do not hit the earlier
`ErrOfflineInputs` or `ErrUnverified`. The **later post-launch source
revalidation** returns `ErrInvalidSource`, nil observation and a durable
`failed` control operation, with no sealed output. These actual control results
are recorded separately; this is not a successful cleanup or source
qualification. Full cleanup/source compatibility is outside AUT-03 and the
production error/recovery behavior is unchanged, never converted to warning.
Only archive bytes differ for the altered-archive run; its observation must be
nil and error must be `ErrUnverified`, never an earlier `ErrOfflineInputs`.
All **mutation** workspaces/output must remain absent. Final directory inventory
must contain only the isolated failed control and deliberate mutation/extraction
files; the sealed source and its parent inventory/modes must remain unchanged.
The failed control is retained under the existing diagnostic failure contract
until disposable test workspace cleanup, never mislabeled as a successful
operation, trusted receipt or upgrade grant.

## Code provenance

The old evidence in `8e015ce7` bound `testedCommit` to the base without this
harness and is withdrawn, not corrected by editing its SHA. Schema v2 separates
the product identity (`trustedInput.sourceCommit`, the immutable v1.14.4
producer) from `qualificationHarnessCommit`, `testedTree` and `harnessSHA256`.
The runner rejects dirty runtime source, pins, workflow or harness, and embeds
these values in the test binary at build time. Runtime environment claims
cannot override them. `runner-evidence.json` separately records source-file,
binary, input and log digests plus the actual commands. Formal evidence is
generated only after the harness commit; an evidence-only follow-up commit
does not change what code was actually tested. CI records its actual checkout
merge commit/tree, not an assumed PR head.

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
python3 bin/qualification/run-aut03.py \
  --sealed-archive <scratch>/src-11-0e106589baef-sealed-evidence.tar.gz \
  --output <scratch>/aut-03
```

On non-Linux hosts add `--linux` to run the cross-built test in the existing
pinned isolated Linux AMD64 image from `run-src11.py`. The sealed bundle and
executable inputs are read-only mounts; their disposable materialized parent
allows the primitive's temporary inspection copy. Output must be absent.
CI path filters cover all Manager internal transitive dependencies and Go
module changes; this adds no duplicate full-repository workflow.

The generated `evidence.json` has `runtimeAssertions=pass`,
`casePass=true`, and `independentAcceptance=pending` when all runtime
assertions pass. The workflow uploads it as the actual run artifact; it is not
a receipt, upgrade permission, or completion of Phase4A-07/Phase4A.
