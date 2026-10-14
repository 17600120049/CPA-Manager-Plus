# SRC-11 exact v1.14.4 evidence

This directory contains the latest real SRC-11 source-generation and v2
primitive replay evidence. It is bound to the Phase4A-07 Plan main commit
`26b5d6bde1c30e8187135c87a8b84b6c808f23f0` and to generator/verifier commit
`c43a0542da81d5b60638a2f89d0d54ac75cc66ff`.

The source producer is the published exact artifact:

- SourceRelease: `v1.14.4`
- SourceCommit: `166262365d506157b99968105c890f480cc7c07c`
- immutable release ID: `404941931`
- asset ID: `616064478`
- asset: `cpa-manager-plus_v1.14.4_linux_amd64.tar.gz`
- archive SHA256: `eab3a90594cb70e0362b0a37f9737f7b249402c0971ff4c7134a53079de1e033`
- executed producer SHA256: `0588b7f28010825cde26511dfecafbb4a70f6c26d35f4cc4998b2f90434b3a98`
- release-info SHA256: `b8f7b97cd242c11d95b905e2b402275fc5d664025bc829773a260d1da17d18b5`

`source-generation/record.json` binds the producer/build identity,
generator identity, controlled profile digest, safe process environment
references, exit/reap observation, checkpoint and SQLite checks, config,
quota/cooldown and pricing writes, source snapshot, and the full bundle
inventory. `bundle/manifest.json` records the complete sealed inventory,
including absent WAL/SHM/journal/archive elements and the generated bundle
digest. The source variant is full raw with no source archives and no raw
deletion.

The replay evidence exercises the existing v2 migration, identity, monitoring
stats/metadata/projection, hourly, pricing and account-history primitives. It
records authoritative-field preservation, event dedupe, historical pricing,
manual-price authority, explicit zero pricing, metadata/session continuity,
quota/reset/cooldown evidence, sanitized exports, Devin revision 2 accounting,
and the labelled retention descendant. `backup-restore.json` records matching
new-root restore and SQLite integrity/foreign-key/checkpoint results;
`tamper-rejection.json` records wrong-key and non-empty-journal rejection.
`linux-replay/` repeats the same replay in the pinned Linux AMD64 container;
its result and semantic files carry the Linux runtime observation.

The original sealed DB, disposable fixture-only data.key, producer.log, build
and execution logs, and complete original Darwin/Linux retention backups are
preserved in an immutable qualification Release asset. `sealed-artifact.json`
pins its release/asset IDs, outer archive SHA256, original bundle manifest
SHA256, generation record SHA256 and packaging inventory SHA256. These are the
original generation bytes with source bundle digest `0e106589baef4148ee075ed77ce93bc71e83ca315a377311d62daeb9144236e3`;
no fixture regeneration or metadata edit was used to repair packaging. The
archive also includes the authentic published v1.14.4 producer archive.

All keys/data are disposable qualification values, never production data. The
archive preserves the historical evidence snapshot (including its historical
README omission statement) verbatim; its `packaging.json` explains the corrected
packaging. Source-generation records and their original digests remain unchanged.

From a clean checkout, download the qualification asset outside the Go test:

```sh
gh release download qualification-src11-0e106589baef-20261014 \
  --repo seakee/CPA-Manager-Plus \
  --pattern src-11-0e106589baef-sealed-evidence.tar.gz --dir <scratch>
python3 bin/qualification/materialize-src11.py \
  --archive <scratch>/src-11-0e106589baef-sealed-evidence.tar.gz \
  --output <scratch>/original
python3 bin/qualification/run-src11.py replay \
  --archive <scratch>/original/producer/cpa-manager-plus_v1.14.4_linux_amd64.tar.gz \
  --evidence-archive <scratch>/src-11-0e106589baef-sealed-evidence.tar.gz \
  --output <scratch>/darwin-replay
# Repeat the same replay with --linux --output <scratch>/linux-replay.
```

Materialization verifies the pinned outer digest before extraction, rejects
unsafe paths/links/duplicate entries, verifies every archive member against the
complete packaging inventory, and verifies the pinned original manifests.
Replay then performs the existing `maintenancebundle.Verify()` and full
provenance/SEM checks offline. An absent archive or any digest/file mismatch
fails closed; neither runner nor Go test downloads evidence automatically.

Regeneration can **regenerate equivalent independently qualified evidence**.
Random data.key bytes and runtime timestamps mean it does not recreate this
specific archive or bundle digest. Independent verification of this exact run
uses the pinned immutable asset and its actual bytes.

Runtime assertions are complete for this slice, but independent acceptance is
still `pending`. `casePass=false`, `UpgradeAuthorized=false`, and
`TrustedHopReceipt=false` remain deliberate. The other 48 Phase4A-07 cases,
Phase4A-07 as a whole, and Phase4A as a whole remain `pending`.
