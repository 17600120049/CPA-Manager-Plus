"""Bounded H1 adapter for the existing disposable S1 owner, NOT a daemon.

The trusted Docker/root operator owns the entire resource graph continuously.
A scan detects mistakes; it does not exclude a hostile Docker/root operator.
SIGKILL leaves the resources STOP/unknown: no automatic takeover or restart.
This driver never sends a normal CPA configuration PUT itself.
"""
import hashlib
import json
import pathlib
import secrets
import shutil
import subprocess
import time

PATH = "/v1/runtime/private-configuration"


def intent(request, domain, mode, *, target="", keeper="", old="", new="", value=0):
    return dict(requestId=request, domain=domain, mode=mode, targetId=target, keeperId=keeper,
                targetSHA256=hashlib.sha256(old.encode()).hexdigest() if old else "",
                keeperSHA256=hashlib.sha256(new.encode()).hexdigest() if new else "", retry=value)


def parent(i):
    return ("k:" if i["domain"] == "key" else "c:") + i["requestId"]


def validate_graph(objects, volume_paths, ids, runtime_id, namespace_owner):
    """Full IDs, named volumes AND bind/ancestor aliases; unknown means STOP."""
    source = next(iter(volume_paths))
    for obj in objects:
        owned = obj["Id"] in ids
        related = False
        for m in obj.get("Mounts", []):
            aliased = {m["Name"]} if m.get("Name") in volume_paths else set()
            if m.get("Type") == "bind":
                p = pathlib.PurePosixPath(m["Source"])
                aliased.update(k for k, v in volume_paths.items() if p == pathlib.PurePosixPath(v)
                               or p in pathlib.PurePosixPath(v).parents or pathlib.PurePosixPath(v) in p.parents)
            if aliased:
                related = True
                assert owned, "unowned volume/bind alias; STOP"
                if source in aliased:
                    assert obj["Id"] == runtime_id, "second source mount; STOP"
            if owned:
                assert not m["Source"].endswith(("docker.sock", "podman.sock")), "daemon socket; STOP"
        mode = obj["HostConfig"]["NetworkMode"]
        if mode.startswith("container:"):
            if namespace_owner(obj) == runtime_id:
                related = True
                assert owned, "unowned namespace consumer; STOP"
        if owned or related:
            h = obj["HostConfig"]
            assert not h["Privileged"] and not h["PortBindings"], "privileged/published private profile; STOP"
            assert h["RestartPolicy"]["Name"] in ("", "no"), "automatic restart forbidden; STOP"
            assert mode != "host" and not h.get("PidMode") and not h.get("IpcMode", "").startswith("container:"), "shared host/process namespace; STOP"


