#!/usr/bin/env python3
"""Verify the machine-readable RET-07 retained-pricing evidence bundle."""

from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
from typing import Any


SOURCE_COMMIT = "166262365d506157b99968105c890f480cc7c07c"
PLAN_COMMIT = "21035045c425e453bd8f3007619fc0734861012e"
PROFILE = "ret-07-retained-pricing-source-complete-v1"
SEM = ["SEM-01", "SEM-02", "SEM-03", "SEM-04", "SEM-05", "SEM-09"]
REQUIRED_SOURCE_FILES = {
    "archive-flow.json",
    "archive-readiness.json",
    "archive-inventory.json",
    "pre-archive-snapshot.json",
    "pre-delete-snapshot.json",
    "post-delete-snapshot.json",
    "raw-event-inventory.json",
    "retained-pricing-source.json",
    "price-authority-snapshot.json",
    "structural-price-change.json",
    "historical-reconciliation.json",
    "v1-archived-analytics.json",
}
REQUIRED_RUNTIME_FILES = {
    "backup-restore.json",
    "execution-record.json",
    "input-bundle.json",
    "raw-event-inventory.json",
    "retained-pricing-source.json",
    "result.json",
    "runner-evidence.json",
    "semantic-assertions.json",
}


class EvidenceError(Exception):
    """Raised for a non-negotiable evidence mismatch."""


def digest(path: Path) -> str:
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def read(root: Path, relative: str) -> Any:
    path = root / relative
    if not path.is_file():
        raise EvidenceError(f"missing evidence file: {relative}")
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except (OSError, json.JSONDecodeError) as exc:
        raise EvidenceError(f"invalid JSON evidence: {relative}: {exc}") from exc


def equal_ids(values: list[Any]) -> list[int]:
    result = []
    for value in values:
        if isinstance(value, bool) or not isinstance(value, (int, float)):
            raise EvidenceError(f"event ID is not numeric: {value!r}")
        if int(value) != value:
            raise EvidenceError(f"event ID is not integral: {value!r}")
        result.append(int(value))
    return result


def approximately(left: Any, right: Any, tolerance: float = 1e-12) -> bool:
    try:
        return abs(float(left) - float(right)) <= tolerance
    except (TypeError, ValueError):
        return False


def require(condition: bool, message: str) -> None:
    if not condition:
        raise EvidenceError(message)


def verify_manifest(root: Path) -> dict[str, Any]:
    manifest = read(root, "evidence-manifest.json")
    require(manifest.get("schemaVersion") == 1, "manifest schemaVersion must be 1")
    require(manifest.get("caseID") == "RET-07", "manifest caseID must be RET-07")
    inventory = manifest.get("inventory")
    require(isinstance(inventory, dict), "manifest inventory must be an object")
    actual = {
        path.relative_to(root).as_posix()
        for path in root.rglob("*")
        if path.is_file() and path.name not in {"README.md", "evidence-manifest.json"}
    }
    require(set(inventory) == actual, "manifest inventory does not match evidence files")
    for relative, expected in inventory.items():
        path = root / relative
        require(expected.get("size") == path.stat().st_size, f"size mismatch: {relative}")
        require(expected.get("sha256") == digest(path), f"digest mismatch: {relative}")
    return manifest


def verify_profile(root: Path) -> dict[str, Any]:
    profile = read(root, "profile.json")
    require(profile.get("profile") == PROFILE, "RET-07 profile mismatch")
    require(len(profile.get("events", [])) == 4, "RET-07 profile must contain four events")
    require(len(profile.get("expectedUSD", {})) == 4, "RET-07 profile must contain four expected costs")
    require(profile.get("prices", {}).get("fixture-free-ret07", {}).get("promptConfigured") is True,
            "explicit-zero prompt must be configured")
    require(profile["prices"]["fixture-free-ret07"].get("completionConfigured") is True,
            "explicit-zero completion must be configured")
    require(profile["prices"]["fixture-free-ret07"].get("prompt") == 0 and
            profile["prices"]["fixture-free-ret07"].get("completion") == 0,
            "explicit-zero profile rates must be zero")
    return profile


