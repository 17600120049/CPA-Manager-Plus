"""Offline P-S0 Host profile tests. No Docker, network or privileged mutation."""
import copy
import unittest
from pah_s0_host_admission import (
    check_config, check_objects, check_volume_info, require_fresh_volumes,
    PROJECT, PORT, SERVICES, IMAGES, VOLUMES, VOLUME_NAMES, MOUNTS,
    RUNTIME_LISTENERS,
)


def configuration():
    r, m, i = SERVICES
    return {"volumes": {v: {"name": VOLUME_NAMES[v]} for v in VOLUMES},
            "services": {
        r: {"platform": "linux/amd64", "image": IMAGES[r], "restart": "no",
            "build": {"dockerfile": "Dockerfile.runtime", "target": "v8-amd64-loopback-proof"},
            "ports": [{"host_ip": "127.0.0.1", "published": PORT,
                       "target": PORT, "protocol": "tcp"}],
            "environment": dict(RUNTIME_LISTENERS),
            "volumes": [{"type": "volume", "source": key, "target": dest,
                         "read_only": not writable}
                        for dest, (key, writable) in MOUNTS[r].items()]},
        m: {"platform": "linux/amd64", "image": IMAGES[m], "restart": "no",
            "network_mode": "service:cpamp-runtime",
            "build": {"dockerfile": "Dockerfile.manager-server",
                      "args": {"VITE_CPAMP_PAH_S0": "1"}},
            "environment": {"CPAMP_PRIVATE_PAH_S0": "1",
                            "HTTP_ADDR": "127.0.0.1:18319"},
            "volumes": [{"type": "volume", "source": key, "target": dest,
                         "read_only": not writable}
                        for dest, (key, writable) in MOUNTS[m].items()]},
        i: {"platform": "linux/amd64", "image": IMAGES[i], "restart": "no",
            "network_mode": "service:cpamp-runtime",
            "build": {"dockerfile": "Dockerfile.ingress",
                      "args": {"CPAMP_PAH_S0_BUILD": "1"}},
            "environment": {"CPAMP_PRIVATE_PAH_S0": "1",
                            "CPAMP_INGRESS_CONTROL_ADDR": "127.0.0.1:9082",
                            "CPAMP_MANAGER_URL": "http://127.0.0.1:18319",
                            "CPAMP_GATEWAY_URL": "http://127.0.0.1:8317"},
            "volumes": []},
    }}


def objects():
    ids = {s: chr(97+n) * 64 for n, s in enumerate(SERVICES)}
    images = {s: "sha256:" + chr(100+n) * 64 for n, s in enumerate(SERVICES)}
    result = []
    for s in SERVICES:
        mounts = [{"Type": "volume", "Name": VOLUME_NAMES[key],
                   "Source": "/docker/volumes/" + VOLUME_NAMES[key] + "/_data",
                   "Destination": dest, "RW": writable}
                  for dest, (key, writable) in MOUNTS[s].items()]
        host = {"NetworkMode": "bridge" if s == "cpamp-runtime"
                else "container:" + ids["cpamp-runtime"],
                "RestartPolicy": {"Name": "no"}, "Privileged": False,
                "PortBindings": {f"{PORT}/tcp": [{"HostIp": "127.0.0.1",
                                                 "HostPort": str(PORT)}]}
                if s == "cpamp-runtime" else {}}
        environment = ["CPAMP_PRIVATE_PAH_S0=1"]
        if s == "cpamp-runtime":
            environment += [k+"="+v for k,v in RUNTIME_LISTENERS.items()]
        result.append({"Id": ids[s], "Image": images[s], "State": {"Running": False},
                       "HostConfig": host, "Mounts": mounts, "Config": {
                           "Labels": {"com.docker.compose.project": PROJECT,
                                      "com.docker.compose.service": s},
                           "Env": environment}})
    return result, ids, images


