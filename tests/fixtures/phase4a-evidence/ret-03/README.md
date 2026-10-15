# RET-03: archived + raw deleted

Only Phase4A-07 RET-03 is covered here. The exact published v1.14.4 producer (`166262365d506157b99968105c890f480cc7c07c`) collected four unique usage events from five delivered payloads, completed readiness, created/resumed/verified an archive, and executed the production `DeleteArchive/DeleteBatch` path. The sealed SQLite source bundle has zero `usage_events` rows after deletion; four archive references, one verified segment and four retained projection rows remain.

The independent source-generation records bind the producer release, executable, controlled input, archive lifecycle, before/delete/after inventories, retained projection, archive coverage, generated bundle and generator commit. `raw-event-inventory.json` records `beforeRawCount=4`, `deletedCount=4`, `afterRawCount=0`, online raw IDs empty, archive event count 4 and retained projection count 4.

macOS and pinned Linux AMD64 replays exercise SEM-01/02/03/06/09. Historical aggregate and model queries continue to return the original calls, token values, metadata and manual historical prices. Archive replay twice adds zero and skips four each time. Coverage reports `raw_complete=false`; raw-only event detail is explicitly degraded. A real backup followed by `RestoreNew` into a distinct root preserves deleted raw rows, archive bytes, retained projection and query results.

The production identity rebuild guard remains fail-closed after raw deletion; RET-03 reopens the already-qualified retained/archive state and does not regenerate raw rows. No synthetic SQL deletion or marker-only evidence is used.

SEM-03 proves that historical pricing authority and resulting costs remain stable across raw deletion, replay and restore. The retained query loads the preserved `model_prices` authority; this qualification does not prove that later price edits leave historical costs unchanged. Current committed replay assertions supersede the historical replay annotations inside the original immutable asset; its source bundle, generation records and sealed bytes remain unchanged.

The immutable qualification asset is pinned in `source-provenance.json#/sourceArtifact` and `seal-verification.json`. It is a prerelease qualification asset only. `casePass=false`, `independentAcceptance=pending`, `UpgradeAuthorized=false`, `TrustedHopReceipt=false`; the remaining Phase4A-07 cases remain pending.
