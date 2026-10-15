"""Validate the committed, machine-readable BAK-01 evidence index."""

import hashlib
import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/bak-01"
SOURCE_COMMIT = "166262365d506157b99968105c890f480cc7c07c"
PLAN_COMMIT = "06a47aaefff1629b3d556f8a49e0544648bfd4bc"


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def read(name):
    return json.loads((EVIDENCE / name).read_text())


class Bak01EvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.manifest = read("evidence-manifest.json")
        cls.result = read("runtime/result.json")
        cls.generation = read("source-generation/record.json")
        cls.provenance = read("source-provenance.json")
        cls.wal = read("source-generation/wal-state.json")
        cls.row = read("source-generation/committed-row.json")
        cls.checkpoint = read("source-generation/checkpoint-observation.json")

    def test_manifest_inventory_and_digests(self):
        self.assertEqual(self.manifest["schemaVersion"], 1)
        self.assertEqual(self.manifest["caseID"], "BAK-01")
        self.assertEqual(self.manifest["status"], "runtime-pass-independent-acceptance-pending")
        inventory = self.manifest["inventory"]
        actual = {p.relative_to(EVIDENCE).as_posix() for p in EVIDENCE.rglob("*")
                  if p.is_file() and p.name not in ("README.md", "evidence-manifest.json")}
        self.assertEqual(set(inventory), actual)
        for name, expected in inventory.items():
            path = EVIDENCE / name
            self.assertEqual(path.stat().st_size, expected["size"], name)
            self.assertEqual(digest(path), expected["sha256"], name)

    def test_authority_and_case_boundary(self):
        self.assertEqual(self.result["caseID"], "BAK-01")
        self.assertEqual(self.result["runtimeEvidence"], "pass")
        self.assertEqual(self.result["runtimeAssertions"], "pass")
        self.assertFalse(self.result["casePass"])
        self.assertEqual(self.result["independentAcceptance"], "pending")
        self.assertFalse(self.result["UpgradeAuthorized"])
        self.assertFalse(self.result["TrustedHopReceipt"])
        self.assertFalse(self.result["Phase4A07Complete"])
        self.assertFalse(self.result["Phase4AComplete"])
        self.assertEqual(self.result["remainingCases"], {"count": 44, "status": "pending"})
        self.assertEqual(self.result["applicableSEM"], ["SEM-01"])

    def test_exact_source_and_generation_chain(self):
        self.assertEqual(self.generation["caseID"], "BAK-01")
        self.assertEqual(self.generation["SourceRelease"], "v1.14.4")
        self.assertEqual(self.generation["SourceCommit"], SOURCE_COMMIT)
        self.assertEqual(self.generation["SourceProducerBuildCodeCommit"], SOURCE_COMMIT)
        self.assertEqual(self.generation["PlanCommit"], PLAN_COMMIT)
        self.assertEqual(self.generation["GenerationProfile"], "bak-01-committed-wal-v1")
        self.assertTrue(self.generation["generationSuccess"])
        self.assertFalse(self.generation["casePass"])
        self.assertTrue(self.generation["process"]["reaped"])
        self.assertEqual(self.generation["process"]["exitCode"], 0)
        self.assertEqual(self.generation["process"]["deliveredPayloads"], 5)
        self.assertEqual(self.generation["ControlledInputSHA256"], digest(EVIDENCE / "profile.json"))
        index = read("generation-index.json")
        self.assertEqual(index["SHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(index["bundleDigest"], self.generation["GeneratedBundleSHA256"])
        for name, expected in self.generation["GenerationEvidenceFiles"].items():
            self.assertEqual(digest(EVIDENCE / "source-generation" / name), expected, name)

    def test_committed_wal_proof_is_nonvacuous(self):
        self.assertEqual(self.wal["journalMode"][0]["journal_mode"], "wal")
        self.assertRegex(self.wal["preTransactionMainDatabaseSHA256"], r"^[0-9a-f]{64}$")
        self.assertGreater(self.wal["preTransactionMainDatabaseSize"], 0)
        self.assertGreater(self.wal["walSize"], 32)
        self.assertGreater(self.wal["walFrameInventory"]["frameCount"], 0)
        self.assertGreater(self.wal["walFrameInventory"]["commitFrameCount"], 0)
        self.assertTrue(self.wal["committedRowVisibleLive"])
        self.assertFalse(self.wal["committedRowVisibleMain"])
        self.assertEqual(self.row["liveConnectionVisible"], True)
        self.assertEqual(self.row["mainDatabaseOnlyVisible"], False)
        self.assertTrue(self.row["requestID"])
        self.assertIn(self.checkpoint["pendingFramesReported"], (True, False))
        self.assertGreaterEqual(self.checkpoint["result"]["log"], self.checkpoint["result"]["checkpointed"])
        self.assertFalse(self.checkpoint["sourceMutated"])

    def test_original_generator_provenance_binding(self):
        proof = read("generator-provenance.json")
        runner = read("generation-runner.json")
        self.assertEqual(proof["generatorCodeCommit"], self.generation["GeneratorCodeCommit"])
        self.assertEqual(proof["generatorCodeCommit"], self.provenance["generatorCodeCommit"])
        self.assertEqual(proof["generatorCodeCommit"], runner["codeCommit"])
        self.assertEqual(proof["sourceFilesCount"], len(runner["sourceFiles"]))
        self.assertEqual(proof["sourceFilesCount"], 504)
        inventory = json.dumps(runner["sourceFiles"], sort_keys=True, separators=(",", ":")).encode()
        self.assertEqual(proof["sourceFilesInventorySHA256"], hashlib.sha256(inventory).hexdigest())
        self.assertTrue(proof["sourceFileHashesVerified"])
        self.assertEqual(proof["provenanceRef"], "refs/heads/qualification/bak01-generator-4582ae7b")

    def test_semantic_and_restore_bindings(self):
        assertions = read("runtime/semantic-assertions.json")
        backup = read("runtime/backup-restore.json")
        self.assertEqual(assertions["applicableSEM"], ["SEM-01"])
        self.assertEqual(assertions["assertions"]["SEM-01"]["status"], "runtime-pass")
        self.assertTrue(assertions["assertions"]["SEM-01"]["committedEventIdentityPreserved"])
        self.assertTrue(assertions["assertions"]["SEM-01"]["authoritativeFieldsPreserved"])
        self.assertTrue(assertions["backupConsistency"]["completeWALCapture"])
        self.assertTrue(backup["inventoryMatched"])
        self.assertTrue(backup["sourceWALNotUsedByRestoredRoot"])
        self.assertEqual(backup["authoritativeBeforeDigest"], backup["authoritativeAfterDigest"])
        self.assertTrue(self.result["backupRestore"] == "pass")

    def test_provenance_and_platform_replays(self):
        pin = self.provenance["sourceArtifact"]
        self.assertEqual(self.provenance["caseID"], "BAK-01")
        self.assertEqual(self.provenance["sourceRecord"], "bak-01/source-generation")
        self.assertEqual(self.provenance["sourceBundleDigest"], self.generation["GeneratedBundleSHA256"])
        self.assertEqual(self.provenance["sourceGenerationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertFalse(pin["immutable"])
        self.assertEqual(pin["publicationStatus"], "not-published; local sealed bytes are committed under sealed/")
        self.assertEqual(self.provenance["artifactHosting"]["status"], "immutable-release-pending")
        self.assertTrue(self.provenance["artifactHosting"]["draftDeleted"])
        self.assertTrue(self.provenance["artifactHosting"]["rulesetRestored"])
        self.assertEqual(pin["sourceBundleDigest"], self.generation["GeneratedBundleSHA256"])
        self.assertEqual(pin["generationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        # The producer is independently pinned to the immutable v1.14.4
        # product release; the BAK sealed archive contains generated fixture
        # and runtime bytes without duplicating that 8.5 MiB asset.
        self.assertNotIn("producer.tar.gz", pin["inventory"])
        self.assertEqual(self.provenance["sourceProducerArchiveSHA256"], self.generation["SourceProducerArchiveSHA256"])
        self.assertRegex(pin["archiveSHA256"], r"^[0-9a-f]{64}$")
        self.assertGreater(pin["archiveSize"], 0)
        sealed = EVIDENCE / pin["committedArchivePath"]
        self.assertEqual(sealed.stat().st_size, pin["archiveSize"])
        self.assertEqual(digest(sealed), pin["archiveSHA256"])
        for prefix in ("runtime", "platform-replay/linux"):
            result = read(f"{prefix}/result.json")
            runner = read(f"{prefix}/runner-evidence.json")
            backup = read(f"{prefix}/backup-restore.json")
            assertions = read(f"{prefix}/semantic-assertions.json")
            self.assertEqual(result["runtimeEvidence"], "pass")
            self.assertEqual(result["runtimeAssertions"], "pass")
            self.assertFalse(result["casePass"])
            self.assertEqual(result["independentAcceptance"], "pending")
            self.assertEqual(result["inputBundleDigest"], self.generation["GeneratedBundleSHA256"])
            self.assertEqual(result["authoritativeBeforeDigest"], result["authoritativeAfterDigest"])
            self.assertEqual(result["executionCommit"], self.generation["GeneratorCodeCommit"])
            self.assertEqual(result["applicableSEM"], ["SEM-01"])
            self.assertEqual(result["backupRestore"], "pass")
            self.assertEqual(runner["codeCommit"], result["executionCommit"])
            self.assertEqual(runner["resultSHA256"], digest(EVIDENCE / prefix / "result.json"))
            self.assertEqual(runner["executionExitCode"], 0)
            for name, expected in runner["logs"].items():
                self.assertEqual(digest(EVIDENCE / prefix / (name + ".txt")), expected, name)
            self.assertTrue(backup["firstRestoreRootDistinct"])
            self.assertTrue(backup["secondRestoreRootDistinct"])
            self.assertTrue(backup["inventoryMatched"])
            self.assertTrue(backup["sourceWALNotUsedByRestoredRoot"])
            self.assertEqual(backup["authoritativeBeforeDigest"], backup["authoritativeAfterDigest"])
            self.assertEqual(assertions["applicableSEM"], ["SEM-01"])
            self.assertEqual(assertions["assertions"]["SEM-01"]["status"], "runtime-pass")
            self.assertTrue(assertions["backupConsistency"]["completeWALCapture"])
            self.assertFalse(assertions["sourceMutation"]["authoritativeMutation"])
        linux = read("platform-replay/linux/result.json")
        self.assertEqual(linux["authoritativeAfterDigest"], self.result["authoritativeAfterDigest"])

    def test_clean_checkout_replay(self):
        record = read("clean-checkout/checkout-record.json")
        self.assertEqual(record["caseID"], "BAK-01")
        self.assertEqual(record["generatorCodeCommit"], self.generation["GeneratorCodeCommit"])
        self.assertEqual(record["checkout"]["status"], "clean branch checkout")
        self.assertTrue(record["checkout"]["trackedSourceMatchesBranch"])
        self.assertEqual(record["sealedEvidenceArchive"]["sha256"], self.provenance["sourceArtifact"]["archiveSHA256"])
        self.assertEqual(record["exactProducer"]["sha256"], self.provenance["sourceProducerArchiveSHA256"])
        for platform in ("macos", "linux"):
            replay = record["replays"][platform]
            result_path = EVIDENCE / replay["result"]
            runner_path = EVIDENCE / replay["runnerEvidence"]
            result = json.loads(result_path.read_text())
            runner = json.loads(runner_path.read_text())
            backup_path = EVIDENCE / replay["backupRestoreEvidence"]
            backup = json.loads(backup_path.read_text())
            self.assertEqual(replay["backupRestoreEvidenceSHA256"], digest(backup_path))
            self.assertEqual(result["evidence"]["backupRestore"], digest(backup_path))
            self.assertTrue(backup["mainFileOnlyCommittedRowAbsent"])
            self.assertGreater(backup["committedWALFrames"]["commitFrameCount"], 0)
            self.assertEqual(runner["sealedArtifactPinSHA256"], digest(EVIDENCE / "source-provenance.json"))
            self.assertEqual(replay["resultSHA256"], digest(result_path))
            self.assertEqual(replay["runnerEvidenceSHA256"], digest(runner_path))
            self.assertEqual(result["executionCommit"], record["checkout"]["commit"])
            self.assertEqual(result["runtimeEvidence"], "pass")
            self.assertEqual(result["runtimeAssertions"], "pass")
            self.assertEqual(result["backupRestore"], "pass")
            self.assertFalse(result["casePass"])
            self.assertEqual(result["independentAcceptance"], "pending")
            self.assertEqual(result["authoritativeBeforeDigest"], result["authoritativeAfterDigest"])
            self.assertEqual(runner["codeCommit"], record["checkout"]["commit"])
            self.assertEqual(runner["resultSHA256"], digest(result_path))
            self.assertEqual(runner["executionExitCode"], 0)
        self.assertTrue(record["replays"]["linux"]["containerized"])


if __name__ == "__main__":
    unittest.main()
