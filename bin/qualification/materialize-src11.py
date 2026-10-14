#!/usr/bin/env python3
"""Materialize the pinned SRC-11 evidence archive locally; never download data."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import tarfile

ROOT = Path(__file__).resolve().parents[2]
PIN = ROOT / "tests/fixtures/phase4a-evidence/src-11/sealed-artifact.json"
MAX_BYTES = 128 * 1024 * 1024


def sha256(data):
    return hashlib.sha256(data).hexdigest()


def materialize(archive, output, pin=None):
    """Verify outer bytes before extraction, then check the complete inventory."""
    pin = json.loads(PIN.read_text()) if pin is None else pin
    output = Path(output)
    if os.path.lexists(output):
        raise ValueError("materialization output must be absent")
    with Path(archive).open("rb") as stream:
        data = stream.read(MAX_BYTES + 1)
    if len(data) > MAX_BYTES or len(data) != pin["archiveSize"] or sha256(data) != pin["archiveSHA256"]:
        raise ValueError("sealed evidence archive size/SHA256 mismatch")
    members = {}
    total = 0
    with tarfile.open(fileobj=io.BytesIO(data), mode="r:gz") as tar:
        for entry in tar:
            path = PurePosixPath(entry.name)
            if (not entry.isfile() or path.is_absolute() or ".." in path.parts
                    or str(path) != entry.name or "\\" in entry.name or ":" in entry.name
                    or entry.name in members or entry.size < 0):
                raise ValueError("unsafe or duplicate archive member")
            if entry.name != "packaging.json" and path.parts[0] not in ("src-11", "producer"):
                raise ValueError("unexpected archive member root")
            total += entry.size
            if total > MAX_BYTES or len(members) >= 256:
                raise ValueError("evidence archive exceeds extraction budget")
            with tar.extractfile(entry) as stream:
                members[entry.name] = stream.read(entry.size + 1)
            if len(members[entry.name]) != entry.size:
                raise ValueError("truncated archive member")
    packaging = members["packaging.json"]
    if sha256(packaging) != pin["packagingManifestSHA256"]:
        raise ValueError("packaging manifest mismatch")
    inventory = json.loads(packaging)["files"]
    if set(inventory) != set(members) - {"packaging.json"}:
        raise ValueError("packaging inventory mismatch")
    for name, entry in inventory.items():
        if len(members[name]) != entry["size"] or sha256(members[name]) != entry["sha256"]:
            raise ValueError(f"packaging member mismatch: {name}")
    for name, field in (
        ("src-11/bundle/manifest.json", "bundleManifestSHA256"),
        ("src-11/source-generation/record.json", "generationRecordSHA256"),
        ("src-11/evidence-manifest.json", "originalEvidenceManifestSHA256"),
    ):
        if sha256(members[name]) != pin[field]:
            raise ValueError(f"pinned evidence mismatch: {name}")
    # No extractall, links, platform path aliases or pre-existing output files.
    output.mkdir(parents=True, mode=0o700)
    try:
        for name, data in members.items():
            target = output / name
            target.parent.mkdir(parents=True, exist_ok=True, mode=0o700)
            with target.open("xb") as stream:
                os.chmod(target, 0o600)
                stream.write(data)
    except BaseException:
        shutil.rmtree(output)
        raise
    return output / "src-11"


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    print(materialize(args.archive, args.output))


if __name__ == "__main__":
    main()
