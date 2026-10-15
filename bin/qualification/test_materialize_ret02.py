"""RET-02 shares the safe ZIP verifier; hostile inputs are not historical evidence."""
import json
import os
from pathlib import Path
import unittest
import test_materialize_src11 as packaging_tests

module = packaging_tests.module

class Ret02MaterializeTests(unittest.TestCase):
    def setUp(self):
        self.helper = packaging_tests.Ret01MaterializeTests()
        self.helper.setUp()
        self.addCleanup(self.helper.doCleanups)

    def test_ret01_bundle_cannot_be_relabelled_ret02(self):
        pin = self.helper.make_archive()
        with self.assertRaises((KeyError, ValueError)):
            module.materialize_retention(self.helper.archive, self.helper.output, "ret-02", pin)
        self.assertFalse(self.helper.output.exists())

    def test_ret02_binding_and_inventory(self):
        pin = self.helper.make_archive([
            ("ret-02/source-generation/record.json", b'{"caseID":"RET-02","GeneratedBundleSHA256":"bundle"}'),
            ("ret-02/bundle/manifest.json", b'{"sourceRevision":"bundle"}'),
        ])
        pin["generationRecordSHA256"] = pin["inventory"]["ret-02/source-generation/record.json"]["sha256"]
        pin["bundleManifestSHA256"] = pin["inventory"]["ret-02/bundle/manifest.json"]["sha256"]
        result = module.materialize_retention(self.helper.archive, self.helper.output, "ret-02", pin)
        self.assertEqual(result, self.helper.output / "ret-02")

    @unittest.skipUnless(os.environ.get("CPAMP_RET02_SEALED_ARCHIVE"), "opt-in actual RET-02 immutable bytes")
    def test_original_bytes(self):
        root = module.materialize_retention(Path(os.environ["CPAMP_RET02_SEALED_ARCHIVE"]),
                                            self.helper.output, "ret-02")
        committed = module.ROOT / "tests/fixtures/phase4a-evidence/ret-02/source-generation/record.json"
        self.assertEqual((root / "source-generation/record.json").read_bytes(), committed.read_bytes())

if __name__ == "__main__":
    unittest.main()
