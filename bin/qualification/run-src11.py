#!/usr/bin/env python3
"""Opt-in SRC-11 qualification; no production admission or upgrade interface."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import platform
import subprocess
import tempfile
import importlib.util
import sys

ROOT = Path(__file__).resolve().parents[2]
MODULE = ROOT / "apps/manager-server"
PKG = "github.com/seakee/cpa-manager-plus/apps/manager-server/tests/qualification/src11"
PROFILE = ROOT / "tests/fixtures/phase4a-src11/profile.json"
# The parent index is pinned by the qualification profile; use its AMD64 child
# manifest so Docker Desktop cannot resolve the same index to a local ARM64 tag.
IMAGE_INDEX = "sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251"
IMAGE = "debian@sha256:f3034a6ec3c1205360777c4aae76234998866ad18806ae62b63a3f84ccad782b"


def capture(args, **kwargs):
    return subprocess.check_output(args, text=True, **kwargs).strip()


def digest(path):
    h = hashlib.sha256()
    with open(path, "rb") as stream:
        for block in iter(lambda: stream.read(1024 * 1024), b""):
            h.update(block)
    return h.hexdigest()


def run(args, log, **kwargs):
    with open(log, "x", encoding="utf-8") as stream:
        result = subprocess.run(args, stdout=stream, stderr=subprocess.STDOUT, **kwargs)
    if result.returncode:
        raise RuntimeError(f"qualification failed ({result.returncode}); inspect {log}")


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("action", choices=["generate", "replay"])
    parser.add_argument("--archive", type=Path, required=True)
    parser.add_argument("--release-info", type=Path)
    parser.add_argument("--evidence", type=Path)
    parser.add_argument("--evidence-archive", type=Path,
                        help="local pinned sealed evidence archive; verified before replay")
    parser.add_argument("--linux", action="store_true",
                        help="replay in the pinned isolated Linux AMD64 container")
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    archive = args.archive.resolve(strict=True)
    if args.action == "replay" and bool(args.evidence) == bool(args.evidence_archive):
        parser.error("replay requires exactly one of --evidence or --evidence-archive")
    if args.action == "generate" and (args.evidence_archive or args.linux):
        parser.error("--evidence-archive and --linux are replay options")
    output = args.output.resolve()
    if output.exists():
        parser.error("output must be absent (never overwrite evidence)")
    tracked_scope = ["apps/manager-server", "bin/qualification",
                     "tests/fixtures/phase4a-src11/profile.json",
                     "tests/fixtures/phase4a-evidence/src-11/sealed-artifact.json"]
    dirty = capture(["git", "status", "--porcelain", "--untracked-files=all", "--", *tracked_scope], cwd=ROOT)
    if dirty:
        parser.error("commit all harness, profile and Manager source first; uncommitted code cannot claim GeneratorCodeCommit")
    commit = capture(["git", "rev-parse", "HEAD"], cwd=ROOT)
    files = capture(["git", "ls-files", "--", *tracked_scope], cwd=ROOT).splitlines()
    source_files = {name: digest(ROOT / name) for name in files}
    output.mkdir(parents=True, mode=0o700)
    evidence = args.evidence.resolve(strict=True) if args.evidence else None
    if args.evidence_archive:
        sys.dont_write_bytecode = True
        spec = importlib.util.spec_from_file_location("materialize_src11", ROOT / "bin/qualification/materialize-src11.py")
        module = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(module)
        evidence = module.materialize(args.evidence_archive, output / "materialized")
    env = {k: v for k, v in os.environ.items() if not k.startswith(("CPA_", "CPAMP_", "USAGE_"))}
    env.update(GOWORK="off", GOFLAGS="-mod=readonly", CGO_ENABLED="0")
    if args.action == "generate" or args.linux:
        if args.action == "generate" and not args.release_info:
            parser.error("generate requires --release-info")
        env.update(GOOS="linux", GOARCH="amd64")
    binary = output / ("qualification.exe" if args.action == "replay" and os.name == "nt" else "qualification")
    command = ["go", "test", "-c", "-trimpath", "-ldflags", f"-X {PKG}.generatorCommit={commit}", "-o", str(binary), "./tests/qualification/src11"]
    run(command, output / "build.log", cwd=MODULE, env=env)
    build = {"codeCommit": commit, "sourceFiles": source_files,
             "command": command, "goVersion": capture(["go", "version"], env=env),
             "moduleGraph": capture(["go", "list", "-m", "all"], cwd=MODULE, env=env),
             "executableSHA256": digest(binary), "profileSHA256": digest(PROFILE),
             "archiveSHA256": digest(archive), "host": platform.platform()}
    if args.action == "generate":
        source = output / "src-11"
        release_info = args.release_info.resolve(strict=True)
        image_id = capture(["docker", "image", "inspect", IMAGE, "--format", "{{.Id}}"])
        build["isolation"] = {"image": IMAGE, "parentIndex": IMAGE_INDEX, "imageID": image_id, "network": "none",
                              "readOnlyRoot": True, "capabilities": "none",
                              "mounts": "runner/archive/profile read-only; unique output writable; private tmpfs"}
        docker = ["docker", "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--read-only",
                  "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,exec,mode=1777",
                  "--mount", f"type=bind,src={binary},dst=/qualification,readonly",
                  "--mount", f"type=bind,src={archive},dst=/archive,readonly",
                  "--mount", f"type=bind,src={release_info},dst=/release-info,readonly",
                  "--mount", f"type=bind,src={PROFILE},dst=/profile,readonly",
                  "--mount", f"type=bind,src={output},dst=/output",
                  "--env", "CPAMP_SRC11_GENERATE=1", "--env", "CPAMP_SRC11_OUTPUT=/output/src-11",
                  "--env", "CPAMP_SRC11_ARCHIVE=/archive", "--env", "CPAMP_SRC11_RELEASE_INFO=/release-info",
                  "--env", "CPAMP_SRC11_PROFILE=/profile", IMAGE,
                  "/qualification", "-test.v", "-test.run", "^TestGenerateExactSource$", "-test.timeout", "4m"]
        run(docker, output / "generation-execution.log")
        build["executionCommand"] = docker
        build["generationRecordSHA256"] = digest(source / "source-generation/record.json")
    else:
        env.update(CPAMP_SRC11_EVIDENCE=str(evidence),
                   CPAMP_SRC11_ARCHIVE=str(archive), CPAMP_SRC11_PROFILE=str(PROFILE),
                   CPAMP_SRC11_REPLAY_OUTPUT=str(output / "runtime"))
        command = [str(binary), "-test.v", "-test.run", "^TestReplayExactSource$", "-test.timeout", "4m"]
        if args.linux:
            command = ["docker", "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--read-only",
                       "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,exec,mode=1777",
                       "--mount", f"type=bind,src={binary},dst=/qualification,readonly",
                       "--mount", f"type=bind,src={archive},dst=/archive,readonly",
                       "--mount", f"type=bind,src={evidence},dst=/evidence,readonly",
                       "--mount", f"type=bind,src={PROFILE},dst=/profile,readonly",
                       "--mount", f"type=bind,src={output},dst=/output",
                       "--env", "CPAMP_SRC11_EVIDENCE=/evidence", "--env", "CPAMP_SRC11_ARCHIVE=/archive",
                       "--env", "CPAMP_SRC11_PROFILE=/profile", "--env", "CPAMP_SRC11_REPLAY_OUTPUT=/output/runtime",
                       IMAGE, "/qualification", "-test.v", "-test.run", "^TestReplayExactSource$", "-test.timeout", "4m"]
            build["isolation"] = {"image": IMAGE, "parentIndex": IMAGE_INDEX, "network": "none", "readOnlyRoot": True,
                                  "imageID": capture(["docker", "image", "inspect", IMAGE, "--format", "{{.Id}}"]),
                                  "capabilities": "none", "evidenceMount": "read-only"}
        if args.evidence_archive:
            build["sealedEvidenceArchiveSHA256"] = digest(args.evidence_archive)
            build["sealedArtifactPinSHA256"] = digest(module.PIN)
        run(command, output / "replay-execution.log", env=env)
        build["executionCommand"] = command
        build["resultSHA256"] = digest(output / "runtime/result.json")
    build["executionExitCode"] = 0
    build["UpgradeAuthorized"] = False
    build["trustedHopReceipt"] = False
    # Build/execution logs and external runner observations are independent of
    # the test's generation JSON. Regeneration qualifies equivalent evidence;
    # the pinned sealed archive authenticates the original bytes.
    build["logs"] = {p.name: digest(p) for p in output.glob("*.log")}
    (output / "runner-evidence.json").write_text(json.dumps(build, indent=2) + "\n", encoding="utf-8")
    print(output)


if __name__ == "__main__":
    main()
