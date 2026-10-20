#!/usr/bin/env python3
"""Read-only inventory of three separately built local CPAMP v2 Linux/amd64 images.

Only creates and removes stopped audit containers. It does not start containers or execute image entrypoints,
mount user volumes, publish ports, contact a registry, or authorize a release.
This is *not* an immutable deployment manifest or a Full Web/NOTICE attestation.
"""

import argparse
import hashlib
import json
import re
import subprocess
import sys
import tempfile
import uuid
from pathlib import Path

CPA_EXECUTABLE_SHA256 = "b682e9e42586263f476888361514f68f89ff4ebf89a5dadba394b850b797ce41"
CPA_VERSION = "8.0.13"
EXTRACT = {
    "manager": ("/usr/local/bin/cpa-manager-plus",),
    "ingress": ("/usr/local/bin/cpamp-ingress",),
    "runtime": (
        "/usr/local/bin/cpamp-runtime-supervisor",
        "/usr/local/bin/cli-proxy-api",
        "/usr/local/share/cpamp/runtime/cpa-artifact.json",
    ),
}


def command(*args):
    result = subprocess.run(args, check=True, capture_output=True, text=True)
    return result.stdout.strip()


def inspect_image(ref):
    entries = json.loads(command("docker", "image", "inspect", ref))
    if len(entries) != 1:
        raise ValueError(f"expected one image for {ref}")
    item = entries[0]
    if item.get("Os") != "linux" or item.get("Architecture") != "amd64":
        raise ValueError(f"unexpected image platform: {ref}")
    if (item.get("Config") or {}).get("Volumes"):
        raise ValueError(f"image declares volumes; do not allocate anonymous audit volumes: {ref}")
    image_id = item.get("Id", "")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", image_id):
        raise ValueError(f"invalid image config ID: {ref}")
    return {"inputRef": ref, "imageConfigId": image_id,
            "repoDigestsObserved": sorted(item.get("RepoDigests") or [])}


def extract_file(container, path, destination):
    command("docker", "cp", f"{container}:{path}", str(destination))
    if destination.is_symlink() or not destination.is_file():
        raise ValueError(f"missing regular file {path}")
    return hashlib.sha256(destination.read_bytes()).hexdigest()


def check_artifact(content):
    value = json.loads(content)
    if (type(value) is not dict or type(value.get("schemaVersion")) is not int or
            value.get("schemaVersion") != 1 or
            value.get("engine") != "cpa" or value.get("version") != CPA_VERSION or
            value.get("artifactId") != "sha256:" + CPA_EXECUTABLE_SHA256):
        raise ValueError("CPA artifact manifest does not identify pinned official v8.0.13")
    return value


def audit_one(role, image, tmpdir):
    record = inspect_image(image)
    name = "cpamp-v2-readonly-" + uuid.uuid4().hex[:16]
    created = False
    try:
        # No docker start: create a stopped container to read packaged bytes.
        command("docker", "create", "--pull=never", "--name", name, "--entrypoint", "/bin/true", record["imageConfigId"])
        created = True
        files = {}
        for i, path in enumerate(EXTRACT[role]):
            destination = tmpdir / f"{role}-{i}"
            digest = extract_file(name, path, destination)
            files[path] = digest
            if path.endswith("cpa-artifact.json"):
                check_artifact(destination.read_text(encoding="utf-8"))
        record["files"] = files
        if role == "runtime" and files["/usr/local/bin/cli-proxy-api"] != CPA_EXECUTABLE_SHA256:
            raise ValueError("actual bundled CPA executable differs from pinned v8.0.13")
        return record
    finally:
        if created:
            command("docker", "rm", "-f", "-v", name)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--source-commit", required=True, help="exact checked-out product SHA (recorded, not attested)")
    parser.add_argument("--manager-image", required=True)
    parser.add_argument("--ingress-image", required=True)
    parser.add_argument("--runtime-image", required=True)
    parser.add_argument("--output", required=True, help="local JSON evidence output file")
    args = parser.parse_args()
    if not re.fullmatch(r"[0-9a-f]{40}", args.source_commit):
        parser.error("--source-commit must be an exact 40-hex SHA")
    with tempfile.TemporaryDirectory(prefix="cpamp-v2-candidate-") as temp:
        temporary = Path(temp)
        images = {
            role: audit_one(role, image, temporary)
            for role, image in (("manager", args.manager_image),
                                ("ingress", args.ingress_image),
                                ("runtime", args.runtime_image))
        }
    evidence = {
        "schemaVersion": 1,
        "result": "PARTIAL_BINARY_IDENTITY_ONLY_NOT_RELEASE_QUALIFIED",
        "sourceCommitClaim": args.source_commit,
        "platform": "linux/amd64",
        "pinnedCPAExecutableSHA256": CPA_EXECUTABLE_SHA256,
        "images": images,
        "notVerified": [
            "source-to-image build provenance or SourceCommit labels",
            "image manifest/index digest and immutable shared deployment set",
            "full-Web complete asset-set hash and Manager embedded API pairing",
            "Docker running-network topology, live-key authentication, readiness, rollback",
            "license and third-party NOTICE bytes packaged in images",
            "registry availability, installer, supported paths, public Beta or release",
        ],
    }
    dest = Path(args.output)
    dest.parent.mkdir(parents=True, exist_ok=True)
    dest.write_text(json.dumps(evidence, ensure_ascii=False, indent=2) + "\n", encoding="utf-8")
    print(f"PARTIAL only: local image and CPA binary identity saved to {dest}; release remains STOP")


if __name__ == "__main__":
    try:
        main()
    except (ValueError, OSError, subprocess.CalledProcessError, json.JSONDecodeError) as exc:
        print(f"STOP: inventory incomplete or source mismatched: {exc}", file=sys.stderr)
        sys.exit(1)
