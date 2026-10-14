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

`source-provenance.json#/sourceArtifact` pins Actions artifact 11884714735 by
run/name/digest and complete file inventory. It contains the original DB,
fixture-only key/config, explicit sidecar/archive absences, producer archive,
generation records and logs. DB/key/runtime logs are not committed. The artifact
has **90-day retention, expiring 2027-01-12T10:14:44Z**; this is not a permanent
immutable Release. Expiry/deletion is an evidence-availability gap and must
fail closed; regeneration must not be substituted for the original digest.

With GitHub Actions read access, use a new task-owned output directory:

```sh
gh run download 38548199153 --repo seakee/CPA-Manager-Plus \
  --name ret01-source-38548199153-1 --dir <original>
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

The existing workflow generates once on Linux and shares that fresh case
fixture across Linux/macOS replay; the original pinned artifact is separately
byte-verified on Windows/Linux/macOS. No new production credential, trusted
authority, public API or second fixture framework is introduced.
