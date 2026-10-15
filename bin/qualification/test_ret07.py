"""RET-07 evidence and regressions for the independent review's false positives."""
import copy
import importlib.util
import json
from pathlib import Path
import unittest

ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/ret-07"
spec = importlib.util.spec_from_file_location("ret07", Path(__file__).with_name("verify-ret07-evidence.py"))
verifier = importlib.util.module_from_spec(spec)
spec.loader.exec_module(verifier)


class Ret07EvidenceTests(unittest.TestCase):
    def test_committed_evidence_and_authority(self):
        result = verifier.verify(EVIDENCE)
        self.assertFalse(result["originalSourceBytesVerified"])
        self.assertFalse(result["casePass"])
        self.assertEqual(result["independentAcceptance"], "pending")

    def test_tier_must_be_exercised_and_distinguish_default(self):
        tier = json.loads((EVIDENCE / "source-generation/historical-reconciliation.json").read_text())["tierSelection"]
        verifier.verify_tier_selection(tier)
        mutations = [
            lambda t: t.update(thresholdTokens=100000, selectedContextThreshold=0),
            lambda t: t.update(baseFallbackUSD=t["historicalTierUSD"]),
            lambda t: t["retainedProjection"][0].update(normalized_total_input_tokens=None),
            lambda t: t["persistedBands"][0].update(context_threshold_tokens=0),
            lambda t: t["contextTiers"][0].update(prompt_per_1m=4),
            lambda t: t.update(tierWasExercised=False),
        ]
        for mutate in mutations:
            changed = copy.deepcopy(tier)
            mutate(changed)
            with self.subTest(mutation=changed), self.assertRaises(verifier.EvidenceError):
                verifier.verify_tier_selection(changed)

    def test_missing_original_source_pin_is_rejected(self):
        from unittest.mock import patch
        with patch.object(verifier, "read", return_value={}), self.assertRaises(verifier.EvidenceError):
            verifier.verify_artifact(EVIDENCE, None)


if __name__ == "__main__":
    unittest.main()
