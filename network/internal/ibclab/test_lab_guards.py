"""Guards for the real-process lab; these do not simulate IBC success."""
import importlib.util
import io
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import Mock, patch

spec = importlib.util.spec_from_file_location("hermes_lab", Path(__file__).with_name("hermes_lab.py"))
lab = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lab)
daemon_spec = importlib.util.spec_from_file_location("mcnd_process_smoke", Path(__file__).parents[2] / "tools/mcnd_process_smoke.py")
daemon = importlib.util.module_from_spec(daemon_spec)
daemon_spec.loader.exec_module(daemon)


class LabGuards(unittest.TestCase):
    def test_real_cli_zero_exit_success_summary_cannot_mask_chain_health_failure(self):
        warning = {"level": "WARN", "fields": {"message": "chain is not healthy"}}
        summary = {"result": "performed health check for all chains in the config", "status": "success"}
        output = json.dumps(warning) + "\n" + json.dumps(summary)
        with self.assertRaisesRegex(RuntimeError, "health warnings"):
            lab.health_check(lambda *args: output)

    def test_namespace_guard_refuses_initial_network_namespace(self):
        # This check is intentionally performed in the host namespace.
        try:
            same = lab.os.readlink("/proc/self/ns/net") == lab.os.readlink("/proc/1/ns/net")
        except OSError as error:
            self.skipTest(f"Host namespace identity unavailable: {error}")
        if not same:
            self.skipTest("negative control requires initial namespace")
        with self.assertRaisesRegex(RuntimeError, "initial network namespace"):
            lab.private_namespace()

    @unittest.skipUnless(lab.os.name == "posix", "Linux namespace API required")
    def test_equal_namespace_identity_is_rejected_before_commands(self):
        with patch.object(lab.os, "readlink", return_value="net:[42]"), patch.object(lab.subprocess, "run") as run:
            with self.assertRaisesRegex(RuntimeError, "initial network namespace"):
                lab.private_namespace()
            run.assert_not_called()

    @unittest.skipUnless(lab.os.name == "posix", "Linux namespace API required")
    def test_unreadable_namespace_identity_fails_closed(self):
        with patch.object(lab.os, "readlink", side_effect=PermissionError("denied")), patch.object(lab.subprocess, "run") as run:
            with self.assertRaises(PermissionError):
                lab.private_namespace()
            run.assert_not_called()


class FakeChild:
    """No PID, OS signal or executable is involved in these lifecycle controls."""
    def __init__(self, fail_stop=False):
        self.running, self.fail_stop, self.calls = True, fail_stop, []

    def poll(self):
        return None if self.running else 0

    def terminate(self):
        self.calls.append("terminate")
        if self.fail_stop:
            raise RuntimeError("ordinary stop failure")

    def send_signal(self, _):
        self.terminate()

    def kill(self):
        self.calls.append("kill")
        self.running = False

    def wait(self, timeout):
        self.calls.append("wait")
        self.running = False
        return 0


class SignalBetweenChildren(list):
    def __init__(self, children, callback):
        super().__init__(children)
        self.callback = callback

    def __reversed__(self):
        iterator = super().__reversed__()
        yield next(iterator)
        self.callback()  # Outside every per-child stop/except block.
        yield from iterator


