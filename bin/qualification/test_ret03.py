"""Check RET-03 archived/raw-deleted evidence and immutable bindings."""
import hashlib
import json
import os
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/ret-03"
SEM = ["SEM-01", "SEM-02", "SEM-03", "SEM-06", "SEM-09"]
SOURCE = "166262365d506157b99968105c890f480cc7c07c"
PLAN = "06a47aaefff1629b3d556f8a49e0544648bfd4bc"


def digest(path):
    h = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def read(name):
    return json.loads((EVIDENCE / name).read_text(encoding="utf-8"))


class Ret03EvidenceTests(unittest.TestCase):
    def test_inventory_is_complete_and_byte_bound(self):
        manifest = read("evidence-manifest.json")
        self.assertEqual(manifest["caseID"], "RET-03")
        actual = {p.relative_to(EVIDENCE).as_posix() for p in EVIDENCE.rglob("*")
                  if p.is_file() and p.name not in ("README.md", "evidence-manifest.json")}
        self.assertEqual(set(manifest["inventory"]), actual)
        for name, item in manifest["inventory"].items():
            path = EVIDENCE / name
            self.assertEqual(path.stat().st_size, item["size"], name)
            self.assertEqual(digest(path), item["sha256"], name)

    def test_exact_source_generation_and_real_delete(self):
        generation = read("source-generation/record.json")
        provenance = read("source-provenance.json")
        self.assertEqual(generation["caseID"], "RET-03")
        self.assertEqual(provenance["sourceRecord"], "ret-03/source-generation")
        self.assertEqual(generation["SourceRelease"], "v1.14.4")
        self.assertEqual(generation["SourceCommit"], SOURCE)
        self.assertEqual(generation["SourceProducerBuildCodeCommit"], SOURCE)
        self.assertEqual(generation["PlanCommit"], PLAN)
        self.assertTrue(generation["generationSuccess"])
        self.assertTrue(generation["process"]["reaped"])
        self.assertEqual(generation["process"]["exitCode"], 0)
        self.assertEqual(generation["process"]["deliveredPayloads"], 5)
        self.assertEqual(generation["process"]["expectedUniqueEvents"], 4)
        self.assertEqual(generation["ControlledInputSHA256"], digest(EVIDENCE / "profile.json"))
        self.assertEqual(provenance["sourceBundleDigest"], generation["GeneratedBundleSHA256"])
        self.assertEqual(provenance["sourceGenerationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        for name, sha in generation["GenerationEvidenceFiles"].items():
            if name == "producer.log":
                self.assertEqual(provenance["externalGenerationFiles"][name], sha)
            else:
                self.assertEqual(digest(EVIDENCE / "source-generation" / name), sha, name)
        flow = read("source-generation/archive-flow.json")
        self.assertTrue(flow["deleteCalled"])
        self.assertEqual(flow["verify"]["run"]["status"], "verified")
        self.assertEqual(flow["delete"]["run"]["status"], "completed")
        self.assertEqual(flow["delete"]["run"]["deleted_event_count"], 4)

    def test_physical_raw_absence_archive_and_projection_coverage(self):
        inventory = read("source-generation/archive-inventory.json")
        self.assertEqual(inventory["rawEventCount"], 0)
        self.assertEqual(inventory["preDeleteRawCount"], 4)
        self.assertEqual(inventory["archiveEventCount"], 4)
        self.assertEqual(inventory["rawDeletedEventCount"], 4)
        self.assertEqual(inventory["retainedProjectionCount"], 4)
        self.assertFalse(inventory["rawComplete"])
        self.assertTrue(inventory["authoritativeArchiveProjectionComplete"])
        self.assertEqual(len(inventory["archiveRecords"]), 4)
        self.assertEqual(len(inventory["refs"]), 4)
        self.assertTrue(all(ref["raw_deleted_at_ms"] is not None for ref in inventory["refs"]))
        raw = read("source-generation/raw-event-inventory.json")
        self.assertEqual(raw, {"beforeRawCount": 4, "deletedCount": 4, "afterRawCount": 0,
                               "rawEventIDsBefore": raw["rawEventIDsBefore"],
                               "onlineRawEventIDsAfter": [], "archiveEventCount": 4,
                               "retainedProjectionCount": 4})

    def test_runtime_semantics_and_restore(self):
        bundle = read("source-provenance.json")["sourceBundleDigest"]
        for prefix in ("runtime", "platform-replay/linux"):
            result = read(prefix + "/result.json")
            runner = read(prefix + "/runner-evidence.json")
            self.assertEqual(result["runtimeEvidence"], "pass")
            self.assertEqual(result["inputBundleDigest"], bundle)
            self.assertEqual(result["applicableSEM"], SEM)
            self.assertFalse(result["onlineRawComplete"])
            self.assertEqual(result["rawEventCount"], 0)
            self.assertEqual(result["deletedCount"], 4)
            self.assertEqual(result["afterRawCount"], 0)
            self.assertEqual(result["backupRestore"], "pass")
            self.assertEqual(runner["resultSHA256"], digest(EVIDENCE / prefix / "result.json"))
            assertions = read(prefix + "/semantic-assertions.json")["assertions"]
            for sem in SEM:
                self.assertEqual(assertions[sem]["status"], "runtime-pass")
            self.assertTrue(assertions["SEM-03"]["historicalPricingAuthorityPreserved"])
            self.assertNotIn("noCurrentPriceFallback", assertions["SEM-03"])
            for imported in assertions["SEM-02"]["archiveImports"]:
                self.assertEqual(imported["added"], 0)
                self.assertEqual(imported["skipped"], 4)
                self.assertEqual(imported["failed"], 0)
            self.assertTrue(assertions["SEM-09"]["rawOnlyQueryDegraded"])
            recovery = read(prefix + "/backup-restore.json")
            self.assertTrue(recovery["newRoot"])
            self.assertTrue(recovery["inventoryMatched"])
            self.assertTrue(recovery["rawStillDeleted"])
            self.assertEqual(recovery["rawEventCount"], 0)
            self.assertEqual(recovery["archiveEventCount"], 4)
            self.assertEqual(recovery["retainedProjectionCount"], 4)

    def test_acceptance_and_authority_remain_closed(self):
        for name in ("runtime/result.json", "platform-replay/linux/result.json", "source-provenance.json"):
            result = read(name)
            self.assertFalse(result["casePass"])
            self.assertEqual(result["independentAcceptance"], "pending")
            self.assertFalse(result["UpgradeAuthorized"])
            self.assertFalse(result["TrustedHopReceipt"])
        result = read("runtime/result.json")
        self.assertFalse(result["Phase4A07Complete"])
        self.assertFalse(result["Phase4AComplete"])
        self.assertEqual(result["remainingCases"], {"count": 44, "status": "pending"})

    def test_clean_checkout_replay(self):
        clean = read("clean-checkout-replay.json")
        pin = read("source-provenance.json")["sourceArtifact"]
        self.assertEqual(clean["materializedArchiveSHA256"], pin["archiveSHA256"])
        for platform in ("linux", "macos"):
            execution = clean["platforms"][platform]
            self.assertEqual(execution["executionExitCode"], 0)
            self.assertTrue(execution["newRootRecovery"])
            self.assertEqual(execution["runtimeResult"]["runtimeEvidence"], "pass")
            self.assertFalse(execution["runtimeResult"]["onlineRawComplete"])
            self.assertEqual(execution["runtimeResult"]["inputBundleDigest"], pin["sourceBundleDigest"])

    @unittest.skipUnless(os.environ.get("CPAMP_RET03_FULL_EVIDENCE"), "opt-in original immutable bytes")
    def test_original_fixture_inventory(self):
        root = Path(os.environ["CPAMP_RET03_FULL_EVIDENCE"])
        inventory = read("source-provenance.json")["sourceArtifact"]["inventory"]
        self.assertEqual(set(inventory), {p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_file()})
        for name, item in inventory.items():
            path = root / name
            self.assertEqual(path.stat().st_size, item["size"], name)
            self.assertEqual(digest(path), item["sha256"], name)


if __name__ == "__main__":
    unittest.main()
