#!/usr/bin/env python3
"""P-S0 *disposable* Host owner: audit exact stopped profile before Ingress starts.

This is not a production launcher or a security claim against trusted Docker
administrators. Never re-use existing containers/volumes or infer Host PASS.
The only public socket is the runtime-owned host-loopback Ingress publication.
"""
import argparse
import json
import os
import time
from pathlib import Path
import subprocess
import sys

ROOT = Path(__file__).resolve().parents[2]
PROJECT = "cpamp-pah-s0-proof"
PROFILE = "docker-compose.pah-s0-proof.yml"
SERVICES = ("cpamp-runtime", "cpamp-manager", "cpamp-ingress")
IMAGES = {s: f"{s}:pah-s0-proof" for s in SERVICES}
OPT_IN = "I_ACCEPT_DISPOSABLE_PAH_S0"
PORT = 28491
VOLUMES = ("cpamp-pah-s0-manager-data", "cpamp-pah-s0-runtime-data",
           "cpamp-pah-s0-runtime-secret")
VOLUME_NAMES = {key: PROJECT + "_" + key for key in VOLUMES}
MOUNTS = {
    "cpamp-runtime": {
        "/runtime": ("cpamp-pah-s0-runtime-data", True),
        "/run/cpamp/runtime-secret": ("cpamp-pah-s0-runtime-secret", True),
    },
    "cpamp-manager": {
        "/data": ("cpamp-pah-s0-manager-data", True),
        "/run/cpamp/runtime-secret": ("cpamp-pah-s0-runtime-secret", False),
    },
    "cpamp-ingress": {},
}
RUNTIME_LISTENERS = {
    "CPAMP_RUNTIME_ADDR": "127.0.0.1:9081",
    "CPAMP_RUNTIME_CPA_ADDR": "127.0.0.1:8317",
}


def require(ok, message):
    if not ok:
        raise RuntimeError("P-S0 STOP: " + message)


def command(*args):
    p = subprocess.run(args, cwd=ROOT, text=True, stdout=subprocess.PIPE,
                       stderr=subprocess.PIPE, timeout=600, check=False)
    require(p.returncode == 0, "command failed: " + " ".join(args[:4]) +
            ": " + p.stderr[-1200:])
    return p.stdout.strip()


def compose(*args):
    return command("docker", "compose", "-p", PROJECT, "-f", PROFILE, *args)


def env(service):
    values = service["environment"]
    if isinstance(values, dict):
        return values
    return dict(item.split("=", 1) for item in values)


def check_config(config, port=PORT):
    services = config.get("services", {})
    require(set(config.get("volumes", {})) == set(VOLUMES),
            "unknown/missing private Compose volume")
    require(set(services) == set(SERVICES), "unknown/missing Compose service")
    for logical in VOLUMES:
        volume = config["volumes"][logical]
        require(volume.get("name") == VOLUME_NAMES[logical] and
                not volume.get("external") and
                volume.get("driver", "local") == "local",
                "private volume redirected or externally managed: " + logical)
    runtime, manager, ingress = (services[n] for n in SERVICES)
    for service, dockerfile, target in (
        (runtime, "Dockerfile.runtime", "v8-amd64-loopback-proof"),
        (manager, "Dockerfile.manager-server", None),
        (ingress, "Dockerfile.ingress", None),
    ):
        build = service.get("build", {})
        require(build.get("dockerfile") == dockerfile and
                (target is None or build.get("target") == target),
                "unexpected P-S0 image build recipe: " + dockerfile)
    require(runtime.get("platform") == manager.get("platform") ==
            ingress.get("platform") == "linux/amd64", "wrong architecture")
    for name, service in zip(SERVICES, (runtime, manager, ingress)):
        require(service.get("image") == IMAGES[name], "unexpected image tag " + name)
        require(service.get("restart") in (None, "no"), "auto restart " + name)
    require(manager.get("build", {}).get("args", {}).get("VITE_CPAMP_PAH_S0") == "1",
            "unpaired Manager/Web build")
    require(ingress.get("build", {}).get("args", {}).get("CPAMP_PAH_S0_BUILD") == "1",
            "unpaired Ingress binary build")
    require(env(manager).get("CPAMP_PRIVATE_PAH_S0") ==
            env(ingress).get("CPAMP_PRIVATE_PAH_S0") == "1", "runtime modes mismatch")
    for key, value in RUNTIME_LISTENERS.items():
        require(env(runtime).get(key) == value, "Runtime/CPA listener not loopback: " + key)
    require(env(manager).get("HTTP_ADDR") == "127.0.0.1:18319", "Manager not loopback")
    require(env(ingress).get("CPAMP_INGRESS_CONTROL_ADDR") == "127.0.0.1:9082",
            "control listener not loopback")
    require(env(ingress).get("CPAMP_MANAGER_URL") == "http://127.0.0.1:18319" and
            env(ingress).get("CPAMP_GATEWAY_URL") == "http://127.0.0.1:8317",
            "upstream not loopback")
    require(manager.get("network_mode") == ingress.get("network_mode") ==
            "service:cpamp-runtime", "private namespace mismatch")
    require(not manager.get("ports") and not ingress.get("ports"), "side port published")
    for name, service in zip(SERVICES, (runtime, manager, ingress)):
        expected = MOUNTS[name]
        actual = service.get("volumes", [])
        require(len(actual) == len(expected), "unexpected Compose mount for " + name)
        for mount in actual:
            target = mount.get("target")
            require(target in expected and mount.get("type") == "volume" and
                    mount.get("source") == expected[target][0] and
                    bool(mount.get("read_only", False)) == (not expected[target][1]),
                    "unsafe Compose source/permission: " + name)
    ports = runtime.get("ports", [])
    require(len(ports) == 1 and ports[0].get("host_ip") == "127.0.0.1" and
            int(ports[0].get("published", 0)) == port and
            int(ports[0].get("target", 0)) == port and
            ports[0].get("protocol", "tcp") == "tcp", "unexpected public port matrix")