class Host:
    def __init__(self, command, *, http, name, runtime, probe, token, images, volumes, containers, out):
        self.command, self.http = command, http
        self.name, self.runtime, self.probe, self.token = name, runtime, probe, token
        self.images, self.volumes, self.containers, self.out = images, volumes, containers, out
        self.ids, self.grant = {}, {}

    def manifest(self, state="HELD / NO AUTOMATIC HANDOFF"):
        self.out.chmod(0o700)
        (self.out / "h1-owner.json").write_text(json.dumps(dict(
            run=self.name, containers=self.ids, volumes=self.volumes, network=self.name, images=self.images,
            state=state, credentials="not exported; original in-memory owner only",
            recovery="terminate original senders and exact CPA; retain volumes; no automatic generation handoff"), indent=2)+"\n")

    def track(self, name, cid):
        assert len(cid) == 64 and all(c in "0123456789abcdef" for c in cid), "exact container ID required"
        self.ids[name] = cid
        self.manifest()

    def prehash(self, seed, management):
        assert shutil.which("htpasswd"), "Host htpasswd required before CPA starts"
        p = subprocess.run([shutil.which("htpasswd"), "-niBC", "10", ""], input=management+"\n",
                           text=True, capture_output=True, timeout=15)
        assert p.returncode == 0, "pre-start bcrypt failed"
        return seed.replace(management, p.stdout.strip().split(":", 1)[1])

    def write_host_file(self, volume, filename, value):
        assert volume in self.volumes and filename in ("source.json", "key-operation.json")
        self.command("docker", "run", "--rm", "-i", "--network", "none", "--entrypoint", "sh",
                     "-v", volume+":/grant", self.images["manager"], "-ec",
                     "umask 077; cat > /grant/"+filename+".tmp; chmod 600 /grant/"+filename+".tmp; mv /grant/"+filename+".tmp /grant/"+filename,
                     data=json.dumps(value))

    def issue(self, delegation=None):
        now = int(time.time()*1000)
        self.grant.update(issuedAtMs=now, expiresAtMs=now+30000)
        if delegation is not None:
            self.grant["h1"] = dict(runId=self.name, **delegation)
        self.write_host_file(self.volumes[4], "source.json", self.grant)

    def authority(self, action, **fields):
        code, v = self.http(9081, PATH, "POST", dict(action=action, sourceId=self.grant["sourceId"],
                            generation=self.grant["generation"], **fields), self.token)
        assert code == 200, ("Runtime authority", action, code)
        return v

    def audit(self):
        from importlib import import_module
        # Reuse the existing namespace resolver, including Docker ID-prefix resolution.
        # __main__ is the original S1 launcher when executed as documented.
        resolver = import_module("__main__").namespace_owner
        c = self.command
        all_ids = c("docker", "ps", "-aq").splitlines()
        objects = json.loads(c("docker", "inspect", *all_ids)) if all_ids else []
        paths = {v: json.loads(c("docker", "volume", "inspect", v))[0]["Mountpoint"] for v in self.volumes}
        validate_graph(objects, paths, set(self.ids.values()), self.ids[self.runtime], resolver)
        # Precise per-role mounts: being a task-owned container is NOT a grant
        # to copy the Management credential, token or source to another role.
        allowed = {self.ids[self.runtime]: {self.volumes[0]: True, self.volumes[1]: True, self.volumes[4]: False}}
        if self.name+"-manager" in self.ids:
            allowed[self.ids[self.name+"-manager"]] = {self.volumes[1]: False, self.volumes[2]: True, self.volumes[3]: False, self.volumes[4]: False}
        for obj in objects:
            if obj["Id"] not in self.ids.values():
                continue
            for m in obj.get("Mounts", []):
                assert m.get("Name") in allowed.get(obj["Id"], {}) and bool(m["RW"]) == allowed[obj["Id"]][m["Name"]], "unexpected owned resource/credential mount; STOP"
        assert json.loads(c("docker", "network", "inspect", self.name))[0]["Internal"]
        code, status = self.http(9081, "/v1/runtime/status", key=self.token)
        assert code == 200 and str(status["runtimeGeneration"]) == self.grant["generation"], "generation drift; STOP"

    def provision(self, seed):
        code, status = self.http(9081, "/v1/runtime/status", key=self.token)
        assert code == 200 and status["state"] == "offline"
        dev, ino = self.command("docker", "exec", self.ids[self.runtime], "stat", "-c", "%d %i", "/runtime/gateway/config.yaml").split()
        ns = self.command("docker", "exec", self.ids[self.runtime], "readlink", "/proc/1/ns/net")
        self.grant = dict(sourceId=self.name, bootstrapRun=self.name, generation=str(status["runtimeGeneration"]),
                          namespace=ns, device=int(dev), inode=int(ino), seedSHA256=hashlib.sha256(seed.encode()).hexdigest())
        self.audit()
        self.issue()
        self.authority("admit", expectedSHA256=self.grant["seedSHA256"])
        # Bootstrap S1 needs a longer read/install window, still under the sole
        # Host launcher. H1 operation grants below are short, exact delegations.
        self.grant["expiresAtMs"] = int(time.time()*1000)+540000
        self.write_host_file(self.volumes[4], "source.json", self.grant)

    def delegate(self, action, i=None, step=""):
        self.audit()
        v = self.authority("observe")
        d = dict(action=action, revision=v["revision"], sha256=v["sha256"])
        if i is not None:
            d.update(parentId=parent(i), intent=i, stepId=step)
        self.issue(d)
        return v

    def exercise(self, admin, api, permit, old, old_id, evidence):
        from fresh_s2a_proof import BODY, counts
        proof = dict(configOperations=[], nativeLinux="NOT RUN", hostFailureInjection="NOT RUN", beta="HOLD")
        evidence["h1"] = proof  # Retain completed observations even if a later assertion fails.
        initial_counts = counts(self.command, self.runtime)
        assert initial_counts["cpaKeyPUT"] == 1 and initial_counts["cpaRetryPUT"] == 0, "unexpected S1 write count; STOP"
        def call(resource, method="GET", body=None):
            return self.http(28471, api+resource, method, body, admin)
        def config(value):
            before = counts(self.command, self.runtime)
            code, view = call("gateway/retry/request-retry")
            assert code == 200
            q = dict(requestId=secrets.token_hex(16), value=value, expected=view["expected"])
            i = intent(q["requestId"], "config", "retry", value=value)
            self.delegate("claim", i)
            bad = dict(q, requestId=secrets.token_hex(16))
            assert call("gateway/retry/request-retry", "PUT", bad)[0] == 409
            code, o = call("gateway/retry/request-retry", "PUT", q)
            assert code == 200 and o["operation"]["state"] == "persisted", ("Config terminal", code)
            released = self.authority("observe")
            assert released["h1"]["parent"] is None
            assert call("gateway/retry/request-retry", "PUT", q)[0] == 200  # tombstone, no PUT
            assert self.authority("observe") == released, "duplicate request changed Runtime; STOP"
            after = counts(self.command, self.runtime)
            assert after["cpaRetryPUT"] == before["cpaRetryPUT"]+1 and after["cpaKeyPUT"] == before["cpaKeyPUT"], "Config PUT replay or cross-domain write; STOP"
            proof["configOperations"].append(dict(operation=o["operation"], released=released,
                                                    retryPUTDelta=1, duplicateRequestChangedRuntime=False))
        self.delegate("activate")
        config(1)
        code, created = call("api-keys", "POST", {"requestId": secrets.token_hex(16)})
        assert code == 201
        new, new_id = created["createdSecret"], created["key"]["id"]
        i = intent(secrets.token_hex(16), "key", "replace", target=old_id, keeper=new_id, old=old, new=new)
        self.delegate("claim", i)
        code, o = call("api-keys/"+old_id+"/revocation", "POST", dict(requestId=i["requestId"], keeperId=new_id, mode="replace"))
        assert code == 200 and o["nextStep"] == "add"
        resource = "api-keys/operations/"+i["requestId"]
        for n, step in enumerate(("add", "revoke")):
            sid = parent(i)+":"+str(n)+":"+step
            v = self.delegate("step", i, sid)
            now = int(time.time()*1000)
            self.write_host_file(self.volumes[3], "key-operation.json", dict(
                version=2, operationId=i["requestId"], step=step, runId=self.name,
                generation=permit["generation"], sourceId=v["sourceId"], parentId=parent(i), stepId=sid,
                revision=v["revision"], sourceSHA256=v["sha256"], issuedAtMs=now, expiresAtMs=now+30000))
            code, o = call(resource+"/continue", "POST", {})
            for _ in range(20):
                if code == 200 and (o.get("nextStep") == "revoke" if step == "add" else o["operation"]["outcome"] == "committed"):
                    break
                time.sleep(.2)
                code, o = call(resource)  # observe only; never repeat continue/PUT
            assert code == 200 and (o.get("nextStep") == "revoke" if step == "add" else o["operation"]["outcome"] == "committed"), ("Key observation", step, code)
            if step == "add":
                v = self.authority("observe")
                assert v["h1"]["parent"]["id"] == parent(i) and len(v["h1"]["parent"]["steps"]) == 1
                assert v["h1"]["parent"]["phase"] == "held"
                code, view = call("gateway/retry/request-retry")
                assert code == 200 and not view["editable"]
                before = counts(self.command, self.runtime)
                blocked = dict(requestId=secrets.token_hex(16), value=0, expected=view["expected"])
                assert call("gateway/retry/request-retry", "PUT", blocked)[0] == 409
                assert self.authority("observe") == v, "blocked Config changed Key parent; STOP"
                after = counts(self.command, self.runtime)
                assert (after["cpaKeyPUT"], after["cpaRetryPUT"]) == (before["cpaKeyPUT"], before["cpaRetryPUT"])
                proof.update(parentHeldAfterAdd=v, configWhileKeyHeldStatus=409, blockedConfigPUTDelta=0)
        proof["keyOperation"] = o["operation"]
        released = self.authority("observe")
        assert released["h1"]["parent"] is None and released["h1"]["released"]["parentId"] == parent(i)
        proof["keyReleased"] = released
        config(0)
        config(0)  # same-value PUT still has its own receipt
        for key, want in ((None, 401), ("wrong", 401), (old, 401), (new, 200)):
            assert self.http(8317, "/v1/models", key=key)[0] == want
        before = counts(self.command, self.runtime)
        proof["ingressDenied"] = []
        for label, key in (("absent", None), ("wrong", "wrong"), ("revoked", old)):
            code, _ = self.http(28471, "/v1/chat/completions", "POST", BODY, key)
            assert code == 503, ("Ingress negative", label, code)
            proof["ingressDenied"].append(dict(credential=label, status=code))
        assert counts(self.command, self.runtime)["cpaChat"] == before["cpaChat"], "denied Chat reached CPA; STOP"
        assert self.http(28471, "/v1/chat/completions", "POST", BODY, new)[0] == 200
        final_counts = counts(self.command, self.runtime)
        assert final_counts["cpaKeyPUT"] == 3 and final_counts["cpaRetryPUT"] == 3, "unexpected final write count; STOP"
        assert final_counts["cpaChat"] == before["cpaChat"]+1
        proof.update(combinedSequence="B2(1)-Key(add/revoke)-B2(0)-B2(same 0)",
                     wrongRequestDenied=True, parentHeldBetweenSteps=True, ingressChatStatus=200,
                     initialCounters=initial_counts, finalCounters=final_counts)

    def stop_exact(self, cid):
        assert cid in self.ids.values(), "unowned stop target; STOP"
        c = self.command
        obj = json.loads(c("docker", "inspect", cid))[0]
        assert obj["Id"] == cid, "container identity changed; STOP"
        if obj["State"]["Running"]:
            try:
                c("docker", "stop", "--time", "5", cid)
            except RuntimeError:
                c("docker", "kill", cid)
            c("docker", "wait", cid)
        obj = json.loads(c("docker", "inspect", cid))[0]
        assert obj["Id"] == cid and not obj["State"]["Running"] and not obj["State"]["Restarting"] and obj["State"]["Pid"] == 0, "stop/reap unproven; STOP"

    def quiesce(self):
        # No restore/restart here. Terminate original request senders FIRST.
        self.manifest("STOP / QUIESCING; receipts retained")
        for name, cid in list(self.ids.items()):
            if name != self.runtime:
                self.stop_exact(cid)
        runtime_id = self.ids.get(self.runtime)
        if runtime_id:
            control = None
            try:
                # Fresh trusted CONTROL-only sender: no source/data/credential
                # mounts, no ports; typed Stop owns exact CPA Kill+Wait.
                control = self.command("docker", "run", "-d", "--network", "container:"+runtime_id,
                    "--cap-drop=ALL", "--security-opt", "no-new-privileges", "--read-only",
                    "python:3.13-alpine", "sleep", "60")
                self.track(self.name+"-stop-control", control)
                program = """import http.client,json,sys
v=json.load(sys.stdin)
def call(path,body=None):
 c=http.client.HTTPConnection('127.0.0.1',9081,timeout=8)
 c.request('GET' if body is None else 'POST',path,json.dumps(body) if body else None,{'Authorization':'Bearer '+v['token'],'Content-Type':'application/json','X-CPAMP-Runtime-Features':'active-gateway-artifact-v1'})
 r=c.getresponse();assert r.status==200;return json.loads(r.read())
s=call('/v1/runtime/status')
r=call('/v1/runtime/operations/stop',dict(operationId=v['operation'],expectedRuntimeIdentity=s['runtimeIdentity'],expectedRuntimeGeneration=s['runtimeGeneration']))
assert r['state']=='succeeded'
"""
                self.command("docker", "exec", "-i", control, "python3", "-c", program,
                             data=json.dumps(dict(token=self.token, operation="h1-stop-"+secrets.token_hex(16))))
            except RuntimeError:
                # Supervisor unavailable: trusted Host owns the exact container
                # cgroup. stop/kill/wait must prove ALL contained processes gone.
                pass
            finally:
                if control:
                    self.stop_exact(control)
                self.stop_exact(runtime_id)
        self.manifest("STOPPED / VOLUMES AND RECEIPTS RETAINED / NO AUTO RECOVERY")
