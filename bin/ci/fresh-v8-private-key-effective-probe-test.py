#!/usr/bin/env python3
"""Scoped offline regressions; no Docker, network or real credentials."""

import contextlib
import copy
import importlib.util
import io
import json
import pathlib
import unittest
from unittest.mock import patch

spec = importlib.util.spec_from_file_location(
    "probe", pathlib.Path(__file__).with_name("fresh-v8-private-key-effective-probe.py"))
probe = importlib.util.module_from_spec(spec)
spec.loader.exec_module(probe)


class ProbeTests(unittest.TestCase):
    def observe(self, states, budget=15):
        self.clock = 0
        self.calls = 0
        self.evidence = {"events": [], "_started": 0}

        def sleep(seconds):
            self.clock += seconds

        def cpa(*_):
            state = states[min(self.calls, len(states) - 1)]
            self.calls += 1
            return state

        with patch.object(probe.time, "monotonic", side_effect=lambda: self.clock), \
                patch.object(probe.time, "sleep", side_effect=sleep), \
                patch.object(probe, "check_ingress"), \
                patch.object(probe, "check_cpa", side_effect=cpa):
            probe.await_auth(8317, "replace", {"old": 401, "new": 200, "absent": 401},
                             {"old": 200, "new": 401, "absent": 401},
                             {"old": "synthetic-old", "new": "synthetic-new", "absent": None},
                             28471, self.evidence, budget)

    def test_single_matching_sweep_does_not_pass(self):
        expected = {"old": 401, "new": 200, "absent": 401}
        self.observe([expected])
        self.assertGreaterEqual(self.calls, 9)
        self.assertGreaterEqual(self.clock, 2)
        self.assertEqual(self.evidence["events"][-1]["action"], "stable-window")

    def test_old_state_then_stable_is_recorded(self):
        self.observe([{"old": 200, "new": 401, "absent": 401},
                      {"old": 401, "new": 200, "absent": 401}])
        sweeps = [e for e in self.evidence["events"] if e["action"] == "auth-sweep"]
        self.assertEqual(sweeps[0]["observation"], "transition")
        self.assertEqual(sweeps[1]["observation"], "candidate")

    def test_regression_stops(self):
        with self.assertRaisesRegex(probe.StopProof, "regressed"):
            self.observe([{"old": 401, "new": 200, "absent": 401},
                          {"old": 200, "new": 401, "absent": 401}])

    def test_partial_regression_stops(self):
        with self.assertRaisesRegex(probe.StopProof, "regressed"):
            self.observe([{"old": 401, "new": 401, "absent": 401},
                          {"old": 200, "new": 401, "absent": 401}])

    def test_absent_key_fail_open_during_replace_stops(self):
        with self.assertRaisesRegex(probe.StopProof, "Unexpected"):
            self.observe([{"old": 200, "new": 401, "absent": 200}])

    def test_unexpected_status_stops(self):
        for status in (302, 403, 429, 500, 503):
            with self.subTest(status=status), self.assertRaises(probe.StopProof):
                self.observe([{"old": status, "new": 401, "absent": 401}])

    def test_never_converges_stops(self):
        with self.assertRaisesRegex(probe.StopProof, "timed out"):
            self.observe([{"old": 200, "new": 401, "absent": 401}], budget=3)

    def test_match_after_deadline_does_not_pass(self):
        with self.assertRaisesRegex(probe.StopProof, "timed out"):
            self.observe([{"old": 401, "new": 200, "absent": 401}], budget=1)

    def test_pending_must_be_exactly_503(self):
        for status in (200, 401, 404, 502):
            with self.subTest(status=status), patch.object(probe, "request", return_value=(status, b"")), \
                    self.assertRaises(probe.StopProof):
                probe.check_ingress(28471, "test", {"events": [], "_started": 0,
                                                   "_latest_secret": "synthetic"})

    def test_uncertain_write_has_no_retry_or_rollback(self):
        with patch.object(probe, "request", side_effect=[(200, b"[]"), probe.StopProof("uncertain")]) as call, \
                self.assertRaises(probe.StopProof):
            probe.set_only_client_keys(8317, "private", ["synthetic"], [], "install",
                                       {"events": [], "_started": 0})
        self.assertEqual(call.call_count, 2)

    def test_foreign_key_change_prevents_write(self):
        with patch.object(probe, "request", return_value=(200, b'["foreign"]')) as call, \
                self.assertRaisesRegex(probe.StopProof, "ownership changed"):
            probe.set_only_client_keys(8317, "private", ["synthetic"], [], "install",
                                       {"events": [], "_started": 0})
        self.assertEqual(call.call_count, 1)

    def test_unsafe_config_is_rejected_before_writes(self):
        cfg = {"config-version": 8, "server": {"host": "127.0.0.1", "port": 8317},
               "management": {"allow-remote": False, "secret-key": "hashed-synthetic",
                              "disable-control-panel": True, "disable-auto-update-panel": True},
               "access": {"api-keys": []}, "oauth": {"auth-dir": "/runtime/gateway/auth"},
               "plugins": {"enabled": False, "dir": "/runtime/gateway/plugins"},
               "observability": {"logs": {"logging-to-file": False},
                                 "usage": {"usage-statistics-enabled": False}}}
        changes = [("gemini-api-key", ["synthetic-provider"]),
                   ("openai-compatibility", [{"base-url": "http://example.invalid"}]),
                   ("ampcode", {"upstream-url": "http://example.invalid"}),
                   ("api-keys", {"codex": ["synthetic-provider"]}),
                   ("providers", {"codex": {}}), ("oauth", {"providers": {"codex": {}}}),
                   ("server", {"host": "0.0.0.0", "port": 8317}),
                   ("plugins", {"enabled": True}), ("access", {"api-keys": ["existing"]}),
                   ("management", {"allow-remote": True}), ("oauth", None), ("server", [])]
        args = type("Args", (), {"cpa_port": 8317})()
        for name, value in changes:
            bad = copy.deepcopy(cfg)
            bad[name] = value
            with self.subTest(field=name), patch.object(probe, "json_value", return_value=bad), \
                    patch.object(probe, "set_only_client_keys") as write, \
                    self.assertRaises(probe.StopProof):
                probe.run(args, "private", {"events": [], "_started": 0})
            write.assert_not_called()

    def test_preflight_stop_preserves_sanitized_evidence(self):
        output = io.StringIO()
        private_key = "synthetic-management-" + "x" * 40
        env = {"CPAMP_PRIVATE_CPA_KEY_PROOF": "1", "CPAMP_PROOF_DISPOSABLE_VOLUME": "1",
               "CPAMP_PROOF_CPA_MANAGEMENT_KEY": private_key}
        with patch.dict(probe.os.environ, env), patch.object(probe.sys, "argv", ["probe"]), \
                patch.object(probe, "request", return_value=(200, b"[]")), \
                contextlib.redirect_stdout(output), self.assertRaises(probe.StopProof):
            probe.main()
        report = json.loads(output.getvalue())
        self.assertEqual(report["result"], "STOP")
        self.assertEqual(report["events"][0]["status"], 200)
        self.assertNotIn(private_key, output.getvalue())
        self.assertNotIn("_started", report)


if __name__ == "__main__":
    unittest.main()
