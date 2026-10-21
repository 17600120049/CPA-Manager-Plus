#!/usr/bin/env python3
"""A1 Public API extension of the existing disposable B1/S1/S2 Host lease.

Host root/Docker and this SERIAL launcher own the complete resource lifetime.
No K/U invocation, valid step permit, second Manager/CPA, management credential
holder, writable alias or public Management route is introduced. This proves
only the resources created here; it is not arbitrary-host attestation.
"""
import hashlib
import importlib.util
import json
import os
import pathlib
import secrets
import sys

ROOT = pathlib.Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("b1_host", ROOT / "bin/ci/fresh-b1-docker-proof.py")
b1 = importlib.util.module_from_spec(spec)
spec.loader.exec_module(b1)


class Host(b1.Host):
    def __init__(self):
        super().__init__()
        self.a1 = []
        self.permit_volume = None

    def record_a1(self, case, **fields):
        self.a1.append(dict(case=case, result="PASS", **fields))
        print("A1 PASS: " + case, flush=True)

    def command(self, *args, data=None):
        # Reject K/U for the entire launcher lifetime, including injected steps.
        assert "--ku" not in sys.argv
        assert not any(str(v).startswith("CPAMP_PRIVATE_KEY_OPERATIONS=") for v in args)
        if args[:3] == ("docker", "run", "-d") and "--name" in args:
            name = args[args.index("--name")+1]
            if name.endswith("-manager"):
                mounts = [args[i+1] for i, v in enumerate(args[:-1]) if v == "-v"]
                self.permit_volume = next(v.split(":")[0] for v in mounts if ":/run/cpamp-s1:" in v)
        # Controlled deterministic failure in the EXISTING S2-A synthetic
        # upstream. No Provider/config/credential override is added.
        def synthetic(program):
            if not (isinstance(program, str) and "events=[]" in program and "s1-synthetic-upstream-only" in program):
                return program
            program = program.replace("events=[]", "events=[]\nfailure=False")
            program = program.replace(" def do_GET(self):\n", " def do_GET(self):\n  global failure\n  if self.path in ('/__a1/fail','/__a1/reset'):\n   failure=self.path.endswith('/fail');self.send_response(204);self.end_headers();return\n")
            return program.replace("  b=json.dumps({'id':'s1-synthetic'", "  if failure:\n   b=b'{\"error\":{\"message\":\"controlled offline failure\"}}';self.send_response(500);self.send_header('Content-Type','application/json');self.send_header('Content-Length',str(len(b)));self.end_headers();self.wfile.write(b);return\n  b=json.dumps({'id':'s1-synthetic'")
        args = tuple(synthetic(v) for v in args)
        return super().command(*args, data=data)

    def extend(self):
        if "--s2b-offline" in sys.argv:
            assert self.http(18321, "/reset", "POST", {})[0] == 204
            self.lifecycle("restart")
        base = self.install_path.split("api-keys/", 1)[0] + "gateway/retry/"

        def read():
            code, v = self.http(28471, base+"request-retry", key=self.admin)
            assert code == 200 and v["effective"] == "unknown" and v["editable"], ("read", code, v.get("reason"))
            return v

        def save(value):
            before = read()
            request = dict(requestId=secrets.token_hex(16), value=value, expected=before["expected"])
            code, v = self.http(28471, base+"request-retry", "PUT", request, self.admin)
            assert code == 200 and v["operation"]["state"] == "persisted" and v["effective"] == "unknown", ("save", code)
            revision = self.observe()["revision"]
            assert self.http(28471, base+"request-retry", "PUT", request, self.admin)[0] == 200
            assert self.observe()["revision"] == revision, "duplicate request reserved another operation"
            assert self.http(28471, base+"operations/"+request["requestId"], key=self.admin)[0] == 200
            assert self.http(28471, base+"operations/"+request["requestId"]+"/reconcile", "POST", {}, self.admin)[0] == 200
            return request

        for key in (None, "wrong", self.secret):
            assert self.http(28471, base+"request-retry", key=key)[0] == 401
        initial = read()
        assert initial["presence"] == "missing" and initial["value"] is None
        before = self.observe()["revision"]
        for value in (-1, 11, 1.5, "0", None, True, [], {}):
            q = dict(requestId=secrets.token_hex(16), value=value, expected=initial["expected"])
            assert self.http(28471, base+"request-retry", "PUT", q, self.admin)[0] == 400
        assert self.observe()["revision"] == before
        self.record_a1("Administrator-only; missing presence; invalid values rejected before Runtime begin")

        # Absence is only a negative check; continuous isolation comes from
        # this Host's complete lifecycle, ownership and non-issuance contract.
        b1.original("docker", "run", "--rm", "--network", "none", "--entrypoint", "sh", "-v", self.permit_volume+":/permit", os.environ["CPAMP_PROOF_IMAGE_PREFIX"]+"-manager:local", "-ec", "umask 077; printf '{}' > /permit/key-operation.json")
        q = dict(requestId=secrets.token_hex(16), value=0, expected=initial["expected"])
        assert self.http(28471, base+"request-retry", "PUT", q, self.admin)[0] == 409
        assert self.observe()["revision"] == before
        b1.original("docker", "run", "--rm", "--network", "none", "--entrypoint", "sh", "-v", self.permit_volume+":/permit", os.environ["CPAMP_PROOF_IMAGE_PREFIX"]+"-manager:local", "-ec", "rm /permit/key-operation.json")
        self.record_a1("reachable K/U permit artifact rejects Config write without Runtime begin")

        zero = save(0)
        assert read()["value"] == 0
        stale = dict(requestId=secrets.token_hex(16), value=1, expected=initial["expected"])
        assert self.http(28471, base+"request-retry", "PUT", stale, self.admin)[0] == 409
        self.auth(); self.admission()
        self.record_a1("Public API saves explicit zero; duplicate requestId is read-only; stale source conflicts; last Key and S2 admission retained")
        if "--s2b-offline" not in sys.argv:
            from fresh_s2a_proof import BODY
            arrivals = []
            for value in (0, 1):
                if value:
                    save(value)
                self.lifecycle("restart")
                assert self.http(18321, "/__a1/fail")[0] == 204
                start = len(self.http(18321, "/events")[1])
                code, _ = self.http(28471, "/v1/chat/completions", "POST", BODY, self.secret)
                count = len(self.http(18321, "/events")[1])-start
                assert code >= 400 and count >= 1, ("controlled failure did not arrive", value, code, count)
                arrivals.append(count)
                assert self.http(18321, "/__a1/reset")[0] == 204
            self.lifecycle("restart"); self.admission()
            effect = "observed offline only" if arrivals == [1, 2] else "unknown"
            self.record_a1("S2-A deterministic failures and actual upstream arrival counts recorded", globalValues=[0, 1], upstreamArrivals=arrivals, effective=effect)
            if effect == "unknown":
                self.a1[-1]["result"] = "EFFECT_UNKNOWN"
                self.a1[-1]["reason"] = "fixed seed omits max-retry-interval; transient failures cool down; A1 cannot change another field to manufacture a witness"
        else:
            save(1)
            self.lifecycle("restart"); self.auth(); self.admission()
            self.record_a1("S2-B credential-level retry zero preserved after global scalar write and restart; no global-effect claim")
        # A real Manager process restart retains the same immutable request ID.
        revision = self.observe()["revision"]
        b1.original("docker", "restart", self.runtime.removesuffix("-runtime")+"-manager")
        import time
        for _ in range(40):
            try:
                if self.http(28471, base+"operations/"+zero["requestId"], key=self.admin)[0] == 200:
                    break
            except RuntimeError:
                pass
            time.sleep(.2)
        assert self.http(28471, base+"request-retry", "PUT", zero, self.admin)[0] == 200
        assert self.observe()["revision"] == revision
        self.record_a1("Manager process restart retains original request tombstone; no Runtime reservation or CPA PUT replay")
        # Resume ALL unchanged B1 fault/recovery and original S1/S2 cases.
        super().extend()
        out = pathlib.Path(sys.argv[1])
        paths = [str(p.relative_to(ROOT)) for p in (ROOT/"apps/manager-server").rglob("*retry*.go")]
        paths += ["apps/manager-server/internal/repository/freshstore/repository.go", "apps/manager-server/internal/repository/freshstore/types.go", "apps/manager-server/internal/service/freshkeyapply/config_target.go", "bin/ci/fresh-a1-docker-proof.py"]
        (out/"a1.json").write_text(json.dumps(dict(result="PASS_OFFLINE_A1_EFFECT_UNKNOWN_LIVE_NOT_RUN_BETA_HOLD", cases=self.a1, sourceSHA256={p:hashlib.sha256((ROOT/p).read_bytes()).hexdigest() for p in paths}, hostBoundary="trusted serial Host owns resources throughout lease; no K/U writer or valid K/U permit; not arbitrary-host attestation"), indent=2)+"\n")


