#!/usr/bin/env python3
"""Verify a disposable, local-only OCI Embedded trio. Never publishes or signs a release.

Input archives must be produced by one explicitly identified candidate build event.
A self-asserted build event is NOT protected-producer provenance.
"""
import argparse
import hashlib
import io
import json
import re
import sys
import tarfile
from pathlib import Path, PurePosixPath

ROLES = ("manager", "ingress", "runtime")
SHA = re.compile(r"^sha256:[0-9a-f]{64}$")
SOURCE = "https://github.com/seakee/CPA-Manager-Plus"
CPA_SHA = "b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41"
CPA_LICENSE_SHA = "879792e89cf1bdd6a8d446033ec87e30496f97dcafc4656dc53f641509b346a6"
CPA_ARCHIVE_SHA = "50ecffb47fdd81c8c5a9825a73a7a905ab66342337e274f39c4276b92d3533f3"
LICENSE = "/usr/local/share/licenses/cpamp/LICENSE"
NOTICE = "/usr/local/share/licenses/cpamp/THIRD_PARTY_NOTICES.txt"
WEB_MANIFEST = "/usr/local/share/cpamp/release/fullweb-manifest.json"
WANTED = {
    "manager": ("/usr/local/bin/cpa-manager-plus", LICENSE, NOTICE, WEB_MANIFEST),
    "ingress": ("/usr/local/bin/cpamp-ingress", LICENSE, NOTICE),
    "runtime": (
        "/usr/local/bin/cpamp-runtime-supervisor",
        "/usr/local/bin/cli-proxy-api",
        "/usr/local/share/cpamp/runtime/cpa-artifact.json",
        "/usr/local/share/cpamp/runtime/config.seed.yaml",
        "/usr/local/bin/cpamp-runtime-v8-entrypoint",
        "/usr/local/share/licenses/cli-proxy-api/LICENSE",
        LICENSE, NOTICE,
    ),
}
MAX_BLOB = 512 * 1024 * 1024
MAX_FILE = 128 * 1024 * 1024


def digest(data):
    return "sha256:" + hashlib.sha256(data).hexdigest()


def require(condition, message):
    if not condition:
        raise ValueError(message)


def json_object(data, label):
    result = json.loads(data)
    require(type(result) is dict, "expected JSON object: " + label)
    return result


def read_member(archive, name, limit=MAX_BLOB):
    item = archive.getmember(name)
    require(item.isfile() and item.size <= limit, "invalid archive member " + name)
    return archive.extractfile(item).read()


def read_blob(archive, identifier):
    require(isinstance(identifier, str) and SHA.fullmatch(identifier), "invalid OCI digest")
    data = read_member(archive, "blobs/sha256/" + identifier[7:])
    require(digest(data) == identifier, "OCI blob digest mismatch: " + identifier)
    return data


def canonical_path(value):
    require(isinstance(value, str) and not value.startswith("/"), "unsafe layer entry")
    # OCI tar paths are relative to root; do not accept parent traversal.
    parts = PurePosixPath(value).parts
    require(".." not in parts, "unsafe layer traversal")
    while parts and parts[0] == ".":
        parts = parts[1:]
    return "/" + "/".join(parts)


def final_files(archive, layers, wanted):
    result = {}
    for descriptor in layers:
        data = read_blob(archive, descriptor.get("digest"))
        with tarfile.open(fileobj=io.BytesIO(data), mode="r:*") as layer:
            for member in layer:
                name = canonical_path(member.name)
                base = PurePosixPath(name).name
                parent = str(PurePosixPath(name).parent)
                if base == ".wh..wh..opq":
                    for target in list(result):
                        if str(PurePosixPath(target).parent) == parent or target.startswith(parent + "/"):
                            del result[target]
                    continue
                if base.startswith(".wh."):
                    result.pop(parent + "/" + base[4:], None)
                    continue
                if name not in wanted:
                    continue
                require(member.isfile() and member.size <= MAX_FILE,
                        "missing regular file in final image: " + name)
                stream = layer.extractfile(member)
                require(stream is not None, "unreadable final image file: " + name)
                result[name] = stream.read()
    require(set(result) == set(wanted), "missing packaged files: " +
            ", ".join(sorted(set(wanted) - set(result))))
    return result


