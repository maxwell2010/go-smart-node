"""Record actual default build inputs using the existing clean-source verifier."""
import argparse
import json
from pathlib import Path
import subprocess
import sys

parser = argparse.ArgumentParser()
parser.add_argument('--source', required=True)
parser.add_argument('--target', choices=['mcnd', 'reference'], required=True)
parser.add_argument('--output', required=True)
args = parser.parse_args()
root = Path(args.source).resolve(strict=True)
sys.path.insert(0, str(root / 'network/tools'))
from source_inventory import json_records, production_environment, verify_build_inputs

environment = production_environment()
package = './cmd/mcnd' if args.target == 'mcnd' else './internal/ibcreference'


def load(module):
    raw = subprocess.check_output(
        ['go', 'list', '-mod=readonly', '-tags=', '-deps', '-json', package],
        cwd=module, env=environment, text=True,
    )
    return json_records(raw)


result = verify_build_inputs(root, environment=environment, package_loader=load)
result['targetPackage'] = package
with Path(args.output).open('x', encoding='utf-8') as stream:
    json.dump(result, stream, indent=2, sort_keys=True)
    stream.write('\n')
