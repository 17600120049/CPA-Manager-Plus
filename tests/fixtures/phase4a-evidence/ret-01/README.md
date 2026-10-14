# RET-01 exact v1.14.4 retention/pricing evidence

This directory contains the independent RET-01 case record. The case consumes
the already accepted SRC-11 exact v1.14.4 provenance as an immutable input
anchor; it does not regenerate, relabel, or authenticate source provenance.

RET-01 is the Plan-defined direct-candidate `full raw` variant. Its applicable
semantic invariants are SEM-01, SEM-02, SEM-03, SEM-06, and SEM-08. The case
also records its required pricing, explicit-zero, quota/reset, retention-state,
SQLite, and complete backup/restore assertions. RET-03 owns the archived plus
raw-deleted/SEM-09 variant; that assertion is explicitly not applicable here.

The committed `evidence-manifest.json` inventories only machine-readable
metadata and runtime result files. The source database and fixture-only key are
not committed; they are materialized from the pinned immutable SRC-11
qualification asset before replay. The result deliberately keeps
`independentAcceptance` pending, `UpgradeAuthorized` false, and
`TrustedHopReceipt` false.

The clean-checkout replay command is:

```sh
python3 bin/qualification/run-src11.py ret01 \
  --archive <materialized>/producer/cpa-manager-plus_v1.14.4_linux_amd64.tar.gz \
  --evidence-archive <src-11-immutable-asset.tar.gz> \
  --output <task-output>
```

The runner verifies the outer archive and complete inventory before the Go
qualification test executes. `bin/qualification/test_ret01.py` verifies the
committed evidence inventory, digests, semantic mapping, and authority
boundaries without opening the source database.
