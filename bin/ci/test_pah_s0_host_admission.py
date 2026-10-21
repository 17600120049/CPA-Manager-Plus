"""Offline fail-closed P-S0 Host graph tests. Do not call Docker."""
import copy
import unittest
from pah_s0_host_admission import check_config, check_objects, PROJECT, PORT, SERVICES, IMAGES


def configuration():
    r, m, i = SERVICES
    return {"services": {
        r: {"platform": "linux/amd64", "image": IMAGES[r], "restart": "no",
            "ports": [{"host_ip": "127.0.0.1", "published": PORT,
                       "target": PORT, "protocol": "tcp"}]},
        m: {"platform": "linux/amd64", "image": IMAGES[m], "restart": "no",
            "network_mode": "service:cpamp-runtime",
            "build": {"args": {"VITE_CPAMP_PAH_S0": "1"}},
            "environment": {"CPAMP_PRIVATE_PAH_S0": "1", "HTTP_ADDR": "127.0.0.1:18319"}},
        i: {"platform": "linux/amd64", "image": IMAGES[i], "restart": "no",
            "network_mode": "service:cpamp-runtime",
            "build": {"args": {"CPAMP_PAH_S0_BUILD": "1"}},
            "environment": {"CPAMP_PRIVATE_PAH_S0": "1",
                            "CPAMP_INGRESS_CONTROL_ADDR": "127.0.0.1:9082",
                            "CPAMP_MANAGER_URL": "http://127.0.0.1:18319",
                            "CPAMP_GATEWAY_URL": "http://127.0.0.1:8317"}}
    }}


def objects():
    ids = {s: chr(97 + n) * 64 for n, s in enumerate(SERVICES)}
    images = {s: "sha256:" + chr(100 + n) * 64 for n, s in enumerate(SERVICES)}
    result = []
    for s in SERVICES:
        host = {"NetworkMode": "bridge" if s == "cpamp-runtime" else
                "container:" + ids["cpamp-runtime"],
                "RestartPolicy": {"Name": "no"}, "Privileged": False,
                "PortBindings": {f"{PORT}/tcp": [{"HostIp": "127.0.0.1",
                                                "HostPort": str(PORT)}]}
                if s == "cpamp-runtime" else {}}
        result.append({"Id": ids[s], "Image": images[s], "State": {"Running": False},
                       "HostConfig": host, "Config": {
                           "Labels": {"com.docker.compose.project": PROJECT,
                                      "com.docker.compose.service": s},
                           "Env": ["CPAMP_PRIVATE_PAH_S0=1"]}})
    return result, ids, images


class PrivateGraphTests(unittest.TestCase):
    def test_compose_matrix(self):
        cfg = configuration()
        check_config(cfg)
        for mutation in ("swapped_build", "manager_off", "exposed_manager",
                         "ipv6", "external", "arm"):
            with self.subTest(mutation=mutation):
                obj = copy.deepcopy(cfg)
                srv = obj["services"]
                if mutation == "swapped_build":
                    srv["cpamp-manager"]["build"]["args"]["VITE_CPAMP_PAH_S0"] = "0"
                elif mutation == "manager_off":
                    srv["cpamp-manager"]["environment"]["CPAMP_PRIVATE_PAH_S0"] = "0"
                elif mutation == "exposed_manager":
                    srv["cpamp-manager"]["ports"] = [{"published": 18319, "target": 18319}]
                elif mutation == "ipv6":
                    srv["cpamp-runtime"]["ports"][0]["host_ip"] = "::"
                elif mutation == "external":
                    srv["cpamp-ingress"]["network_mode"] = "bridge"
                elif mutation == "arm":
                    srv["cpamp-manager"]["platform"] = "linux/arm64"
                with self.assertRaises(RuntimeError):
                    check_config(obj)

    def test_images_listener_and_stale_namespace(self):
        graph, ids, images = objects()
        check_objects(graph, images, ids)
        for mutation in ("old_ingress", "sideport", "host_port", "wrong_image",
                         "runtime_restart", "foreign_peer", "prestarted", "missing_optin"):
            with self.subTest(mutation=mutation):
                objs, names, im = copy.deepcopy(graph), dict(ids), dict(images)
                if mutation == "old_ingress":
                    objs[2]["HostConfig"]["NetworkMode"] = "bridge"
                elif mutation == "sideport":
                    objs[1]["HostConfig"]["PortBindings"] = {"18319/tcp": [{"HostPort": "18319"}]}
                elif mutation == "host_port":
                    objs[0]["HostConfig"]["PortBindings"][f"{PORT}/tcp"][0]["HostIp"] = "0.0.0.0"
                elif mutation == "wrong_image":
                    objs[2]["Image"] = "sha256:" + "1" * 64
                elif mutation == "runtime_restart":
                    objs[0]["HostConfig"]["RestartPolicy"]["Name"] = "always"
                elif mutation == "foreign_peer":
                    peer = copy.deepcopy(objs[2])
                    peer["Id"] = "f" * 64
                    objs.append(peer)
                elif mutation == "prestarted":
                    objs[2]["State"]["Running"] = True
                elif mutation == "missing_optin":
                    objs[1]["Config"]["Env"] = []
                with self.assertRaises(RuntimeError):
                    check_objects(objs, im, names)
    def test_post_start_audit_is_not_deployment_qualification(self):
        graph, ids, images = objects()
        for o in graph:
            o["State"]["Running"] = True
        check_objects(graph, images, ids, stopped=False,
                      running={service: True for service in SERVICES})
        graph[2]["State"]["Running"] = False
        with self.assertRaises(RuntimeError):
            check_objects(graph, images, ids, stopped=False,
                          running={service: True for service in SERVICES})


if __name__ == "__main__":
    unittest.main()