def verify_generation(root: Path, profile: dict[str, Any]) -> tuple[dict[str, Any], dict[str, Any], dict[str, Any]]:
    generation = read(root, "source-generation/record.json")
    provenance = read(root, "source-provenance.json")
    runner = read(root, "generation-runner.json")
    require(generation.get("caseID") == "RET-07", "generation caseID mismatch")
    require(generation.get("SourceRelease") == "v1.14.4", "source release mismatch")
    require(generation.get("SourceCommit") == SOURCE_COMMIT, "source commit mismatch")
    require(generation.get("SourceProducerBuildCodeCommit") == SOURCE_COMMIT,
            "published producer build commit mismatch")
    require(generation.get("PlanCommit") == PLAN_COMMIT, "plan commit mismatch")
    require(generation.get("GenerationProfile") == PROFILE, "generation profile mismatch")
    require(generation.get("generationSuccess") is True, "source generation did not succeed")
    require(generation.get("casePass") is False, "source generation casePass must remain false")
    process = generation.get("process", {})
    require(process.get("reaped") is True and process.get("exitCode") == 0,
            "source producer was not cleanly reaped")
    require(process.get("deliveredPayloads") == 5 and process.get("expectedUniqueEvents") == 4,
            "source producer delivery evidence is incomplete")
    require(generation.get("ControlledInputSHA256") == digest(root / "profile.json"),
            "controlled input is not bound to profile bytes")
    require(runner.get("codeCommit") == generation.get("GeneratorCodeCommit"),
            "generation runner is not bound to GeneratorCodeCommit")
    require(runner.get("generationRecordSHA256") == digest(root / "source-generation/record.json"),
            "generation runner record digest mismatch")
    require(runner.get("executionExitCode") == 0, "generation runner failed")
    require(provenance.get("caseID") == "RET-07", "source provenance caseID mismatch")
    require(provenance.get("generatorCodeCommit") == generation.get("GeneratorCodeCommit"),
            "source provenance generator commit mismatch")
    require(provenance.get("sourceGenerationRecordSHA256") == digest(root / "source-generation/record.json"),
            "source provenance generation digest mismatch")
    require(provenance.get("sourceBundleDigest") == generation.get("GeneratedBundleSHA256"),
            "source bundle digest mismatch")
    require(provenance.get("sourceRecord") == "ret-07/source-generation",
            "source record path mismatch")
    for name, expected in generation.get("GenerationEvidenceFiles", {}).items():
        if name == "producer.log":
            require(provenance.get("externalGenerationFiles", {}).get(name) == expected,
                    "external producer log digest mismatch")
            continue
        path = root / "source-generation" / name
        require(path.is_file(), f"generation evidence file is missing: {name}")
        require(digest(path) == expected, f"generation evidence digest mismatch: {name}")
    require(REQUIRED_SOURCE_FILES.issubset(
        {path.name for path in (root / "source-generation").iterdir() if path.is_file()}),
            "source-generation evidence is incomplete")
    return generation, provenance, runner


def verify_deleted_history(root: Path) -> dict[str, Any]:
    flow = read(root, "source-generation/archive-flow.json")
    require(flow.get("deleteCalled") is True, "production delete was not called")
    require(flow.get("deletionMethod") == "production DeleteArchive/DeleteBatch",
            "raw deletion method is not the production lifecycle")
    require(flow.get("verify", {}).get("run", {}).get("status") == "verified",
            "archive verification did not pass")
    require(flow.get("delete", {}).get("run", {}).get("status") == "completed",
            "archive deletion did not complete")
    require(flow.get("delete", {}).get("run", {}).get("deleted_event_count") == 4,
            "production deletion count is not four")
    inventory = read(root, "source-generation/archive-inventory.json")
    require(inventory.get("rawEventCount") == 0 and inventory.get("preDeleteRawCount") == 4,
            "raw event boundary is not physically deleted")
    require(inventory.get("archiveEventCount") == 4 and inventory.get("rawDeletedEventCount") == 4,
            "archive deletion inventory is incomplete")
    require(inventory.get("retainedProjectionCount") == 4 and inventory.get("rawComplete") is False,
            "retained projection/raw completeness boundary is dishonest")
    require(len(inventory.get("archiveRecords", [])) == 4 and len(inventory.get("refs", [])) == 4,
            "archive records or refs are incomplete")
    require(all(ref.get("raw_deleted_at_ms") is not None for ref in inventory["refs"]),
            "archive refs do not prove raw deletion")
    raw = read(root, "source-generation/raw-event-inventory.json")
    require(raw == {
        "beforeRawCount": 4,
        "deletedCount": 4,
        "afterRawCount": 0,
        "onlineRawEventIDsAfter": [],
        "archiveEventCount": 4,
        "retainedProjectionCount": 4,
        "rawComplete": False,
    }, "raw deletion inventory is not the expected machine-checkable boundary")
    return inventory


