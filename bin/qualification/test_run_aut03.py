"""Runner guard tests; synthetic inputs are never runtime qualification evidence."""
import importlib.util
from pathlib import Path
import sys
import tempfile
import unittest
from unittest.mock import Mock, patch

sys.dont_write_bytecode = True
spec = importlib.util.spec_from_file_location("run_aut03", Path(__file__).with_name("run-aut03.py"))
module = importlib.util.module_from_spec(spec)
spec.loader.exec_module(module)


class RunnerGuards(unittest.TestCase):
    def test_dirty_source_cannot_build_or_mint_evidence(self):
        with tempfile.TemporaryDirectory() as root:
            output = Path(root) / "evidence"
            shared, materializer = Mock(), Mock()
            shared.capture.return_value = " M apps/manager-server/internal/service/legacyartifact/aut03_qualification_test.go"
            argv = ["run-aut03.py", "--linux", "--sealed-archive", "unused", "--output", str(output)]
            with patch.object(sys, "argv", argv), patch.object(module, "load", side_effect=[shared, materializer]):
                with self.assertRaises(SystemExit) as caught:
                    module.main()
            self.assertEqual(caught.exception.code, 2)
            self.assertFalse(output.exists())
            shared.run.assert_not_called()
            materializer.materialize.assert_not_called()

    def test_existing_output_is_never_overwritten(self):
        with tempfile.TemporaryDirectory() as root:
            output = Path(root)
            sentinel = output / "unrelated"
            sentinel.write_bytes(b"preserve")
            shared, materializer = Mock(), Mock()
            argv = ["run-aut03.py", "--linux", "--sealed-archive", "unused", "--output", str(output)]
            with patch.object(sys, "argv", argv), patch.object(module, "load", side_effect=[shared, materializer]):
                with self.assertRaises(SystemExit):
                    module.main()
            self.assertEqual(sentinel.read_bytes(), b"preserve")
            shared.capture.assert_not_called()
            shared.run.assert_not_called()
            materializer.materialize.assert_not_called()


if __name__ == "__main__":
    unittest.main()
