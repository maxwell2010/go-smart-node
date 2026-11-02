import hashlib
import json
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest
from unittest.mock import patch

from capture_govuln import capture
from govuln_gate import evaluate_capture, validate_manifest

CONFIG = {'config': {'protocol_version': 'v1.0.0', 'scan_level': 'symbol',
                     'scanner_version': 'test', 'scan_mode': 'source',
                     'scanner_name': 'govulncheck', 'go_version': 'go1.26.10'}}
GO = [sys.executable, '-c', 'print("go version go1.26.10 test/arch")']


class CaptureTests(unittest.TestCase):
    def setUp(self):
        self.directory = tempfile.TemporaryDirectory()
        self.addCleanup(self.directory.cleanup)
        self.raw = Path(self.directory.name) / 'scan.json'
        self.manifest = Path(self.directory.name) / 'completion.json'

    def run_capture(self, text, exit_code=0):
        command = [sys.executable, '-c', f'import sys; print({text!r}); sys.exit({exit_code})']
        return capture(command, self.raw, self.manifest, 'a' * 40,
                       'ordinary ./cmd/dscd', GO)

    def test_genuine_completed_config_only_scan_can_pass(self):
        result = self.run_capture(json.dumps(CONFIG))
        self.assertEqual(json.loads(self.manifest.read_text()), result)
        self.assertEqual(result['raw_sha256'], hashlib.sha256(self.raw.read_bytes()).hexdigest())
        status, report = evaluate_capture(self.raw.read_bytes(), result)
        self.assertEqual(status, 0)
        self.assertTrue(report['passed'])
        self.assertTrue(report['completion_verified'])

    def test_findings_with_actual_exit_zero_fail_gate(self):
        finding = {'finding': {'osv': 'GO-TEST', 'trace': [{'function': 'Unsafe'}]}}
        result = self.run_capture(json.dumps(CONFIG) + '\n' + json.dumps(finding))
        status, report = evaluate_capture(self.raw.read_bytes(), result)
        self.assertEqual(status, 1)
        self.assertFalse(report['passed'])

    def test_failed_scanner_records_actual_status_and_cannot_pass(self):
        result = self.run_capture(json.dumps(CONFIG), 7)
        self.assertEqual(result['scanner_exit_code'], 7)
        self.assertEqual(evaluate_capture(self.raw.read_bytes(), result)[0], 2)

    def test_missing_completion_and_typed_fields_fail(self):
        result = self.run_capture(json.dumps(CONFIG))
        for field, value in [('completed', False), ('schema_version', True),
                             ('raw_bytes', True), ('scanner_exit_code', '0'),
                             ('source_commit', 'HEAD'), ('scope', False),
                             ('argv', ['scanner', 1]), ('raw_sha256', []),
                             ('toolchain', ''), ('started_utc', None)]:
            with self.subTest(field=field):
                with self.assertRaises(ValueError):
                    validate_manifest(self.raw.read_bytes(), dict(result, **{field: value}))
        with self.assertRaises(ValueError):
            validate_manifest(self.raw.read_bytes(), {})

    def test_raw_alteration_and_boundary_truncation_fail_binding(self):
        finding = {'finding': {'osv': 'GO-TEST', 'trace': [{'function': 'Unsafe'}]}}
        result = self.run_capture(json.dumps(CONFIG) + '\n' + json.dumps(finding))
        for raw in [self.raw.read_bytes() + b' ', (json.dumps(CONFIG) + '\n').encode()]:
            with self.assertRaises(ValueError):
                evaluate_capture(raw, result)
        altered = self.raw.read_bytes().replace(b'Unsafe', b'Safely')
        self.assertEqual(len(altered), result['raw_bytes'])
        with self.assertRaises(ValueError):
            evaluate_capture(altered, result)

    def test_scanner_completed_with_truncated_json_still_fails_parse(self):
        result = self.run_capture(json.dumps(CONFIG) + '\n{"finding":')
        with self.assertRaises(ValueError):
            evaluate_capture(self.raw.read_bytes(), result)

    def test_failed_launch_invalidates_old_completion(self):
        self.run_capture(json.dumps(CONFIG))
        with patch('capture_govuln.subprocess.run', side_effect=OSError('launch failed')):
            with self.assertRaises(OSError):
                capture(['scanner'], self.raw, self.manifest, 'a' * 40, 'ordinary', GO)
        self.assertFalse(self.manifest.exists())
        self.assertFalse(list(self.raw.parent.glob('.scanner-*')))

    def test_manifest_publication_failure_cannot_leave_completion(self):
        self.run_capture(json.dumps(CONFIG))
        with patch('capture_govuln.atomic_json', side_effect=OSError('disk full')):
            with self.assertRaises(OSError):
                self.run_capture(json.dumps(CONFIG))
        self.assertFalse(self.manifest.exists())

    def test_missing_sidecar_cli_is_not_release_pass(self):
        self.raw.write_text(json.dumps(CONFIG))
        process = subprocess.run([sys.executable, str(Path(__file__).with_name('govuln_gate.py')),
                                  str(self.raw), '--manifest', str(self.manifest)], capture_output=True)
        self.assertEqual(process.returncode, 2)


if __name__ == '__main__':
    unittest.main()
