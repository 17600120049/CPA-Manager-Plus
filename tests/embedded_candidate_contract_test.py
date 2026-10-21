#!/usr/bin/env python3
"""Contract tests for disposable R1-1 OCI verifier; no Docker or network."""
import gzip
import hashlib
import importlib.util
import io
import json
import tarfile
import tempfile
import unittest
from unittest.mock import patch
from pathlib import Path

VERIFIER = Path(__file__).resolve().parents[1] / "bin/release/verify-embedded-candidate.py"
spec = importlib.util.spec_from_file_location("embedded_candidate", VERIFIER)
subject = importlib.util.module_from_spec(spec)
spec.loader.exec_module(subject)
SHA = "a" * 40
EVENT = "local-test-event"


def blob(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def tar_content(files):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w") as archive:
        for path, data in files.items():
            entry = tarfile.TarInfo(path.lstrip("/"))
            entry.size = len(data)
            archive.addfile(entry, io.BytesIO(data))
    return output.getvalue()


def sample_elf():
    data = bytearray(64)
    data[0:4] = b"\x7fELF"
    data[18:20] = (62).to_bytes(2, "little")
    return bytes(data)


def make_oci(destination, role, license_bytes, *,
             source=SHA, event=EVENT, arch="amd64", corrupt=False, missing=None,
             bad_descriptor_size=False, bad_descriptor_type=False, bad_diff_id=False):
    files = {
        "/usr/local/share/licenses/cpamp/LICENSE": license_bytes,
        subject.NOTICE: b"NOT_VERIFIED: license inventory only",
    }
    if role == "manager":
        files["/usr/local/bin/cpa-manager-plus"] = sample_elf()
        files[subject.WEB_MANIFEST] = json.dumps({
            "schemaVersion": 1, "files": [
                {"path": "index.html", "sha256": blob(b"<html/>")},
                {"path": "assets/a.js", "sha256": blob(b"x")},
            ],
        }).encode()
    elif role == "ingress":
        files["/usr/local/bin/cpamp-ingress"] = sample_elf()
    elif role == "runtime":
        files["/usr/local/bin/cpamp-runtime-supervisor"] = sample_elf()
        files["/usr/local/bin/cli-proxy-api"] = sample_elf()
        files["/usr/local/share/cpamp/runtime/cpa-artifact.json"] = json.dumps({
            "schemaVersion": 1, "engine": "cpa", "version": "8.0.13",
            "artifactId": "sha256:" + subject.CPA_SHA,
        }).encode()
        files["/usr/local/share/cpamp/runtime/config.seed.yaml"] = b"seed"
        files["/usr/local/bin/cpamp-runtime-v8-entrypoint"] = b"#!/bin/sh"
        files["/usr/local/share/licenses/cli-proxy-api/LICENSE"] = b"official"
    if missing:
        files.pop(missing)
    uncompressed = tar_content(files)
    layer = gzip.compress(uncompressed)
    layer_id = blob(layer)
    diff_id = blob(uncompressed)
    config = json.dumps({
        "os": "linux", "architecture": arch,
        "rootfs": {"type": "layers", "diff_ids": [
            "sha256:" + "d" * 64 if bad_diff_id else diff_id,
        ]},
        "config": {"Labels": {
            "org.opencontainers.image.revision": source,
            "org.opencontainers.image.source": subject.SOURCE,
            "org.opencontainers.image.version": subject.CANDIDATE_VERSION,
            "io.cpamp.candidate.event": event,
            "io.cpamp.candidate.role": role,
        }},
    }).encode()
    config_id = blob(config)
    manifest = json.dumps({"schemaVersion": 2, "mediaType": subject.MANIFEST_TYPE,
                           "config": {"digest": config_id, "size": len(config),
                                      "mediaType": subject.CONFIG_TYPE},
                           "layers": [{"digest": layer_id, "size": len(layer),
                                       "mediaType": subject.LAYER_GZIP}]}).encode()
    manifest_id = blob(manifest)
    index = json.dumps({"schemaVersion": 2, "manifests": [{
        "digest": manifest_id,
        "size": len(manifest) + (1 if bad_descriptor_size else 0),
        "mediaType": "application/invalid" if bad_descriptor_type else subject.MANIFEST_TYPE,
        "platform": {"os": "linux", "architecture": arch},
    }]}).encode()
    items = {
        "oci-layout": b'{"imageLayoutVersion":"1.0.0"}',
        "index.json": index,
        "blobs/sha256/" + layer_id[7:]: layer,
        "blobs/sha256/" + config_id[7:]: config,
        "blobs/sha256/" + manifest_id[7:]: manifest,
    }
    if corrupt:
        items["blobs/sha256/" + layer_id[7:]] = b"not the declared layer"
    destination.write_bytes(tar_content(items))


class EmbeddedCandidateContracts(unittest.TestCase):
    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.license = b"MIT License\n"
        self.file = self.root / "candidate.tar"

    def inspect(self, role="manager", **kwargs):
        make_oci(self.file, role, self.license, **kwargs)
        return subject.inspect(self.file, role, SHA, EVENT, self.license, b"seed")

    def test_three_role_structural_receipt_with_mocked_official_fixture(self):
        # Only this fixture mocks CPA official hashes; production verifier uses immutable pins.
        (self.root / "LICENSE").write_bytes(self.license)
        seed = self.root / "docker/runtime/config.v8.default.seed.yaml"
        seed.parent.mkdir(parents=True)
        seed.write_bytes(b"seed")
        fake_cpa = hashlib.sha256(sample_elf()).hexdigest()
        fake_license = hashlib.sha256(b"official").hexdigest()
        with patch.object(subject, "CPA_SHA", fake_cpa), patch.object(
                subject, "CPA_LICENSE_SHA", fake_license):
            archives = {}
            for role in subject.ROLES:
                archive = self.root / (role + ".tar")
                make_oci(archive, role, self.license)
                archives[role] = archive
            receipt = subject.verify(archives, SHA, EVENT, self.root)
            self.assertEqual(receipt["status"],
                             "STRUCTURAL_CANDIDATE_EVIDENCE_ONLY_NOT_RELEASE_QUALIFIED")
            self.assertEqual(set(receipt["images"]), set(subject.ROLES))
            self.assertEqual(len({x["platformManifestDigest"] for x in receipt["images"].values()}), 3)
            make_oci(archives["ingress"], "ingress", self.license, event="wrong-event")
            with self.assertRaisesRegex(ValueError, "build event"):
                subject.verify(archives, SHA, EVENT, self.root)

    def test_manager_positive_manifest_and_binary_hashes(self):
        evidence = self.inspect()
        self.assertEqual(evidence["platform"], "linux/amd64")
        self.assertEqual(len(evidence["platformManifestDigest"]), 71)
        self.assertIn(subject.WEB_MANIFEST, evidence["packagedFiles"])
        self.assertNotEqual(evidence["ociLayoutIndexDigest"],
                            evidence["platformManifestDigest"])

    def test_ingress_positive(self):
        evidence = self.inspect("ingress")
        self.assertIn("/usr/local/bin/cpamp-ingress", evidence["packagedFiles"])

    def test_reject_invalid_descriptor_size(self):
        with self.assertRaisesRegex(ValueError, "descriptor size"):
            self.inspect(bad_descriptor_size=True)

    def test_reject_invalid_descriptor_type(self):
        with self.assertRaisesRegex(ValueError, "descriptor mediaType"):
            self.inspect(bad_descriptor_type=True)

    def test_reject_invalid_rootfs_diff_id(self):
        with self.assertRaisesRegex(ValueError, "rootfs diff_id"):
            self.inspect(bad_diff_id=True)

    def test_reject_modified_oci_blob(self):
        with self.assertRaisesRegex(ValueError, "digest mismatch"):
            self.inspect(corrupt=True)

    def test_reject_wrong_source(self):
        with self.assertRaisesRegex(ValueError, "source identity"):
            self.inspect(source="b" * 40)

    def test_reject_wrong_event(self):
        with self.assertRaisesRegex(ValueError, "build event"):
            self.inspect(event="different-event")

    def test_reject_wrong_platform(self):
        with self.assertRaisesRegex(ValueError, "linux/amd64"):
            self.inspect(arch="arm64")

    def test_reject_missing_web_evidence(self):
        with self.assertRaisesRegex(ValueError, "missing packaged files"):
            self.inspect(missing=subject.WEB_MANIFEST)

    def test_reject_missing_notice_inventory(self):
        with self.assertRaisesRegex(ValueError, "missing packaged files"):
            self.inspect("ingress", missing=subject.NOTICE)

    def test_reject_runtime_with_unpinned_cpa(self):
        with self.assertRaisesRegex(ValueError, "official CPA"):
            self.inspect("runtime")


if __name__ == "__main__":
    unittest.main()
