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
import os
import subprocess
from unittest.mock import patch
from pathlib import Path

VERIFIER = Path(__file__).resolve().parents[1] / "bin/release/verify-embedded-candidate.py"
spec = importlib.util.spec_from_file_location("embedded_candidate", VERIFIER)
subject = importlib.util.module_from_spec(spec)
spec.loader.exec_module(subject)
SHA = "a" * 40
EVENT = "local-test-event"
MOCK_NOTICE = b"NOT_VERIFIED: license inventory only"
MOCK_RUNTIME_ENTRYPOINT = b"#!/bin/sh\n"
MOCK_API_CONTRACT = json.dumps({
    "schemaVersion": 1, "qualification": subject.API_CONTRACT_QUALIFICATION,
    "apiSuffix": "/api/v2", "expectedPath": "/phase4-test/api/v2/technical",
}).encode()


def blob(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def tar_content(files, bad_mode=None):
    output = io.BytesIO()
    with tarfile.open(fileobj=output, mode="w") as archive:
        for path, data in files.items():
            entry = tarfile.TarInfo(path.lstrip("/"))
            entry.size = len(data)
            entry.mode = 0o755 if path.startswith("/usr/local/bin/") else 0o644
            if path == bad_mode:
                entry.mode = 0o644
            archive.addfile(entry, io.BytesIO(data))
    return output.getvalue()


def sample_elf():
    # The 64-byte header was the pre-fix false positive: not a loadable binary.
    data = bytearray(64)
    data[0:4] = b"\x7fELF"
    data[4:7] = b"\x02\x01\x01"
    data[16:18] = (2).to_bytes(2, "little")
    data[18:20] = (62).to_bytes(2, "little")
    data[20:24] = (1).to_bytes(4, "little")
    return bytes(data)


ROLE_BINARIES = {}


def make_oci(destination, role, license_bytes, *,
             source=SHA, event=EVENT, arch="amd64", corrupt=False, missing=None,
             bad_descriptor_size=False, bad_descriptor_type=False, bad_diff_id=False,
             notice_bytes=MOCK_NOTICE, api_contract=MOCK_API_CONTRACT,
             bad_mode=None, invalid_elf=False, wrong_module=False, wrong_source_stamp=False,
             wrong_sha_suffix=False, wrong_version_suffix=False, wrong_entrypoint=False,
             wrong_cmd=False, runtime_script=MOCK_RUNTIME_ENTRYPOINT):
    files = {
        "/usr/local/share/licenses/cpamp/LICENSE": license_bytes,
        subject.NOTICE: notice_bytes,
    }
    if role == "manager":
        files["/usr/local/bin/cpa-manager-plus"] = (
            sample_elf() if invalid_elf else
            ROLE_BINARIES["ingress"] if wrong_module else
            ROLE_BINARIES["manager_wrong_stamp"] if wrong_source_stamp else
            ROLE_BINARIES["manager_sha_suffix"] if wrong_sha_suffix else
            ROLE_BINARIES["manager_version_suffix"] if wrong_version_suffix else
            ROLE_BINARIES["manager"]
        )
        files[subject.API_CONTRACT] = api_contract
        files[subject.WEB_MANIFEST] = json.dumps({
            "schemaVersion": 1, "files": [
                {"path": "index.html", "sha256": blob(b"<html/>")},
                {"path": "assets/a.js", "sha256": blob(b"x")},
            ],
        }).encode()
    elif role == "ingress":
        files["/usr/local/bin/cpamp-ingress"] = (
            sample_elf() if invalid_elf else ROLE_BINARIES["ingress"]
        )
    elif role == "runtime":
        files["/usr/local/bin/cpamp-runtime-supervisor"] = (
            sample_elf() if invalid_elf else ROLE_BINARIES["runtime"]
        )
        files["/usr/local/bin/cli-proxy-api"] = ROLE_BINARIES["ingress"]
        files["/usr/local/share/cpamp/runtime/cpa-artifact.json"] = json.dumps({
            "schemaVersion": 1, "engine": "cpa", "version": "8.0.13",
            "artifactId": "sha256:" + subject.CPA_SHA,
        }).encode()
        files["/usr/local/share/cpamp/runtime/config.seed.yaml"] = b"seed"
        files["/usr/local/bin/cpamp-runtime-v8-entrypoint"] = runtime_script
        files["/usr/local/share/licenses/cli-proxy-api/LICENSE"] = b"official"
    if missing:
        files.pop(missing)
    uncompressed = tar_content(files, bad_mode=bad_mode)
    layer = gzip.compress(uncompressed)
    layer_id = blob(layer)
    diff_id = blob(uncompressed)
    entrypoint, command = subject.STARTUP[role]
    config = json.dumps({
        "os": "linux", "architecture": arch,
        "rootfs": {"type": "layers", "diff_ids": [
            "sha256:" + "d" * 64 if bad_diff_id else diff_id,
        ]},
        "config": {
            "Entrypoint": ["/bin/false"] if wrong_entrypoint else entrypoint,
            "Cmd": ["/bin/false"] if wrong_cmd else command,
            "Labels": {
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
    @classmethod
    def setUpClass(cls):
        # Real, offline Go ELF fixtures: synthetic 64-byte ELF headers must
        # never be accepted as a valid CPAMP binary. No CPA upstream code used.
        cls.go_fixture_tmp = tempfile.TemporaryDirectory(prefix="cpamp-go-binary-fixture-")
        cls.addClassCleanup(cls.go_fixture_tmp.cleanup)
        base = Path(cls.go_fixture_tmp.name)
        for key in ("manager", "manager_wrong_stamp", "manager_sha_suffix",
                    "manager_version_suffix", "ingress", "runtime"):
            role = "manager" if key.startswith("manager") else key
            module, command = subject.GO_MODULES[role]
            work = base / key
            (work / command).mkdir(parents=True)
            (work / "go.mod").write_text("module " + module + "\n\ngo 1.20\n", encoding="utf-8")
            source = "package main\nfunc main() {}\n"
            if role == "manager":
                buildinfo = work / "internal/buildinfo"
                buildinfo.mkdir(parents=True)
                (buildinfo / "buildinfo.go").write_text(
                    'package buildinfo\nvar Version = "dev"\nvar SourceCommit = "unknown"\n',
                    encoding="utf-8",
                )
                source = ('package main\nimport ("fmt"; "' + module + '/internal/buildinfo")\n'
                          'func main() { fmt.Println(buildinfo.Version, buildinfo.SourceCommit) }\n')
            (work / command / "main.go").write_text(source, encoding="utf-8")
            env = dict(os.environ, GOTOOLCHAIN="local", GOPROXY="off", GOSUMDB="off",
                       GOOS="linux", GOARCH="amd64", CGO_ENABLED="0")
            args = ["go", "build", "-o", str(work / "candidate")]
            if role != "manager":
                args.insert(2, "-trimpath")
            # Go omits -ldflags metadata when -trimpath is enabled. The actual
            # Manager Docker build keeps the non-trimpath linker stamp.
            if role == "manager":
                stamp = "b" * 40 if key == "manager_wrong_stamp" else SHA
                if key == "manager_sha_suffix":
                    stamp += "extra"
                version = subject.CANDIDATE_VERSION
                if key == "manager_version_suffix":
                    version += "-modified"
                args += ["-ldflags",
                         "-X " + module + "/internal/buildinfo.Version=" +
                         version +
                         " -X " + module + "/internal/buildinfo.SourceCommit=" + stamp]
            args += ["./" + command]
            completed = subprocess.run(args, cwd=work, env=env, capture_output=True,
                                       text=True, timeout=75)
            if completed.returncode != 0:
                raise AssertionError("cannot create local Go fixture: " + completed.stderr)
            ROLE_BINARIES[key] = (work / "candidate").read_bytes()

    def setUp(self):
        self.temp = tempfile.TemporaryDirectory()
        self.addCleanup(self.temp.cleanup)
        self.root = Path(self.temp.name)
        self.license = b"MIT License\n"
        self.file = self.root / "candidate.tar"

    def inspect(self, role="manager", **kwargs):
        make_oci(self.file, role, self.license, **kwargs)
        return subject.inspect(self.file, role, SHA, EVENT, self.license, b"seed",
                               MOCK_RUNTIME_ENTRYPOINT)

    def test_three_role_structural_receipt_with_mocked_official_fixture(self):
        # Only this fixture mocks CPA official hashes; production verifier uses immutable pins.
        (self.root / "LICENSE").write_bytes(self.license)
        seed = self.root / "docker/runtime/config.v8.default.seed.yaml"
        seed.parent.mkdir(parents=True)
        seed.write_bytes(b"seed")
        entrypoint = self.root / "docker/runtime/v8-entrypoint.sh"
        entrypoint.write_bytes(MOCK_RUNTIME_ENTRYPOINT)
        inventory = self.root / "bin/release/embedded-third-party-inventory.txt"
        inventory.parent.mkdir(parents=True)
        inventory.write_bytes(MOCK_NOTICE)
        api = self.root / "tests/fixtures/r1-embedded-api-transport.json"
        api.parent.mkdir(parents=True)
        api.write_bytes(MOCK_API_CONTRACT)
        fake_cpa = hashlib.sha256(ROLE_BINARIES["ingress"]).hexdigest()
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
            make_oci(archives["ingress"], "ingress", self.license, notice_bytes=MOCK_NOTICE + b" tampered")
            with self.assertRaisesRegex(ValueError, "third-party inventory differs"):
                subject.verify(archives, SHA, EVENT, self.root)
            make_oci(archives["ingress"], "ingress", self.license)
            make_oci(archives["manager"], "manager", self.license, api_contract=MOCK_API_CONTRACT + b" ")
            with self.assertRaisesRegex(ValueError, "public API transport contract differs"):
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

    def test_reject_missing_api_transport_fixture(self):
        with self.assertRaisesRegex(ValueError, "missing packaged files"):
            self.inspect(missing=subject.API_CONTRACT)

    def test_reject_mismatched_api_contract(self):
        with self.assertRaisesRegex(ValueError, "Web/API transport contract"):
            self.inspect(api_contract=b"{}")

    def test_reject_non_executable_manager(self):
        with self.assertRaisesRegex(ValueError, "not safely executable"):
            self.inspect(bad_mode=subject.GO_BINARIES["manager"])

    def test_reject_non_executable_entrypoint(self):
        with self.assertRaisesRegex(ValueError, "not safely executable"):
            self.inspect("runtime", bad_mode="/usr/local/bin/cpamp-runtime-v8-entrypoint")

    def test_reject_fake_elf_header_without_loadable_segments(self):
        with self.assertRaisesRegex(ValueError, "invalid ELF program headers"):
            self.inspect(invalid_elf=True)

    def test_reject_wrong_compiled_go_module(self):
        with self.assertRaisesRegex(ValueError, "Go compiled module/entrypoint mismatch"):
            self.inspect(wrong_module=True)

    def test_reject_wrong_manager_compiled_source_stamp(self):
        with self.assertRaisesRegex(ValueError, "Manager compiled source/version stamp mismatch"):
            self.inspect(wrong_source_stamp=True)

    def test_reject_wrong_startup_entrypoint_on_each_role(self):
        for role in subject.ROLES:
            with self.subTest(role=role):
                with self.assertRaisesRegex(ValueError, "Entrypoint/Cmd mismatch"):
                    self.inspect(role, wrong_entrypoint=True)

    def test_reject_wrong_startup_command_on_each_role(self):
        for role in subject.ROLES:
            with self.subTest(role=role):
                with self.assertRaisesRegex(ValueError, "Entrypoint/Cmd mismatch"):
                    self.inspect(role, wrong_cmd=True)

    def test_reject_runtime_bootstrap_script_source_mismatch(self):
        with self.assertRaisesRegex(ValueError, "Runtime entrypoint differs from locked source"):
            self.inspect("runtime",
                         runtime_script=MOCK_RUNTIME_ENTRYPOINT + b"echo tampered\\n")

    def test_reject_manager_source_commit_with_correct_prefix(self):
        with self.assertRaisesRegex(ValueError, "Manager compiled source/version stamp mismatch"):
            self.inspect(wrong_sha_suffix=True)

    def test_reject_manager_version_with_correct_prefix(self):
        with self.assertRaisesRegex(ValueError, "Manager compiled source/version stamp mismatch"):
            self.inspect(wrong_version_suffix=True)

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



class CandidateBuilderSnapshotContracts(unittest.TestCase):
    def test_all_three_roles_use_one_locked_git_archive_even_if_worktree_changes(self):
        # A fake buildx inspects the actual --file / build-context arguments.
        # It mutates the original worktree after Manager has built. If Ingress
        # or Runtime reads from that worktree, the test fails deterministically.
        with tempfile.TemporaryDirectory(prefix="cpamp-r1-snapshot-test-") as temp:
            base = Path(temp)
            source = base / "source"
            source.mkdir()
            for name, content in {
                "Dockerfile.manager-server": b"FROM scratch\n",
                "Dockerfile.ingress": b"FROM scratch\n",
                "Dockerfile.runtime": b"FROM scratch\n",
                "source.txt": b"original source bytes\n",
            }.items():
                (source / name).write_bytes(content)
            build_script = VERIFIER.parent / "build-embedded-candidate.sh"
            script_in_source = source / "bin/release/build-embedded-candidate.sh"
            script_in_source.parent.mkdir(parents=True)
            script_in_source.write_bytes(build_script.read_bytes())
            verifier = source / "bin/release/verify-embedded-candidate.py"
            verifier.write_text("""\
import json
import pathlib
import sys
a = sys.argv
root = pathlib.Path(a[a.index("--source-root") + 1])
assert (root / "source.txt").read_text() == "original source bytes\\n"
for role in ("manager", "ingress", "runtime"):
    archive = pathlib.Path(a[a.index("--" + role) + 1])
    assert archive.read_text() == "fake OCI " + role
out = pathlib.Path(a[a.index("--out") + 1])
out.write_text(json.dumps({"sourceRoot": str(root), "testOnly": True}))
""", encoding="utf-8")
            subprocess.run(["git", "init", "-q"], cwd=source, check=True)
            subprocess.run(["git", "add", "."], cwd=source, check=True)
            subprocess.run(["git", "-c", "user.name=CI Test",
                            "-c", "user.email=ci@example.invalid", "commit", "-qm",
                            "snapshot fixture"], cwd=source, check=True)
            pinned_commit = subprocess.check_output(
                ["git", "rev-parse", "HEAD"], cwd=source, text=True).strip()

            fakebin = base / "fakebin"
            fakebin.mkdir()
            docker = fakebin / "docker"
            docker.write_text("""\
#!/usr/bin/env python3
import json, os, pathlib, sys
args = sys.argv[1:]
if args == ["buildx", "version"]:
    print("buildx mocked")
    sys.exit(0)
assert args[:2] == ["buildx", "build"], args
context = pathlib.Path(args[-1])
dockerfile = pathlib.Path(args[args.index("--file") + 1])
role = [item.split("=", 1)[1] for item in args
        if item.startswith("io.cpamp.candidate.role=")][0]
event = [item.split("=", 1)[1] for item in args
         if item.startswith("io.cpamp.candidate.event=")][0]
commit = [item.split("=", 1)[1] for item in args
          if item.startswith("org.opencontainers.image.revision=")][0]
source = pathlib.Path(os.environ["FAKE_SOURCE_DIR"])
assert context.resolve() != source.resolve()
assert dockerfile.parent == context
assert dockerfile.name == {
    "manager": "Dockerfile.manager-server",
    "ingress": "Dockerfile.ingress",
    "runtime": "Dockerfile.runtime",
}[role]
assert (context / "source.txt").read_text() == "original source bytes\\n"
assert (context / "source.txt").stat().st_mode & 0o222 == 0
assert commit == os.environ["FAKE_COMMIT"]
target = [item.removeprefix("type=oci,dest=") for item in args
          if item.startswith("type=oci,dest=")][0]
pathlib.Path(target).write_text("fake OCI " + role)
with open(os.environ["FAKE_RECORD"], "a", encoding="utf-8") as log:
    log.write(json.dumps({"role": role, "context": str(context),
                          "event": event, "commit": commit}) + "\\n")
if role == "manager":
    (source / "source.txt").write_text("MUTATED AFTER MANAGER BUILD\\n")
""", encoding="utf-8")
            docker.chmod(0o755)
            output = base / "candidate-output"
            record = base / "docker-record.jsonl"
            env = dict(os.environ)
            env["PATH"] = str(fakebin) + os.pathsep + env.get("PATH", "")
            env["FAKE_SOURCE_DIR"] = str(source)
            env["FAKE_COMMIT"] = pinned_commit
            env["FAKE_RECORD"] = str(record)
            result = subprocess.run(
                ["bash", str(script_in_source), str(output)],
                cwd=source, env=env, capture_output=True, text=True, timeout=25)
            self.assertEqual(result.returncode, 0,
                             result.stdout + "\n" + result.stderr)
            events = [json.loads(row) for row in record.read_text().splitlines()]
            self.assertEqual([row["role"] for row in events],
                             ["manager", "ingress", "runtime"])
            self.assertEqual({row["commit"] for row in events}, {pinned_commit})
            self.assertEqual(len({row["event"] for row in events}), 1)
            self.assertEqual(len({row["context"] for row in events}), 1)
            self.assertEqual((source / "source.txt").read_text(),
                             "MUTATED AFTER MANAGER BUILD\n")
            snapshot = Path(events[0]["context"])
            self.assertFalse(snapshot.exists(), "disposable source snapshot was not removed")
            self.assertTrue((output / "candidate-evidence.json").exists())


if __name__ == "__main__":
    unittest.main()
