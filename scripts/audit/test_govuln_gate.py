import json
import unittest
from govuln_gate import evaluate

CONFIG = {'config': {'protocol_version': 'v1.0.0', 'scan_level': 'symbol', 'scanner_version': 'test',
                     'scanner_name': 'govulncheck', 'scan_mode': 'source', 'go_version': 'go1.26.10'}}


class GateTests(unittest.TestCase):
    def test_clean_diagnostic_without_completion_cannot_pass(self):
        status, report = evaluate(json.dumps(CONFIG), 0)
        self.assertEqual(status, 2)
        self.assertFalse(report['passed'])
        self.assertTrue(report['diagnostic_clean'])

    def test_findings_fail_even_with_scanner_json_exit_zero(self):
        finding = {'finding': {'osv': 'GO-TEST', 'trace': [{'function': 'Vulnerable'}]}}
        text = '\n'.join(json.dumps(x) for x in [CONFIG, finding, finding])
        status, report = evaluate(text, 0)
        self.assertEqual(status, 1)
        self.assertFalse(report['passed'])
        self.assertEqual(report['distinct_symbol_advisories'], 1)

    def test_scanner_failure_cannot_pass_empty_findings(self):
        status, report = evaluate(json.dumps(CONFIG), 2)
        self.assertEqual(status, 2)
        self.assertFalse(report['passed'])

    def test_empty_truncated_and_invalid_inputs_fail_closed(self):
        for text in ['', '{}', '[]', json.dumps(CONFIG) + '\n{"finding":']:
            with self.assertRaises((ValueError, TypeError)):
                evaluate(text, 0)

    def test_module_only_scan_is_not_accepted_as_symbol_clean(self):
        config = {'config': dict(CONFIG['config'], scan_level='module')}
        with self.assertRaises(ValueError):
            evaluate(json.dumps(config), 0)

    def test_malformed_findings_fail_closed(self):
        text = json.dumps(CONFIG) + '\n' + json.dumps({'finding': {'trace': 'invalid'}})
        with self.assertRaises(ValueError):
            evaluate(text, 0)

    def test_typed_scanner_fields_are_required(self):
        for field in ['scanner_name', 'scanner_version', 'go_version', 'scan_mode']:
            config = {'config': dict(CONFIG['config'], **{field: False})}
            with self.assertRaises(ValueError):
                evaluate(json.dumps(config), 0, completion_verified=True)

    def test_typed_findings_are_required(self):
        for finding in [{'osv': False, 'trace': []},
                        {'osv': 'GO-TEST', 'trace': [1]},
                        {'osv': 'GO-TEST', 'trace': [{'function': True}]}]:
            with self.assertRaises(ValueError):
                evaluate(json.dumps(CONFIG) + '\n' + json.dumps({'finding': finding}), 0,
                         completion_verified=True)


if __name__ == '__main__':
    unittest.main()
