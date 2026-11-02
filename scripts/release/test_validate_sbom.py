"""Official-schema validation without network or artifact execution."""
import importlib.util
from pathlib import Path
import shutil
import tempfile
import unittest

from jsonschema.exceptions import ValidationError


def load(name):
    spec = importlib.util.spec_from_file_location(name, Path(__file__).with_name(name + ".py"))
    module = importlib.util.module_from_spec(spec)
    spec.loader.exec_module(module)
    return module


generator = load("generate_sbom")
validator = load("validate_sbom")
INFO = '/private/build/dscd: go1.24.4\n mod example.org/node (devel)\n dep example.org/module v1.0.0 h1:module-sum\n'


class SchemaTests(unittest.TestCase):
    def test_generated_inventory_and_noassertion_license_are_valid(self):
        bom = generator.make_bom(INFO, 'a' * 64, 'b' * 40, 'ordinary', '0.1.0-alpha.1')
        validator.validate_bom(bom)

    def test_invalid_hash_and_unknown_fields_are_rejected(self):
        bom = generator.make_bom(INFO, 'a' * 64, 'b' * 40, 'ordinary', '0.1.0-alpha.1')
        bom['metadata']['component']['hashes'][0]['content'] = 'not-a-valid-hash'
        with self.assertRaises(ValidationError):
            validator.validate_bom(bom)
        bom['metadata']['component']['hashes'][0]['content'] = 'a' * 64
        bom['unexpected_field'] = True
        with self.assertRaises(ValidationError):
            validator.validate_bom(bom)

    def test_modified_schema_is_rejected_before_use(self):
        with tempfile.TemporaryDirectory() as directory:
            destination = Path(directory)
            for name in validator.SHA256:
                shutil.copyfile(validator.SCHEMAS / name, destination / name)
            with (destination / 'bom-1.5.schema.json').open('ab') as stream:
                stream.write(b' ')
            with self.assertRaisesRegex(ValueError, 'checksum mismatch'):
                validator.validate_bom({}, destination)


if __name__ == '__main__':
    unittest.main()
