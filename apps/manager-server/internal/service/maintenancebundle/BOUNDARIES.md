# Phase4A-04-3 — Migration Boundary Qualification

Status: implementation submitted for independent acceptance. Phase4A-04 remains
**pending / not accepted**. BAK-05/06/07/08 and AUT-08 remain **pending**, with no
Phase4A-07 fixture PASS claimed by this slice. Baseline: independently accepted
04-1 and 04-2 / PR #958, merged at `1a9c868ce41718f9f65e71d821e165cfdc21fb00`.

This tracked contract/evidence index supplies reviewable dependency updates.
The invocation checkout's ignored `docs/plan/` and sibling evidence-preparation
worktrees are deliberately untouched; their owners can consume this index after
independent acceptance without treating submission as acceptance.

## Primitive boundary and evidence

| Boundary | What 04-3 verifies | Qualification limit / remaining owner |
| --- | --- | --- |
| Non-cooperative writer | Real external SQL/file writers ignore the held Manager fence; mutations during copying, after matching and immediately before publication are rejected; stale source revalidation fails after the exact child has exited/reaped | Content observations do not prove writer absence. Arbitrary write/revert between observations, or writes after final observation, require actual isolation. External isolation procedure/platform qualification remains **Phase4A**; product stop/drain/fence orchestration belongs to **Phase4-04 backend** |
| Complete sidecar set | DB, WAL, SHM, rollback journal, archives, config/key mutations plus observed optional-file additions/deletions invalidate the baseline; no valid output is published | No snapshot claim for an unisolated mutable file set; manifest is integrity metadata, never authorization |
| Cancellation | Before rename: abort, remove exact recorded staging, preserve source and allow retry. After rename: finish committed publication, no destructive rollback | Storage/permission failure during cleanup can require explicit recovery; product operation cancellation/journals are backend-owned |
| Process crash / restart | Real subprocess Kill + Wait before/after rename; unpublished roots fail Verify; recovery distinguishes absent-target abort from matching complete target commit; retry uses same target; sync failures after rename are explicitly uncertain | Fault injection verifies process/flush ordering. True physical power-loss/storage-stack qualification remains **Phase4A**, including Windows directory-metadata durability limits |
| Rollback journal | Real SQLite interrupted write with spilled uncommitted pages; restricted format/checksum gate, recovery only on copy, all committed rows restored, integrity/foreign-key and matching gates, resealed manifest with pre/post revisions, source unchanged | Unsupported/partial journal formats and super-journals fail closed. Historical exact-source fixtures remain **Phase4A-07**, not synthetic test PASS |
| Restore safety | New absent root only; full manifest/matching validation; no target replacement, source-root overlap or active-root adoption; active source reader/writer continue unchanged; failure/restart tests | This is an offline restore primitive. Live quiesce, reader/writer drain, owned handle lifecycle, target-generation fencing, activation, failure rollback and service restart belong to **Phase4-04 backend** |
| Linux runtime | Bundle/CLI runtime plus race JSON and environment are uploaded by the Manager Server job | Only the actual successful run, tested SHA and artifact count as evidence; no inference from workflow text |
| Windows runtime | Bundle/CLI runtime; real WAL/journal; Windows no-delete-sharing handles block rename/delete/replacement until released; existing destination never replaced; URI paths include spaces, percent, hash and Unicode | Requires successful Windows runtime job. MoveFileEx WRITE_THROUGH is not universal power-loss proof; no portable Windows directory fsync is claimed |
| Source mutation after reap | Mutation subprocesses are waited/reaped before stale-source revalidation; matching bytes and revision are required again before publication | Does not qualify the exact v1.14.4 hop writer, trusted receipt consumer or Manager authority bootstrap. Those remain **03B-2 / Lane B/E** |

## Ownership contract

1. The caller owns canonical, private source, backup and restore placement. All
   external source writers must actually be stopped/isolated, including archive,
   config and key writers; a flag, Manager lock or matching hash is not proof.
