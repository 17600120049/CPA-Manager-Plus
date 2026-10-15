"""Validate the committed, machine-readable BAK-02 matching-bundle evidence."""

import hashlib
import json
from pathlib import Path
from zipfile import ZipFile
import unittest
import importlib.util
from unittest.mock import patch


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
        self.assertEqual(self.manifest["remainingCases"], {"count": 41, "status": "pending"})
        self.assertEqual(self.manifest["caseMatrix"], {"total": 49, "accepted": 8, "pending": 41, "includesBAK02Pending": True})
        self.assertEqual(self.manifest["backupFamily"], {"total": 10, "accepted": 1, "pending": 9})
        for prefix in ("runtime", "platform-replay/linux", "clean-checkout/macos", "clean-checkout/linux"):
            result = read(f"{prefix}/result.json")
            self.assertEqual(result["remainingCases"], {"count": 41, "status": "pending"})

    def test_original_generator_provenance(self):
        proof = read("generator-provenance.json")
        runner = read("generation-runner.json")
        self.assertEqual(proof["generatorCodeCommit"], self.generation["GeneratorCodeCommit"])
        self.assertEqual(proof["generatorCodeCommit"], self.provenance["generatorCodeCommit"])
        self.assertEqual(proof["generatorCodeCommit"], runner["codeCommit"])
        self.assertEqual(proof["provenanceRef"], "refs/heads/qualification/bak02-generator-af53fed")
        self.assertTrue(proof["sourceFileHashesVerified"])
        self.assertEqual(proof["sourceFilesCount"], 516)
        self.assertEqual(proof["sourceFilesCount"], len(runner["sourceFiles"]))
        self.assertEqual(proof["sourceFilesInventorySHA256"], hashlib.sha256(
            json.dumps(runner["sourceFiles"], sort_keys=True, separators=(",", ":")).encode()).hexdigest())
        self.assertEqual(proof["generationRunnerSHA256"], digest(EVIDENCE / "generation-runner.json"))
        self.assertEqual(proof["generatorExecutableSHA256"], runner["executableSHA256"])
        self.assertEqual(proof["generationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(proof["sourceBundleDigest"], self.generation["GeneratedBundleSHA256"])
        self.assertEqual(self.manifest["generatorProvenance"], "generator-provenance.json")
        build = read("generator-build-verification.json")
        self.assertEqual(build["generatorCodeCommit"], runner["codeCommit"])
        self.assertEqual(build["checkoutStatus"], "clean")
        self.assertEqual(build["goVersion"], runner["goVersion"])
        self.assertEqual(build["actualExecutableSHA256"], runner["executableSHA256"])
        self.assertEqual(build["expectedExecutableSHA256"], runner["executableSHA256"])
        self.assertTrue(build["executableDigestMatchesOriginal"])
        self.assertTrue(build["originalGenerationRecordUnchanged"])
        self.assertTrue(build["originalBundleUnchanged"])

    def test_generator_verifier_rejects_other_commit_or_changed_source(self):
        spec = importlib.util.spec_from_file_location("verify_bak02_generator", ROOT / "bin/qualification/verify-bak02-generator.py")
        verifier = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(verifier)
        commit = self.provenance["generatorCodeCommit"]
        with patch.object(verifier, "git", return_value=b"0" * 40):
            with self.assertRaisesRegex(ValueError, "commit mismatch"):
                verifier.verify("wrong-ref")
        raw = b"tree 0000000000000000000000000000000000000000\n\nfixture commit\n"
        fake_commit = hashlib.sha1(b"commit " + str(len(raw)).encode() + b"\0" + raw).hexdigest()
        # Supply a valid commit object and the complete declared source inventory,
        # but return a changed source blob. A syntactically valid SHA is insufficient.
        def fake_git(*args, **kwargs):
            if args[0] == "rev-parse":
                return fake_commit.encode()
            if args[1] == "commit":
                return raw
            return b"0000000000000000000000000000000000000000 blob 7\nchanged\n"
        actual_loads = json.loads
        def fake_loads(data, *args, **kwargs):
            value = actual_loads(data, *args, **kwargs)
            for field in ("codeCommit", "GeneratorCodeCommit", "generatorCodeCommit"):
                if field in value:
                    self.assertEqual(value[field], commit)
                    value[field] = fake_commit
            return value
        with patch.object(verifier, "git", side_effect=fake_git), patch.object(verifier.json, "loads", side_effect=fake_loads):
            with self.assertRaisesRegex(ValueError, "source hash mismatch"):
                verifier.verify("changed-source")

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

        provenance = self.provenance
        self.assertEqual(provenance["sourceRelease"], "v1.14.4")
        self.assertEqual(provenance["sourceCommit"], SOURCE_COMMIT)
        self.assertEqual(provenance["sourceBundleDigest"], self.generation["GeneratedBundleSHA256"])
        artifact = provenance["sourceArtifact"]
        archive = EVIDENCE / artifact["committedArchivePath"]
        self.assertEqual(artifact["archiveSize"], archive.stat().st_size)
        self.assertEqual(artifact["archiveSHA256"], digest(archive))
        with ZipFile(archive) as sealed:
            self.assertEqual(set(artifact["inventory"]), set(sealed.namelist()))
            for name, expected in artifact["inventory"].items():
                info = sealed.getinfo(name)
                self.assertEqual(info.file_size, expected["size"], name)
                self.assertEqual(hashlib.sha256(sealed.read(name)).hexdigest(), expected["sha256"], name)
        self.assertEqual(artifact["sourceBundleDigest"], snapshot["sourceRevision"])
        self.assertEqual(artifact["generationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(artifact["bundleManifestSHA256"], digest(EVIDENCE / "bundle/manifest.json"))

    def test_matching_asset_identity_and_clean_replay_chain(self):
        matching = read("source-generation/matching-snapshot.json")
        bundle = read("bundle/manifest.json")
        self.assertEqual(matching["sourceRevision"], bundle["sourceRevision"])
        self.assertEqual(matching["expectedMembers"], sorted(bundle["entries"]))
        self.assertEqual(matching["actualMembers"], matching["expectedMembers"])
        self.assertEqual(matching["missingMembers"], [])
        self.assertEqual(matching["unexpectedCriticalMembers"], [])
        self.assertEqual(matching["digestMismatches"], [])
        for source_name, key in (("database-identity.json", "database"),
                                 ("key-identity.json", "dataKey"),
                                 ("config-identity.json", "config")):
            identity = read(f"source-generation/{source_name}")
            self.assertEqual(identity["entry"], matching[key])
            if "sourceRevision" in identity:
                self.assertEqual(identity["sourceRevision"], matching["sourceRevision"])
        for prefix in ("clean-checkout/macos", "clean-checkout/linux"):
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
        checkout = read("clean-checkout/checkout-record.json")
        self.assertEqual(checkout["sourceBundleDigest"], matching["sourceRevision"])
        self.assertEqual(checkout["sourceGenerationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(checkout["replays"]["macos"]["resultSHA256"], digest(EVIDENCE / "clean-checkout/macos/result.json"))
        self.assertEqual(checkout["replays"]["linux-amd64"]["resultSHA256"], digest(EVIDENCE / "clean-checkout/linux/result.json"))

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
        self.assertEqual(self.manifest["immutableHosting"]["status"], "gap")
        self.assertEqual(self.manifest["authority"]["UpgradeAuthorized"], False)
        self.assertEqual(self.manifest["authority"]["TrustedHopReceipt"], False)


if __name__ == "__main__":
    unittest.main()