def verify_pricing_source(root: Path) -> dict[str, Any]:
    source = read(root, "source-generation/retained-pricing-source.json")
    expected = equal_ids(source.get("expectedHistoricalEvents", []))
    covered_rows = source.get("coveredPricingEvents", [])
    covered = equal_ids([row.get("eventID") for row in covered_rows])
    require(expected == [1, 2, 3, 4], f"unexpected retained event IDs: {expected}")
    require(covered == expected, "retained pricing coverage does not match archive IDs")
    require(equal_ids(source.get("missingPricingEvents", [])) == [],
            "retained pricing source has missing events")
    require(equal_ids(source.get("duplicateEventIDs", [])) == [],
            "retained pricing source has duplicate event IDs")
    require(source.get("retainedPricingSourceComplete") is True,
            "retained pricing source is not complete")
    require(equal_ids(source.get("manualPriceAuthorityEvents", [])) == [1, 2, 3],
            "manual price authority coverage mismatch")
    require(equal_ids(source.get("explicitZeroEvents", [])) == [3],
            "explicit-zero coverage mismatch")
    require(equal_ids(source.get("unknownUnpricedEvents", [])) == [4],
            "unknown/unpriced coverage mismatch")
    revision = source.get("pricingRevision")
    require(isinstance(revision, str) and revision, "retained pricing revision is missing")
    require(len(covered_rows) == len(set(covered)), "covered pricing events are duplicated")
    rows_by_model = {row.get("model"): row for row in source.get("priceRows", [])}
    require("fixture-free-ret07" in rows_by_model and "fixture-manual-ret07" in rows_by_model,
            "manual price rows are missing")
    free = rows_by_model["fixture-free-ret07"]
    require(all(free.get(key) == 1 for key in (
        "prompt_configured", "completion_configured", "cache_read_configured", "cache_creation_configured")),
            "explicit-zero row is not fully configured")
    require(all(approximately(free.get(key), 0) for key in (
        "prompt_per_1m", "completion_per_1m", "cache_per_1m", "cache_read_per_1m", "cache_creation_per_1m")),
            "explicit-zero row contains a non-zero rate")
    by_id = {int(row["eventID"]): row for row in covered_rows}
    for event_id in expected:
        row = by_id[event_id]
        identity = row.get("priceIdentity", {})
        require(row.get("retainedProjectionEventID") == event_id,
                f"retained projection ID mismatch for event {event_id}")
        require(identity.get("model") == row.get("model") and identity.get("provider") == row.get("provider"),
                f"price identity model/provider mismatch for event {event_id}")
        require(identity.get("pricingRevision") == revision,
                f"pricing revision missing for event {event_id}")
        if event_id == 3:
            require(row.get("pricingAuthority") == "manual" and row.get("pricingStatus") == "explicit-zero",
                    "event 3 is not an explicit manual zero")
            require(identity.get("configured") is True and identity.get("allFieldsConfigured") is True and
                    identity.get("explicitZero") is True and identity.get("unknownUnpriced") is False,
                    "event 3 zero/unknown semantics collapsed")
        elif event_id == 4:
            require(row.get("pricingAuthority") == "unknown" and row.get("pricingStatus") == "unknown-unpriced",
                    "event 4 is not unknown/unpriced")
            require(identity.get("unknownUnpriced") is True and identity.get("explicitZero") is False,
                    "event 4 unknown/zero semantics collapsed")
        else:
            require(row.get("pricingAuthority") == "manual" and row.get("pricingStatus") == "priced",
                    f"event {event_id} manual authority missing")
            require(identity.get("source") == "manual" and identity.get("configured") is True and
                    identity.get("unknownUnpriced") is False,
                    f"event {event_id} manual price identity missing")
    return source


