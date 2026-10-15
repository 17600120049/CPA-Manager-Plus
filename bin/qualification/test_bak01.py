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


if __name__ == "__main__":
    unittest.main()
