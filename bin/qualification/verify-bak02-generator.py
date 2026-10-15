#!/usr/bin/env python3
"""Verify the original BAK-02 generator Git object against its runtime inventory."""
import argparse
import hashlib
import io
import json
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parents[2]
EVIDENCE = ROOT / "tests/fixtures/phase4a-evidence/bak-02"
REF = "refs/heads/qualification/bak02-generator-af53fed"


def git(*args, **kwargs):
    return subprocess.check_output(["git", *args], cwd=ROOT, **kwargs)


def verify(ref):
    runner = json.loads((EVIDENCE / "generation-runner.json").read_text())
    generation = json.loads((EVIDENCE / "source-generation/record.json").read_text())
    provenance = json.loads((EVIDENCE / "source-provenance.json").read_text())
    commit = provenance["generatorCodeCommit"]
    if (git("rev-parse", ref + "^{commit}").decode().strip() != commit
            or runner["codeCommit"] != commit or generation["GeneratorCodeCommit"] != commit):
        raise ValueError("generator ref/runner/generation commit mismatch")
    raw = git("cat-file", "commit", commit)
    if hashlib.sha1(b"commit " + str(len(raw)).encode() + b"\0" + raw).hexdigest() != commit:
        raise ValueError("generator commit object identity mismatch")
    files = runner["sourceFiles"]
    if not files or any("\n" in name or "\r" in name for name in files):
        raise ValueError("invalid generator source inventory")
    requests = "".join(f"{commit}:{name}\n" for name in files).encode()
    objects = io.BytesIO(git("cat-file", "--batch", input=requests))
    for name, expected in files.items():
        header = objects.readline().split()
        if len(header) != 3 or header[1] != b"blob":
            raise ValueError(f"missing generator source blob: {name}")
        data = objects.read(int(header[2]))
        if objects.read(1) != b"\n" or hashlib.sha256(data).hexdigest() != expected:
            raise ValueError(f"generator source hash mismatch: {name}")
    if objects.read():
        raise ValueError("unexpected generator object output")
    record_hash = hashlib.sha256((EVIDENCE / "source-generation/record.json").read_bytes()).hexdigest()
    if (record_hash != runner["generationRecordSHA256"]
            or record_hash != provenance["sourceGenerationRecordSHA256"]
            or generation["GeneratedBundleSHA256"] != provenance["sourceBundleDigest"]):
        raise ValueError("generator execution/record/bundle binding mismatch")
    return {
        "schemaVersion": 1, "caseID": "BAK-02", "generatorCodeCommit": commit,
        "provenanceRef": REF,
        "commitURL": f"https://github.com/seakee/CPA-Manager-Plus/commit/{commit}",
        "tree": git("rev-parse", commit + "^{tree}").decode().strip(),
        "commitObjectSHA256": hashlib.sha256(raw).hexdigest(),
        "sourceFilesCount": len(files),
        "sourceFilesInventorySHA256": hashlib.sha256(json.dumps(files, sort_keys=True, separators=(",", ":")).encode()).hexdigest(),
        "sourceFileHashesVerified": True,
        "generationRunnerSHA256": hashlib.sha256((EVIDENCE / "generation-runner.json").read_bytes()).hexdigest(),
        "generatorExecutableSHA256": runner["executableSHA256"],
        "generationRecordSHA256": record_hash,
        "sourceBundleDigest": generation["GeneratedBundleSHA256"],
    }


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--ref", required=True, help="locally fetched generator ref or commit")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    proof = verify(args.ref)
    expected = json.loads((EVIDENCE / "generator-provenance.json").read_text())
    if proof != expected:
        raise ValueError("generator proof differs from committed provenance record")
    with args.output.open("x", encoding="utf-8", newline="\n") as stream:
        stream.write(json.dumps(proof, indent=2) + "\n")
    print(f"Verified {proof['sourceFilesCount']} generator source files at {proof['generatorCodeCommit']}")