class ProcessLifecycleGuards(unittest.TestCase):
    def setUp(self):
        # Both imported helpers use this same stdlib module. Every real spawn
        # is forbidden; individual command tests replace run with fake results.
        self.spawn_guard = patch.object(subprocess, "Popen", side_effect=AssertionError("real child forbidden in unit tests"))
        self.run_guard = patch.object(subprocess, "run", side_effect=AssertionError("real command forbidden in unit tests"))
        self.spawn_guard.start()
        self.run_guard.start()
        self.addCleanup(self.spawn_guard.stop)
        self.addCleanup(self.run_guard.stop)

    def owners(self):
        return [(lab, lab.Lab(Path("unused"), {}, {})), (daemon, daemon.ProcessOwner())]

    def test_cleanup_attempts_every_owned_child_and_handle_after_failure(self):
        for module, owner in self.owners():
            with self.subTest(module=module.__name__):
                first, broken, last, unrelated = FakeChild(), FakeChild(True), FakeChild(), FakeChild()
                good_handle, bad_handle = Mock(), Mock()
                bad_handle.close.side_effect = OSError("ordinary close failure")
                owner.children = [first, broken, last]
                owner.handles = [bad_handle, good_handle]
                with self.assertRaisesRegex(RuntimeError, "owned process cleanup failed"):
                    owner.close()
                for child in (first, broken, last):
                    self.assertFalse(child.running)
                self.assertIn("kill", broken.calls)
                good_handle.close.assert_called_once()
                bad_handle.close.assert_called_once()
                self.assertEqual(unrelated.calls, [], "unregistered process must not be signalled")

    def test_signal_callback_unwinds_once_through_only_fake_owned_children(self):
        for module, owner in self.owners():
            with self.subTest(module=module.__name__):
                child, unrelated = FakeChild(), FakeChild()
                owner.children = [child]
                with patch.object(module.signal, "signal") as install:
                    guard = module.SignalGuard()
                    with self.assertRaises(module.TerminationRequested):
                        with guard:
                            try:
                                guard.handle(module.signal.SIGTERM, None)
                            finally:
                                guard.handle(module.signal.SIGINT, None)  # No second interruption during cleanup.
                                owner.close()
                    self.assertEqual(guard.requested, module.signal.SIGTERM)
                    self.assertEqual(install.call_count, 4, "install and restore both handlers")
                self.assertFalse(child.running)
                self.assertEqual(unrelated.calls, [])

    def test_private_environment_contains_child_home_and_all_temp_locations(self):
        for module in (lab, daemon):
            with self.subTest(module=module.__name__), tempfile.TemporaryDirectory() as directory:
                root = Path(directory)
                with patch.dict(module.os.environ, {"SYNTHETIC_SECRET_ENV": "not-for-child"}):
                    env = module.private_environment(root)
                self.assertNotIn("SYNTHETIC_SECRET_ENV", env)
                for key in ("HOME", "TMPDIR", "TMP", "TEMP"):
                    self.assertTrue(Path(env[key]).is_dir())
                    self.assertTrue(Path(env[key]).is_relative_to(root))

    def test_signal_while_spawn_returns_cannot_skip_child_registration(self):
        for module in (lab, daemon):
            with self.subTest(module=module.__name__), tempfile.TemporaryDirectory() as directory:
                root, guard, child = Path(directory), module.SignalGuard(), FakeChild()
                owner = lab.Lab(root, {}, {}, guard) if module is lab else daemon.ProcessOwner(guard)
                def fake_spawn(*_, **__):
                    guard.handle(module.signal.SIGTERM, None)
                    return child
                with patch.object(module.subprocess, "Popen", side_effect=fake_spawn):
                    with self.assertRaises(module.TerminationRequested):
                        if module is lab:
                            owner.spawn(["fake"], "fake-child")
                        else:
                            owner.spawn(["fake"], root / "fake-child.log", {})
                self.assertEqual(owner.children, [child])
                owner.close()
                self.assertFalse(child.running)
                self.assertTrue(all(handle.closed for handle in owner.handles))

    def test_first_signal_during_cleanup_still_closes_all_owned_resources(self):
        for module in (lab, daemon):
            with self.subTest(module=module.__name__):
                guard = module.SignalGuard()
                owner = lab.Lab(Path("unused"), {}, {}, guard) if module is lab else daemon.ProcessOwner(guard)
                first, second, unrelated = FakeChild(), FakeChild(), FakeChild()
                handle = Mock()
                owner.children = SignalBetweenChildren([first, second], lambda: guard.handle(module.signal.SIGTERM, None))
                owner.handles = [handle]
                with self.assertRaises(module.TerminationRequested):
                    owner.close()
                self.assertFalse(first.running)
                self.assertFalse(second.running)
                self.assertEqual(unrelated.calls, [])
                handle.close.assert_called_once()

    def test_hermes_key_imports_always_mask_command_errors(self):
        marker = "SYNTHETIC_PRIVATE_MARKER"
        for chain, path in ((lab.A_ID, "m/44'/60'/0'/0/0"), (lab.B_ID, "m/44'/118'/0'/0/0")):
            with self.subTest(chain=chain), patch.object(lab.subprocess, "run", return_value=subprocess.CompletedProcess(["fake"], 1, marker, marker)):
                with self.assertRaisesRegex(RuntimeError, "output suppressed") as failure:
                    lab.import_hermes_key("fake-hermes", "fake-config", chain, "synthetic-mnemonic-file", path, {})
                self.assertNotIn(marker, str(failure.exception))
        with patch.object(lab.subprocess, "run", side_effect=subprocess.TimeoutExpired(["fake"], 1, output=marker, stderr=marker)):
            with self.assertRaisesRegex(RuntimeError, "output suppressed") as failure:
                lab.import_hermes_key("fake-hermes", "fake-config", lab.A_ID, "synthetic-mnemonic-file", "unused", {})
            self.assertNotIn(marker, str(failure.exception))

    def test_daemon_key_generation_masks_failure_and_timeout(self):
        marker = "SYNTHETIC_PRIVATE_MARKER"
        with patch.object(daemon.subprocess, "run", return_value=subprocess.CompletedProcess(["fake"], 1, marker, marker)):
            with self.assertRaisesRegex(RuntimeError, "suppressed") as failure:
                daemon.run_command("fake", Path("unused"), ["keys", "add"], {}, secret=True)
            self.assertNotIn(marker, str(failure.exception))
        with patch.object(daemon.subprocess, "run", side_effect=subprocess.TimeoutExpired(["fake"], 1, output=marker, stderr=marker)):
            with self.assertRaisesRegex(RuntimeError, "suppressed") as failure:
                daemon.run_command("fake", Path("unused"), ["keys", "add"], {}, secret=True)
            self.assertNotIn(marker, str(failure.exception))

    def test_hermes_cleanup_failure_cannot_preserve_success_or_release_flag(self):
        def completed_fake_suite(owner, *_):
            owner.report.update(status="TWO_PROCESS_HERMES_PASS", release_gate=True)
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            args = ["--mode", "full", "--reference-binary", "fake-reference", "--reference-sha256", "0" * 64,
                    "--reference-source-commit", "0" * 40, "--mcnd-binary", "fake-mcnd", "--mcnd-sha256", "0" * 64,
                    "--mcnd-source-commit", "0" * 40, "--hermes-binary", "fake-hermes", "--report", str(report)]
            with patch.object(lab, "private_namespace"), patch.object(lab, "binary", return_value=(Path("fake"), "0" * 64)), \
                    patch.object(lab, "command", return_value="fake-version"), patch.object(lab, "full_suite", side_effect=completed_fake_suite), \
                    patch.object(lab.Lab, "close", side_effect=RuntimeError("ordinary cleanup failure")), patch("sys.stdout", new=io.StringIO()):
                code = lab.main(args)
            result = json.loads(report.read_text())
            self.assertEqual(code, 1)
            self.assertEqual(result["status"], "FAIL")
            self.assertFalse(result["release_gate"])
            self.assertIn("cleanup failure", result["error"])

    def test_daemon_cleanup_failure_produces_failure_report_and_removes_only_fixture(self):
        def completed_fake_execution(args, binary, home, env, report, owner):
            report["status"] = "PASS"
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            fake = root / "synthetic-binary"
            fake.write_text("never executed")
            unrelated = root / "unrelated.txt"
            unrelated.write_text("preserve")
            report = root / "report.json"
            with patch.object(daemon, "private_namespace"), patch.object(daemon, "execute", side_effect=completed_fake_execution), \
                    patch.object(daemon.ProcessOwner, "close", side_effect=RuntimeError("ordinary cleanup failure")), patch("sys.stdout", new=io.StringIO()):
                code = daemon.main(["--binary", str(fake), "--report", str(report), "--home-parent", str(root)])
            result = json.loads(report.read_text())
            self.assertEqual(code, 1)
            self.assertEqual(result["status"], "FAIL")
            self.assertIn("cleanup failure", result["error"])
            self.assertEqual(unrelated.read_text(), "preserve")
            self.assertFalse(Path(result["home"]).parent.exists(), "only owned TemporaryDirectory is removed")

    def test_signal_between_cleanup_iterations_after_success_fails_report_and_closes_all(self):
        child, second, unrelated = FakeChild(), FakeChild(), FakeChild()
        guard = lab.SignalGuard()
        def interrupted_fake_suite(owner, *_):
            owner.children = SignalBetweenChildren([child, second], lambda: guard.handle(lab.signal.SIGTERM, None))
            owner.report.update(status="TWO_PROCESS_HERMES_PASS", release_gate=True)
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "report.json"
            args = ["--mode", "full", "--reference-binary", "fake-reference", "--reference-sha256", "0" * 64,
                    "--reference-source-commit", "0" * 40, "--mcnd-binary", "fake-mcnd", "--mcnd-sha256", "0" * 64,
                    "--mcnd-source-commit", "0" * 40, "--hermes-binary", "fake-hermes", "--report", str(report)]
            with patch.object(lab, "private_namespace"), patch.object(lab, "binary", return_value=(Path("fake"), "0" * 64)), \
                    patch.object(lab, "command", return_value="fake-version"), patch.object(lab, "full_suite", side_effect=interrupted_fake_suite), \
                    patch.object(lab.signal, "signal"), patch("sys.stdout", new=io.StringIO()), guard:
                code = lab.main(args, signals=guard)
            result = json.loads(report.read_text())
            self.assertEqual(code, 1)
            self.assertEqual(result["status"], "FAIL")
            self.assertFalse(result["release_gate"])
            self.assertIn("termination requested", result["error"])
            self.assertFalse(child.running)
            self.assertFalse(second.running)
            self.assertEqual(unrelated.calls, [])


if __name__ == "__main__":
    unittest.main()
