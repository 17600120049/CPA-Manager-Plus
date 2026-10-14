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

The production data key and SQLite bytes are intentionally not committed here:
repository rules prohibit committing private keys or runtime SQLite data.
Their exact verified SHA256/size and complete present/absent inventory are
recorded in `evidence-manifest.json`, `source-generation/record.json`, and the
sealed manifest. The task-owned run output used for independent regeneration
was `bin/tmp/phase4a-src11/generation-20261014-evidence` and
`bin/tmp/phase4a-src11/replay-20261014-evidence`; the pinned archive and exact
procedure can recreate those bytes without trusting a same-schema fixture.

Runtime assertions are complete for this slice, but independent acceptance is
still `pending`. `casePass=false`, `UpgradeAuthorized=false`, and
`TrustedHopReceipt=false` remain deliberate. The other 48 Phase4A-07 cases,
Phase4A-07 as a whole, and Phase4A as a whole remain `pending`.
