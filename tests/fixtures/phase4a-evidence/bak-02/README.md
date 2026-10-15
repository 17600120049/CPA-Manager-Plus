# BAK-02 matching-bundle evidence

This fixture records only Phase4A-07 / BAK-02 against the frozen contract:
exact v1.14.4, matching maintenance bundle, complete cross-asset snapshot,
RestoreNew to a distinct root, and applicable SEM-01 / SEM-06.

The original generator commit `af53fedab0af8fa272b7816a0c3ee18857a422b6`
is retained at `refs/heads/qualification/bak02-generator-af53fed` and is
accessible through the repository commit API. `generator-provenance.json`
binds the Git object/tree to all 516 source-file SHA256 values in the original
generation runner, its executable, the unchanged generation record, and the
original source bundle. CI fetches this ref and verifies every source blob.
Preserve this provenance branch when cleaning up task branches.
`generator-build-verification.json` records a clean checkout rebuild using
Go 1.26.1 and the original build flags; its executable SHA256 exactly matches
the original runner's `5846e4849ccc0c7d19679618cade0c3182f2aa847231bc92223ac22ac0c5d30a`.
The later replay commit has three different BAK-02 source files and does not
authenticate the original generator; the original commit and source inventory do.

The exact producer is `v1.14.4` at
`166262365d506157b99968105c890f480cc7c07c`. Its Linux AMD64 archive is pinned
by SHA256 in `source-provenance.json` and in the source-generation record. The
matching source bundle revision is
`4e1f3ad0b922283b87374a31a4c1520a0ad32f64bd93757d38d4ce4739a758d1`.
The bundle manifest covers the database, canonical `data-key` member, config,
and the complete usage-archives manifest/segment inventory. The generation
record binds those members to one stopped-source snapshot and one
`maintenance-bundle-create@<sourceRevision>` operation; missing, unexpected,
and digest-mismatch lists are empty.

`runtime/` is the macOS-host replay and `platform-replay/linux/` is the pinned
Linux AMD64 replay. Both verify the bundle, restore into a new root, remove the
source input copy before any query, decrypt persisted setup and manager config
with the restored key without creating a replacement, read archive manifests
and segments, execute archive-backed historical queries, and perform a second
backup/RestoreNew cycle. `clean-checkout/` records the same replays from a
clean checkout at the qualification harness commit. Windows remains
compile/materialization scope only; no Windows SQLite replay PASS is claimed.

The config bytes are inventory-bound and preserved exactly. The config's
relative `data.key` setting remains inside the restored root; the maintenance
bundle's canonical returned layout supplies its `data-key` member. Evidence
records both identities and uses the returned layout for key loading, so no
source key, default config, or regenerated key is consulted after source input
is removed.

The sealed archive under `sealed/` is committed with its SHA256 and Git object
anchor. A qualification Release/tag could not be hosted because the current
GitHub `Protect release tags` ruleset blocks the required evidence tag; this is
recorded as a hosting gap and does not invalidate the committed evidence.

The current archive is `bak-02-bundle-4e1f3ad0b922-reviewed-evidence.zip`.
It contains the unchanged original source generation and bundle plus the new
actual macOS/Linux and clean-checkout replays at `9ae1137a2777928d63431967ff6dd6ffe76b1ff8`.
The earlier sealed archive is preserved byte-for-byte as historical input;
its replay results incorrectly reported 40 pending and are explicitly
superseded in `source-provenance.json`. They carry no current matrix authority.
The current 49-case matrix remains **8 accepted / 41 pending**, including
BAK-02 pending independent acceptance. The BAK family itself has 10 cases:
1 accepted / 9 pending. Only independent acceptance of BAK-02 can reduce
the matrix to 40 pending.

`casePass` remains `false` while `independentAcceptance` is `pending`. This
case does not authorize upgrades or trusted hops and does not complete
Phase4A-07 or Phase4A.
