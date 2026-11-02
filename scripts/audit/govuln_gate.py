"""Fail closed on govulncheck symbol findings, including JSON-mode exit zero."""
from __future__ import annotations
import argparse
import hashlib
import json
from pathlib import Path
import re
import sys

from summarize_govuln import messages


def validate_manifest(raw: bytes, manifest: dict) -> int:
    if not isinstance(manifest, dict):
        raise ValueError('Completion manifest must be an object')
    if type(manifest.get('schema_version')) is not int or manifest['schema_version'] != 1:
        raise ValueError('Unsupported completion manifest schema')
    if manifest.get('completed') is not True:
        raise ValueError('Scanner completion was not recorded')
    for field in ['scanner_exit_code', 'raw_bytes']:
        if type(manifest.get(field)) is not int:
            raise ValueError('Manifest integer required: ' + field)
    if manifest['raw_bytes'] < 0 or manifest['raw_bytes'] != len(raw):
        raise ValueError('Raw scanner byte count does not match completion evidence')
    for field in ['scope', 'toolchain', 'started_utc', 'completed_utc']:
        if not isinstance(manifest.get(field), str) or not manifest[field].strip():
            raise ValueError('Manifest nonempty string required: ' + field)
    if not manifest['toolchain'].startswith('go version '):
        raise ValueError('Missing actual Go toolchain identity')
    if not isinstance(manifest.get('source_commit'), str) or not re.fullmatch(r'[0-9a-f]{40}', manifest['source_commit']):
        raise ValueError('Exact source commit required')
    argv = manifest.get('argv')
    if not isinstance(argv, list) or not argv or any(not isinstance(x, str) or not x for x in argv):
        raise ValueError('Scanner argv must contain nonempty strings')
    digest = manifest.get('raw_sha256')
    if not isinstance(digest, str) or not re.fullmatch(r'[0-9a-f]{64}', digest):
        raise ValueError('Manifest SHA256 required')
    if hashlib.sha256(raw).hexdigest() != digest:
        raise ValueError('Raw scanner SHA256 does not match completion evidence')
    return manifest['scanner_exit_code']


def evaluate(text: str, scanner_exit_code: int, completion_verified: bool = False) -> tuple[int, dict]:
    if type(scanner_exit_code) is not int:
        raise ValueError('Scanner exit status must be an integer')
    records = list(messages(text))
    if not records or any(not isinstance(r, dict) for r in records):
        raise ValueError('Missing or invalid scanner JSON records')
    configs = [r['config'] for r in records if 'config' in r]
    if len(configs) != 1 or not isinstance(configs[0], dict):
        raise ValueError('Expected one scanner configuration record')
    config = configs[0]
    if config.get('protocol_version') != 'v1.0.0' or config.get('scan_level') != 'symbol':
        raise ValueError('Unsupported protocol or non-symbol scan cannot pass this gate')
    for field in ['scanner_name', 'scanner_version', 'scan_mode']:
        if not isinstance(config.get(field), str) or not config[field]:
            raise ValueError('Scanner configuration string required: ' + field)
    if config['scanner_name'] != 'govulncheck' or config['scan_mode'] not in ['source', 'binary']:
        raise ValueError('Unsupported scanner identity or mode')
    # Binary mode reports the binary's compiler in SBOM, not config.go_version.
    if config['scan_mode'] == 'binary':
        sboms = [record['SBOM'] for record in records if 'SBOM' in record]
        if len(sboms) != 1 or not isinstance(sboms[0], dict):
            raise ValueError('Binary scan requires one build SBOM record')
        go_version = sboms[0].get('go_version')
    else:
        go_version = config.get('go_version')
    if not isinstance(go_version, str) or not go_version:
        raise ValueError('Actual scanned Go version is required')
    identifiers = set()
    for record in records:
        if 'finding' not in record:
            continue
        finding = record['finding']
        if not isinstance(finding, dict) or not isinstance(finding.get('trace'), list):
            raise ValueError('Malformed finding record')
        trace = finding['trace']
        if not isinstance(finding.get('osv'), str) or not finding['osv']:
            raise ValueError('Malformed finding trace')
        for frame in trace:
            if not isinstance(frame, dict) or ('function' in frame and not isinstance(frame['function'], str)):
                raise ValueError('Malformed finding frame or symbol name')
        if trace and trace[0].get('function'):
            identifiers.add(finding['osv'])
    report = {'scanner_exit_code': scanner_exit_code,
              'distinct_symbol_advisories': len(identifiers),
              'advisory_ids': sorted(identifiers),
              'scanner_version': config.get('scanner_version'),
              'go_version': go_version,
              'db_last_modified': config.get('db_last_modified'),
              'completion_verified': completion_verified,
              'diagnostic_clean': scanner_exit_code == 0 and not identifiers,
              'passed': completion_verified and scanner_exit_code == 0 and not identifiers}
    return (2 if scanner_exit_code != 0 else 1 if identifiers else 0 if completion_verified else 2), report


def evaluate_capture(raw: bytes, manifest: dict) -> tuple[int, dict]:
    status = validate_manifest(raw, manifest)
    code, report = evaluate(raw.decode('utf-8-sig'), status, completion_verified=True)
    report.update({field: manifest[field] for field in ['source_commit', 'scope', 'toolchain', 'raw_sha256', 'raw_bytes', 'argv']})
    return code, report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('scan', type=Path)
    evidence = parser.add_mutually_exclusive_group(required=True)
    evidence.add_argument('--manifest', type=Path, help='Required completion evidence for a release PASS')
    evidence.add_argument('--scanner-exit-code', type=int, help='Diagnostic only; cannot return a release PASS')
    args = parser.parse_args()
    try:
        raw = args.scan.read_bytes()
        if args.manifest is not None:
            status, report = evaluate_capture(raw, json.loads(args.manifest.read_text(encoding='utf-8-sig')))
        else:
            status, report = evaluate(raw.decode('utf-8-sig'), args.scanner_exit_code)
    except (OSError, ValueError, TypeError, KeyError, UnicodeError) as exc:
        print(json.dumps({'passed': False, 'error': str(exc)}))
        return 2
    print(json.dumps(report, sort_keys=True))
    return status


if __name__ == '__main__':
    sys.exit(main())