def require_fresh_volumes(existing):
    require(not set(VOLUME_NAMES.values()).intersection(existing),
            "retained private volume from prior run; refusing reuse")


def check_volume_info(volumes):
    require(set(volumes) == set(VOLUME_NAMES.values()), "missing/unexpected private volume")
    paths = {}
    for item in volumes.values():
        labels = item.get("Labels") or {}
        require(labels.get("com.docker.compose.project") == PROJECT,
                "private volume owned by another project")
        require(item.get("Driver") == "local", "unexpected volume driver")
        path = item.get("Mountpoint")
        require(path and Path(path).is_absolute(), "volume mountpoint is not absolute")
        paths[item["Name"]] = str(Path(path))
    require(len(set(paths.values())) == len(paths), "private volume mountpoint alias")
    return paths


def aliases_private_path(mount, volume_paths):
    name = mount.get("Name", "")
    if name in volume_paths:
        return True
    source = mount.get("Source", "")
    if not source or not Path(source).is_absolute():
        return False
    source_path = Path(os.path.realpath(source))
    return any(source_path == Path(os.path.realpath(path)) or
               source_path in Path(os.path.realpath(path)).parents or
               Path(os.path.realpath(path)) in source_path.parents
               for path in volume_paths.values())


def check_objects(objects, image_ids, ids, port=PORT, stopped=True, running=None,
                  volume_paths=None):
    require(set(ids) == set(SERVICES) and len(set(ids.values())) == 3,
            "missing/non-unique profile containers")
    by_id = {x["Id"]: x for x in objects}
    require(all(cid in by_id for cid in ids.values()), "unresolved container")
    if volume_paths is None:
        volume_paths = {name: "/docker/volumes/" + name + "/_data"
                        for name in VOLUME_NAMES.values()}
    require(set(volume_paths) == set(VOLUME_NAMES.values()), "unverified private volumes")
    runtime_id = ids["cpamp-runtime"]
    for service, cid in ids.items():
        obj = by_id[cid]
        labels = obj.get("Config", {}).get("Labels", {})
        host = obj["HostConfig"]
        require(labels.get("com.docker.compose.project") == PROJECT and
                labels.get("com.docker.compose.service") == service,
                "foreign/stale Compose owner: " + service)
        require(obj.get("Image") == image_ids[service], "swapped image: " + service)
        expected_mounts = MOUNTS[service]
        actual_mounts = obj.get("Mounts", [])
        require(len(actual_mounts) == len(expected_mounts),
                "unexpected mount count for " + service)
        for mount in actual_mounts:
            target = mount.get("Destination")
            require(target in expected_mounts, "unrecognized private mount: " + service)
            key, writable = expected_mounts[target]
            name = VOLUME_NAMES[key]
            require(mount.get("Type") == "volume" and mount.get("Name") == name and
                    mount.get("Source") == volume_paths[name] and
                    mount.get("RW") is writable,
                    "wrong data/secret role/permissions: " + service)
        require(not host.get("Privileged") and
                host.get("RestartPolicy", {}).get("Name", "no") in ("", "no"),
                "privileged/auto-restarting component: " + service)
        require(not host.get("PidMode") and
                not host.get("IpcMode", "").startswith("container:"),
                "host/process-namespace alias: " + service)
        if stopped:
            require(not obj["State"].get("Running"), "pre-existing running container")
        if running is not None:
            require(bool(obj["State"].get("Running")) == running[service],
                    "unexpected process start/stop order: " + service)
        entries = obj.get("Config", {}).get("Env", [])
        if service == "cpamp-runtime":
            for key, value in RUNTIME_LISTENERS.items():
                require(key + "=" + value in entries,
                        "runtime listener env differs from loopback: " + key)
            require(host.get("NetworkMode") not in ("host", "none"),
                    "runtime host/none network")
            bindings = host.get("PortBindings") or {}
            require(set(bindings) == {f"{port}/tcp"} and
                    bindings[f"{port}/tcp"] == [{"HostIp": "127.0.0.1",
                                                  "HostPort": str(port)}],
                    "public port exposure differs from exact private profile")
        else:
            require(not host.get("PortBindings"), "sidecar published port")
            mode = host.get("NetworkMode", "")
            require(mode.startswith("container:") and
                    mode.removeprefix("container:") in
                    (runtime_id, runtime_id[:12], by_id[runtime_id].get("Name", "").lstrip("/")),
                    "private container not in runtime namespace")
            entries = obj.get("Config", {}).get("Env", [])
            require("CPAMP_PRIVATE_PAH_S0=1" in entries,
                    "private runtime mode missing: " + service)
    # A pre-existing different Ingress using the private namespace or published
    # port cannot be blessed as part of this profile. Fail closed, don't stop it.
    for obj in objects:
        if obj["Id"] in ids.values():
            continue
        h = obj.get("HostConfig", {})
        mode = h.get("NetworkMode", "")
        related = mode in ("container:" + runtime_id,
                           "container:" + runtime_id[:12],
                           "container:" + by_id[runtime_id].get("Name", "").lstrip("/"))
        bindings = h.get("PortBindings") or {}
        published = any(x.get("HostPort") == str(port)
                        for entries in bindings.values() for x in (entries or []))
        aliased = any(aliases_private_path(m, volume_paths)
                      for m in obj.get("Mounts", []))
        require(not (related or published or aliased),
                "foreign container/Ingress/credential mount shares P-S0 exposure")