class PrivateGraphTests(unittest.TestCase):
    def test_compose_matrix(self):
        check_config(configuration())
        for mutation in ("build", "manager_off", "side_port", "ipv6", "namespace",
                         "arm", "cpa_wildcard", "runtime_wildcard", "secret_writable",
                         "extra_volume", "volume_alias", "external_volume", "runtime_recipe"):
            with self.subTest(mutation=mutation):
                cfg = configuration()
                srv = cfg["services"]
                if mutation == "build":
                    srv["cpamp-manager"]["build"]["args"]["VITE_CPAMP_PAH_S0"] = "0"
                elif mutation == "manager_off":
                    srv["cpamp-manager"]["environment"]["CPAMP_PRIVATE_PAH_S0"] = "0"
                elif mutation == "side_port":
                    srv["cpamp-manager"]["ports"] = [{"published": 18319, "target": 18319}]
                elif mutation == "ipv6":
                    srv["cpamp-runtime"]["ports"][0]["host_ip"] = "::"
                elif mutation == "namespace":
                    srv["cpamp-ingress"]["network_mode"] = "bridge"
                elif mutation == "arm":
                    srv["cpamp-manager"]["platform"] = "linux/arm64"
                elif mutation == "cpa_wildcard":
                    srv["cpamp-runtime"]["environment"]["CPAMP_RUNTIME_CPA_ADDR"] = "0.0.0.0:8317"
                elif mutation == "runtime_wildcard":
                    srv["cpamp-runtime"]["environment"]["CPAMP_RUNTIME_ADDR"] = "0.0.0.0:9081"
                elif mutation == "secret_writable":
                    srv["cpamp-manager"]["volumes"][1]["read_only"] = False
                elif mutation == "extra_volume":
                    srv["cpamp-ingress"]["volumes"] = [{
                        "type": "volume", "source": "cpamp-pah-s0-runtime-secret",
                        "target": "/secrets"}]
                elif mutation == "volume_alias":
                    cfg["volumes"]["cpamp-pah-s0-runtime-secret"]["name"] = "foreign-credential-volume"
                elif mutation == "external_volume":
                    cfg["volumes"]["cpamp-pah-s0-manager-data"]["external"] = True
                elif mutation == "runtime_recipe":
                    srv["cpamp-runtime"]["build"]["target"] = "latest-unverified-runtime"
                with self.assertRaises(RuntimeError):
                    check_config(cfg)

    def test_actual_graph_and_foreign_mounts(self):
        graph, ids, images = objects()
        check_objects(graph, images, ids)
        for mutation in ("old_ingress", "side_port", "host_port", "wrong_image",
                         "auto_restart", "foreign_peer", "foreign_mount",
                         "foreign_bind", "manager_secret_rw", "runtime_wildcard",
                         "cpa_wildcard", "prestarted"):
            with self.subTest(mutation=mutation):
                objs = copy.deepcopy(graph)
                if mutation == "old_ingress":
                    objs[2]["HostConfig"]["NetworkMode"] = "bridge"
                elif mutation == "side_port":
                    objs[1]["HostConfig"]["PortBindings"] = {"18319/tcp": [{"HostPort": "18319"}]}
                elif mutation == "host_port":
                    objs[0]["HostConfig"]["PortBindings"][f"{PORT}/tcp"][0]["HostIp"] = "0.0.0.0"
                elif mutation == "wrong_image":
                    objs[2]["Image"] = "sha256:" + "1"*64
                elif mutation == "auto_restart":
                    objs[0]["HostConfig"]["RestartPolicy"]["Name"] = "always"
                elif mutation in ("foreign_peer", "foreign_mount", "foreign_bind"):
                    peer = copy.deepcopy(objs[2]); peer["Id"] = "f"*64
                    if mutation != "foreign_peer":
                        peer["HostConfig"]["NetworkMode"] = "bridge"
                        peer["Mounts"] = ([copy.deepcopy(objs[1]["Mounts"][1])]
                                          if mutation == "foreign_mount" else [{
                                              "Type": "bind", "Source": "/docker/volumes",
                                              "Destination": "/private", "RW": True}])
                    objs.append(peer)
                elif mutation == "manager_secret_rw":
                    objs[1]["Mounts"][1]["RW"] = True
                elif mutation == "runtime_wildcard":
                    objs[0]["Config"]["Env"] = [e.replace(
                        "127.0.0.1:9081", "0.0.0.0:9081") for e in objs[0]["Config"]["Env"]]
                elif mutation == "cpa_wildcard":
                    objs[0]["Config"]["Env"] = [e.replace(
                        "127.0.0.1:8317", "0.0.0.0:8317") for e in objs[0]["Config"]["Env"]]
                elif mutation == "prestarted":
                    objs[2]["State"]["Running"] = True
                with self.assertRaises(RuntimeError):
                    check_objects(objs, images, ids)

    def test_fresh_volume_reuse_and_ownership(self):
        require_fresh_volumes([])
        with self.assertRaises(RuntimeError):
            require_fresh_volumes([VOLUME_NAMES["cpamp-pah-s0-runtime-secret"]])
        samples = {name: {"Name": name,
                          "Mountpoint": "/docker/volumes/" + name + "/_data",
                          "Driver": "local",
                          "Labels": {"com.docker.compose.project": PROJECT}}
                   for name in VOLUME_NAMES.values()}
        self.assertEqual(set(check_volume_info(samples)), set(samples))
        bad = copy.deepcopy(samples)
        bad[VOLUME_NAMES["cpamp-pah-s0-manager-data"]]["Labels"]["com.docker.compose.project"] = "other"
        with self.assertRaises(RuntimeError):
            check_volume_info(bad)

    def test_stale_ingress_after_restart(self):
        graph, ids, images = objects()
        for obj in graph:
            obj["State"]["Running"] = True
        check_objects(graph, images, ids, stopped=False,
                      running={s: True for s in SERVICES})
        graph[2]["State"]["Running"] = False
        with self.assertRaises(RuntimeError):
            check_objects(graph, images, ids, stopped=False,
                          running={s: True for s in SERVICES})


if __name__ == "__main__":
    unittest.main()
