#!/usr/bin/env python3
"""Offline unit tests; no Docker daemon or registry required."""
import importlib.util
import json
from pathlib import Path
import unittest
from unittest import mock

P = Path(__file__).with_name('check-v2-private-candidate.py')
spec = importlib.util.spec_from_file_location('v2_candidate', P)
audit = importlib.util.module_from_spec(spec)
spec.loader.exec_module(audit)


class AuditTest(unittest.TestCase):
    def test_artifact_requires_exact_pinned_v8(self):
        v = {'schemaVersion': 1, 'engine': 'cpa', 'version': '8.0.13',
             'artifactId': 'sha256:' + audit.CPA_EXECUTABLE_SHA256}
        self.assertEqual(audit.check_artifact(json.dumps(v)), v)
        for field, bad in [('version', '7.3.3'), ('artifactId', 'sha256:deadbeef'),
                           ('engine', 'cpamp'), ('schemaVersion', 2)]:
            broken = v.copy()
            broken[field] = bad
            with self.subTest(field=field), self.assertRaises(ValueError):
                audit.check_artifact(json.dumps(broken))

    def test_platform_and_image_id_fail_closed(self):
        value = {'Os': 'linux', 'Architecture': 'amd64', 'Id': 'sha256:' + 'a' * 64,
                 'RepoDigests': ['test/image@sha256:' + 'b' * 64]}
        with mock.patch.object(audit, 'command', return_value=json.dumps([value])):
            r = audit.inspect_image('example:test')
            self.assertEqual(r['imageConfigId'], value['Id'])
        for change in ({'Architecture': 'arm64'}, {'Os': 'windows'},
                       {'Id': 'sha256:unverified'}, {'Config': {'Volumes': {'/data': {}}}}):
            with self.subTest(change=change), mock.patch.object(audit, 'command',
                       return_value=json.dumps([{**value, **change}])), self.assertRaises(ValueError):
                audit.inspect_image('example:wrong')

    def test_audit_creates_from_inspected_id_not_mutable_tag(self):
        image_id = 'sha256:' + 'a' * 64
        with mock.patch.object(audit, 'inspect_image', return_value={'imageConfigId': image_id}), \
             mock.patch.object(audit, 'command', return_value='container-id') as cmd, \
             mock.patch.object(audit, 'extract_file', return_value='b' * 64):
            from tempfile import TemporaryDirectory
            with TemporaryDirectory() as td:
                record = audit.audit_one('manager', 'mutable:latest', Path(td))
        self.assertEqual(record['imageConfigId'], image_id)
        self.assertEqual(record['files']['/usr/local/bin/cpa-manager-plus'], 'b' * 64)
        create = [call.args for call in cmd.call_args_list if call.args[:2] == ('docker', 'create')]
        self.assertEqual(len(create), 1)
        self.assertEqual(create[0][-1], image_id)
        self.assertNotIn('mutable:latest', create[0])

    def test_stopped_container_removed_on_cp_failure(self):
        with mock.patch.object(audit, 'inspect_image', return_value={'imageConfigId': 'sha256:' + 'a'*64}), \
             mock.patch.object(audit, 'command', return_value='123') as cmd, \
             mock.patch.object(audit, 'extract_file', side_effect=ValueError('missing binary')):
            from tempfile import TemporaryDirectory
            with TemporaryDirectory() as td, self.assertRaises(ValueError):
                audit.audit_one('manager', 'example:test', Path(td))
            self.assertEqual(cmd.call_args_list[-1].args[:3], ('docker', 'rm', '-f'))
            self.assertEqual(cmd.call_args_list[0].args[:3], ('docker', 'create', '--name'))


if __name__ == '__main__':
    unittest.main()