def inspect(path, role, source_sha, event, root_license, seed):
    require(path.is_file(), "missing OCI archive: " + str(path))
    with tarfile.open(path, mode="r:*") as archive:
        layout = json_object(read_member(archive, "oci-layout", 4096), "oci-layout")
        require(layout.get("imageLayoutVersion") == "1.0.0", "not an OCI layout")
        raw_index = read_member(archive, "index.json", 1024 * 1024)
        index = json_object(raw_index, "index")
        require(index.get("schemaVersion") == 2, "invalid OCI index schema")
        descriptors = index.get("manifests")
        require(isinstance(descriptors, list), "missing OCI index descriptors")
        matches = [m for m in descriptors if isinstance(m, dict) and
                   m.get("platform") == {"architecture": "amd64", "os": "linux"}]
        # Reject extra image-platform entries (and all alternate-arch releases).
        require(len(matches) == 1 and len(descriptors) == 1,
                "candidate must contain exactly one linux/amd64 manifest")
        descriptor = matches[0]
        manifest_id = descriptor.get("digest")
        manifest = json_object(read_blob(archive, manifest_id), "manifest")
        require(manifest.get("schemaVersion") == 2, "invalid OCI manifest")
        config_id = manifest.get("config", {}).get("digest")
        config = json_object(read_blob(archive, config_id), "config")
        require(config.get("os") == "linux" and config.get("architecture") == "amd64",
                "image config architecture mismatch")
        labels = (config.get("config") or {}).get("Labels") or {}
        require(labels.get("org.opencontainers.image.revision") == source_sha,
                "source identity label mismatch")
        require(labels.get("org.opencontainers.image.source") == SOURCE,
                "source repository mismatch")
        require(labels.get("io.cpamp.candidate.event") == event and
                labels.get("io.cpamp.candidate.role") == role,
                "candidate build event or role mismatch")
        layers = manifest.get("layers")
        require(isinstance(layers, list) and len(layers) > 0, "missing image layers")
        files = final_files(archive, layers, WANTED[role])
        require(files[LICENSE] == root_license, "CPAMP LICENSE differs from source")
        require(files[NOTICE].strip(), "empty third-party notice inventory")
        for path_ in WANTED[role]:
            if path_.startswith("/usr/local/bin/") and path_ != "/usr/local/bin/cpamp-runtime-v8-entrypoint":
                binary = files[path_]
                require(binary[:4] == b"\\x7fELF" and int.from_bytes(binary[18:20], "little") == 62,
                        "binary is not linux/amd64 ELF: " + path_)
        if role == "runtime":
            require(hashlib.sha256(files["/usr/local/bin/cli-proxy-api"]).hexdigest() == CPA_SHA,
                    "official CPA v8.0.13 executable hash mismatch")
            manifest_file = json_object(files["/usr/local/share/cpamp/runtime/cpa-artifact.json"],
                                        "CPA artifact")
            require(manifest_file == {"schemaVersion": 1, "engine": "cpa",
                                     "version": "8.0.13", "artifactId": "sha256:" + CPA_SHA},
                    "CPA artifact identity mismatch")
            require(hashlib.sha256(files["/usr/local/share/licenses/cli-proxy-api/LICENSE"]).hexdigest()
                    == CPA_LICENSE_SHA, "official CPA license mismatch")
            require(files["/usr/local/share/cpamp/runtime/config.seed.yaml"] == seed,
                    "official v8 default seed mismatch")
        if role == "manager":
            fullweb = json_object(files[WEB_MANIFEST], "Full Web file-set manifest")
            require(fullweb.get("schemaVersion") == 1 and
                    isinstance(fullweb.get("files"), list) and len(fullweb["files"]) > 0,
                    "missing complete Full Web evidence")
            names = [item.get("path") for item in fullweb["files"] if isinstance(item, dict)]
            require(len(names) == len(fullweb["files"]) and len(set(names)) == len(names) and
                    any(n == "index.html" for n in names) and
                    all(isinstance(item.get("sha256"), str) and SHA.fullmatch(item["sha256"])
                        for item in fullweb["files"]),
                    "invalid Full Web file-set evidence")
        return {
            "archive": path.name,
            "ociLayoutIndexDigest": digest(raw_index),
            "platformManifestDigest": manifest_id,
            "imageConfigDigest": config_id,
            "platform": "linux/amd64",
            "packagedFiles": {key: digest(value) for key, value in sorted(files.items())},
        }


def verify(paths, source_sha, event, root):
    require(re.fullmatch(r"[0-9a-f]{40}", source_sha) is not None, "invalid source SHA")
    require(re.fullmatch(r"[A-Za-z0-9._-]{8,128}", event) is not None, "invalid build event")
    license_bytes = (root / "LICENSE").read_bytes()
    seed = (root / "docker/runtime/config.v8.default.seed.yaml").read_bytes()
    result = {role: inspect(paths[role], role, source_sha, event, license_bytes, seed)
              for role in ROLES}
    require(len({result[role]["platformManifestDigest"] for role in ROLES}) == 3,
            "three different image manifests required")
    return {
        "schemaVersion": 1,
        "status": "STRUCTURAL_CANDIDATE_EVIDENCE_ONLY_NOT_RELEASE_QUALIFIED",
        "sourceCommitClaim": source_sha,
        "candidateBuildEventClaim": event,
        "platform": "linux/amd64",
        "images": result,
        "officialCPA": {"version": "8.0.13", "archiveSHA256": CPA_ARCHIVE_SHA,
                        "executableSHA256": CPA_SHA},
        "notVerified": [
            "trusted production producer identity or independent registry readback",
            "Manager ELF source stamp and complete compiled embedded Web bytes/API compatibility",
            "third-party LICENSE/NOTICE legal sufficiency (inventory only)",
            "1.x strict consumer replay, Host, network, startup, readiness, rollback",
            "production release, beta, mutable aliases or channels",
        ],
    }


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--manager", type=Path, required=True, help="OCI archive from same local build event")
    parser.add_argument("--ingress", type=Path, required=True)
    parser.add_argument("--runtime", type=Path, required=True)
    parser.add_argument("--source-commit", required=True)
    parser.add_argument("--build-event", required=True)
    parser.add_argument("--source-root", type=Path, default=Path("."))
    parser.add_argument("--out", type=Path, required=True)
    args = parser.parse_args()
    evidence = verify({role: getattr(args, role) for role in ROLES},
                      args.source_commit, args.build_event, args.source_root)
    args.out.parent.mkdir(parents=True, exist_ok=True)
    args.out.write_text(json.dumps(evidence, indent=2, sort_keys=True) + "\n", encoding="utf-8")
    print("STRUCTURAL CANDIDATE EVIDENCE ONLY; Release/Registry/Channel STOP: " + str(args.out))


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, tarfile.TarError, json.JSONDecodeError, KeyError) as exc:
        print("STOP: invalid Embedded candidate: " + str(exc), file=sys.stderr)
        sys.exit(1)
