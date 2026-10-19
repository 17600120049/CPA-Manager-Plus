#!/usr/bin/env python3
"""Check the opt-in image's exact local input and existing manifest contract.

Usage: python3 bin/ci/runtime-strict-auth-image-check.py LOCAL_IMAGE
No credentials, network, persistent mounts or running services are required.
The separate restricted integration smoke owns client-auth behavior evidence.
"""

import hashlib
import json
import pathlib
import subprocess
import sys


PIN = "0adcfa2283438cdb041e78074eae89321ca5986e5dc5d1da399b8def3ca22150"
SOURCE = "1fbb56551218bc7eadad1598d6009e9248447cf6"
UPSTREAM = "d7914afdedca7af95ee974a42453dc49fc1388ce"
VERSION = "8.0.13-cpamp-strict-auth.1"


def main():
    if len(sys.argv) != 2:
        raise SystemExit("usage: runtime-strict-auth-image-check.py LOCAL_IMAGE")
    image = json.loads(subprocess.check_output(
        ["docker", "image", "inspect", sys.argv[1]], text=True))[0]
    assert (image["Os"], image["Architecture"]) == ("linux", "amd64")
    # Resolve the mutable local image name once, then inspect that exact image.
    output = subprocess.check_output([
        "docker", "run", "--rm", "--network", "none", "--read-only",
        "--cap-drop", "ALL", "--security-opt", "no-new-privileges",
        "--tmpfs", "/tmp", "--entrypoint", "sh", image["Id"], "-ec", r'''
sha256sum /usr/local/bin/cli-proxy-api /usr/local/share/cpamp/runtime/config.seed.yaml
cat /usr/local/share/cpamp/runtime/cpa-artifact.json
cat /usr/local/share/cpamp/runtime/cpa-provenance.json
test "$(stat -c '%u:%g:%a' /usr/local/share/cpamp/runtime/cpa-artifact.json)" = 0:0:444
test "$(stat -c '%u:%g:%a' /usr/local/share/cpamp/runtime/cpa-provenance.json)" = 0:0:444
/usr/local/bin/cli-proxy-api --help >/tmp/identity 2>/tmp/help
head -n 1 /tmp/identity
'''], text=True).splitlines()
    assert len(output) == 5, output
    assert output[0].split()[0] == PIN
    root = pathlib.Path(__file__).resolve().parents[2]
    seed = hashlib.sha256((root / "docker/runtime/config.v8.seed.yaml").read_bytes()).hexdigest()
    assert output[1].split()[0] == seed
    manifest, provenance = map(json.loads, output[2:4])
    assert manifest == {"schemaVersion": 1, "engine": "cpa", "version": VERSION,
                        "artifactId": "sha256:" + PIN}
    assert provenance == {"schemaVersion": 1, "variant": "cpamp-strict-auth",
                          "version": VERSION, "sourceCommit": SOURCE,
                          "upstreamCommit": UPSTREAM, "executableSha256": PIN,
                          "inputMode": "verified-local-binary"}
    assert "Version: " + VERSION + ", Commit: " + SOURCE + "," in output[4]
    assert image["Config"]["Entrypoint"] == ["/usr/local/bin/cpamp-runtime-entrypoint"]
    assert image["Config"]["Cmd"] == ["/usr/local/bin/cpamp-runtime-supervisor"]
    print(json.dumps({"result": "PASS", "imageId": image["Id"], "manifest": manifest,
                      "provenance": provenance, "seedSha256": seed}, indent=2))


if __name__ == "__main__":
    main()
