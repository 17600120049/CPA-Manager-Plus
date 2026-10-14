"""Adversarial packaging tests. Synthetic archives never count as runtime evidence."""
import copy
import hashlib
import importlib.util
import io
import json
import os
from pathlib import Path
import tarfile
import tempfile
import unittest
from unittest import mock
import sys
import stat
import warnings
import zipfile

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


class Ret01MaterializeTests(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.archive = self.root / "adversarial.zip"
        self.output = self.root / "materialized"

    def make_archive(self, files=None, mode=stat.S_IFREG | 0o600):
        if files is None:
            files = [
                ("ret-01/source-generation/record.json", b'{"caseID":"RET-01","GeneratedBundleSHA256":"bundle"}'),
                ("ret-01/bundle/manifest.json", b'{"sourceRevision":"bundle"}'),
            ]
        inventory = {}
        with warnings.catch_warnings():
            warnings.simplefilter("ignore", UserWarning)  # intentional duplicate negative
            with zipfile.ZipFile(self.archive, "w") as zipped:
                for name, data in files:
                    entry = zipfile.ZipInfo(name)
                    # Bypass Windows-only constructor normalization so negative
                    # tests serialize the actual hostile name on every platform.
                    entry.filename = name
                    entry.create_system = 3
                    entry.external_attr = mode << 16
                    zipped.writestr(entry, data)
                    inventory[name] = {"size": len(data), "sha256": module.sha256(data)}
        raw = self.archive.read_bytes()
        return {"archiveSize": len(raw), "archiveSHA256": module.sha256(raw), "inventory": inventory,
                "generationRecordSHA256": inventory.get("ret-01/source-generation/record.json", {}).get("sha256"),
                "bundleManifestSHA256": inventory.get("ret-01/bundle/manifest.json", {}).get("sha256"),
                "sourceBundleDigest": "bundle"}

    def reject(self, pin, message):
        with self.assertRaisesRegex(ValueError, message):
            module.materialize_ret01(self.archive, self.output, pin)
        self.assertFalse(self.output.exists())

    def test_outer_mismatch_before_output(self):
        for field, value in (("archiveSHA256", "0" * 64), ("archiveSize", 0)):
            pin = self.make_archive()
            pin[field] = value
            self.reject(pin, "size/SHA256 mismatch")

    def test_paths_links_and_duplicates_before_output(self):
        for name in ("../escape", "/absolute", "ret-01/../../escape", "ret-01/./alias",
                     "ret-01\\escape", "ret-01/C:drive", "ret-01//alias", "ret-01/directory/",
                     "ret-01/file\x00suffix"):
            with self.subTest(name=name):
                self.reject(self.make_archive([(name, b"x")]), "archive member")
        for mode in (stat.S_IFLNK | 0o777, stat.S_IFIFO | 0o600):
            self.reject(self.make_archive([("ret-01/link", b"outside")], mode), "archive member")
        self.reject(self.make_archive([("ret-01/file", b"x"), ("ret-01/file", b"y")]), "duplicate")

    def test_inventory_content_and_critical_bindings_before_output(self):
        for alteration, message in (("missing", "inventory mismatch"), ("size", "member mismatch"),
                                    ("digest", "member mismatch"), ("record", "pinned evidence mismatch"),
                                    ("manifest", "pinned evidence mismatch"), ("bundle", "bundle binding mismatch")):
            with self.subTest(alteration=alteration):
                pin = self.make_archive()
                name = "ret-01/source-generation/record.json"
                if alteration == "missing":
                    del pin["inventory"][name]
                elif alteration == "size":
                    pin["inventory"][name]["size"] = 0
                elif alteration == "digest":
                    pin["inventory"][name]["sha256"] = "0" * 64
                elif alteration == "record":
                    pin["generationRecordSHA256"] = "0" * 64
                elif alteration == "manifest":
                    pin["bundleManifestSHA256"] = "0" * 64
                else:
                    pin["sourceBundleDigest"] = "different-bundle"
                self.reject(pin, message)

    def test_windows_zipinfo_normalization_rejected_before_inventory(self):
        pin = self.make_archive([("ret-01\\escape", b"x")])
        # Exercise Windows' ZipInfo constructor branch on non-Windows hosts too.
        with mock.patch.object(zipfile.os, "sep", "\\"):
            self.reject(pin, "archive member")

    def test_extraction_budget_before_output(self):
        pin = self.make_archive()
        previous = module.MAX_BYTES
        try:
            module.MAX_BYTES = 1
            self.reject(pin, "size/SHA256 mismatch")
        finally:
            module.MAX_BYTES = previous
        self.reject(self.make_archive([(f"ret-01/file-{i}", b"x") for i in range(257)]), "extraction budget")

    def test_existing_output_preserved(self):
        self.output.mkdir()
        sentinel = self.output / "unrelated"
        sentinel.write_bytes(b"preserve")
        with self.assertRaisesRegex(ValueError, "output must be absent"):
            module.materialize_ret01(self.archive, self.output, self.make_archive())
        self.assertEqual(sentinel.read_bytes(), b"preserve")

    def test_release_metadata_fail_closed(self):
        pin = json.loads((module.ROOT / "tests/fixtures/phase4a-evidence/ret-01/source-provenance.json").read_text())["sourceArtifact"]
        metadata = {"id": pin["releaseID"], "tag_name": pin["tag"], "target_commitish": pin["tagCommit"],
                    "draft": False, "prerelease": True, "immutable": True,
                    "assets": [{"id": pin["assetID"], "name": pin["archiveName"], "size": pin["archiveSize"],
                                "state": "uploaded", "digest": "sha256:" + pin["archiveSHA256"]}]}
        module.validate_ret01_release(metadata, pin)
        for field, value in (("id", 0), ("tag_name", "other"), ("target_commitish", "other"),
                             ("draft", True), ("prerelease", False), ("immutable", False), ("immutable", None)):
            altered = copy.deepcopy(metadata)
            altered[field] = value
            with self.subTest(field=field), self.assertRaises(ValueError):
                module.validate_ret01_release(altered, pin)
        for assets in ([], metadata["assets"] * 2):
            altered = copy.deepcopy(metadata)
            altered["assets"] = assets
            with self.assertRaisesRegex(ValueError, "inventory mismatch"):
                module.validate_ret01_release(altered, pin)
        for field, value in (("id", 0), ("name", "other"), ("size", 0), ("state", "new"), ("digest", "sha256:" + "0" * 64)):
            altered = copy.deepcopy(metadata)
            altered["assets"][0][field] = value
            with self.subTest(field=field), self.assertRaisesRegex(ValueError, "identity mismatch"):
                module.validate_ret01_release(altered, pin)

    def test_release_target_requires_separate_actual_tag_identity(self):
        pin = json.loads((module.ROOT / "tests/fixtures/phase4a-evidence/ret-01/source-provenance.json").read_text())["sourceArtifact"]
        pin["releaseTargetCommitish"] = "main"
        metadata = {"id": pin["releaseID"], "tag_name": pin["tag"], "target_commitish": "main",
                    "draft": False, "prerelease": True, "immutable": True,
                    "assets": [{"id": pin["assetID"], "name": pin["archiveName"], "size": pin["archiveSize"],
                                "state": "uploaded", "digest": "sha256:" + pin["archiveSHA256"]}]}
        tag = {"ref": "refs/tags/" + pin["tag"], "object": {"type": "commit", "sha": pin["tagCommit"]}}
        module.validate_ret01_release(metadata, pin, tag)
        for altered in (None, {}, {"ref": "refs/tags/other", "object": tag["object"]},
                        {"ref": tag["ref"], "object": {"type": "tag", "sha": pin["tagCommit"]}},
                        {"ref": tag["ref"], "object": {"type": "commit", "sha": "0" * 40}}):
            with self.subTest(tag=altered), self.assertRaisesRegex(ValueError, "tag commit mismatch"):
                module.validate_ret01_release(metadata, pin, altered)

    @unittest.skipUnless(os.environ.get("CPAMP_RET01_SEALED_ARCHIVE"), "opt-in original immutable RET-01 ZIP")
    def test_original_ret01_zip(self):
        root = module.materialize_ret01(Path(os.environ["CPAMP_RET01_SEALED_ARCHIVE"]), self.output)
        committed = module.ROOT / "tests/fixtures/phase4a-evidence/ret-01/source-generation/record.json"
        self.assertEqual((root / "source-generation/record.json").read_bytes(), committed.read_bytes())


if __name__ == "__main__":
    unittest.main()
