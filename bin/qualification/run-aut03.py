#!/usr/bin/env python3
"""Run AUT-03 from committed source against the pinned sealed SRC-11 input."""
import argparse
import importlib.util
import json
import os
from pathlib import Path
import platform
import sys

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
MODULE = ROOT / "apps/manager-server"
PKG = "github.com/seakee/cpa-manager-plus/apps/manager-server/internal/service/legacyartifact"
HARNESS = MODULE / "internal/service/legacyartifact/aut03_qualification_test.go"


def load(name, filename):
    spec = importlib.util.spec_from_file_location(name, ROOT / "bin/qualification" / filename)
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sealed-archive", type=Path, required=True)
    parser.add_argument("--output", type=Path, required=True)
    parser.add_argument("--linux", action="store_true", help="use the existing pinned Linux AMD64 container")
    args = parser.parse_args()
    shared = load("src11_runner", "run-src11.py")
    materializer = load("src11_materializer", "materialize-src11.py")
    output = args.output.resolve()
    if os.path.lexists(output):
        parser.error("output must be absent; never overwrite evidence")
    if not args.linux and (platform.system() != "Linux" or platform.machine() not in ("x86_64", "amd64")):
        parser.error("native execution requires Linux AMD64; use --linux for isolated replay")
    scope = ["apps/manager-server", "bin/qualification", ".github/workflows/qualification-aut03.yml",
             "tests/fixtures/phase4a-evidence/src-11/sealed-artifact.json"]
    dirty = shared.capture(["git", "status", "--porcelain", "--untracked-files=all", "--", *scope], cwd=ROOT)
    if dirty:
        parser.error("commit all harness, runner, workflow, pins and Manager code before qualification")
    commit = shared.capture(["git", "rev-parse", "HEAD"], cwd=ROOT)
    tree = shared.capture(["git", "rev-parse", "HEAD^{tree}"], cwd=ROOT)
    harness_digest = shared.digest(HARNESS)
    files = shared.capture(["git", "ls-files", "--", *scope], cwd=ROOT).splitlines()
    source_files = {name: shared.digest(ROOT / name) for name in files}
    archive = args.sealed_archive.resolve(strict=True)
    output.mkdir(parents=True, mode=0o700)
    evidence = materializer.materialize(archive, output / "materialized")
    producer = output / "materialized/producer/cpa-manager-plus_v1.14.4_linux_amd64.tar.gz"
    env = {k: v for k, v in os.environ.items() if not k.startswith(("CPA_", "CPAMP_", "USAGE_"))}
    env.update(GOWORK="off", GOFLAGS="-mod=readonly", CGO_ENABLED="0", GOOS="linux", GOARCH="amd64")
    binary = output / "qualification"
    flags = " ".join(f"-X {PKG}.{key}={value}" for key, value in (
        ("aut03HarnessCommit", commit), ("aut03TestedTree", tree), ("aut03HarnessSHA256", harness_digest)))
    build = ["go", "test", "-c", "-trimpath", "-ldflags", flags, "-o", str(binary), "./internal/service/legacyartifact"]
    shared.run(build, output / "build.log", cwd=MODULE, env=env)
    record = {"qualificationHarnessCommit": commit, "testedTree": tree, "harnessSHA256": harness_digest,
              "sourceFiles": source_files, "buildCommand": build,
              "goVersion": shared.capture(["go", "version"], env=env),
              "executableSHA256": shared.digest(binary), "sealedArchiveSHA256": shared.digest(archive),
              "sealedPinSHA256": shared.digest(materializer.PIN), "host": platform.platform()}
    test_args = ["-test.v", "-test.run", "^TestAUT03Qualification$", "-test.timeout", "10m"]
    env.update(CPAMP_AUT03_ARCHIVE=str(producer), CPAMP_AUT03_INPUT_BUNDLE=str(evidence / "bundle"),
               CPAMP_AUT03_EVIDENCE=str(output / "evidence.json"))
    command = [str(binary), *test_args]
    if args.linux:
        # The bundle is read-only, but its parent must permit offlineSource's
        # disposable inspection copy. Both are task-owned materializations.
        command = ["docker", "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--read-only", "--workdir", "/",
                   "--user", f"{os.getuid()}:{os.getgid()}", "--cap-drop", "ALL",
                   "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,exec,mode=1777",
                   "--mount", f"type=bind,src={output},dst=/output",
                   "--mount", f"type=bind,src={binary},dst=/qualification,readonly",
                   "--mount", f"type=bind,src={producer},dst=/archive,readonly",
                   "--mount", f"type=bind,src={evidence / 'bundle'},dst=/output/materialized/src-11/bundle,readonly",
                   "--env", "CPAMP_AUT03_ARCHIVE=/archive",
                   "--env", "CPAMP_AUT03_INPUT_BUNDLE=/output/materialized/src-11/bundle",
                   "--env", "CPAMP_AUT03_EVIDENCE=/output/evidence.json",
                   shared.IMAGE, "/qualification", *test_args]
        record["isolation"] = {"image": shared.IMAGE, "parentIndex": shared.IMAGE_INDEX,
                               "imageID": shared.capture(["docker", "image", "inspect", shared.IMAGE, "--format", "{{.Id}}"]),
                               "network": "none", "readOnlyRoot": True, "capabilities": "none", "bundleMount": "read-only"}
    shared.run(command, output / "execution.log", env=env, cwd=HARNESS.parent)
    if source_files != {name: shared.digest(ROOT / name) for name in files}:
        raise RuntimeError("qualification code changed while running; evidence cannot be accepted")
    record.update(executionCommand=command, executionExitCode=0,
                  evidenceSHA256=shared.digest(output / "evidence.json"),
                  logs={p.name: shared.digest(p) for p in output.glob("*.log")},
                  UpgradeAuthorized=False, TrustedHopReceipt=False)
    (output / "runner-evidence.json").write_text(json.dumps(record, indent=2) + "\n", encoding="utf-8")
    print(output)


if __name__ == "__main__":
    main()
