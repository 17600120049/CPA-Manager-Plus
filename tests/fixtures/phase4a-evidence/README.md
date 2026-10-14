# Phase4A qualification evidence

Evidence here belongs to individual cases. Source generation, primitive execution,
semantic assertions and independent acceptance are separate results. A generated
bundle is not a trusted hop receipt and grants no production upgrade authority.

SRC-11 targets only v1.14.4 at 166262365d506157b99968105c890f480cc7c07c.
The current real runtime evidence is in [`src-11/`](src-11/): exact published
v1.14.4 source generation, sealed inventory/provenance, v2 primitive replay,
SEM-01 through SEM-09 assertions, backup/restore, and tamper rejection. Its
runtime assertions pass and it has been independently accepted;
`casePass=true`, `UpgradeAuthorized=false`, and `TrustedHopReceipt=false`.

AUT-03 has its own focused runtime evidence contract in [`aut-03/`](aut-03/);
the runtime matrix is diagnostic only: `casePass=true`, while
`independentAcceptance=pending`. The other 48 Phase4A-07 cases remain
pending. This evidence does not complete Phase4A-07 or Phase4A.
