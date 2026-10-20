#!/usr/bin/env python3
"""B0/B1 extension of the unchanged S1/S2 disposable Host launcher.

The Host owns Docker/root and serializes this entire lease, not just a scan.
Only task-created resources are selected; no daemon socket or writable Runtime
alias is passed to Manager/Ingress. Network peers have no Management credential.
htpasswd (Host tool) prepares bcrypt BEFORE the first CPA child starts.
No public config endpoint, Key replacement/revocation or B2 UI is installed.
"""
import concurrent.futures
import hashlib
import importlib.util
import json
import os
import pathlib
import re
import secrets
import shutil
import subprocess
import sys
import time

ROOT = pathlib.Path(__file__).resolve().parents[2]
spec = importlib.util.spec_from_file_location("s1_host", ROOT / "bin/ci/fresh-s1-docker-proof.py")
s1 = importlib.util.module_from_spec(spec)
spec.loader.exec_module(s1)
original = s1.command
PATH = "/v1/runtime/private-configuration"


class Host:
    def __init__(self):
        self.runtime = self.probe = self.volume = self.token = self.secret = self.management = ""
        self.admin = self.install_path = ""
        self.grant = {}
        self.events = []
        self.extended = self.restarted = False
        self.pending_restart = None
        self.restart_failure = None
        self.result = "STOP"

    def record(self, case):
        self.events.append({"case": case, "result": "PASS"})
        print("B1 PASS: " + case, flush=True)

    def http(self, port, path, method="GET", body=None, key=None):
        program = """import http.client,json,sys
v=json.load(sys.stdin);c=http.client.HTTPConnection('127.0.0.1',v['port'],timeout=8)
h={'X-CPAMP-Runtime-Features':'active-gateway-artifact-v1'}
if v['key']:h['Authorization']='Bearer '+v['key']
if v['body'] is not None:h['Content-Type']='application/json'
c.request(v['method'],v['path'],json.dumps(v['body']) if v['body'] is not None else None,h)
r=c.getresponse();b=r.read(1048577);assert len(b)<=1048576
print(json.dumps({'code':r.status,'body':b.decode()}))
"""
        result = json.loads(original("docker", "exec", "-i", self.probe, "python3", "-c", program,
                                     data=json.dumps(dict(port=port, path=path, method=method, body=body, key=key))))
        try:
            body = json.loads(result["body"])
        except ValueError:
            body = result["body"]
        return result["code"], body

    def authority(self, action, **fields):
        q = dict(action=action, sourceId=self.grant["sourceId"], generation=self.grant["generation"], **fields)
        return self.http(9081, PATH, "POST", q, self.token)

    def observe(self):
        code, v = self.authority("observe")
        assert code == 200, ("authority observe", code)
        return v

    def disk_hash(self):
        return original("docker", "exec", self.runtime, "sha256sum", "/runtime/gateway/config.yaml").split()[0]

    def issue(self):
        original("docker", "run", "--rm", "-i", "--network", "none", "--entrypoint", "sh",
                 "-v", self.volume + ":/grant", os.environ["CPAMP_PROOF_IMAGE_PREFIX"] + "-manager:local",
                 "-ec", "umask 077; cat > /grant/source.json; chmod 600 /grant/source.json",
                 data=json.dumps(self.grant))

    def provision(self, seed):
        # Binding is from the task-created Runtime, not supplied Admin JSON.
        code, status = self.http(9081, "/v1/runtime/status", key=self.token)
        assert code == 200 and status["state"] == "offline"
        dev, ino = original("docker", "exec", self.runtime, "stat", "-c", "%d %i", "/runtime/gateway/config.yaml").split()
        ns = original("docker", "exec", self.runtime, "readlink", "/proc/1/ns/net")
        self.grant = dict(sourceId=self.runtime.removesuffix("-runtime"), generation=str(status["runtimeGeneration"]),
                          namespace=ns, device=int(dev), inode=int(ino), seedSHA256=hashlib.sha256(seed.encode()).hexdigest(),
                          issuedAtMs=int(time.time()*1000), expiresAtMs=int(time.time()*1000)+540000)
        self.issue()
        assert self.authority("observe")[0] == 409
        assert self.authority("admit", expectedSHA256=self.disk_hash())[0] == 200
        self.record("Host source admitted before first CPA child; prehashed Management credential")

    def lifecycle(self, action):
        code, status = self.http(9081, "/v1/runtime/status", key=self.token)
        assert code == 200
        q = dict(operationId="b1-" + secrets.token_hex(12), expectedRuntimeIdentity=status["runtimeIdentity"],
                 expectedRuntimeGeneration=status["runtimeGeneration"])
        if action == "start":
            q["expectedActiveArtifactId"] = "sha256:" + s1.PIN
        code, v = self.http(9081, "/v1/runtime/operations/" + action, "POST", q, self.token)
        assert code == 200 and v["state"] == "succeeded", (action, code)
        if action in ("start", "restart"):
            for _ in range(40):
                if self.http(9081, "/v1/runtime/status", key=self.token)[1]["state"] == "ready":
                    return
                time.sleep(.25)
            raise RuntimeError("CPA did not become ready")

    def begin(self, retry):
        v = self.observe()
        q = dict(operationId="b1-" + secrets.token_hex(12), scope="request-retry", retry=retry,
                 expectedRevision=v["revision"], expectedSHA256=v["sha256"])
        code, begun = self.authority("begin", **q)
        assert code == 200 and begun["created"], ("begin", code)
        q["expectedRevision"] = begun["revision"]
        return q

    def commit(self, q):
        q = dict(q, expectedSHA256=self.disk_hash())
        code, v = self.authority("commit", **q)
        assert code == 200 and v["state"] == "persisted", ("commit", code)

    def auth(self):
        for key, expected in ((None, 401), ("wrong", 401), (self.secret, 200)):
            assert self.http(8317, "/v1/models", key=key)[0] == expected

    def admission(self, allowed=True):
        if "--s2b-offline" in sys.argv:
            from fresh_s2b_proof import BODY
            body = BODY
        else:
            from fresh_s2a_proof import BODY
            body = BODY
        for key, expected in ((None, 503), ("wrong", 503), (self.secret, 200 if allowed else 503)):
            assert self.http(28471, "/v1/chat/completions", "POST", body, key)[0] == expected

    def extend(self):
        assert self.observe()["installed"]
        if "--s2b-offline" in sys.argv:
            assert self.http(18321, "/reset", "POST", {})[0] == 204
        self.lifecycle("restart"); self.auth(); self.admission()
        self.record("S1 committed Key survives CPA child restart; S2 admission retained")
        # A trusted Host test driver exercises the future Manager-owned scalar
        # window. This is NOT a B2 Public API or Web editor.
        q = self.begin(0)
        assert self.http(8317, "/v8/management/config/routing/retry/request-retry", "PUT", 0, self.management)[0] == 200
        self.commit(q)
        self.lifecycle("restart"); self.auth(); self.admission()
        self.record("official scalar 0 persisted; restart; Key/Provider/secret preserved; S2 passes")

        v = self.observe()
        def competing(retry):
            return self.authority("begin", operationId=secrets.token_hex(16), scope="request-retry", retry=retry,
                                  expectedRevision=v["revision"], expectedSHA256=v["sha256"])
        with concurrent.futures.ThreadPoolExecutor(2) as pool:
            results = list(pool.map(competing, (0, 1)))
        assert sorted(code for code, _ in results) == [200, 409]
        assert self.authority("begin", operationId=secrets.token_hex(16), scope="initial-key",
                              keySHA256=hashlib.sha256(self.secret.encode()).hexdigest(),
                              expectedRevision=v["revision"], expectedSHA256=v["sha256"])[0] == 409
        active = self.observe()
        q = dict(operationId=active["operation"]["id"], expectedRevision=active["revision"])
        self.admission(False)
        self.record("concurrent scalar reservations admit one; Key reinstall rejected; unknown denies S2")
        # Pre-PUT interruption: no retry. Stop/reap first, explicitly close the
        # original window through Host recovery even though bytes are unchanged.
        assert self.authority("recover", **q, expectedSHA256=self.disk_hash())[0] == 409
        self.lifecycle("stop")
        self.grant.update(recoveryOperation=q["operationId"], recoverySHA256=self.disk_hash())
        self.issue()
        assert self.authority("recover", **q, expectedSHA256=self.disk_hash())[0] == 200
        self.lifecycle("start"); self.admission()
        self.record("pre-PUT interruption requires original-operation Host recovery; no PUT replay")

        q = self.begin(1)
        self.http(8317, "/v8/management/config/routing/retry/request-retry", "PUT", 1, self.management)
        # Deliberately discard the response and lose the client process before
        # terminal recording. New process reads the same Runtime operation.
        original("docker", "restart", self.runtime.removesuffix("-runtime") + "-manager")
        assert self.observe()["state"] == "unknown"
        self.commit(q); self.lifecycle("restart"); self.auth(); self.admission()
        self.record("lost response + Manager restart reconciles same operation without repeating PUT")

        q = self.begin(0)
        original("docker", "exec", "--user", "10001:10001", self.runtime,
                 "sh", "-ec", "printf 'routing: [' > /runtime/gateway/config.yaml")
        assert self.authority("commit", **dict(q, expectedSHA256=self.disk_hash()))[0] == 409
        self.admission(False)
        self.grant.update(recoveryOperation=q["operationId"], recoverySHA256=self.disk_hash()); self.issue()
        assert self.authority("recover", **dict(q, expectedSHA256=self.disk_hash()))[0] == 409
        self.lifecycle("stop")
        # A changed expected fingerprint cannot authorize a blind overwrite.
        assert self.authority("recover", **dict(q, expectedSHA256="0"*64))[0] == 409
        assert self.authority("recover", **dict(q, expectedSHA256=self.disk_hash()))[0] == 200
        self.lifecycle("start"); self.auth(); self.admission()
        assert self.http(8317, "/v8/management/config/routing/retry/request-retry", key=self.management) == (200, 1)
        self.record("O_TRUNC partial-file failure; no live restore; exact original last-good recovered")
        # Independent later corruption is not mistaken for a pending PUT.
        self.lifecycle("stop")
        last = self.observe()
        original("docker", "exec", "--user", "10001:10001", self.runtime,
                 "sh", "-ec", "printf 'unexpected damage' > /runtime/gateway/config.yaml")
        self.grant.update(recoveryOperation=last["operation"]["id"], recoverySHA256=self.disk_hash()); self.issue()
        assert self.authority("recover", operationId=last["operation"]["id"], expectedRevision=last["revision"],
                              expectedSHA256=self.disk_hash())[0] == 200
        self.lifecycle("start"); self.auth(); self.admission()
        self.record("later corruption records recovery intention before restoring the committed last-good")
        self.extended = True

    def full_restart(self):
        # The unchanged S1 launcher has already checked the OLD generation's
        # 409s. Host now renews only Runtime source admission, not the S1 permit
        # or Manager checkpoint. No automatic generation rebind is performed.
        status = self.http(9081, "/v1/runtime/status", key=self.token)[1]
        assert status["state"] == "offline" and str(status["runtimeGeneration"]) != self.grant["generation"]
        self.grant.update(generation=str(status["runtimeGeneration"]),
                          namespace=original("docker", "exec", self.runtime, "readlink", "/proc/1/ns/net"),
                          issuedAtMs=int(time.time()*1000), expiresAtMs=int(time.time()*1000)+180000)
        assert self.pending_restart is not None
        self.grant.update(recoveryOperation=self.pending_restart["operationId"], recoverySHA256=self.disk_hash())
        self.issue()
        assert self.observe()["state"] == "unknown"
        self.commit(self.pending_restart)
        self.lifecycle("start"); self.auth(); self.admission(False)
        assert self.http(8317, "/v8/management/config/routing/retry/request-retry", key=self.management) == (200, 2)
        self.record("Supervisor restart reconciles original unknown write while stopped; explicit Host Start; stale S1 admission stays denied")
        self.restarted = True

    def command(self, *args, data=None):
        args = list(args)
        if args[:3] == ["docker", "run", "-d"] and "--name" in args:
            name = args[args.index("--name")+1]
            if name.endswith("-runtime"):
                self.runtime = name; self.probe = name.removesuffix("-runtime") + "-probe"
                self.volume = name + "-b1-grant"
                original("docker", "volume", "create", self.volume)
                self.token = next(v.split("=",1)[1] for v in args if v.startswith("CPAMP_RUNTIME_TOKEN="))
                args[3:3] = ["-e", "CPAMP_PRIVATE_CONFIG_AUTHORITY=1", "-v", self.volume + ":/run/cpamp-config:ro"]
            elif name.endswith("-manager"):
                args[3:3] = ["-e", "CPAMP_PRIVATE_CONFIG_AUTHORITY=1", "-v", self.volume + ":/run/cpamp-config:ro"]
        is_seed = args[:4] == ["docker", "exec", "-i", self.runtime] and data and "cat > /runtime/gateway/config.yaml" in args[-1]
        if is_seed:
            if data.lstrip().startswith("{"):
                self.management = json.loads(data)["management"]["secret-key"]
            else:
                self.management = re.search(r"secret-key: ['\"]([a-f0-9]{64})['\"]", data).group(1)
            p = subprocess.run([shutil.which("htpasswd"), "-niBC", "10", ""],
                               input=self.management+"\n", text=True, capture_output=True, check=True)
            hashed = p.stdout.strip().split(":",1)[1]
            data = data.replace(self.management, hashed)
        # Extend only the synthetic OFFLINE stub with a reset after its
        # existing upstream-failure negative. No product Provider behavior.
        if data and '"program"' in data and "if self.path=='/fail':failure=True" in data:
            setup = json.loads(data)
            setup["program"] = setup["program"].replace(
                "  if self.path=='/fail':failure=True;",
                "  if self.path=='/reset':failure=False;self.send_response(204);self.end_headers();return\n  if self.path=='/fail':failure=True;")
            data = json.dumps(setup)
        if args[:3] == ["docker", "restart", self.runtime.removesuffix("-runtime") + "-manager"] and not self.extended:
            self.extend()
        if args[:3] == ["docker", "restart", self.runtime] and self.extended and self.pending_restart is None:
            self.pending_restart = self.begin(2)
            assert self.http(8317, "/v8/management/config/routing/retry/request-retry", "PUT", 2, self.management)[0] == 200
        if args[:3] == ["docker", "rm", "-f"] and self.extended and not self.restarted:
            # Finish the original launcher's cleanup even when this final
            # recovery assertion fails; never leak task-owned live writers.
            self.restarted = True
            try:
                self.full_restart()
            except Exception as error:
                self.restart_failure = error
        result = original(*args, data=data)
        if is_seed:
            self.provision(data)
        if args[:4] == ["docker", "exec", "-i", self.probe] and data:
            # Capture only in-memory synthetic credentials already held by the
            # original Host runner. Never put them in the report or exceptions.
            try:
                request = json.loads(data)
                response = json.loads(result)
                value = json.loads(response["body"])
                if isinstance(value, dict) and "createdSecret" in value and not self.secret:
                    self.secret = value["createdSecret"]
                if "/initial-install" in request.get("path","") and request.get("key"):
                    self.install_path, self.admin = request["path"], request["key"]
            except (ValueError, KeyError, TypeError):
                pass
        return result


