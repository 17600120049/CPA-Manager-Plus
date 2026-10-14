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

ROOT = Path(__file__).resolve().parents[2]
MODULE = ROOT / "apps/manager-server"
PKG = "github.com/seakee/cpa-manager-plus/apps/manager-server/tests/qualification/src11"
PROFILE = ROOT / "tests/fixtures/phase4a-src11/profile.json"
IMAGE = "debian@sha256:3783cc01769c7b2b1b83a5c5ad96c815348e28ed7da68e2e3687004faa906251"


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
    parser.add_argument("--output", type=Path, required=True)
    args = parser.parse_args()
    archive = args.archive.resolve(strict=True)
    output = args.output.resolve()
    if output.exists():
        parser.error("output must be absent (never overwrite evidence)")
    tracked_scope = ["apps/manager-server", "bin/qualification/run-src11.py",
                     "tests/fixtures/phase4a-src11/profile.json"]
    dirty = capture(["git", "status", "--porcelain", "--untracked-files=all", "--", *tracked_scope], cwd=ROOT)
    if dirty:
        parser.error("commit all harness, profile and Manager source first; uncommitted code cannot claim GeneratorCodeCommit")
    commit = capture(["git", "rev-parse", "HEAD"], cwd=ROOT)
    files = capture(["git", "ls-files", "--", *tracked_scope], cwd=ROOT).splitlines()
    source_files = {name: digest(ROOT / name) for name in files}
    output.mkdir(parents=True, mode=0o700)
    env = {k: v for k, v in os.environ.items() if not k.startswith(("CPA_", "CPAMP_", "USAGE_"))}
    env.update(GOWORK="off", GOFLAGS="-mod=readonly", CGO_ENABLED="0")
    if args.action == "generate":
        if not args.release_info:
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
        build["isolation"] = {"image": IMAGE, "imageID": image_id, "network": "none",
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
        if not args.evidence:
            parser.error("replay requires --evidence")
        env.update(CPAMP_SRC11_EVIDENCE=str(args.evidence.resolve(strict=True)),
                   CPAMP_SRC11_ARCHIVE=str(archive), CPAMP_SRC11_PROFILE=str(PROFILE),
                   CPAMP_SRC11_REPLAY_OUTPUT=str(output / "runtime"))
        command = [str(binary), "-test.v", "-test.run", "^TestReplayExactSource$", "-test.timeout", "4m"]
        run(command, output / "replay-execution.log", env=env)
        build["executionCommand"] = command
        build["resultSHA256"] = digest(output / "runtime/result.json")
    build["executionExitCode"] = 0
    build["UpgradeAuthorized"] = False
    build["trustedHopReceipt"] = False
    # Build/execution logs and external runner observations are independent of
    # the test's generation JSON; a verifier checks both or regenerates.
    build["logs"] = {p.name: digest(p) for p in output.glob("*.log")}
    (output / "runner-evidence.json").write_text(json.dumps(build, indent=2) + "\n", encoding="utf-8")
    print(output)


if __name__ == "__main__":
    main()
