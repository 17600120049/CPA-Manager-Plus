"""Offline Host protocol/topology tests only; never invoke Docker."""
import copy
import json
import pathlib
import sys
import tempfile
import unittest

sys.path.insert(0, str(pathlib.Path(__file__).parent))
from fresh_h1_proof import Host, intent, parent, validate_graph


class H1HostTest(unittest.TestCase):
    def objects(self):
        runtime = dict(Id="a"*64, HostConfig=dict(NetworkMode="h1", Privileged=False, PortBindings={}, RestartPolicy={"Name": "no"}),
                       Mounts=[dict(Type="volume", Name="source", Source="/docker/volumes/source/_data", RW=True)])
        probe = dict(Id="b"*64, HostConfig=dict(NetworkMode="container:"+runtime["Id"], Privileged=False, PortBindings={}, RestartPolicy={"Name": "no"}), Mounts=[])
        return [runtime, probe]

    def graph(self, objects):
        validate_graph(objects, {"source": "/docker/volumes/source/_data"}, {"a"*64, "b"*64}, "a"*64, lambda _: "a"*64)

    def test_graph_detects_alias_namespace_restart_and_second_source(self):
        self.graph(self.objects())
        for case in ("alias", "namespace", "restart", "source", "privileged", "ports"):
            with self.subTest(case=case):
                objects = self.objects()
                if case == "alias":
                    obj = copy.deepcopy(objects[1]); obj["Id"] = "c"*64; obj["HostConfig"]["NetworkMode"] = "bridge"
                    obj["Mounts"] = [dict(Type="bind", Source="/docker/volumes", RW=True)]
                    objects.append(obj)
                elif case == "namespace":
                    obj = copy.deepcopy(objects[1]); obj["Id"] = "c"*64; objects.append(obj)
                elif case == "restart": objects[0]["HostConfig"]["RestartPolicy"]["Name"] = "always"
                elif case == "source": objects[1]["Mounts"] = copy.deepcopy(objects[0]["Mounts"])
                elif case == "privileged": objects[1]["HostConfig"]["Privileged"] = True
                elif case == "ports": objects[1]["HostConfig"]["PortBindings"] = {"8317/tcp": [{"HostPort": "8317"}]}
                with self.assertRaises(AssertionError): self.graph(objects)

    def test_original_config_request_and_value_are_in_host_delegation(self):
        a = intent("1"*32, "config", "retry", value=0)
        self.assertEqual(parent(a), "c:"+"1"*32)
        self.assertNotEqual(a, intent("2"*32, "config", "retry", value=0))
        self.assertNotEqual(a, intent("1"*32, "config", "retry", value=1))

    def test_exact_stop_reap_and_unknown_when_unprovable(self):
        with tempfile.TemporaryDirectory() as tmp:
            calls = []
            state = dict(Running=True, Restarting=False, Pid=42)
            cid = "a"*64
            def command(*args, **kwargs):
                calls.append(args)
                if args[:2] == ("docker", "inspect"): return json.dumps([dict(Id=cid, State=state)])
                if args[:2] == ("docker", "stop"): raise RuntimeError("Supervisor unresponsive")
                if args[:2] == ("docker", "kill"): state.update(Running=False, Pid=0)
                return "0"
            h = Host(command, http=None, name="h1-test", runtime="runtime", probe="probe", token="not-exported", images={}, volumes=[], containers=[], out=pathlib.Path(tmp))
            h.track("runtime", cid)
            h.stop_exact(cid)
            self.assertEqual([a[1] for a in calls], ["inspect", "stop", "kill", "wait", "inspect"])
            self.assertTrue(all(a[-1] == cid for a in calls))
            with self.assertRaises(AssertionError): h.stop_exact("b"*64)
            state.update(Running=False, Pid=42)
            with self.assertRaises(AssertionError): h.stop_exact(cid)
            self.assertNotIn("not-exported", (pathlib.Path(tmp)/"h1-owner.json").read_text())


if __name__ == "__main__":
    unittest.main()
