"""Validate the committed MIG-03 clearing Recovery/block evidence contract."""

import hashlib
import json
import os
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/mig-03"
SOURCE_COMMIT = "166262365d506157b99968105c890f480cc7c07c"
PLAN_COMMIT = "21035045c425e453bd8f3007619fc0734861012e"
PROFILE = "mig-03-clearing-recovery-block-v1"
ACCEPTED = ["SRC-11", "RET-01", "RET-02", "RET-03", "AUT-03", "MIG-01", "MIG-02", "BAK-01"]


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def read(path):
    return json.loads(path.read_text(encoding="utf-8"))


class Mig03EvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not (EVIDENCE / "evidence-manifest.json").exists():
            if os.environ.get("CPAMP_MIG03_REQUIRED") == "1":
                raise AssertionError("MIG-03 evidence is required")
            raise unittest.SkipTest("MIG-03 evidence has not been recorded")
        cls.manifest = read(EVIDENCE / "evidence-manifest.json")
        cls.generation = read(EVIDENCE / "source-generation/record.json")
        cls.provenance = read(EVIDENCE / "source-provenance.json")

    def test_manifest_inventory_and_digests(self):
        self.assertEqual(self.manifest["schemaVersion"], 1)
        self.assertEqual(self.manifest["caseID"], "MIG-03")
        actual = {
            path.relative_to(EVIDENCE).as_posix()
            for path in EVIDENCE.rglob("*")
            if path.is_file() and path.name not in ("README.md", "evidence-manifest.json")
        }
        self.assertEqual(set(self.manifest["inventory"]), actual)
        for name, entry in self.manifest["inventory"].items():
            path = EVIDENCE / name
            self.assertEqual(path.stat().st_size, entry["size"], name)
            self.assertEqual(digest(path), entry["sha256"], name)

    def test_exact_source_generation_is_independent(self):
        record = self.generation
        self.assertEqual(record["caseID"], "MIG-03")
        self.assertEqual(record["SourceRelease"], "v1.14.4")
        self.assertEqual(record["SourceCommit"], SOURCE_COMMIT)
        self.assertEqual(record["SourceProducerBuildCodeCommit"], SOURCE_COMMIT)
        self.assertEqual(record["PlanCommit"], PLAN_COMMIT)
        self.assertEqual(record["GenerationProfile"], PROFILE)
        self.assertTrue(record["generationSuccess"])
        self.assertFalse(record["casePass"])
        self.assertFalse(record["UpgradeAuthorized"])
        self.assertFalse(record["TrustedHopReceipt"])
        self.assertTrue(record["process"]["reaped"])
        self.assertEqual(record["process"]["exitCode"], 0)
        self.assertEqual(record["process"]["deliveredPayloads"], 5)
        self.assertEqual(record["ControlledInputSHA256"], digest(EVIDENCE / "profile.json"))
        index = read(EVIDENCE / "generation-index.json")
        self.assertEqual(index["location"], "source-generation/record.json")
        self.assertEqual(index["SHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(index["SHA256"], read(EVIDENCE / "generation-runner.json")["generationRecordSHA256"])
        self.assertEqual(index["bundleDigest"], record["GeneratedBundleSHA256"])
        runner = read(EVIDENCE / "generation-runner.json")
        self.assertEqual(runner["codeCommit"], record["GeneratorCodeCommit"])
        self.assertEqual(runner["executionExitCode"], 0)
        for name, expected in runner["logs"].items():
            self.assertEqual(digest(EVIDENCE / ("generation-" + name + ".txt")), expected, name)
        for name, expected in record["GenerationEvidenceFiles"].items():
            self.assertEqual(digest(EVIDENCE / "source-generation" / name), expected, name)
        self.assertNotEqual(record["GeneratedBundleSHA256"], read(ROOT / "tests/fixtures/phase4a-evidence/mig-02/source-generation/record.json")["GeneratedBundleSHA256"])
        self.assertTrue(record["sourceVariant"].startswith("full raw"))

    def _assert_replay(self, prefix):
        root = EVIDENCE / prefix
        result = read(root / "result.json")
        self.assertEqual(result["caseID"], "MIG-03")
        self.assertEqual(result["runtimeEvidence"], "pass")
        self.assertFalse(result["casePass"])
        self.assertEqual(result["independentAcceptance"], "pending")
        self.assertEqual(result["SourceRelease"], "v1.14.4")
        self.assertEqual(result["SourceCommit"], SOURCE_COMMIT)
        self.assertEqual(result["PlanCommit"], PLAN_COMMIT)
        self.assertEqual(result["Cache"], "clearing")
        self.assertEqual(result["Quota"], "completed")
        self.assertEqual(result["recovery"], "block")
        self.assertEqual(result["recoveryClassification"], "Recovery / block")
        self.assertTrue(result["normalReadinessBlocked"])
        self.assertFalse(result["recoveryResumeActuallyExecuted"])
        self.assertEqual(result["controlledDirectCandidateReadiness"], "blocked")
        self.assertEqual(result["productionBridgeReadiness"], "unknown")
        self.assertEqual(result["observedBridgeClassification"], "blocked")
        self.assertEqual(result["applicableSEM"], ["SEM-07"])
        for name in ("UpgradeAuthorized", "TrustedHopReceipt", "Phase4A07Complete", "Phase4AComplete"):
            self.assertFalse(result[name])

        state = read(root / "clearing-state-snapshot.json")
        self.assertEqual(state["state"]["status"], "clearing")
        self.assertGreater(state["state"]["changedRows"], 0)
        self.assertEqual(state["state"]["appliedRows"], state["state"]["changedRows"])
        self.assertEqual(state["inspection"]["cache"][0]["status"], "clearing")
        self.assertEqual(state["inspection"]["quota"][0]["status"], "completed")
        self.assertEqual(state["inspection"]["cacheStagingRows"], 0)
        self.assertGreater(state["remainingCleanupRows"], 0)

        operation = read(root / "clearing-operation.json")
        self.assertGreater(operation["clearBatch"]["Processed"], 0)
        self.assertFalse(operation["clearBatch"]["Completed"])
        self.assertEqual(operation["clearBatch"]["State"]["status"], "clearing")
        self.assertGreater(operation["remainingCleanupState"]["remainingRows"], 0)
        self.assertEqual(operation["interruption"]["mechanism"], "context cancellation at the next production clear-batch boundary")
        self.assertTrue(operation["interruption"]["noMutationAfterCancel"])
        self.assertTrue(operation["interruption"]["cancelledBeforeCall"])
        self.assertEqual(operation["migrationRevision"], 3)

        before = read(root / "readiness-before-recovery.json")
        self.assertEqual(before["controlled"]["status"], "blocked")
        self.assertEqual(before["controlled"]["blocker"], "cache-accounting-clearing")
        self.assertEqual(before["controlled"]["recovery"], "block")
        self.assertEqual(before["controlled"]["preflight"]["bridgeReadiness"]["status"], "blocked")
        self.assertTrue(before["normalStartupBlocked"])
        self.assertFalse(before["automaticRecovery"])
        self.assertFalse(before["automaticCleanup"])
        self.assertFalse(before["automaticReset"])

        recovery = read(root / "recovery-block-record.json")
        self.assertEqual(recovery["classification"], "Recovery / block")
        self.assertFalse(recovery["resumeExecuted"])
        self.assertFalse(recovery["cleanupResumeAuthorized"])
        self.assertFalse(recovery["markerDeleted"])
        self.assertFalse(recovery["stateReset"])

        quota = read(root / "quota-evidence.json")
        self.assertEqual(quota["quotaStatus"], "completed")
        self.assertEqual(quota["before"], quota["clearing"])
        self.assertEqual(quota["clearing"], quota["afterBlockedReadiness"])
        for key in ("observationsWindowsCyclesSnapshots", "resetEvidence", "bankedResets", "cooldownState"):
            self.assertEqual(quota[key], "preserved")

        semantic = read(root / "semantic-assertions.json")
        self.assertEqual(semantic["SEM-07"]["status"], "runtime-pass")
        for key in ("quotaRowsEqual", "quotaMigrationEqual", "quotaSettingsEqual", "bankedResetEvidencePreserved", "cooldownEvidencePreserved", "cacheClearingDoesNotAlterQuota"):
            self.assertTrue(semantic["SEM-07"][key], key)
        for key in ("historyPreserved", "sourceBytesUnchanged", "unrelatedSettingsUnchanged", "noAutomaticDestructiveCleanup", "noAutomaticReset", "normalReadinessBlocked"):
            self.assertTrue(semantic[key], key)

        integrity = read(root / "authoritative-digest.json")
        self.assertEqual(integrity["clearingSnapshotDigest"], integrity["blockedReadinessSnapshotDigest"])
        self.assertEqual(integrity["clearingUsageEventsDigest"], integrity["blockedReadinessUsageEventsDigest"])
        self.assertTrue(integrity["noMutationDuringBlockedReadiness"])
        self.assertTrue(integrity["usageEventsStableDuringBlockedReadiness"])

        execution = read(root / "execution-record.json")
        self.assertTrue(execution["interruptionActuallyExecuted"])
        self.assertTrue(execution["recoveryInspectionActuallyExecuted"])
        self.assertFalse(execution["recoveryResumeActuallyExecuted"])
        self.assertEqual(execution["productionBridgeReadiness"], "unknown")
        self.assertEqual(execution["observedBridgeClassification"], "blocked")
        runner = read(root / "runner-evidence.json")
        self.assertEqual(runner["codeCommit"], result["executionCommit"])
        self.assertEqual(runner["resultSHA256"], digest(root / "result.json"))
        self.assertEqual(runner["executionExitCode"], 0)
        for name, expected in runner["logs"].items():
            self.assertEqual(digest(root / (name + ".txt")), expected, name)

    def test_runtime_and_clean_checkout(self):
        self._assert_replay("runtime")
        self._assert_replay("clean-checkout")
        checkout = read(EVIDENCE / "clean-checkout/checkout-record.json")
        self.assertTrue(checkout["cleanCheckout"])
        self.assertEqual(checkout["statusBefore"], "")
        self.assertEqual(checkout["statusAfter"], "")
        self.assertEqual(checkout["testedCommit"], checkout["executionCommit"])
        self.assertEqual(checkout["executionCommit"], read(EVIDENCE / "clean-checkout/result.json")["executionCommit"])
        self.assertEqual(checkout["sourceBundleDigest"], self.generation["GeneratedBundleSHA256"])

    def test_delivery_boundary_and_completion_criteria(self):
        result = read(EVIDENCE / "result.json")
        self.assertEqual(result["caseID"], "MIG-03")
        self.assertEqual(result["runtimeEvidence"], "pass")
        self.assertTrue(result["casePass"])
        self.assertEqual(result["independentAcceptance"], "pending")
        self.assertEqual(result["acceptedCases"], ACCEPTED)
        self.assertEqual(result["independentAcceptancePendingCases"], 41)
        self.assertEqual(result["Cache"], "clearing")
        self.assertEqual(result["Quota"], "completed")
        self.assertEqual(result["recoveryClassification"], "Recovery / block")
        self.assertEqual(result["productionBridgeReadiness"], "unknown")
        criteria = result["completionCriteria"]
        self.assertEqual(len(criteria), 14)
        self.assertTrue(all(criteria.values()))
        for name in ("UpgradeAuthorized", "TrustedHopReceipt", "Phase4A07Complete", "Phase4AComplete"):
            self.assertFalse(result[name])

    def test_provenance_and_scope(self):
        pin = self.provenance["sourceArtifact"]
        self.assertEqual(self.provenance["caseID"], "MIG-03")
        self.assertEqual(self.provenance["sourceRelease"], "v1.14.4")
        self.assertEqual(self.provenance["sourceCommit"], SOURCE_COMMIT)
        self.assertEqual(self.provenance["planCommit"], PLAN_COMMIT)
        self.assertEqual(self.provenance["sourceRecord"], "mig-03/source-generation")
        self.assertEqual(self.provenance["sourceBundleDigest"], self.generation["GeneratedBundleSHA256"])
        self.assertEqual(self.provenance["sourceGenerationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertTrue(pin["producerArchiveImmutable"])
        self.assertRegex(pin["archiveSHA256"], "^[a-f0-9]{64}$")
        self.assertRegex(pin["executableSHA256"], "^[a-f0-9]{64}$")
        self.assertGreater(pin["releaseID"], 0)
        self.assertGreater(pin["assetID"], 0)
        scope = read(EVIDENCE / "product-scope.json")
        self.assertEqual(scope["caseID"], "MIG-03")
        self.assertFalse(scope["productionCodeChanged"])
        self.assertFalse(scope["upgradeAuthorized"])
        self.assertFalse(scope["trustedHopReceipt"])
        self.assertEqual(scope["remainingCases"], 41)
        self.assertNotIn("MIG-04", scope["implementedCases"])
        self.assertNotIn("MIG-04", scope["qualifiedCases"])
        self.assertNotIn("synthetic-sql", scope["generationMechanism"])


if __name__ == "__main__":
    unittest.main()
