# Phase4A qualification evidence

Evidence here belongs to individual cases. Source generation, primitive execution,
semantic assertions and independent acceptance are separate results. A generated
bundle is not a trusted hop receipt and grants no production upgrade authority.

SRC-11 targets only v1.14.4 at 166262365d506157b99968105c890f480cc7c07c.
The current real runtime evidence is in [`src-11/`](src-11/): exact published
v1.14.4 source generation, sealed inventory/provenance, v2 primitive replay,
SEM-01 through SEM-09 assertions, backup/restore, and tamper rejection. The
latest `cpamp-plan/main` records SRC-11 as independently accepted; the sealed
archive remains the immutable source anchor and its historical generation
metadata is not rewritten here.

RET-01 has an independent runtime evidence location in
[`ret-01/`](ret-01/). It consumes the accepted SRC-11 source anchor and owns
its own input identity, execution record, before/after digests, pricing,
accounting, metadata, quota, SQLite, backup/restore, and authority-boundary
assertions. Its runtime case is pass, while independent acceptance remains
pending; `UpgradeAuthorized=false` and `TrustedHopReceipt=false`.

The remaining 47 Phase4A-07 cases remain pending. This evidence does not
complete Phase4A-07 or Phase4A.