if __name__ == "__main__":
    assert set(sys.argv[2:]) in ({"--s2a"}, {"--s2b-offline"})
    os.environ.setdefault("CPAMP_PROOF_IMAGE_PREFIX", "cpamp-a1")
    os.environ.setdefault("CPAMP_PROOF_IMAGE_TAG", "local")
    b1.Host = Host
    try:
        b1.main()
    finally:
        out = pathlib.Path(sys.argv[1])
        if (out/"a1.json").exists() and (out/"b1.json").exists():
            evidence = json.loads((out/"a1.json").read_text())
            boundary = json.loads((out/"b1.json").read_text())
            evidence["b1Result"] = boundary["result"]
            evidence["head"] = b1.original("git", "-C", str(ROOT), "rev-parse", "HEAD")
            evidence["images"] = {kind: b1.original("docker", "image", "inspect", "--format", "{{.Id}}", os.environ["CPAMP_PROOF_IMAGE_PREFIX"]+"-"+kind+":local") for kind in ("manager", "runtime", "ingress")}
            if boundary["result"] != "PASS_OFFLINE_B0_B1_LIVE_NOT_RUN_BETA_HOLD":
                evidence["result"] = "STOP"
            boundary["B2"] = "A1 Public API consumer; A2 UI not installed; effective unknown"
            (out/"b1.json").write_text(json.dumps(boundary, indent=2)+"\n")
            (out/"a1.json").write_text(json.dumps(evidence, indent=2)+"\n")