def inspect_profile(stopped, running=None):
    cfg = json.loads(compose("config", "--format", "json"))
    check_config(cfg)
    ids = {}
    for name in SERVICES:
        selected = compose("ps", "-a", "-q", name).splitlines()
        require(len(selected) == 1, "missing/duplicated profile service: " + name)
        ids[name] = json.loads(command("docker", "inspect", selected[0]))[0]["Id"]
    all_ids = command("docker", "ps", "-aq").splitlines()
    objects = json.loads(command("docker", "inspect", *all_ids))
    image_ids = {name: json.loads(command("docker", "image", "inspect", IMAGES[name]))[0]["Id"]
                 for name in SERVICES}
    volumes = json.loads(command("docker", "volume", "inspect", *VOLUME_NAMES.values()))
    by_name = {v["Name"]: v for v in volumes}
    check_objects(objects, image_ids, ids, stopped=stopped, running=running,
                  volume_paths=check_volume_info(by_name))
    return ids


def probe_manager_resource_deny(container_id):
    """Fresh New is not selected yet, so guard 204 is NOT expected at launch.

    Prove the Manager listener is alive and denies executable resources
    with no-store, before any public Ingress process can start. Ingress
    requires a fresh guard 204 only after Fresh qualification.
    """
    url = "http://127.0.0.1:18319/v0/resource/plugins/pah-s0-proof.js"
    last = ""
    for _ in range(20):
        reply = subprocess.run(
            ["docker", "exec", container_id, "wget", "-S", "-O", "/dev/null", url],
            text=True, capture_output=True, timeout=8, check=False)
        last = (reply.stdout + reply.stderr).lower()
        if "404 not found" in last and "cache-control: no-store" in last:
            return
        time.sleep(0.25)
    raise RuntimeError("P-S0 STOP: Manager did not prove pre-Ingress resource denial: " +
                       last[-350:])


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--launch", action="store_true", help="explicit disposable launch")
    args = parser.parse_args()
    require(os.name == "posix", "not a Unix Host")
    require(os.environ.get("CPAMP_PAH_S0_TRUSTED_HOST") == OPT_IN,
            "explicit private Host opt-in missing")
    require(not compose("ps", "-a", "-q").strip() if args.launch else True,
            "previously created profile; no unattended reuse/restart")
    if args.launch:
        require_fresh_volumes(command("docker", "volume", "ls", "-q").splitlines())
        compose("build")
        # Recheck immediately before create; no pre-existing private state
        # may be silently inherited even if no containers remain.
        require_fresh_volumes(command("docker", "volume", "ls", "-q").splitlines())
        compose("create")
        # Docker's port-binding container is still STOPPED; no Ingress listener
        # has begun serving while image/provenance/namespace is checked.
        inspect_profile(stopped=True)
        try:
            compose("start", "cpamp-runtime", "cpamp-manager")
            checked = inspect_profile(stopped=False, running={
                "cpamp-runtime": True, "cpamp-manager": True,
                "cpamp-ingress": False})
            probe_manager_resource_deny(checked["cpamp-manager"])
            compose("start", "cpamp-ingress")
            inspect_profile(stopped=False, running={s: True for s in SERVICES})
        except Exception:
            # Stop only the new *named* private project, never foreign resources.
            compose("stop", "cpamp-ingress", "cpamp-manager", "cpamp-runtime")
            raise
    else:
        inspect_profile(stopped=False, running={s: True for s in SERVICES})
    print("P-S0 graph checked; NOT Host/network/browser qualification. STOP pending independent witness")


if __name__ == "__main__":
    try:
        main()
    except (RuntimeError, ValueError, KeyError, IndexError) as exc:
        print(str(exc), file=sys.stderr)
        raise SystemExit(1)
