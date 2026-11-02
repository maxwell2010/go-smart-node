"""SBOM parser regression tests. No artifact execution or network access."""
import importlib.util
import json
from pathlib import Path
import unittest

spec = importlib.util.spec_from_file_location("generate_sbom", Path(__file__).with_name("generate_sbom.py"))
sbom = importlib.util.module_from_spec(spec)
spec.loader.exec_module(sbom)
INFO = '''/private/build/dscd: go1.24.4
 path example.org/node/cmd/dscd
 mod example.org/node (devel)
 dep example.org/original v1.0.0
 => example.org/replacement v1.2.3 h1:example-go-sum
 dep example.org/other v2.0.0 h1:other-go-sum
 build -trimpath=true
'''


class SBOMTests(unittest.TestCase):
    def test_replacement_and_go_sums_are_metadata(self):
        bom = sbom.make_bom(INFO, 'a'*64, 'b'*40, 'ordinary', '0.1.0-alpha.1')
        replacement = next(c for c in bom['components'] if c['name'] == 'example.org/replacement')
        self.assertEqual(replacement['version'], 'v1.2.3')
        self.assertNotIn('hashes', replacement)
        props = {p['name']: p['value'] for p in replacement['properties']}
        self.assertEqual(props['go.module.sum'], 'h1:example-go-sum')
        self.assertEqual(props['go.replaces.path'], 'example.org/original')
        text = json.dumps(bom)
        self.assertNotIn('/private/build', text)
        self.assertIn('go1.24.4', text)
        self.assertEqual(bom, sbom.make_bom(INFO, 'a'*64, 'b'*40, 'ordinary', '0.1.0-alpha.1'))

    def test_local_replacement_fails_closed(self):
        for path in ['../local', '/private/local', 'C:\\private\\local']:
            with self.assertRaises(ValueError):
                sbom.parse_build_info(INFO.replace('example.org/replacement', path))

    def test_malformed_build_info_rejected(self):
        for text in ['', 'not-go-info', '/binary: go1.24.4\n dep module v1']:
            with self.assertRaises(ValueError):
                sbom.parse_build_info(text)


if __name__ == '__main__':
    unittest.main()
