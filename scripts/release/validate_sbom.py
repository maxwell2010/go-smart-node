"""Validate release SBOMs with pinned official CycloneDX 1.5 schemas, offline."""
from __future__ import annotations

import argparse
import hashlib
import json
from pathlib import Path

from jsonschema import Draft7Validator
from referencing import Registry, Resource

SCHEMAS = Path(__file__).with_name("schemas")
SHA256 = {
    "bom-1.5.schema.json": "067f7824b08653839ea050ae9e09ca48375eadc2652b0e2a299476e7db90335b",
    "spdx.schema.json": "4f6e2b05c05d26a4f2dc5879fbc2fca94b0a28db46289d0c51345621b71cfbfc",
    "jsf-0.82.schema.json": "8bae002c25e723db7ee1f26afde680ae1a2b1a8f6b4b4b0fd65dc3becb090aae",
}


def validate_bom(bom: dict, schema_dir: Path = SCHEMAS) -> None:
    registry = Registry()
    schemas = {}
    for name, digest in SHA256.items():
        data = (schema_dir / name).read_bytes()
        if hashlib.sha256(data).hexdigest() != digest:
            raise ValueError("Pinned schema checksum mismatch: " + name)
        schema = json.loads(data)
        schemas[name] = schema
        resource = Resource.from_contents(schema)
        for prefix in ("http://cyclonedx.org/schema/", "https://cyclonedx.org/schema/"):
            registry = registry.with_resource(prefix + name, resource)
    # No remote retrieval callback is supplied: all schema resolution is offline.
    Draft7Validator(schemas["bom-1.5.schema.json"], registry=registry).validate(bom)


def main() -> None:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("sbom", type=Path)
    args = parser.parse_args()
    validate_bom(json.loads(args.sbom.read_text(encoding="utf-8")))
    print("CycloneDX 1.5 schema validation passed")


if __name__ == "__main__":
    main()
