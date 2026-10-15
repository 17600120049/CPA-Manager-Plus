#!/usr/bin/env python3
"""Materialize pinned SRC-11/RET-01 qualification bytes locally; never download."""
import argparse
import hashlib
import io
import json
import os
from pathlib import Path, PurePosixPath
import shutil
import stat
import tarfile
import zipfile

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
    write_members(output, members)
    return output / "src-11"


def write_members(output, members):
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


def validate_ret01_release(metadata, pin, tag_metadata=None):
    return validate_retention_release(metadata, pin, "ret-01", tag_metadata)


def validate_retention_release(metadata, pin, case, tag_metadata=None):
    """Check durable hosting identity, not production admission or acceptance."""
    if (metadata.get("id") != pin["releaseID"] or metadata.get("tag_name") != pin["tag"]
            or metadata.get("target_commitish") != pin.get("releaseTargetCommitish", pin["tagCommit"])
            or metadata.get("draft") is not False or metadata.get("prerelease") is not True
            or metadata.get("immutable") is not True):
        raise ValueError(f"{case.upper()} immutable qualification release mismatch")
    assets = metadata.get("assets", [])
    if len(assets) != 1:
        raise ValueError(f"{case.upper()} release asset inventory mismatch")
    asset = assets[0]
    if (asset.get("id") != pin["assetID"] or asset.get("name") != pin["archiveName"]
            or asset.get("size") != pin["archiveSize"] or asset.get("state") != "uploaded"
            or asset.get("digest") != "sha256:" + pin["archiveSHA256"]):
        raise ValueError(f"{case.upper()} release asset identity mismatch")
    if "releaseTargetCommitish" in pin:
        if (not tag_metadata or tag_metadata.get("ref") != "refs/tags/" + pin["tag"]
                or tag_metadata.get("object", {}).get("type") != "commit"
                or tag_metadata["object"].get("sha") != pin["tagCommit"]):
            raise ValueError("qualification tag commit mismatch")


def materialize_ret01(archive, output, pin=None):
    return materialize_case(archive, output, "ret-01", pin)


def materialize_retention(archive, output, case, pin=None):
    """Authenticate the original ZIP, then reuse the existing safe writer."""
    if case not in ("ret-01", "ret-02"):
        raise ValueError("unknown retention case")
    return materialize_case(archive, output, case, pin)


def materialize_case(archive, output, case, pin=None):
    """Authenticate one case's original ZIP against its independent pins."""
    if case not in ("ret-01", "ret-02", "mig-01"):
        raise ValueError("unknown qualification case")
    if pin is None:
        provenance = ROOT / f"tests/fixtures/phase4a-evidence/{case}/source-provenance.json"
        pin = json.loads(provenance.read_text())["sourceArtifact"]
    output = Path(output)
    if os.path.lexists(output):
        raise ValueError("materialization output must be absent")
    with Path(archive).open("rb") as stream:
        data = stream.read(MAX_BYTES + 1)
    if len(data) > MAX_BYTES or len(data) != pin["archiveSize"] or sha256(data) != pin["archiveSHA256"]:
        raise ValueError("sealed evidence archive size/SHA256 mismatch")
    members = {}
    total = 0
    with zipfile.ZipFile(io.BytesIO(data)) as zipped:
        for entry in zipped.infolist():
            # ZipInfo normalizes backslashes on Windows (and truncates NULs).
            # Reject aliases using the raw member name before any filesystem use.
            name = entry.orig_filename
            path = PurePosixPath(name)
            mode = entry.external_attr >> 16
            if (entry.filename != name or entry.is_dir() or path.is_absolute() or ".." in path.parts
                    or str(path) != name or "\\" in name or ":" in name
                    or name in members or entry.file_size < 0
                    or stat.S_IFMT(mode) not in (0, stat.S_IFREG)):
                raise ValueError("unsafe or duplicate archive member")
            total += entry.file_size
            if total > MAX_BYTES or len(members) >= 256:
                raise ValueError("evidence archive exceeds extraction budget")
            with zipped.open(entry) as stream:
                members[name] = stream.read(entry.file_size + 1)
            if len(members[name]) != entry.file_size:
                raise ValueError("truncated archive member")
    inventory = pin["inventory"]
    if set(members) != set(inventory):
        raise ValueError(f"{case.upper()} source inventory mismatch")
    for name, entry in inventory.items():
        if len(members[name]) != entry["size"] or sha256(members[name]) != entry["sha256"]:
            raise ValueError(f"{case.upper()} source member mismatch: {name}")
    for name, field in ((f"{case}/source-generation/record.json", "generationRecordSHA256"),
                        (f"{case}/bundle/manifest.json", "bundleManifestSHA256")):
        if sha256(members[name]) != pin[field]:
            raise ValueError(f"{case.upper()} pinned evidence mismatch: {name}")
    record = json.loads(members[f"{case}/source-generation/record.json"])
    bundle = json.loads(members[f"{case}/bundle/manifest.json"])
    if (record["caseID"] != case.upper() or record["GeneratedBundleSHA256"] != pin["sourceBundleDigest"]
            or bundle["sourceRevision"] != pin["sourceBundleDigest"]):
        raise ValueError(f"{case.upper()} source bundle binding mismatch")
    write_members(output, members)
    return output / case


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--case", choices=["src-11", "ret-01", "ret-02", "mig-01"], default="src-11")
    parser.add_argument("--release-metadata", type=Path, help="optional local qualification Release API response")
    parser.add_argument("--tag-metadata", type=Path, help="local Git ref API response when release target differs from the pinned tag commit")
    parser.add_argument("--archive", required=True, type=Path)
    parser.add_argument("--output", required=True, type=Path)
    args = parser.parse_args()
    if args.case in ("ret-01", "ret-02", "mig-01"):
        if args.release_metadata:
            pin = json.loads((ROOT / f"tests/fixtures/phase4a-evidence/{args.case}/source-provenance.json").read_text())["sourceArtifact"]
            validate_retention_release(json.loads(args.release_metadata.read_text()), pin, args.case,
                                       json.loads(args.tag_metadata.read_text()) if args.tag_metadata else None)
        print(materialize_case(args.archive, args.output, args.case))
    else:
        if args.release_metadata:
            parser.error("--release-metadata is only applicable to qualification ZIP cases")
        print(materialize(args.archive, args.output))


if __name__ == "__main__":
    main()
