"""Validate the committed MIG-02 applying/recovery evidence contract."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import unittest


ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/mig-02"
SOURCE_COMMIT = "166262365d506157b99968105c890f480cc7c07c"
PLAN_COMMIT = "06a47aaefff1629b3d556f8a49e0544648bfd4bc"
PROFILE = "mig-02-applying-supported-resume-v1"


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def read(path):
    return json.loads(path.read_text(encoding="utf-8"))


class Mig02EvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not (EVIDENCE / "evidence-manifest.json").exists():
            if os.environ.get("CPAMP_MIG02_REQUIRED") == "1":
                raise AssertionError("MIG-02 evidence is required")
            raise unittest.SkipTest("MIG-02 evidence has not been recorded")
        cls.manifest = read(EVIDENCE / "evidence-manifest.json")
        cls.generation = read(EVIDENCE / "source-generation/record.json")
        cls.provenance = read(EVIDENCE / "source-provenance.json")
        cls.runtime_result = read(EVIDENCE / "runtime/result.json")
        cls.clean_result = read(EVIDENCE / "clean-checkout/result.json")

    def test_manifest_inventory_and_digests(self):
        self.assertEqual(self.manifest["schemaVersion"], 1)
        self.assertEqual(self.manifest["caseID"], "MIG-02")
        inventory = self.manifest["inventory"]
        actual = {
            path.relative_to(EVIDENCE).as_posix()
            for path in EVIDENCE.rglob("*")
            if path.is_file() and path.name not in ("README.md", "evidence-manifest.json")
        }
        self.assertEqual(set(inventory), actual)
        for name, entry in inventory.items():
            path = EVIDENCE / name
            self.assertEqual(path.stat().st_size, entry["size"], name)
            self.assertEqual(digest(path), entry["sha256"], name)

    def test_exact_source_and_independent_generation(self):
        record = self.generation
        self.assertEqual(record["caseID"], "MIG-02")
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
        self.assertEqual(digest(EVIDENCE / "profile.json"), record["ControlledInputSHA256"])
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
        self.assertEqual(
            [state["name"] for state in record["migrationState"]],
            ["quota_snapshot_lifecycle_v1", "usage_cache_accounting_v1", "usage_cache_accounting_v2"],
        )
        self.assertTrue(all(state["status"] == "completed" for state in record["migrationState"]))
        self.assertEqual(record["StoppedBeforeCheckpointInventory"]["sourceRevision"], record["GeneratedBundleSHA256"])

        provenance = self.provenance
        self.assertEqual(provenance["caseID"], "MIG-02")
        self.assertEqual(provenance["sourceRelease"], "v1.14.4")
        self.assertEqual(provenance["sourceCommit"], SOURCE_COMMIT)
        self.assertEqual(provenance["planCommit"], PLAN_COMMIT)
        self.assertEqual(provenance["sourceBundleDigest"], record["GeneratedBundleSHA256"])
        self.assertEqual(provenance["sourceGenerationRecordSHA256"], index["SHA256"])
        self.assertFalse(provenance["sourceArtifact"]["immutable"])
        self.assertTrue(provenance["sourceArtifact"]["producerArchiveImmutable"])
        self.assertEqual(provenance["sourceArtifact"]["archiveSHA256"], record["SourceProducerArchiveSHA256"])

    def test_source_bundle_and_product_scope(self):
        bundle = read(EVIDENCE / "bundle/manifest.json")
        self.assertEqual(bundle["sourceRevision"], self.generation["GeneratedBundleSHA256"])
        self.assertEqual(bundle["entries"]["database"]["present"], True)
        self.assertTrue((EVIDENCE / "bundle/database").is_file())
        self.assertEqual(digest(EVIDENCE / "bundle/database"), bundle["entries"]["database"]["sha256"])
        scope = read(EVIDENCE / "product-scope.json")
        self.assertEqual(scope["baselineCommit"], "72653d68327146ed46c423e3b5849e769c5f6999")
        self.assertEqual(scope["verificationCommit"], self.generation["GeneratorCodeCommit"])
        self.assertFalse(scope["productCodeChanged"])
        self.assertTrue(scope["harnessOnly"])
        # baselineCommit is provenance metadata and can predate this repository layout.
        # Bind the frozen product tree pins to the qualification execution boundary instead
        # of mutable HEAD or an older commit that did not expose these paths.
        for path, expected in scope["baselineTrees"].items():
            actual = subprocess.check_output(
                ["git", "rev-parse", scope["verificationCommit"] + ":" + path],
                cwd=ROOT,
                text=True,
            ).strip()
            self.assertEqual(actual, expected, f'{scope["verificationCommit"]}:{path}')

    def _assert_runtime(self, prefix):
        root = EVIDENCE / prefix
        result = read(root / "result.json")
        self.assertEqual(result["caseID"], "MIG-02")
        self.assertEqual(result["runtimeEvidence"], "pass")
        self.assertFalse(result["casePass"])
        self.assertEqual(result["independentAcceptance"], "pending")
        self.assertEqual(result["SourceRelease"], "v1.14.4")
        self.assertEqual(result["SourceCommit"], SOURCE_COMMIT)
        self.assertEqual(result["PlanCommit"], PLAN_COMMIT)
        self.assertEqual(result["CacheBeforeRecovery"], "applying")
        self.assertEqual(result["CacheAfterRecovery"], "completed")
        self.assertEqual(result["Quota"], "completed")
        self.assertEqual(result["recovery"], "supported-resume")
        self.assertTrue(result["recoveryActuallyExecuted"])
        self.assertTrue(result["normalReadinessBlocked"])
        self.assertEqual(result["controlledDirectCandidateReadinessBefore"], "blocked")
        self.assertEqual(result["controlledDirectCandidateReadinessAfter"], "ready")
        self.assertEqual(result["productionBridgeReadiness"], "unknown")
        self.assertEqual(result["applicableSEM"], ["SEM-07"])
        for name in ("UpgradeAuthorized", "TrustedHopReceipt", "Phase4A07Complete", "Phase4AComplete"):
            self.assertFalse(result[name])

        runner = read(root / "runner-evidence.json")
        self.assertEqual(runner["codeCommit"], result["executionCommit"])
        self.assertEqual(runner["resultSHA256"], digest(root / "result.json"))
        self.assertEqual(runner["executionExitCode"], 0)
        for name, expected in runner["logs"].items():
            self.assertEqual(digest(root / (name + ".txt")), expected, name)

        applying = read(root / "applying-state-snapshot.json")
        applying_state = applying["state"]
        self.assertEqual(applying_state["status"], "applying")
        self.assertEqual(applying_state["changedRows"], 2)
        self.assertEqual(applying_state["appliedRows"], 1)
        self.assertEqual(applying["inspection"]["cache"][0]["status"], "applying")
        self.assertEqual(applying["inspection"]["quota"][0]["status"], "completed")
        self.assertEqual(applying["inspection"]["cacheStagingRows"], 1)
        self.assertEqual(len(applying["staging"]), 1)

        before = read(root / "readiness-before-recovery.json")
        self.assertEqual(before["controlled"]["status"], "blocked")
        self.assertEqual(before["controlled"]["blocker"], "cache-accounting-applying")
        self.assertEqual(before["controlled"]["recovery"], "supported-resume")
        self.assertTrue(before["normalStartupBlocked"])
        self.assertTrue(before["normalStartupError"])

        resume = read(root / "recovery-resume-record.json")
        self.assertTrue(resume["noReset"])
        self.assertEqual(resume["supportedProductionCalls"], ["DiscoverUsageCacheAccounting", "RunUsageCacheAccountingBatch"])
        self.assertEqual(resume["resumeCheckpoint"]["status"], "applying")
        self.assertEqual(resume["resumeCheckpoint"]["appliedRows"], 1)
        self.assertEqual(len(resume["alreadyAppliedEventIDs"]), 1)
        self.assertEqual(len(resume["resumedEventIDs"]), 1)
        self.assertTrue(resume["idempotentCompletedReplay"]["Completed"])

        after = read(root / "post-resume-migration-state.json")
        self.assertEqual(after["state"]["status"], "completed")
        self.assertEqual(after["state"]["appliedRows"], 2)
        self.assertEqual(after["inspection"]["cache"][0]["status"], "completed")
        self.assertEqual(after["inspection"]["cacheStagingRows"], 0)
        self.assertEqual(after["inspection"]["quota"][0]["status"], "completed")

        readiness = read(root / "readiness-after-recovery.json")
        self.assertEqual(readiness["status"], "ready")
        self.assertEqual(readiness["scope"], "controlled-direct-candidate-data-compatibility")
        self.assertEqual(readiness["preflight"]["bridgeReadiness"]["status"], "unknown")
        self.assertFalse(readiness["UpgradeAuthorized"])
        self.assertFalse(readiness["TrustedHopReceipt"])

        semantic = read(root / "semantic-assertions.json")
        self.assertEqual(semantic["SEM-07"]["status"], "runtime-pass")
        for key in ("quotaRowsEqual", "quotaMigrationEqual", "quotaSettingsEqual",
                    "bankedResetEvidencePreserved", "cooldownEvidencePreserved",
                    "historyPreserved", "noDuplicateProcessing", "noLostEvents",
                    "sourceBytesUnchanged", "unrelatedProviderStateUnchanged"):
            value = semantic["SEM-07"].get(key, semantic.get(key))
            self.assertTrue(value, key)

        interruption = read(root / "interruption-record.json")
        self.assertTrue(interruption["persistedApplying"])
        self.assertEqual(interruption["recoveryEligibility"], "supported-resume")
        self.assertTrue(interruption["quotaCompleted"])
        self.assertEqual(interruption["nativeIdentity"]["revision"], 3)

    def test_runtime_and_clean_checkout_replay(self):
        self._assert_runtime("runtime")
        self._assert_runtime("clean-checkout")
        checkout = read(EVIDENCE / "clean-checkout/checkout-record.json")
        self.assertEqual(checkout["statusBefore"], "")
        self.assertEqual(checkout["statusAfter"], "")
        self.assertEqual(checkout["testedCommit"], checkout["executionCommit"])
        self.assertEqual(checkout["executionCommit"], self.clean_result["executionCommit"])
        self.assertTrue(checkout["cleanCheckout"])
        self.assertEqual(checkout["sourceBundleDigest"], self.generation["GeneratedBundleSHA256"])

    def test_delivery_result_and_authority_boundary(self):
        result = read(EVIDENCE / "result.json")
        self.assertEqual(result["caseID"], "MIG-02")
        self.assertEqual(result["runtimeEvidence"], "pass")
        self.assertTrue(result["casePass"])
        self.assertEqual(result["independentAcceptance"], "pending")
        self.assertEqual(result["acceptedCases"], ["SRC-11", "RET-01", "RET-02", "AUT-03", "MIG-01"])
        self.assertEqual(result["independentAcceptancePendingCases"], 44)
        self.assertEqual(result["CacheBeforeRecovery"], "applying")
        self.assertEqual(result["CacheAfterRecovery"], "completed")
        self.assertEqual(result["Quota"], "completed")
        self.assertEqual(result["applicableSEM"], ["SEM-07"])
        for name in ("UpgradeAuthorized", "TrustedHopReceipt", "Phase4A07Complete", "Phase4AComplete"):
            self.assertFalse(result[name])


if __name__ == "__main__":
    unittest.main()
