#!/usr/bin/env python3
"""Opt-in Phase4A qualification; no production admission or upgrade interface."""
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
    parser.add_argument("action", choices=["generate", "generate-ret01", "generate-ret02", "generate-ret03", "generate-ret07", "generate-mig01", "generate-mig02", "generate-mig03", "generate-bak01", "replay", "ret01", "ret02", "ret03", "ret07", "mig01", "mig02", "mig03", "bak01"])
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
    generating = args.action.startswith("generate")
    ret01 = args.action in ("generate-ret01", "ret01")
    ret02 = args.action in ("generate-ret02", "ret02")
    ret03 = args.action in ("generate-ret03", "ret03")
    ret07 = args.action in ("generate-ret07", "ret07")
    mig01 = args.action in ("generate-mig01", "mig01")
    mig02 = args.action in ("generate-mig02", "mig02")
    mig03 = args.action in ("generate-mig03", "mig03")
    bak01 = args.action in ("generate-bak01", "bak01")
    retention = "ret-07" if ret07 else "ret-03" if ret03 else "ret-02" if ret02 else "ret-01" if ret01 else None
    case_profile = "bak-01" if bak01 else "mig-03" if mig03 else "mig-02" if mig02 else "mig-01" if mig01 else retention
    profile = ROOT / f"tests/fixtures/phase4a-evidence/{case_profile}/profile.json" if case_profile else PROFILE
    if args.action == "replay" and bool(args.evidence) == bool(args.evidence_archive):
        parser.error("replay requires exactly one of --evidence or --evidence-archive")
    if args.action in ("ret01", "ret02", "ret03") and (not args.evidence or args.evidence_archive):
        parser.error("retention replay requires --evidence pointing to its own complete generated fixture")
    if args.action == "ret07" and bool(args.evidence) == bool(args.evidence_archive):
        parser.error("ret07 requires exactly one of --evidence or --evidence-archive")
    if args.action in ("mig01", "mig02", "mig03") and (not args.evidence or args.evidence_archive or args.release_info):
        parser.error("migration replay requires --evidence pointing to its own complete generated fixture")
    if args.action == "bak01" and (bool(args.evidence) == bool(args.evidence_archive) or args.release_info):
        parser.error("bak01 requires exactly one of --evidence or --evidence-archive")
    if generating and (args.evidence_archive or args.linux):
        parser.error("--evidence-archive and --linux are replay options")
    if generating and args.evidence:
        parser.error("--evidence is a replay option")
    if args.action in ("ret01", "ret02", "ret03", "ret07") and args.release_info:
        parser.error("--release-info is only valid for source generation")
    output = args.output.resolve()
    if output.exists():
        parser.error("output must be absent (never overwrite evidence)")
    tracked_scope = ["apps/manager-server", "bin/qualification",
                     "tests/fixtures/phase4a-src11/profile.json",
                     "tests/fixtures/phase4a-evidence/src-11/sealed-artifact.json",
                     "tests/fixtures/phase4a-evidence/ret-01/profile.json",
                     "tests/fixtures/phase4a-evidence/ret-02/profile.json",
                     "tests/fixtures/phase4a-evidence/ret-03/profile.json",
                     "tests/fixtures/phase4a-evidence/ret-07/profile.json",
                     "tests/fixtures/phase4a-evidence/mig-01/profile.json",
                     "tests/fixtures/phase4a-evidence/mig-02/profile.json",
                     "tests/fixtures/phase4a-evidence/mig-03/profile.json",
                     "tests/fixtures/phase4a-evidence/bak-01/profile.json"]
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
        if ret07:
            evidence = module.materialize_case(args.evidence_archive, output / "materialized", "ret-07")
        elif bak01:
            evidence = module.materialize_case(args.evidence_archive, output / "materialized", "bak-01")
        else:
            evidence = module.materialize(args.evidence_archive, output / "materialized")
    env = {k: v for k, v in os.environ.items() if not k.startswith(("CPA_", "CPAMP_", "USAGE_"))}
    env.update(GOWORK="off", GOFLAGS="-mod=readonly", CGO_ENABLED="0")
    if generating or args.linux:
        if generating and not args.release_info:
            parser.error("generate requires --release-info")
        env.update(GOOS="linux", GOARCH="amd64")
    binary = output / ("qualification.exe" if args.action == "replay" and os.name == "nt" else "qualification")
    command = ["go", "test", "-c", "-trimpath", "-ldflags", f"-X {PKG}.generatorCommit={commit}", "-o", str(binary), "./tests/qualification/src11"]
    run(command, output / "build.log", cwd=MODULE, env=env)
    build = {"codeCommit": commit, "sourceFiles": source_files,
             "command": command, "goVersion": capture(["go", "version"], env=env),
             "moduleGraph": capture(["go", "list", "-m", "all"], cwd=MODULE, env=env),
             "executableSHA256": digest(binary), "profileSHA256": digest(profile),
             "archiveSHA256": digest(archive), "host": platform.platform()}
    if generating:
        case_dir = case_profile or "src-11"
        test_name = f"^TestGenerate{case_profile.replace('-', '').upper()}$" if case_profile else "^TestGenerateExactSource$"
        source = output / case_dir
        release_info = args.release_info.resolve(strict=True)
        image_id = capture(["docker", "image", "inspect", IMAGE, "--format", "{{.Id}}"])
        build["isolation"] = {"image": IMAGE, "parentIndex": IMAGE_INDEX, "imageID": image_id, "network": "none",
                              "readOnlyRoot": True, "capabilities": "none",
                              "mounts": "runner/archive/profile read-only; unique output writable; private tmpfs"}
        docker = ["docker", "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--read-only",
                  "--user", f"{os.getuid()}:{os.getgid()}",
                  "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,exec,mode=1777",
                  "--mount", f"type=bind,src={binary},dst=/qualification,readonly",
                  "--mount", f"type=bind,src={archive},dst=/archive,readonly",
                  "--mount", f"type=bind,src={release_info},dst=/release-info,readonly",
                  "--mount", f"type=bind,src={profile},dst=/profile,readonly",
                  "--mount", f"type=bind,src={output},dst=/output",
                  "--env", "CPAMP_SRC11_GENERATE=1", "--env", f"CPAMP_SRC11_OUTPUT=/output/{case_dir}",
                  "--env", f"CPAMP_RET01_GENERATE={int(ret01)}",
                  "--env", f"CPAMP_RET02_GENERATE={int(ret02)}",
                  "--env", f"CPAMP_RET03_GENERATE={int(ret03)}",
                  "--env", f"CPAMP_RET07_GENERATE={int(ret07)}",
                  "--env", f"CPAMP_MIG01_GENERATE={int(mig01)}",
                  "--env", f"CPAMP_MIG02_GENERATE={int(mig02)}",
                  "--env", f"CPAMP_MIG03_GENERATE={int(mig03)}",
                  "--env", f"CPAMP_BAK01_GENERATE={int(bak01)}",
                  "--env", "CPAMP_SRC11_ARCHIVE=/archive", "--env", "CPAMP_SRC11_RELEASE_INFO=/release-info",
                  "--env", "CPAMP_SRC11_PROFILE=/profile", IMAGE,
                  "/qualification", "-test.v", "-test.run", test_name, "-test.timeout", "4m"]
        run(docker, output / "generation-execution.log")
        build["executionCommand"] = docker
        build["generationRecordSHA256"] = digest(source / "source-generation/record.json")
    else:
        env.update(CPAMP_SRC11_EVIDENCE=str(evidence),
                   CPAMP_SRC11_ARCHIVE=str(archive), CPAMP_SRC11_PROFILE=str(profile),
                   CPAMP_SRC11_REPLAY_OUTPUT=str(output / "runtime"))
        if retention:
            prefix = "CPAMP_" + retention.replace("-", "").upper()
            env.update({prefix + "_SOURCE_EVIDENCE": str(evidence),
                        prefix + "_ARCHIVE": str(archive),
                        prefix + "_REPLAY_OUTPUT": str(output / "runtime")})
            test_name = f"^TestReplay{retention.replace('-', '').upper()}$"
        else:
            test_name = "^TestReplayExactSource$"
        if mig01:
            env.update(CPAMP_MIG01_SOURCE=str(evidence), CPAMP_MIG01_OUTPUT=str(output / "runtime"))
            test_name = "^TestQualifyMIG01$"
        if mig02:
            env.update(CPAMP_MIG02_SOURCE=str(evidence), CPAMP_MIG02_OUTPUT=str(output / "runtime"))
            test_name = "^TestQualifyMIG02$"
        if mig03:
            env.update(CPAMP_MIG03_SOURCE=str(evidence), CPAMP_MIG03_OUTPUT=str(output / "runtime"))
            test_name = "^TestQualifyMIG03$"
        if bak01:
            env.update(CPAMP_BAK01_SOURCE=str(evidence), CPAMP_BAK01_ARCHIVE=str(archive),
                       CPAMP_BAK01_REPLAY_OUTPUT=str(output / "runtime"))
            test_name = "^TestReplayBAK01$"
        command = [str(binary), "-test.v", "-test.run", test_name, "-test.timeout", "4m"]
        if args.linux:
            command = ["docker", "run", "--rm", "--platform", "linux/amd64", "--network", "none", "--read-only",
                       "--user", f"{os.getuid()}:{os.getgid()}",
                       "--cap-drop", "ALL", "--security-opt", "no-new-privileges", "--tmpfs", "/tmp:rw,exec,mode=1777",
                       "--mount", f"type=bind,src={binary},dst=/qualification,readonly",
                       "--mount", f"type=bind,src={archive},dst=/archive,readonly",
                       "--mount", f"type=bind,src={evidence},dst=/evidence,readonly",
                       "--mount", f"type=bind,src={profile},dst=/profile,readonly",
                       "--mount", f"type=bind,src={output},dst=/output",
                       "--env", "CPAMP_SRC11_EVIDENCE=/evidence", "--env", "CPAMP_SRC11_ARCHIVE=/archive",
                       "--env", "CPAMP_SRC11_PROFILE=/profile", "--env", "CPAMP_SRC11_REPLAY_OUTPUT=/output/runtime"]
            if retention:
                command.extend(["--env", prefix + "_SOURCE_EVIDENCE=/evidence",
                                "--env", prefix + "_ARCHIVE=/archive",
                                "--env", prefix + "_REPLAY_OUTPUT=/output/runtime"])
            if mig01:
                command.extend(["--env", "CPAMP_MIG01_SOURCE=/evidence",
                                "--env", "CPAMP_MIG01_OUTPUT=/output/runtime"])
            if mig02:
                command.extend(["--env", "CPAMP_MIG02_SOURCE=/evidence",
                                "--env", "CPAMP_MIG02_OUTPUT=/output/runtime"])
            if mig03:
                command.extend(["--env", "CPAMP_MIG03_SOURCE=/evidence",
                                "--env", "CPAMP_MIG03_OUTPUT=/output/runtime"])
            if bak01:
                command.extend(["--env", "CPAMP_BAK01_SOURCE=/evidence",
                                "--env", "CPAMP_BAK01_ARCHIVE=/archive",
                                "--env", "CPAMP_BAK01_REPLAY_OUTPUT=/output/runtime"])
            command.extend([IMAGE, "/qualification", "-test.v", "-test.run", test_name, "-test.timeout", "4m"])
            build["isolation"] = {"image": IMAGE, "parentIndex": IMAGE_INDEX, "network": "none", "readOnlyRoot": True,
                                  "imageID": capture(["docker", "image", "inspect", IMAGE, "--format", "{{.Id}}"]),
                                  "capabilities": "none", "evidenceMount": "read-only",
                                  "user": f"{os.getuid()}:{os.getgid()}"}
        if args.evidence_archive:
            build["sealedEvidenceArchiveSHA256"] = digest(args.evidence_archive)
            pin_case = "ret-07" if ret07 else "bak-01" if bak01 else None
            pin_path = ROOT / f"tests/fixtures/phase4a-evidence/{pin_case}/source-provenance.json" if pin_case else module.PIN
            build["sealedArtifactPinSHA256"] = digest(pin_path)
        run(command, output / "replay-execution.log", env=env)
        build["executionCommand"] = command
        build["resultSHA256"] = digest(output / "runtime/result.json")
    build["executionExitCode"] = 0
    build["UpgradeAuthorized"] = False
    build["TrustedHopReceipt"] = False
    if retention and not generating:
        result = json.loads((output / "runtime/result.json").read_text(encoding="utf-8"))
        build.update({
            "caseID": retention.upper(),
            "runtimeEvidence": result.get("runtimeEvidence"),
            "casePass": result.get("casePass"),
            "independentAcceptance": result.get("independentAcceptance"),
            "inputBundleDigest": result.get("inputBundleDigest"),
            "beforeDigest": result.get("beforeDigest"),
            "afterDigest": result.get("afterDigest"),
            "applicableSEM": result.get("applicableSEM"),
        })
    if bak01 and not generating:
        result = json.loads((output / "runtime/result.json").read_text(encoding="utf-8"))
        build.update({
            "caseID": "BAK-01",
            "runtimeEvidence": result.get("runtimeEvidence"),
            "casePass": result.get("casePass"),
            "independentAcceptance": result.get("independentAcceptance"),
            "inputBundleDigest": result.get("inputBundleDigest"),
            "beforeDigest": result.get("beforeDigest"),
            "afterDigest": result.get("afterDigest"),
            "authoritativeBeforeDigest": result.get("authoritativeBeforeDigest"),
            "authoritativeAfterDigest": result.get("authoritativeAfterDigest"),
            "applicableSEM": result.get("applicableSEM"),
        })
    # Build/execution logs and external runner observations are independent of
    # the test's generation JSON. Regeneration qualifies equivalent evidence;
    # the pinned sealed archive authenticates the original bytes.
    build["logs"] = {p.name: digest(p) for p in output.glob("*.log")}
    (output / "runner-evidence.json").write_text(json.dumps(build, indent=2) + "\n", encoding="utf-8")
    print(output)


if __name__ == "__main__":
    main()