def verify_structural_change(root: Path, pricing: dict[str, Any]) -> None:
    authority = read(root, "source-generation/price-authority-snapshot.json")
    require(authority.get("beforeRevision") != authority.get("afterRevision"),
            "price authority revision did not change")
    require(set(authority.get("manualAuthorityModels", [])) == {
        "fixture-manual-ret07", "fixture-paid-ret07", "fixture-free-ret07"},
            "manual authority model inventory mismatch")
    require(authority.get("explicitZeroModel") == "fixture-free-ret07" and
            authority.get("unknownModel") == "fixture-unknown-ret07",
            "zero/unknown model identity mismatch")
    change = read(root, "source-generation/structural-price-change.json")
    require(change.get("kind") == "context-tier-structure", "structural price change kind mismatch")
    require(change.get("model") == "fixture-paid-ret07", "structural price change model mismatch")
    require(change.get("beforeRevision") == authority.get("beforeRevision") and
            change.get("afterRevision") == authority.get("afterRevision"),
            "structural price change revisions are not bound")
    require(change.get("oldContextTiers") != change.get("newContextTiers"),
            "structural price change did not change context tiers")
    require(change.get("historicalEventsCrossingNewTier") == 0,
            "historical event crossed the new structural tier")
    require(pricing.get("pricingRevision") == authority.get("afterRevision"),
            "retained pricing source does not use the post-change revision")


def verify_reconciliation(root: Path) -> None:
    reconciliation = read(root, "source-generation/historical-reconciliation.json")
    aggregate = reconciliation.get("aggregate", {})
    require(aggregate.get("reconciled") is True, "historical aggregate was not reconciled")
    require(approximately(aggregate.get("expectedHistorical"), aggregate.get("actualHistorical")) and
            approximately(aggregate.get("actualHistorical"), aggregate.get("currentProjection")),
            "historical aggregate drifted after structural price change")
    for model in ("fixture-manual-ret07", "fixture-paid-ret07", "fixture-free-ret07", "fixture-unknown-ret07"):
        row = reconciliation.get(model, {})
        require(row.get("calls") == 1, f"historical reconciliation call count mismatch: {model}")
        require(approximately(row.get("beforeStructuralChange"), row.get("afterStructuralChangeHistorical")) and
                approximately(row.get("afterStructuralChangeHistorical"), row.get("currentProjection")),
                f"historical cost drifted for {model}")


