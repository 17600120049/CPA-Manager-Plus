"""Validate the committed, machine-readable RET-01 evidence index."""

import hashlib
import json
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/ret-01"
EXPECTED_SEM = ["SEM-01", "SEM-02", "SEM-03", "SEM-06", "SEM-08"]
SOURCE_COMMIT = "166262365d506157b99968105c890f480cc7c07c"
SOURCE_BUNDLE = "0e106589baef4148ee075ed77ce93bc71e83ca315a377311d62daeb9144236e3"


def digest(path):
    value = hashlib.sha256()
    with path.open("rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            value.update(block)
    return value.hexdigest()


class Ret01EvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.manifest = json.loads((EVIDENCE / "evidence-manifest.json").read_text())
        cls.result = json.loads((EVIDENCE / "runtime/result.json").read_text())
        cls.input_bundle = json.loads((EVIDENCE / "runtime/input-bundle.json").read_text())
        cls.assertions = json.loads((EVIDENCE / "runtime/semantic-assertions.json").read_text())
        cls.provenance = json.loads((EVIDENCE / "source-provenance.json").read_text())

    def test_manifest_inventory_and_digests(self):
        self.assertEqual(self.manifest["schemaVersion"], 1)
        self.assertEqual(self.manifest["caseID"], "RET-01")
        self.assertEqual(self.manifest["status"], "runtime-pass-independent-acceptance-pending")
        inventory = self.manifest["inventory"]
        self.assertNotIn("evidence-manifest.json", inventory)
        for name, expected in inventory.items():
            path = EVIDENCE / name
            self.assertTrue(path.is_file(), name)
            self.assertEqual(path.stat().st_size, expected["size"], name)
            self.assertEqual(digest(path), expected["sha256"], name)

    def test_case_and_authority_boundaries(self):
        self.assertEqual(self.result["caseID"], "RET-01")
        self.assertEqual(self.result["runtimeEvidence"], "pass")
        self.assertTrue(self.result["casePass"])
        self.assertEqual(self.result["independentAcceptance"], "pending")
        self.assertFalse(self.result["UpgradeAuthorized"])
        self.assertFalse(self.result["TrustedHopReceipt"])
        self.assertFalse(self.result["Phase4A07Complete"])
        self.assertFalse(self.result["Phase4AComplete"])
        self.assertEqual(self.result["remainingCases"], {"count": 47, "status": "pending"})
        self.assertEqual(self.result["applicableSEM"], EXPECTED_SEM)

    def test_source_anchor_and_independent_input_identity(self):
        source = self.result["sourceProvenance"]
        self.assertEqual(source["anchorCaseID"], "SRC-11")
        self.assertEqual(source["acceptance"], "accepted")
        self.assertEqual(source["sourceRelease"], "v1.14.4")
        self.assertEqual(source["sourceCommit"], SOURCE_COMMIT)
        self.assertEqual(source["sourceBundleDigest"], SOURCE_BUNDLE)
        self.assertEqual(self.result["inputBundleDigest"], SOURCE_BUNDLE)
        self.assertEqual(self.result["inputBundleIdentity"], "RET-01/input-bundle@" + SOURCE_BUNDLE)
        self.assertEqual(self.input_bundle["sourceAnchor"]["caseID"], "SRC-11")
        self.assertEqual(self.input_bundle["inputBundleIdentity"], "RET-01/input-bundle@" + SOURCE_BUNDLE)
        self.assertEqual(self.input_bundle["inputBundle"]["manifestDigest"], SOURCE_BUNDLE)
        self.assertIn("no source regeneration", self.input_bundle["provenanceRole"])
        self.assertEqual(self.provenance["sourceAnchorCaseID"], "SRC-11")
        self.assertEqual(self.provenance["sourceCommit"], SOURCE_COMMIT)
        self.assertEqual(self.provenance["sourceBundleDigest"], SOURCE_BUNDLE)

    def test_semantic_assertions_are_runtime_passes(self):
        self.assertEqual(self.assertions["caseID"], "RET-01")
        self.assertEqual(self.assertions["applicableSEM"], EXPECTED_SEM)
        for sem in EXPECTED_SEM:
            self.assertEqual(self.assertions["assertions"][sem]["status"], "runtime-pass", sem)
        self.assertTrue(self.assertions["assertions"]["SEM-01"]["authoritativeFieldsPreserved"])
        self.assertTrue(self.assertions["assertions"]["SEM-03"]["historicalProjectionStable"])
        self.assertTrue(self.assertions["assertions"]["SEM-06"]["metadataContinuity"])
        self.assertEqual(self.assertions["assertions"]["SEM-08"]["devinRevision"], 2)
        self.assertEqual(self.assertions["additionalRequiredAssertions"]["retainedHistoricalPricing"]["status"], "runtime-pass")
        self.assertEqual(self.assertions["additionalRequiredAssertions"]["quotaResetEvidence"]["status"], "runtime-pass")

    def test_digests_and_non_applicable_retention_boundary(self):
        self.assertEqual(self.result["beforeDigest"], SOURCE_BUNDLE)
        self.assertNotEqual(self.result["afterDigest"], self.result["beforeDigest"])
        self.assertEqual(self.result["authoritativeBeforeDigest"], self.result["authoritativeAfterDigest"])
        retention = self.result["retentionArchiveState"]
        self.assertEqual(retention["variant"], "full raw")
        self.assertEqual(retention["archiveRuns"], 0)
        self.assertEqual(retention["archiveSegments"], 0)
        self.assertEqual(retention["rawDeletedEventCount"], 0)
        self.assertEqual(retention["rawDeletedAssertion"]["status"], "not-applicable")
        self.assertEqual(retention["rawDeletedAssertion"]["case"], "RET-03")


if __name__ == "__main__":
    unittest.main()
