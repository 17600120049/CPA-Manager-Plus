"""Validate the committed, machine-readable RET-01 evidence index."""

import hashlib
import json
import os
from pathlib import Path
import unittest


ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/ret-01"
EXPECTED_SEM = ["SEM-01", "SEM-02", "SEM-03", "SEM-06", "SEM-08"]
SOURCE_COMMIT = "166262365d506157b99968105c890f480cc7c07c"
SRC11_BUNDLE = "0e106589baef4148ee075ed77ce93bc71e83ca315a377311d62daeb9144236e3"


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
        cls.generation = json.loads((EVIDENCE / "source-generation/record.json").read_text())
        cls.bundle_digest = cls.generation["GeneratedBundleSHA256"]
        cls.provenance = json.loads((EVIDENCE / "source-provenance.json").read_text())

    def test_manifest_inventory_and_digests(self):
        self.assertEqual(self.manifest["schemaVersion"], 1)
        self.assertEqual(self.manifest["caseID"], "RET-01")
        self.assertEqual(self.manifest["status"], "runtime-pass-independent-acceptance-pending")
        inventory = self.manifest["inventory"]
        self.assertNotIn("evidence-manifest.json", inventory)
        actual = {p.relative_to(EVIDENCE).as_posix() for p in EVIDENCE.rglob("*")
                  if p.is_file() and p.name not in ("README.md", "evidence-manifest.json")}
        self.assertEqual(set(inventory), actual)
        for name, expected in inventory.items():
            path = EVIDENCE / name
            self.assertTrue(path.is_file(), name)
            self.assertEqual(path.stat().st_size, expected["size"], name)
            self.assertEqual(digest(path), expected["sha256"], name)

    def test_case_and_authority_boundaries(self):
        self.assertEqual(self.result["caseID"], "RET-01")
        self.assertEqual(self.result["runtimeEvidence"], "pass")
        self.assertFalse(self.result["casePass"])
        self.assertFalse(self.manifest["authority"]["casePass"])
        self.assertFalse(self.generation["casePass"])
        self.assertEqual(self.result["independentAcceptance"], "pending")
        self.assertFalse(self.result["UpgradeAuthorized"])
        self.assertFalse(self.result["TrustedHopReceipt"])
        self.assertFalse(self.result["Phase4A07Complete"])
        self.assertFalse(self.result["Phase4AComplete"])
        self.assertEqual(self.result["remainingCases"], {"count": 48, "status": "pending"})
        self.assertEqual(self.result["applicableSEM"], EXPECTED_SEM)

    def test_source_anchor_and_independent_input_identity(self):
        source = self.result["sourceProvenance"]
        self.assertEqual(source["caseID"], "RET-01")
        self.assertEqual(source["acceptance"], "pending")
        self.assertEqual(source["sourceRelease"], "v1.14.4")
        self.assertEqual(source["sourceCommit"], SOURCE_COMMIT)
        self.assertEqual(source["sourceBundleDigest"], self.bundle_digest)
        self.assertEqual(self.result["inputBundleDigest"], self.bundle_digest)
        self.assertEqual(self.result["inputBundleIdentity"], "RET-01/input-bundle@" + self.bundle_digest)
        self.assertEqual(self.input_bundle["sourceAnchor"]["caseID"], "RET-01")
        self.assertEqual(self.input_bundle["inputBundleIdentity"], "RET-01/input-bundle@" + self.bundle_digest)
        self.assertEqual(self.input_bundle["inputBundle"]["manifestDigest"], self.bundle_digest)
        self.assertIn("RET-01 controlled generation", self.input_bundle["provenanceRole"])
        self.assertNotEqual(self.bundle_digest, SRC11_BUNDLE)
        self.assertEqual(self.generation["caseID"], "RET-01")
        self.assertEqual(self.generation["GenerationProfile"], "ret-01-full-raw-revision2-v1")
        self.assertEqual(digest(EVIDENCE / "profile.json"), self.generation["ControlledInputSHA256"])
        for name, sha in self.generation["GenerationEvidenceFiles"].items():
            if name == "producer.log":
                self.assertEqual(self.provenance["externalGenerationFiles"][name], sha)
            else:
                self.assertEqual(digest(EVIDENCE / "source-generation" / name), sha, name)
        self.assertEqual(self.provenance["caseID"], "RET-01")
        self.assertEqual(self.provenance["sourceCommit"], SOURCE_COMMIT)
        self.assertEqual(self.provenance["sourceBundleDigest"], self.bundle_digest)

    def test_generation_execution_and_runtime_bindings(self):
        index = json.loads((EVIDENCE / "generation-index.json").read_text())
        self.assertEqual(index["SHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(index["bundleDigest"], self.bundle_digest)
        runner = json.loads((EVIDENCE / "generation-runner.json").read_text())
        self.assertEqual(runner["codeCommit"], self.generation["GeneratorCodeCommit"])
        self.assertEqual(runner["generationRecordSHA256"], index["SHA256"])
        self.assertEqual(runner["executionExitCode"], 0)
        for prefix in ("runtime", "platform-replay/linux"):
            result = json.loads((EVIDENCE / prefix / "result.json").read_text())
            execution = json.loads((EVIDENCE / prefix / "runner-evidence.json").read_text())
            self.assertEqual(execution["codeCommit"], result["executionCommit"])
            self.assertEqual(execution["resultSHA256"], digest(EVIDENCE / prefix / "result.json"))
            self.assertEqual(result["inputBundleDigest"], self.bundle_digest)
            self.assertEqual(result["runtimeEvidence"], "pass")
            self.assertFalse(result["casePass"])
            self.assertEqual(result["independentAcceptance"], "pending")
            self.assertFalse(result["UpgradeAuthorized"])
            self.assertFalse(result["TrustedHopReceipt"])
        self.assertEqual(self.result["sourceGenerationRecordSHA256"], index["SHA256"])
        self.assertEqual(self.generation["SourceCommit"], SOURCE_COMMIT)
        self.assertEqual(self.generation["SourceProducerBuildCodeCommit"], SOURCE_COMMIT)
        self.assertEqual(self.generation["PlanCommit"], "2719d8466951da1ea457bd1a00dca3fa6f09dc43")
        self.assertRegex(self.generation["GeneratorCodeCommit"], r"^[a-f0-9]{40}$")
        self.assertNotEqual(self.generation["GeneratorCodeCommit"], SOURCE_COMMIT)
        self.assertTrue(self.generation["generationSuccess"])
        self.assertTrue(self.generation["process"]["reaped"])
        self.assertEqual(self.generation["process"]["exitCode"], 0)
        self.assertEqual(self.generation["process"]["deliveredPayloads"], 5)
        for key, name in (("inputBundle", "input-bundle"), ("semanticAssertions", "semantic-assertions"),
                          ("executionRecord", "execution-record"), ("backupRestore", "backup-restore")):
            self.assertEqual(self.result["evidence"][key], digest(EVIDENCE / ("runtime/" + name + ".json")))

    @unittest.skipUnless(os.environ.get("CPAMP_RET01_FULL_EVIDENCE"),
                         "opt-in sealed original fixture verification; no SQLite/platform PASS implied")
    def test_original_fixture_bytes(self):
        root = Path(os.environ["CPAMP_RET01_FULL_EVIDENCE"])
        expected_files = self.provenance["sourceArtifact"]["inventory"]
        self.assertEqual(set(expected_files), {p.relative_to(root).as_posix() for p in root.rglob("*") if p.is_file()})
        for name, expected in expected_files.items():
            path = root / name
            self.assertTrue(path.is_file(), name)
            self.assertEqual(path.stat().st_size, expected["size"], name)
            self.assertEqual(digest(path), expected["sha256"], name)
        self.assertEqual(digest(root / "ret-01/source-generation/record.json"),
                         digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(digest(root / "producer.tar.gz"), self.generation["SourceProducerArchiveSHA256"])
        for name, sha in self.generation["GenerationEvidenceFiles"].items():
            self.assertEqual(digest(root / "ret-01/source-generation" / name), sha, name)
        bundle = json.loads((root / "ret-01/bundle/manifest.json").read_text())
        self.assertEqual(bundle, self.generation["bundleInventory"])
        for name, entry in bundle["entries"].items():
            path = root / "ret-01/bundle" / name
            if entry["present"]:
                self.assertEqual(path.stat().st_size, entry["size"], name)
                self.assertEqual(digest(path), entry["sha256"], name)
            else:
                self.assertFalse(path.exists(), name)

    def test_durable_seal_pins_original_identity_without_acceptance(self):
        pin = self.provenance["sourceArtifact"]
        self.assertTrue(pin["immutableRelease"])
        self.assertEqual(pin["repository"], "seakee/CPA-Manager-Plus")
        self.assertEqual(pin["releaseID"], 412621764)
        self.assertEqual(pin["assetID"], 640121411)
        self.assertEqual(pin["tag"], "qualification-ret01-82e2287ed64e-20261014")
        self.assertEqual(pin["tagCommit"], "9eeba5f442a100168f715637781cec3644d1e1bb")
        self.assertEqual(pin["archiveName"], "ret-01-82e2287ed64e-sealed-evidence.zip")
        self.assertEqual(pin["archiveSize"], 8622918)
        self.assertEqual(pin["archiveSHA256"], "e7f5df5ce2792a7049d2fac29f7c67b9d7e407237e52167c22b1c9eefc7ff790")
        self.assertEqual(pin["downloadURL"], "https://github.com/" + pin["repository"]
                         + "/releases/download/" + pin["tag"] + "/" + pin["archiveName"])
        self.assertEqual(pin["sourceBundleDigest"], self.bundle_digest)
        self.assertEqual(pin["generationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        bundle = self.generation["bundleInventory"]
        self.assertEqual(pin["bundleManifestSHA256"], pin["inventory"]["ret-01/bundle/manifest.json"]["sha256"])
        self.assertEqual(bundle["sourceRevision"], pin["sourceBundleDigest"])
        self.assertEqual(pin["originActionsArtifact"]["artifactID"], 11884714735)
        self.assertEqual(pin["originActionsArtifact"]["artifactDigest"], "sha256:" + pin["archiveSHA256"])
        self.assertEqual(len(pin["inventory"]), 19)
        self.assertNotIn("expiresAt", pin)
        self.assertNotIn("retentionDays", pin)
        self.assertNotIn("original-source-artifact", {gap["scope"] for gap in self.manifest["gaps"]})

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
        self.assertEqual(self.result["beforeDigest"], self.bundle_digest)
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
