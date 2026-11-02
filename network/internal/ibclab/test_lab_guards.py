"""Guards for the real-process lab; these do not simulate IBC success."""
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("hermes_lab", Path(__file__).with_name("hermes_lab.py"))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)


class LabGuards(unittest.TestCase):
    def test_real_cli_zero_exit_success_summary_cannot_mask_chain_health_failure(self):
        warning = {"level": "WARN", "fields": {"message": "chain is not healthy"}}
        summary = {"result": "performed health check for all chains in the config", "status": "success"}
        output = json.dumps(warning) + "\n" + json.dumps(summary)
        with self.assertRaisesRegex(RuntimeError, "health warnings"):
            lab.health_check(lambda *args: output)

    def test_namespace_guard_refuses_initial_network_namespace(self):
        # This check is intentionally performed in the host namespace.
        if not Path("/proc/1/ns/net").exists():
            self.skipTest("Linux namespace API required")
        if lab.os.readlink("/proc/self/ns/net") != lab.os.readlink("/proc/1/ns/net"):
            self.skipTest("negative control requires initial namespace")
        with self.assertRaisesRegex(RuntimeError, "initial network namespace"):
            lab.private_namespace()


if __name__ == "__main__":
    unittest.main()
