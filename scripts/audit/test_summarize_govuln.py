import importlib.util
from pathlib import Path
import json
import unittest

spec = importlib.util.spec_from_file_location('summary', Path(__file__).with_name('summarize_govuln.py'))
summary = importlib.util.module_from_spec(spec)
spec.loader.exec_module(summary)


class SummaryTests(unittest.TestCase):
    def test_duplicate_symbols_and_mixed_provenance_are_not_double_advisories(self):
        messages = [{'osv': {'id': 'GO-TEST', 'summary': 'example', 'affected': []}}]
        for module in ['stdlib', 'example.org/direct', 'example.org/direct']:
            messages.append({'finding': {'osv': 'GO-TEST', 'trace': [{'module': module, 'package': module, 'function': 'F'}]}})
        text = '\n'.join(json.dumps(x) for x in messages)
        result = summary.summarize(text, 'require (\n example.org/direct v1.0.0\n example.org/indirect v1.0.0 // indirect\n)\n')
        self.assertEqual(result['distinct_symbol_advisories'], 1)
        self.assertEqual(result['provenance_buckets'], {'direct+toolchain': 1})
        self.assertEqual(result['advisories'][0]['symbol_finding_count'], 3)

    def test_import_only_findings_are_not_symbol_reachability(self):
        text = json.dumps({'finding': {'osv': 'GO-TEST', 'trace': [{'module': 'stdlib'}]}})
        self.assertEqual(summary.summarize(text, '')['distinct_symbol_advisories'], 0)

    def test_truncated_json_is_an_error_not_a_clean_scan(self):
        with self.assertRaises(json.JSONDecodeError):
            list(summary.messages('{"finding":'))


if __name__ == '__main__':
    unittest.main()
