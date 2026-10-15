# BAK-02 matching-bundle evidence

This fixture records only Phase4A-07 / BAK-02 against the frozen contract:
exact v1.14.4, matching maintenance bundle, complete cross-asset snapshot,
RestoreNew to a distinct root, and applicable SEM-01 / SEM-06.

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

`casePass` remains `false` while `independentAcceptance` is `pending`. This
case does not authorize upgrades or trusted hops and does not complete
Phase4A-07 or Phase4A.
