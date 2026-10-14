# RET-01 exact v1.14.4 retention/pricing evidence

This is the Plan-defined full-raw RET-01 case, not the SRC-11 bundle under a
new label. The existing generator launched the independently accepted exact
published v1.14.4 producer with `profile.json` (RET-01-specific request/session
identities), then stopped/reaped it, checkpointed SQLite and sealed a new
complete bundle. `source-generation/record.json`, `generation-index.json` and
`generation-runner.json` bind the actual process, producer, generator commit,
controlled input and generated inventory. SRC-11's accepted producer identity
is a dependency; its database/key/bundle and historical records are unchanged.

The source bundle digest is
`82e2287ed64ea6584a54d53e7de06f8d657cbddf2df9c7f5e682b9c3e937da9c`.
Generation ran in Actions run 38548199153 (the generation job passed; that
intermediate run's replay/index jobs failed because the new evidence had not
yet been recorded). `GeneratorCodeCommit` is the actual checked-out PR merge
commit `2c8579c77d79612c8dc5d310d9f8b09c4fa038c2`, not its head alias.

`runtime/` records actual macOS replay at commit
`5780feac5c5738110ac285e8631babbbe0729aed`; `platform-replay/linux/` records
offline Linux replay of the **same original case bundle**. Each exercises
SEM-01/02/03/06/08, retained/historical pricing, manual authority, configured
zero, Devin Revision 2, metadata/session and quota/reset continuity, SQLite
integrity/FK/checkpoint, and complete backup → new-root restore. Raw deletion
and SEM-09 are RET-03, not applicable here. Windows validates evidence bytes
and compiles the harness; full Windows SQLite replay remains unqualified.

`runtimeEvidence=pass` does not grant acceptance: `casePass=false`,
`independentAcceptance=pending`, 48 cases pending, `UpgradeAuthorized=false`,
`TrustedHopReceipt=false`. Lane D, Phase4A-07 and Phase4A remain incomplete.

## Original bytes and clean-checkout verification

`source-provenance.json#/sourceArtifact` pins immutable qualification Release
`412621764`, asset `640121411`, tag `qualification-ret01-82e2287ed64e-20261014`,
archive size/SHA256, generation record, bundle digest and complete file inventory.
It contains the original DB,
fixture-only key/config, explicit sidecar/archive absences, producer archive,
generation records and logs. DB/key/runtime logs are not committed. The ZIP is
**byte-for-byte Actions artifact 11884714735**, SHA256
`e7f5df5ce2792a7049d2fac29f7c67b9d7e407237e52167c22b1c9eefc7ff790`.
All 19 files, the original ZIP and bundle remain unchanged; nothing was
regenerated or repackaged. Actions origin metadata is historical only: neither
its retention nor its availability is an evidence dependency anymore. The
Release is immutable, prerelease and not latest; it is not a product release.
Its tag targets archival commit `9eeba5f442a100168f715637781cec3644d1e1bb`,
not a replacement generator/execution identity or independent acceptance.

With GitHub release read access, use new task-owned output directories:

```sh
gh release download qualification-ret01-82e2287ed64e-20261014 \
  --repo seakee/CPA-Manager-Plus \
  --pattern ret-01-82e2287ed64e-sealed-evidence.zip --dir <download>
gh api repos/seakee/CPA-Manager-Plus/releases/412621764 > <download>/release.json
python3 bin/qualification/materialize-src11.py --case ret-01 \
  --release-metadata <download>/release.json \
  --archive <download>/ret-01-82e2287ed64e-sealed-evidence.zip --output <original>
CPAMP_RET01_FULL_EVIDENCE=<original> PYTHONDONTWRITEBYTECODE=1 \
  python3 -m unittest discover -s bin/qualification -p 'test_ret01.py' -v
python3 bin/qualification/run-src11.py ret01 \
  --archive <original>/producer.tar.gz --evidence <original>/ret-01 \
  --output <new-replay-output>
```

Use `--linux` for the pinned offline Linux container. The verifier compares
literal bytes (no LF/CRLF normalization); `.gitattributes` fixes checkout LF.
The runner revalidates every generation hash and complete bundle with existing
production integrity primitives before restoring into a new root. Runner JSON
records commands, exact execution commit, toolchain, source-file digests,
executable/output digests and exit result; replay logs are task-owned and CI
uploads runtime logs/results separately.

## Equivalent controlled regeneration

On a clean committed checkout with the pinned Docker image available:

```sh
python3 bin/qualification/run-src11.py generate-ret01 \
  --archive <original>/producer.tar.gz \
  --release-info <original>/ret-01/source-generation/release-info.json \
  --output <new-generation-output>
python3 bin/qualification/run-src11.py ret01 \
  --archive <original>/producer.tar.gz \
  --evidence <new-generation-output>/ret-01 --output <new-replay-output>
```

Controlled regeneration is an opt-in equivalent-fixture diagnostic only; it
does not replace the original evidence identity. The existing workflow now
replays the **same sealed original fixture** on Linux/macOS and byte-verifies
it on Windows/Linux/macOS. It neither regenerates historical input nor depends
on expiring source Actions artifacts. CI replay output artifacts remain temporary.
No new production credential, trusted
authority, public API or second fixture framework is introduced.
