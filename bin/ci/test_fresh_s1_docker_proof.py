"""Reference-spelling regression for the bounded S1 Docker namespace gate."""
import pathlib
import runpy
import unittest
from unittest.mock import patch


namespace_owner = runpy.run_path(str(pathlib.Path(__file__).with_name("fresh-s1-docker-proof.py")))["namespace_owner"]


class NamespaceReferenceTest(unittest.TestCase):
    def test_names_ids_prefixes_and_joined_consumers_resolve_to_same_owner(self):
        runtime_id = "a" * 64
        runtime = {"Id": runtime_id, "HostConfig": {"NetworkMode": "private-network"}}
        peer = {"Id": "b" * 64, "HostConfig": {"NetworkMode": "container:runtime-name"}}
        aliases = {runtime_id: runtime, runtime_id[:12]: runtime, "runtime-name": runtime, "peer-name": peer}

        def inspect(*args):
            import json
            self.assertEqual(args[:2], ("docker", "inspect"))
            return json.dumps([aliases[args[2]]])

        with patch.dict(namespace_owner.__globals__, command=inspect):
            for reference in aliases:
                with self.subTest(reference=reference):
                    consumer = {"Id": "c" * 64, "HostConfig": {"NetworkMode": "container:" + reference}}
                    self.assertEqual(namespace_owner(consumer), runtime_id)
            self.assertNotEqual(namespace_owner({"Id": "d" * 64, "HostConfig": {"NetworkMode": "bridge"}}), runtime_id)


if __name__ == "__main__":
    unittest.main()
