"""Deduplicate govulncheck JSON findings by advisory, not symbol count."""
from __future__ import annotations
import argparse
import collections
import json
from pathlib import Path
import re


def messages(text):
    decoder = json.JSONDecoder()
    offset = 0
    while offset < len(text):
        while offset < len(text) and text[offset].isspace():
            offset += 1
        if offset == len(text):
            return
        value, offset = decoder.raw_decode(text, offset)
        yield value


def direct_modules(go_mod):
    return set(re.findall(r'^\s*([^\s()]+)\s+v[^\s]+\s*$', go_mod, re.M))


def summarize(text, go_mod):
    direct = direct_modules(go_mod)
    osvs = {}
    findings = collections.defaultdict(list)
    for message in messages(text):
        if 'osv' in message:
            osvs[message['osv']['id']] = message['osv']
        if 'finding' in message:
            finding = message['finding']
            if finding.get('trace') and finding['trace'][0].get('function'):
                findings[finding['osv']].append(finding)
    records = []
    for identifier, fs in sorted(findings.items()):
        modules = sorted({f['trace'][0]['module'] for f in fs})
        provenance = {m: 'toolchain' if m == 'stdlib' else 'direct' if m in direct else 'transitive' for m in modules}
        record = osvs.get(identifier, {})
        trace = min((f['trace'] for f in fs), key=len)
        records.append({'id': identifier, 'summary': record.get('summary', ''),
            'modules': provenance, 'symbol_finding_count': len(fs),
            'entrypoints': sorted({f["trace"][-1].get('package', '') + '.' + f['trace'][-1].get('function', '') for f in fs}),
            'shortest_trace': [t.get('package', '') + '.' + t.get('function', '') for t in reversed(trace)],
            'fixed_versions_in_scanned_modules': {
                m: sorted({event['fixed'] for affected in record.get('affected', [])
                           if affected.get('package', {}).get('name') == m
                           for version_range in affected.get('ranges', [])
                           for event in version_range.get('events', []) if 'fixed' in event})
                for m in modules},
            'source': 'https://pkg.go.dev/vuln/' + identifier})
    counts = collections.Counter()
    for record in records:
        kinds = set(record['modules'].values())
        # Separate mutually exclusive inventory buckets from runtime triage.
        key = '+'.join(sorted(kinds))
        counts[key] += 1
    return {'distinct_symbol_advisories': len(records), 'provenance_buckets': dict(sorted(counts.items())),
            'notes': ['Symbol traces are static potential reachability, not exploit validation.',
                      'Toolchain vulnerabilities execute in runtime libraries too; provenance is not exposure.',
                      'Direct means an explicit non-indirect go.mod require in this snapshot.'],
            'advisories': records}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('scan', type=Path)
    parser.add_argument('--go-mod', type=Path, required=True)
    parser.add_argument('--output', type=Path, required=True)
    args = parser.parse_args()
    result = summarize(args.scan.read_text(encoding='utf-8-sig'), args.go_mod.read_text(encoding='utf-8'))
    args.output.write_text(json.dumps(result, indent=2, sort_keys=True) + '\n', encoding='utf-8')
    print(result['distinct_symbol_advisories'], result['provenance_buckets'])


if __name__ == '__main__':
    main()
