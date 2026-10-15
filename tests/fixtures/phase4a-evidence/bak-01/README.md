# BAK-01 committed-WAL evidence

This fixture records only Phase4A-07 / BAK-01 against the frozen plan contract:
exact v1.14.4, committed WAL variant, maintenance bundle, consistent pass,
applicable SEM-01.

The source generation uses the published v1.14.4 Linux AMD64 producer. A held
read snapshot sees the committed event while an immutable copy of the main DB
file does not. The source WAL is inventoried at the SQLite header/frame level;
the complete maintenance bundle captures `database`, `database-wal`, and
`database-shm` without checkpointing the source. The exact producer bytes are
independently pinned to the immutable v1.14.4 product release by SHA256; the
sealed qualification archive preserves the generated source and runtime bytes
without duplicating that producer asset.

`runtime/` is the macOS-host replay and `platform-replay/linux/` is the pinned
Linux AMD64 replay. Both restore to distinct new roots, reopen with their own
WAL/SHM, repeat backup/restore, and preserve the authoritative digest. Windows
is compile/materialization scope only; no Windows SQLite replay PASS is claimed.

`clean-checkout/` records a clean branch checkout replay on both the macOS
host and the pinned Linux AMD64 container. The archive and exact v1.14.4
producer digests are rechecked before materialization.
Each replay reopens an immutable main-file-only copy and confirms that the
committed event is absent there before reading it through the restored WAL.
Its runner pin identifies BAK-01's `source-provenance.json`; the original
generation and first replay runner records retain the earlier runner's
SRC-11 pin field unchanged, alongside their BAK-01 archive digest.

The qualification Release attempt was deleted after GitHub rejected creation
of the evidence tag (`GH013`); `source-provenance.json` records that pending
hosting state. The byte-identical sealed archive is committed under `sealed/`
for deterministic replay and is not presented as an immutable Release.

`casePass` remains `false` while `independentAcceptance` is `pending`. This
case does not authorize upgrades or trusted hops and does not complete Phase4A.
