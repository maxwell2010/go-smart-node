"""Inventory the verified default mcnd binary and its effective Go modules."""
import hashlib
import json
from urllib.parse import quote


OWNED_REPLACEMENTS = {
    "github.com/cosmos/cosmos-sdk": "./third_party/cosmos-sdk",
    "github.com/cosmos/evm": "./third_party/cosmos-evm",
    "github.com/ethereum/go-ethereum": "./third_party/cosmos-geth",
}


def _properties(values):
    return [{"name": name, "value": value} for name, value in values.items()]


def _owned_component(declared, replacement, source_inputs, archive_files):
    path = replacement["path"]
    if OWNED_REPLACEMENTS.get(declared["path"]) != path or replacement["version"] != "(devel)":
        raise ValueError("unrecognized owned Go module replacement: " + path)
    prefix = "network/" + path.removeprefix("./") + "/"
    manifest_path = "provenance/" + prefix + "UPSTREAM.json"
    try:
        manifest_bytes = archive_files[manifest_path]
    except KeyError:
        raise ValueError("missing archived owned-source manifest: " + manifest_path) from None
    manifest = json.loads(manifest_bytes)
    upstream = manifest["upstreamModule"]
    if manifest["schemaVersion"] != 1 or manifest.get("moduleDeclaration", upstream) != declared["path"]:
        raise ValueError("owned-source manifest does not match declared Go module: " + path)
    expected = manifest["canonicalFiles"] | manifest["modifiedFiles"] | manifest["addedFiles"]
    compiled = {name.removeprefix(prefix): digest for name, digest in source_inputs.items() if name.startswith(prefix)}
    if "go.mod" not in compiled or any(expected.get(name) != digest for name, digest in compiled.items()):
        raise ValueError("owned compiled inputs differ from source manifest: " + path)
    # Hash the compact sorted JSON map of module-relative input paths to SHA-256.
    compiled_digest = hashlib.sha256(json.dumps(compiled, sort_keys=True, separators=(",", ":")).encode()).hexdigest()
    return {
        "type": "library",
        "bom-ref": "owned-go-module:" + declared["path"],
        "name": upstream,
        "version": manifest.get("patchLabel") or manifest["patchVersion"],
        "properties": _properties({
            "go.declared.module": declared["path"],
            "go.declared.version": declared["version"],
            "go.owned.replacement": path,
            "go.owned.upstream.version": manifest["upstreamVersion"],
            "go.owned.upstream.commit": manifest["upstreamCommit"],
            "go.owned.upstream.sum": manifest["upstreamModuleSum"],
            "go.owned.manifest.path": manifest_path,
            "go.owned.manifest.sha256": hashlib.sha256(manifest_bytes).hexdigest(),
            "go.owned.compiled_inputs.path": "SOURCE_INPUTS.json",
            "go.owned.compiled_inputs.sha256": compiled_digest,
            "go.owned.compiled_inputs.count": str(len(compiled)),
        }),
    }


def _remote_component(declared, effective):
    path, version = effective["path"], effective["version"]
    values = {"go.inventory.source": "verified Go build information"}
    if effective["sum"]:
        values["go.module.sum"] = effective["sum"]
    if effective != declared:
        values["go.declared.module"] = declared["path"]
        values["go.declared.version"] = declared["version"]
    return {
        "type": "library",
        "bom-ref": "go-module:" + declared["path"],
        "name": path,
        "version": version,
        "purl": "pkg:golang/" + quote(path, safe="/") + "@" + quote(version, safe=""),
        "properties": _properties(values),
    }


def make_sbom(verified_build, source_inventory, archive_files, binary_sha256):
    revision = verified_build["settings"]["vcs.revision"]
    if source_inventory["sourceRevision"] != revision:
        raise ValueError("SBOM source inventory differs from verified binary revision")
    components = [{
        "type": "platform", "bom-ref": "go-toolchain", "name": "Go",
        "version": verified_build["goVersion"],
        "properties": _properties({"go.component.role": "build toolchain"}),
    }]
    for declared in verified_build["dependencies"].values():
        effective = declared.get("replace", declared)
        if "replace" in declared and effective["version"] == "(devel)":
            components.append(_owned_component(declared, effective, source_inventory["files"], archive_files))
        else:
            components.append(_remote_component(declared, effective))
    return {
        "bomFormat": "CycloneDX",
        "specVersion": "1.5",
        "version": 1,
        "metadata": {"component": {
            "type": "application", "bom-ref": "mcnd", "name": "mcnd", "version": revision,
            "hashes": [{"alg": "SHA-256", "content": binary_sha256}],
            "properties": _properties({
                "source.commit": revision,
                "go.main.module": verified_build["module"]["path"],
                "inventory.scope": "Default Linux amd64 mcnd Go module inventory and toolchain. "
                                   "CGo system libraries and vulnerability assessment require separate evidence.",
            }),
        }},
        "components": sorted(components, key=lambda component: component["bom-ref"]),
    }
