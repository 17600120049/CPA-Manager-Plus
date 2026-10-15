"""Validate the committed, machine-readable BAK-02 matching-bundle evidence."""

import hashlib
import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/bak-02"
SOURCE_COMMIT = "166262365d506157b99968105c890f480cc7c07c"
PLAN_COMMIT = "21035045c425e453bd8f3007619fc0734861012e"


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


def read(name):
    return json.loads((EVIDENCE / name).read_text())


class Bak02EvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.manifest = read("evidence-manifest.json")
        cls.result = read("runtime/result.json")
        cls.generation = read("source-generation/record.json")
        cls.provenance = read("source-provenance.json")

    def test_manifest_inventory_and_digests(self):
        self.assertEqual(self.manifest["schemaVersion"], 1)
        self.assertEqual(self.manifest["caseID"], "BAK-02")
        inventory = self.manifest["inventory"]
        actual = {p.relative_to(EVIDENCE).as_posix() for p in EVIDENCE.rglob("*")
                  if p.is_file() and p.name not in ("README.md", "evidence-manifest.json")}
        self.assertEqual(set(inventory), actual)
        for name, expected in inventory.items():
            path = EVIDENCE / name
            self.assertEqual(path.stat().st_size, expected["size"], name)
            self.assertEqual(digest(path), expected["sha256"], name)

    def test_authority_and_case_boundary(self):
        self.assertEqual(self.result["caseID"], "BAK-02")
        self.assertEqual(self.result["runtimeEvidence"], "pass")
        self.assertEqual(self.result["runtimeAssertions"], "pass")
        self.assertFalse(self.result["casePass"])
        self.assertEqual(self.result["independentAcceptance"], "pending")
        self.assertFalse(self.result["UpgradeAuthorized"])
        self.assertFalse(self.result["TrustedHopReceipt"])
        self.assertFalse(self.result["Phase4A07Complete"])
        self.assertFalse(self.result["Phase4AComplete"])
        self.assertEqual(self.result["applicableSEM"], ["SEM-01", "SEM-06"])

    def test_exact_source_and_same_snapshot_chain(self):
        self.assertEqual(self.generation["caseID"], "BAK-02")
        self.assertEqual(self.generation["SourceRelease"], "v1.14.4")
        self.assertEqual(self.generation["SourceCommit"], SOURCE_COMMIT)
        self.assertEqual(self.generation["SourceProducerBuildCodeCommit"], SOURCE_COMMIT)
        self.assertEqual(self.generation["PlanCommit"], PLAN_COMMIT)
        self.assertEqual(self.generation["GenerationProfile"], "bak-02-matching-bundle-v1")
        self.assertTrue(self.generation["generationSuccess"])
        self.assertFalse(self.generation["casePass"])
        self.assertTrue(self.generation["process"]["reaped"])
        self.assertEqual(self.generation["process"]["exitCode"], 0)
        self.assertEqual(self.generation["ControlledInputSHA256"], digest(EVIDENCE / "profile.json"))
        index = read("generation-index.json")
        self.assertEqual(index["SHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(index["bundleDigest"], self.generation["GeneratedBundleSHA256"])
        for name, expected in self.generation["GenerationEvidenceFiles"].items():
            self.assertEqual(digest(EVIDENCE / "source-generation" / name), expected, name)
        snapshot = read("source-generation/matching-snapshot.json")
        self.assertEqual(snapshot["sourceRevision"], self.generation["GeneratedBundleSHA256"])
        self.assertEqual(snapshot["expectedMembers"], snapshot["actualMembers"])
        self.assertEqual(snapshot["missingMembers"], [])
        self.assertEqual(snapshot["unexpectedCriticalMembers"], [])
        self.assertEqual(snapshot["digestMismatches"], [])
        self.assertRegex(snapshot["operationID"], r"^maintenance-bundle-create@[0-9a-f]{64}$")

    def test_asset_identities_and_restore_are_self_contained(self):
        matching = read("source-generation/matching-snapshot.json")
        restored = read("runtime/restored-inventory.json")
        restore = read("runtime/restore-record.json")
        self.assertEqual(matching["database"]["sha256"], read("runtime/database-identity.json")["input"]["sha256"])
        self.assertTrue(restore["inputBundleVerified"])
        self.assertTrue(restore["firstRestoreNew"])
        self.assertTrue(restore["secondRestoreNew"])
        self.assertTrue(restore["sourceInputRemovedBeforeQueries"])
        self.assertFalse(restore["sourceRootDependency"])
        self.assertEqual(restored["firstMatchesInput"], True)
        self.assertEqual(restored["secondMatchesBackup"], True)
        key = read("runtime/key-identity.json")
        self.assertFalse(key["createdOnRestore"])
        self.assertTrue(key["decryptsSetup"])
        self.assertTrue(key["decryptsManagerConfig"])
        config = read("runtime/config-identity.json")
        self.assertFalse(config["defaultFallback"])
        archive = read("runtime/archive-inventory.json")
        self.assertTrue(archive["rootPresent"])
        self.assertTrue(archive["manifestAndSegmentsRead"])
        self.assertTrue(archive["digestAndSizeMatched"])
        self.assertTrue(archive["archiveRefsMatchDatabase"])

    def test_semantics_and_runner_boundary(self):
        sem = read("runtime/semantic-assertions.json")
        self.assertEqual(sem["applicableSEM"], ["SEM-01", "SEM-06"])
        self.assertEqual(sem["assertions"]["SEM-01"]["status"], "runtime-pass")
        self.assertEqual(sem["assertions"]["SEM-06"]["status"], "runtime-pass")
        self.assertTrue(sem["assertions"]["SEM-01"]["archiveBackedHistoryReadable"])
        self.assertTrue(sem["assertions"]["SEM-06"]["archiveMetadataLinkage"])
        execution = read("runtime/execution-record.json")
        self.assertTrue(execution["sourceInputRemovedBeforeQueries"])
        self.assertTrue(execution["noSilentRegeneration"])
        for prefix in ("runtime", "platform-replay/linux"):
            result = read(f"{prefix}/result.json")
            runner = read(f"{prefix}/runner-evidence.json")
            self.assertEqual(result["runtimeEvidence"], "pass")
            self.assertEqual(result["runtimeAssertions"], "pass")
            self.assertFalse(result["casePass"])
            self.assertEqual(result["independentAcceptance"], "pending")
            self.assertEqual(result["applicableSEM"], ["SEM-01", "SEM-06"])
            self.assertEqual(runner["caseID"], "BAK-02")
            self.assertEqual(runner["executionExitCode"], 0)
            self.assertEqual(runner["resultSHA256"], digest(EVIDENCE / prefix / "result.json"))


if __name__ == "__main__":
    unittest.main()