2. The source Manager fence remains held through final source revalidation and
   publication. It excludes cooperating Manager writers only; SQLite's own file
   locks cannot fence arbitrary file writes or independent archive/config/key
   assets. This slice intentionally does not acquire writable source SQLite
   connections merely to manufacture a lock claim or trigger source recovery.
3. The per-destination publication fence protects cooperating publishers and
   restart recovery. The owner excludes other publishers and case/path aliases
   and does not replace ancestors. Staging belongs exclusively to that operation.
4. Restore targets are new roots. Opening/adopting a restored root is a separate
   owner action after publication reconciliation; active roots are never swapped.
   Product activation must revalidate source/target generation and hold its
   ownership/fencing contract across consumption, rather than trust an earlier
   validation result or a PID alone.
5. Recovery never deletes/repairs original source data, blindly cleans siblings,
   adopts unknown output, grants UpgradeAuthorized, or starts old/runtime servers.

## Phase4A-07 Lane C dependency update

Eligibility to execute is distinct from a case PASS. The primitives below become
eligible inputs only **after independent 04-3 acceptance and successful runtime
evidence for the platform under test**. Every real case additionally needs its
own exact-source generation provenance, isolated fixture, bundle digests,
applicable SEM assertions and durable execution evidence from Phase4A-07.

| Case | Dependency after 04-3 acceptance | Case status |
| --- | --- | --- |
| BAK-05 | Controlled concurrent-writer/stale-rejection primitive available. Fixture must define actual writer isolation and mutation timing; no claim that observations fence arbitrary writers | pending; controlled rejection fixture eligible subject to provenance/isolation; general quiescence unqualified |
| BAK-06 | Complete sidecar inventory and final revalidation available; mutation/addition/deletion fixtures may execute under the ownership contract | pending; fixture execution/evidence required |
| BAK-07 | Cancellation commit point, exact staging cleanup, recoverable state and same-target retry available | pending; fixture execution/evidence required |
| BAK-08 | Process-crash/restart and flush-failure branches may execute using recorded per-target state | pending; **power-loss branch remains gated** by storage/platform qualification; do not mark the whole case PASS from Kill tests |
| AUT-08 | Post-reap content revalidation primitive available for a controlled child and immutable expected inventory | pending; real hop semantics still require **Lane B qualified writer + Lane E trusted authority consumer**, in addition to Lane C evidence |

Full live restore is not a prerequisite for executing the offline/new-root Lane C
branches above. It remains required for the eventual product migration workflow
and must be implemented/accepted with Phase4-04 backend ownership orchestration.
Removing that unrelated prerequisite does not waive the per-case isolation,
physical power-loss or trusted-authority gates.

## Verification and evidence location

From `apps/manager-server`:

```sh
go test ./internal/service/maintenancebundle ./internal/command/managerdatabundle -count=1 -timeout 15m
go test -race ./internal/service/maintenancebundle ./internal/command/managerdatabundle ./internal/processlock -count=1 -timeout 20m
go test ./... -count=1 -timeout 20m
go vet ./...
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o <task-temp>/manager-linux ./cmd/cpa-manager-plus
CGO_ENABLED=0 GOOS=windows GOARCH=amd64 go build -o <task-temp>/manager-windows.exe ./cmd/cpa-manager-plus
git diff --check
```

CI `PR Check / Manager Server` uploads `manager-bundle-linux-<run>-<attempt>`:
`environment.json` and `boundary.jsonl` (race-enabled runtime). `Manager Server
SQLite (Windows)` uploads `manager-preflight-windows-<run>-<attempt>`:
`environment.json` and `bundle-boundary.jsonl`, alongside pre-existing evidence.
Environment records bind requested source head, actual tested commit, OS/arch,
Go and SQLite driver. Review the runtime test events and failures/skips; artifact
existence alone is insufficient. CI run URLs and observed results belong in the
PR validation record. These artifacts are primitive regression evidence, not
Phase4A-07 exact historical fixture evidence.

No Upgrade API, wizard, #870 change, production ExecuteHop/ValidateHop enablement,
upgrade authority, release, merge or full migration orchestration is included.