def main():
    assert shutil.which("htpasswd"), "Host htpasswd is required for pre-start bcrypt"
    assert "--s2a" in sys.argv or "--s2b-offline" in sys.argv, "select existing S2 fixture"
    assert not any("expiry" in v or v in ("--preflight", "--ui") for v in sys.argv[2:])
    os.environ.setdefault("CPAMP_PROOF_IMAGE_PREFIX", "cpamp-b1")
    os.environ.setdefault("CPAMP_PROOF_IMAGE_TAG", "local")
    host = Host()
    s1.command = host.command
    try:
        s1.main()
        if host.restart_failure is not None:
            raise RuntimeError("B1 Supervisor restart assertion failed") from host.restart_failure
        assert host.extended and host.restarted
        host.result = "PASS_OFFLINE_B0_B1_LIVE_NOT_RUN_BETA_HOLD"
    finally:
        if host.volume:
            original("docker", "volume", "rm", host.volume)
        out = pathlib.Path(sys.argv[1])
        if out.exists():
            (out / "b1.json").write_text(json.dumps(dict(result=host.result, cases=host.events,
                runtimeTarget="v8-amd64-loopback-proof", ordinaryRuntime="UNCHANGED",
                hostBoundary="trusted Docker/root maintains exclusive resources throughout the lease; no production qualification",
                B2="NO PUBLIC API OR UI; Host test driver only"), indent=2)+"\n")


if __name__ == "__main__":
    main()
