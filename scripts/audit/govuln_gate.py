"""Fail closed on govulncheck symbol findings, including JSON-mode exit zero."""
from __future__ import annotations
import argparse
import json
from pathlib import Path
import sys

from summarize_govuln import messages


def evaluate(text: str, scanner_exit_code: int) -> tuple[int, dict]:
    records = list(messages(text))
    if not records or any(not isinstance(r, dict) for r in records):
        raise ValueError('Missing or invalid scanner JSON records')
    configs = [r['config'] for r in records if 'config' in r]
    if len(configs) != 1 or not isinstance(configs[0], dict):
        raise ValueError('Expected one scanner configuration record')
    config = configs[0]
    if config.get('protocol_version') != 'v1.0.0' or config.get('scan_level') != 'symbol':
        raise ValueError('Unsupported protocol or non-symbol scan cannot pass this gate')
    identifiers = set()
    for record in records:
        if 'finding' not in record:
            continue
        finding = record['finding']
        if not isinstance(finding, dict) or not isinstance(finding.get('trace'), list):
            raise ValueError('Malformed finding record')
        trace = finding['trace']
        if trace and (not isinstance(trace[0], dict) or not finding.get('osv')):
            raise ValueError('Malformed finding trace')
        if trace and trace[0].get('function'):
            identifiers.add(finding['osv'])
    report = {'scanner_exit_code': scanner_exit_code,
              'distinct_symbol_advisories': len(identifiers),
              'advisory_ids': sorted(identifiers),
              'scanner_version': config.get('scanner_version'),
              'go_version': config.get('go_version'),
              'db_last_modified': config.get('db_last_modified'),
              'passed': scanner_exit_code == 0 and not identifiers}
    return (2 if scanner_exit_code != 0 else 1 if identifiers else 0), report


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('scan', type=Path)
    parser.add_argument('--scanner-exit-code', required=True, type=int)
    args = parser.parse_args()
    try:
        status, report = evaluate(args.scan.read_text(encoding='utf-8-sig'), args.scanner_exit_code)
    except (OSError, ValueError, TypeError, KeyError) as exc:
        print(json.dumps({'passed': False, 'error': str(exc)}))
        return 2
    print(json.dumps(report, sort_keys=True))
    return status


if __name__ == '__main__':
    sys.exit(main())