def verify_runtime(root: Path, prefix: str, source_bundle: str) -> None:
    result = read(root, f"{prefix}/result.json")
    runner = read(root, f"{prefix}/runner-evidence.json")
    execution = read(root, f"{prefix}/execution-record.json")
    require(result.get("caseID") == "RET-07" and result.get("runtimeEvidence") == "pass",
            f"{prefix} runtime evidence did not pass")
    require(result.get("casePass") is False and result.get("independentAcceptance") == "pending",
            f"{prefix} acceptance boundary changed")
    require(result.get("UpgradeAuthorized") is False and result.get("TrustedHopReceipt") is False and
            result.get("Phase4A07Complete") is False and result.get("Phase4AComplete") is False,
            f"{prefix} authority boundary changed")
    require(result.get("applicableSEM") == SEM, f"{prefix} SEM scope mismatch")
    require(result.get("inputBundleDigest") == source_bundle, f"{prefix} source bundle mismatch")
    require(result.get("rawEventCount") == 0 and result.get("retainedPricingSourceComplete") is True and
            result.get("onlineRawComplete") is False and result.get("backupRestore") == "pass",
            f"{prefix} raw/retained boundary mismatch")
    require(runner.get("codeCommit") == execution.get("executionCommit") and runner.get("executionExitCode") == 0,
            f"{prefix} runner commit/exit mismatch")
    require(runner.get("resultSHA256") == digest(root / f"{prefix}/result.json"),
            f"{prefix} result digest mismatch")
    require(execution.get("retainedPricingSourceComplete") is True and execution.get("rawRowsAfterDelete") == 0,
            f"{prefix} execution record does not prove retained source")
    assertions = read(root, f"{prefix}/semantic-assertions.json")
    require(assertions.get("applicableSEM") == SEM, f"{prefix} semantic scope mismatch")
    for sem in SEM:
        require(assertions.get("assertions", {}).get(sem, {}).get("status") == "runtime-pass",
                f"{prefix} {sem} did not pass")
    sem02 = assertions["assertions"]["SEM-02"]
    require(sem02.get("duplicateEventIDs") == [] and sem02.get("reconciliationRepeated") is True,
            f"{prefix} deduplication evidence is incomplete")
    for imported in sem02.get("archiveImports", []):
        require(imported.get("added") == 0 and imported.get("skipped") == 4 and imported.get("failed") == 0,
                f"{prefix} archive replay was not deduplicated")
    sem05 = assertions["assertions"]["SEM-05"]
    require(sem05.get("explicitZeroDistinctFromUnknown") is True and
            sem05.get("unknownDistinctFromMissingAsZero") is True,
            f"{prefix} zero/unknown distinction is incomplete")
    recovery = read(root, f"{prefix}/backup-restore.json")
    require(recovery.get("newRoot") is True and recovery.get("inventoryMatched") is True and
            recovery.get("rawStillDeleted") is True and recovery.get("rawEventCount") == 0 and
            recovery.get("archiveEventCount") == 4 and recovery.get("retainedProjectionCount") == 4,
            f"{prefix} backup/restore evidence is incomplete")
    retained = read(root, f"{prefix}/retained-pricing-source.json")
    require(retained.get("complete") is True, f"{prefix} retained pricing replay is incomplete")


def verify_clean_checkout(root: Path, generator_commit: str, source_bundle: str) -> None:
    clean = read(root, "clean-checkout-replay.json")
    require(clean.get("testedCommit") == generator_commit and clean.get("executionExitCode") == 0,
            "clean-checkout replay is not bound to the generator commit")
    require(clean.get("newRootRecovery") is True, "clean-checkout replay did not restore a new root")
    require(clean.get("runtimeResult", {}).get("runtimeEvidence") == "pass" and
            clean["runtimeResult"].get("inputBundleDigest") == source_bundle and
            clean["runtimeResult"].get("casePass") is False,
            "clean-checkout runtime result is invalid")


def verify(root: Path) -> dict[str, Any]:
    manifest = verify_manifest(root)
    profile = verify_profile(root)
    generation, provenance, runner = verify_generation(root, profile)
    verify_deleted_history(root)
    pricing = verify_pricing_source(root)
    verify_structural_change(root, pricing)
    verify_reconciliation(root)
    source_bundle = generation["GeneratedBundleSHA256"]
    verify_runtime(root, "runtime", source_bundle)
    verify_runtime(root, "platform-replay/linux", source_bundle)
    verify_clean_checkout(root, generation["GeneratorCodeCommit"], source_bundle)
    require(manifest.get("authority", {}).get("casePass") is False, "manifest casePass must remain false")
    require(provenance.get("casePass") is False and runner.get("UpgradeAuthorized") is False,
            "provenance/runner authority boundary changed")
    return {
        "caseID": "RET-07",
        "status": "pass",
        "runtimeEvidence": "pass",
        "casePass": False,
        "independentAcceptance": "pending",
        "checked": [
            "exact-v1.14.4-provenance",
            "production-raw-deletion",
            "retained-pricing-source-completeness",
            "structural-price-change",
            "historical-reconciliation",
            "SEM-01/02/03/04/05/09",
            "macos-linux-replay",
            "clean-checkout-replay",
            "backup-new-root-restore",
        ],
    }


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument(
        "--evidence",
        type=Path,
        default=Path(__file__).resolve().parents[2] / "tests/fixtures/phase4a-evidence/ret-07",
    )
    args = parser.parse_args()
    try:
        result = verify(args.evidence.resolve())
    except (EvidenceError, OSError, KeyError, TypeError) as exc:
        print(json.dumps({"status": "fail", "error": str(exc)}, sort_keys=True))
        return 1
    print(json.dumps(result, indent=2, sort_keys=True))
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
