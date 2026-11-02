"""Create a deterministic CycloneDX inventory from Go binary build information."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
from urllib.parse import quote


def module_record(fields: list[str]) -> dict[str, str]:
    if len(fields) < 2:
        raise ValueError("Incomplete Go module record")
    path, version = fields[:2]
    if path.startswith(("/", ".", "\\")) or ":" in path or "\\" in path:
        raise ValueError("Local module replacements are not permitted in release SBOMs")
    return {"path": path, "version": version,
            "sum": fields[2] if len(fields) > 2 else ""}


def parse_build_info(text: str) -> tuple[str, dict[str, str], list[dict]]:
    lines = text.splitlines()
    if not lines:
        raise ValueError("Empty Go binary build information")
    match = re.search(r":\s+(go\S+)\s*$", lines[0])
    if not match:
        raise ValueError("Missing Go toolchain version")
    toolchain = match.group(1)
    main = None
    modules = []
    previous = None
    for line in lines[1:]:
        fields = line.split()
        if not fields:
            continue
        if fields[0] == "mod":
            main = module_record(fields[1:])
            previous = main
        elif fields[0] == "dep":
            previous = module_record(fields[1:])
            modules.append(previous)
        elif fields[0] == "=>":
            if previous is None or "replacement" in previous:
                raise ValueError("Unexpected Go replacement record")
            previous["replacement"] = module_record(fields[1:])
        elif fields[0] not in {"path", "build"}:
            raise ValueError("Unknown Go build information record")
    if main is None:
        raise ValueError("Missing main module")
    return toolchain, main, modules


def make_bom(info: str, binary_hash: str, revision: str, flavor: str, version: str) -> dict:
    if not re.fullmatch(r"[0-9a-f]{64}", binary_hash):
        raise ValueError("Invalid binary SHA-256")
    if not re.fullmatch(r"[0-9a-f]{40}", revision):
        raise ValueError("Invalid source revision")
    if flavor not in {"ordinary", "experimental_ibc"}:
        raise ValueError("Invalid flavor")
    toolchain, main, modules = parse_build_info(info)
    components = []
    for original in modules:
        effective = original.get("replacement", original)
        identity = effective['path'] + '@' + effective['version']
        ref = "go-module-" + hashlib.sha256(identity.encode()).hexdigest()
        props = [{"name": "go.inventory.source", "value": "go version -m"}]
        if effective["sum"]:
            props.append({"name": "go.module.sum", "value": effective["sum"]})
        if "replacement" in original:
            props.extend([{"name": "go.replaces.path", "value": original["path"]},
                          {"name": "go.replaces.version", "value": original["version"]}])
            if original["sum"]:
                props.append({"name": "go.replaces.sum", "value": original["sum"]})
        components.append({"type": "library", "bom-ref": ref,
            "name": effective["path"], "version": effective["version"],
            "purl": "pkg:golang/" + quote(effective['path'], safe='/') + '@' + quote(effective['version'], safe=''),
            "licenses": [{"license": {"name": "NOASSERTION"}}], "properties": props})
    components.append({"type": "platform", "bom-ref": "go-toolchain", "name": "Go",
        "version": toolchain, "licenses": [{"license": {"name": "NOASSERTION"}}],
        "properties": [{"name": "go.component.role", "value": "build-toolchain"}]})
    return {"bomFormat": "CycloneDX", "specVersion": "1.5", "version": 1,
        "metadata": {"component": {"type": "application", "bom-ref": "dscd", "name": "dscd",
            "version": version, "hashes": [{"alg": "SHA-256", "content": binary_hash}],
            "licenses": [{"license": {"name": "NOASSERTION"}}], "properties": [
                {"name": "source.commit", "value": revision},
                {"name": "build.flavor", "value": flavor},
                {"name": "go.main.module", "value": main['path']},
                {"name": "inventory.limits", "value": "Go build-info module inventory; native system libraries and exact import graph are not enumerated"}]}},
        "components": sorted(components, key=lambda c: c['bom-ref'])}


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("binary", type=Path)
    parser.add_argument("output", type=Path)
    parser.add_argument("--revision", required=True)
    parser.add_argument("--flavor", required=True)
    parser.add_argument("--version", required=True)
    args = parser.parse_args()
    info = subprocess.check_output(["go", "version", "-m", str(args.binary)], text=True)
    hasher = hashlib.sha256()
    with args.binary.open("rb") as stream:
        for chunk in iter(lambda: stream.read(1024 * 1024), b""):
            hasher.update(chunk)
    digest = hasher.hexdigest()
    bom = make_bom(info, digest, args.revision, args.flavor, args.version)
    args.output.write_text(json.dumps(bom, sort_keys=True, indent=2) + "\n", encoding="utf-8")


if __name__ == "__main__":
    main()
