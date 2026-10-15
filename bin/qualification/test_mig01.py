"""MIG-01 evidence contract; no other matrix case is qualified here."""
import hashlib
import importlib.util
import json
import os
from pathlib import Path
import sqlite3
import subprocess
import unittest

ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/mig-01"
SOURCE_COMMIT = "166262365d506157b99968105c890f480cc7c07c"


def digest(path):
    return hashlib.sha256(path.read_bytes()).hexdigest()


def read(path):
    return json.loads(path.read_text(encoding="utf-8"))


class Mig01EvidenceTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        if not (EVIDENCE / "evidence-manifest.json").exists():
            if os.environ.get("CPAMP_MIG01_REQUIRED") == "1":
                raise AssertionError("MIG-01 evidence is required")
            raise unittest.SkipTest("generation not yet recorded")
        cls.manifest = read(EVIDENCE / "evidence-manifest.json")
        cls.generation = read(EVIDENCE / "source-generation/record.json")
        cls.provenance = read(EVIDENCE / "source-provenance.json")

    def test_complete_literal_inventory(self):
        inventory = self.manifest["inventory"]
        actual = {p.relative_to(EVIDENCE).as_posix() for p in EVIDENCE.rglob("*")
                  if p.is_file() and p.name not in ("README.md", "evidence-manifest.json")}
        self.assertEqual(set(inventory), actual)
        for name, entry in inventory.items():
            path = EVIDENCE / name
            self.assertEqual(path.stat().st_size, entry["size"], name)
            self.assertEqual(digest(path), entry["sha256"], name)

    def test_frozen_product_scope(self):
        scope = read(EVIDENCE / "product-scope.json")
        self.assertEqual(scope["baselineCommit"], "3f65813749a89371a3005b4a710836df587a7582")
        self.assertEqual(
            scope["verificationCommit"],
            read(EVIDENCE / "result.json")["qualificationExecutionCommit"],
        )
        self.assertFalse(scope["productCodeChanged"])
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
        for path in ("internal/repository/sqlite/legacy_migration_readiness.go",
                     "internal/service/legacypreflight/direct_candidate.go"):
            self.assertFalse((ROOT / "apps/manager-server" / path).exists(), path)
        for name in ("mig01_readiness_test.go", "mig01_migration_state_test.go"):
            self.assertTrue((ROOT / "apps/manager-server/tests/qualification/src11" / name).is_file())

    def test_independent_exact_generation(self):
        record = self.generation
        self.assertEqual(record["caseID"], "MIG-01")
        self.assertEqual(record["SourceRelease"], "v1.14.4")
        self.assertEqual(record["SourceCommit"], SOURCE_COMMIT)
        self.assertEqual(record["SourceProducerBuildCodeCommit"], SOURCE_COMMIT)
        self.assertEqual(record["PlanCommit"], "7eefdbf50d3596890a75e97a400dc1f5286e6498")
        self.assertEqual(record["GenerationProfile"], "mig-01-completed-revision2-v1")
        self.assertEqual(digest(EVIDENCE / "profile.json"), record["ControlledInputSHA256"])
        self.assertTrue(record["process"]["reaped"])
        self.assertEqual(record["process"]["exitCode"], 0)
        self.assertFalse(record["UpgradeAuthorized"])
        self.assertFalse(record["TrustedHopReceipt"])
        index = read(EVIDENCE / "generation-index.json")
        self.assertEqual(index["SHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertEqual(index["bundleDigest"], record["GeneratedBundleSHA256"])
        runner = read(EVIDENCE / "generation-runner.json")
        self.assertEqual(runner["codeCommit"], record["GeneratorCodeCommit"])
        self.assertEqual(runner["generationRecordSHA256"], index["SHA256"])
        self.assertEqual(runner["executionExitCode"], 0)
        for name, sha in runner["logs"].items():
            self.assertEqual(digest(EVIDENCE / ("generation-" + name + ".txt")), sha)
        for name, sha in record["GenerationEvidenceFiles"].items():
            self.assertEqual(digest(EVIDENCE / "source-generation" / name), sha, name)
        for other in ("src-11/source-generation/record.json", "ret-01/source-generation/record.json"):
            old = read(EVIDENCE.parent / other)
            self.assertNotEqual(record["GeneratedBundleSHA256"], old["GeneratedBundleSHA256"])

    def test_actual_readiness_semantics_and_source_immutability(self):
        for prefix in ("runtime", "platform-replay/linux", "clean-checkout", "verification/qualification-race"):
            root = EVIDENCE / prefix
            result = read(root / "result.json")
            ready = read(root / "readiness-result.json")
            state = read(root / "migration-state-snapshot.json")
            semantic = read(root / "semantic-assertions.json")
            runner = read(root / "runner-evidence.json") if prefix != "verification/qualification-race" else None
            self.assertEqual(result["runtimeEvidence"], "pass")
            self.assertEqual(result["independentAcceptance"], "pending")
            self.assertEqual(result["applicableSEM"], ["SEM-01", "SEM-07"])
            self.assertEqual(ready["status"], "ready")
            self.assertEqual(ready["scope"], "controlled-direct-candidate-data-compatibility")
            self.assertEqual(ready["sourceCommit"], SOURCE_COMMIT)
            self.assertEqual(ready["preflight"]["exactSourceRelease"]["status"], "unknown")
            self.assertEqual(ready["preflight"]["bridgeReadiness"]["status"], "unknown")
            self.assertEqual(result["controlledDirectCandidateReadiness"], ready["status"])
            self.assertEqual(result["productionBridgeReadiness"], ready["preflight"]["bridgeReadiness"]["status"])
            self.assertNotIn("BridgeReadiness", result)
            execution = read(root / "execution-record.json")
            for key in ("controlledDirectCandidateReadiness", "productionBridgeReadiness"):
                self.assertEqual(execution[key], result[key])
            self.assertFalse(ready["preflight"]["upgradeAuthorized"])
            self.assertNotIn("requiredMigrationReadiness", ready["preflight"]["observed"])
            self.assertEqual(set(ready["checks"].values()), {"pass"})
            self.assertEqual(state["before"], state["after"])
            self.assertEqual(state["readinessInspection"]["cacheRevision"], "2")
            self.assertEqual(state["readinessInspection"]["cacheStagingRows"], 0)
            self.assertEqual(state["readinessInspection"]["quotaUnattachedRows"], 0)
            for key in ("cache", "quota"):
                rows = state["readinessInspection"][key]
                self.assertEqual(len(rows), 1)
                self.assertEqual(rows[0]["status"], "completed")
                self.assertFalse(rows[0]["hasError"])
            self.assertEqual(result["beforeDigest"], result["afterDigest"])
            self.assertEqual(result["sourceBeforeDigest"], result["sourceAfterDigest"])
            self.assertEqual(result["inputBundleDigest"], self.generation["GeneratedBundleSHA256"])
            history = read(root / "authoritative-digest.json")
            self.assertEqual(history["beforeSnapshot"], history["afterSnapshot"])
            self.assertEqual(history["beforeSnapshot"], read(EVIDENCE / "source-generation/source-snapshot.json"))
            for sem in ("SEM-01", "SEM-07"):
                self.assertEqual(semantic[sem]["status"], "runtime-pass")
            self.assertTrue(semantic["SEM-07"]["bankedResetEvidence"])
            self.assertTrue(semantic["SEM-07"]["cooldownEvidence"])
            self.assertFalse(semantic["sourceMutation"]["detected"])
            self.assertEqual(semantic["guard"]["missingExactGenerationEvidence"], "rejected")
            if runner is not None:
                self.assertEqual(runner["resultSHA256"], digest(root / "result.json"))
                self.assertEqual(runner["codeCommit"], result["executionCommit"])
                self.assertEqual(runner["executionExitCode"], 0)
                for name, sha in runner["logs"].items():
                    self.assertEqual(digest(root / (name + ".txt")), sha)
            for name in ("UpgradeAuthorized", "TrustedHopReceipt", "Phase4A07Complete", "Phase4AComplete"):
                self.assertFalse(result[name])

    def test_durable_asset_and_delivery_boundary(self):
        pin = self.provenance["sourceArtifact"]
        self.assertTrue(pin["immutable"])
        self.assertEqual(pin["sourceBundleDigest"], self.generation["GeneratedBundleSHA256"])
        self.assertEqual(pin["generationRecordSHA256"], digest(EVIDENCE / "source-generation/record.json"))
        self.assertRegex(pin["archiveSHA256"], "^[a-f0-9]{64}$")
        self.assertGreater(pin["releaseID"], 0)
        self.assertGreater(pin["assetID"], 0)
        spec = importlib.util.spec_from_file_location("mig_materializer", ROOT / "bin/qualification/materialize-src11.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        module.validate_ret01_release(read(EVIDENCE / "release-metadata.json"), pin,
                                      read(EVIDENCE / "tag-metadata.json"))
        clean = read(EVIDENCE / "clean-checkout/checkout-record.json")
        self.assertEqual(clean["statusBefore"], "")
        self.assertEqual(clean["statusAfter"], "")
        self.assertEqual(clean["executionCommit"], read(EVIDENCE / "clean-checkout/result.json")["executionCommit"])
        self.assertEqual(clean["downloadedArchiveSHA256"], pin["archiveSHA256"])
        result = read(EVIDENCE / "result.json")
        self.assertTrue(result["casePass"])
        self.assertEqual(result["independentAcceptance"], "pending")
        self.assertEqual(len(result["completionCriteria"]), 13)
        self.assertTrue(all(result["completionCriteria"].values()))
        self.assertNotIn("actualBridgeReadinessPass", result["completionCriteria"])
        self.assertTrue(result["completionCriteria"]["controlledDirectCandidateReadinessPass"])
        self.assertNotIn("BridgeReadiness", result)
        runtime = read(EVIDENCE / "runtime/result.json")
        for key in ("controlledDirectCandidateReadiness", "productionBridgeReadiness"):
            self.assertEqual(result[key], runtime[key])
        self.assertEqual(result["acceptedCases"], ["SRC-11", "RET-01", "AUT-03"])
        self.assertEqual(result["independentAcceptancePendingCases"], 46)
        for name in ("UpgradeAuthorized", "TrustedHopReceipt", "Phase4A07Complete", "Phase4AComplete"):
            self.assertFalse(result[name])

    @unittest.skipUnless(os.environ.get("CPAMP_MIG01_FULL_EVIDENCE"), "opt-in actual immutable source bytes")
    def test_persisted_source_bytes(self):
        root = Path(os.environ["CPAMP_MIG01_FULL_EVIDENCE"])
        self.assertEqual(digest(root / "source-generation/record.json"), digest(EVIDENCE / "source-generation/record.json"))
        uri = (root / "bundle/database").resolve().as_uri() + "?mode=ro&immutable=1"
        with sqlite3.connect(uri, uri=True) as db:
            db.row_factory = sqlite3.Row
            self.assertEqual(db.execute("select value from settings where key='usage_cache_accounting_semantics_revision'").fetchone()[0], "2")
            states = [dict(r) for r in db.execute("select * from usage_data_migrations order by name")]
            self.assertEqual(states, self.generation["migrationState"])
            self.assertEqual(db.execute("select count(*) from usage_cache_accounting_v2_changes").fetchone()[0], 0)
            self.assertEqual(db.execute("select count(*) from account_quota_snapshots where observation_id is null").fetchone()[0], 0)
            generated = read(root / "source-generation/source-snapshot.json")
            for table, records in generated.items():
                actual = [dict(r) for r in db.execute(f"select * from {table} order by rowid")]
                self.assertEqual(actual, records, table)


if __name__ == "__main__":
    unittest.main()
