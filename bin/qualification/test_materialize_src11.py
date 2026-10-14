"""Adversarial packaging tests. Synthetic archives never count as SRC-11 evidence."""
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
import sys

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("materialize_src11", Path(__file__).with_name("materialize-src11.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class MaterializeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.archive = self.root / "adversarial.tar.gz"
        self.output = self.root / "materialized"

    def make_archive(self, names, kind=tarfile.REGTYPE):
        with tarfile.open(self.archive, "w:gz") as tar:
            for name in names:
                entry = tarfile.TarInfo(name)
                entry.type = kind
                entry.linkname = "outside" if kind != tarfile.REGTYPE else ""
                entry.size = 1 if kind == tarfile.REGTYPE else 0
                tar.addfile(entry, io.BytesIO(b"x") if entry.size else None)
        data = self.archive.read_bytes()
        return {"archiveSize": len(data), "archiveSHA256": hashlib.sha256(data).hexdigest()}

    def test_outer_mismatch_does_not_create_output(self):
        pin = self.make_archive(["src-11/file"])
        pin["archiveSHA256"] = "0" * 64
        with self.assertRaisesRegex(ValueError, "SHA256 mismatch"):
            module.materialize(self.archive, self.output, pin)
        self.assertFalse(self.output.exists())

    def test_paths_and_links_rejected_even_with_matching_outer_digest(self):
        for name in ["../escape", "/absolute", "src-11/../../escape", "src-11/./alias", "src-11\\escape", "src-11/C:drive", "other/file"]:
            with self.subTest(name=name):
                pin = self.make_archive([name])
                with self.assertRaisesRegex(ValueError, "archive member"):
                    module.materialize(self.archive, self.output, pin)
                self.assertFalse(self.output.exists())
        for kind in [tarfile.SYMTYPE, tarfile.LNKTYPE]:
            pin = self.make_archive(["src-11/link"], kind)
            with self.assertRaisesRegex(ValueError, "archive member"):
                module.materialize(self.archive, self.output, pin)

    def test_duplicate_rejected(self):
        pin = self.make_archive(["src-11/duplicate", "src-11/duplicate"])
        with self.assertRaisesRegex(ValueError, "duplicate"):
            module.materialize(self.archive, self.output, pin)

    def test_existing_output_preserved(self):
        self.output.mkdir()
        sentinel = self.output / "unrelated"
        sentinel.write_bytes(b"preserve")
        pin = self.make_archive(["src-11/file"])
        with self.assertRaisesRegex(ValueError, "output must be absent"):
            module.materialize(self.archive, self.output, pin)
        self.assertEqual(sentinel.read_bytes(), b"preserve")

    @unittest.skipUnless(os.environ.get("CPAMP_SRC11_SEALED_ARCHIVE"), "opt-in actual sealed archive")
    def test_original_sealed_bytes_and_inventory(self):
        root = module.materialize(Path(os.environ["CPAMP_SRC11_SEALED_ARCHIVE"]), self.output)
        record = json.loads((root / "source-generation/record.json").read_text())
        self.assertEqual(record["GeneratedBundleSHA256"], "0e106589baef4148ee075ed77ce93bc71e83ca315a377311d62daeb9144236e3")
        for name, entry in record["bundleInventory"]["entries"].items():
            p = root / "bundle" / name
            if entry["present"]:
                self.assertEqual(p.stat().st_size, entry["size"])
                self.assertEqual(module.sha256(p.read_bytes()), entry["sha256"])
            else:
                self.assertFalse(p.exists())
        for name, digest in record["GenerationEvidenceFiles"].items():
            self.assertEqual(module.sha256((root / "source-generation" / name).read_bytes()), digest)


if __name__ == "__main__":
    unittest.main()
