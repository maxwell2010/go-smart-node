#!/usr/bin/env python3
"""Rebuild pinned fresh-registry artifacts; no network calls or deployment."""
import hashlib
import json
import os
from pathlib import Path
import subprocess

ROOT = Path(__file__).resolve().parent
COMPILER_SHA256 = "0479d44fdf9c501c25337fdc540419f1593b884a87b47f023da4f1c700fda782"
COMPILER_VERSION = "0.8.20+commit.a1b79de6.Linux.g++"


def main():
    compiler = Path(os.environ["SOLC"]).resolve(strict=True)
    if hashlib.sha256(compiler.read_bytes()).hexdigest() != COMPILER_SHA256:
        raise RuntimeError("Solidity compiler checksum mismatch")
    version = subprocess.run([str(compiler), "--version"], check=True, text=True, capture_output=True, timeout=10).stdout
    if ("Version: " + COMPILER_VERSION) not in version:
        raise RuntimeError("Solidity compiler version mismatch")
    manifest_data = (ROOT / "source-manifest.json").read_bytes()
    manifest = json.loads(manifest_data)
    sources = {}
    for entry in manifest["sources"]:
        data = (ROOT / "sources" / entry["file"]).read_bytes()
        if hashlib.sha256(data).hexdigest() != entry["sha256"]:
            raise RuntimeError("Upstream source checksum mismatch: " + entry["file"])
        sources["sources/" + entry["file"]] = {"content": data.decode("utf-8")}
    own_name = "NewNetworkProxyBootstrap.sol"
    own_source = (ROOT / own_name).read_bytes()
    sources[own_name] = {"content": own_source.decode("utf-8")}
    targets = {
        "sources/DecimalContractCenter.sol": "DecimalContractCenter",
        "sources/DecimalContractCenterProxy.sol": "DecimalContractCenterProxy",
        own_name: "NewNetworkProxyBootstrap",
    }
    output_fields = ["abi", "evm.bytecode.object", "evm.bytecode.linkReferences", "evm.deployedBytecode.object", "evm.deployedBytecode.linkReferences"]
    settings = {
        "optimizer": {"enabled": True, "runs": 200},
        "evmVersion": "paris",
        "viaIR": False,
        "metadata": {"bytecodeHash": "ipfs"},
        "libraries": {},
        "outputSelection": {source: {target: output_fields} for source, target in targets.items()},
    }
    request = {"language": "Solidity", "sources": sources, "settings": settings}
    result = subprocess.run([str(compiler), "--standard-json"], input=json.dumps(request), text=True, capture_output=True, check=True, timeout=120)
    response = json.loads(result.stdout)
    errors = [error for error in response.get("errors", []) if error["severity"] == "error"]
    if errors:
        raise RuntimeError("\n".join(error["formattedMessage"] for error in errors))
    directory = ROOT / "artifacts"
    directory.mkdir(exist_ok=True)
    for source, target in targets.items():
        contract = response["contracts"][source][target]
        evm = contract["evm"]
        if evm["bytecode"]["linkReferences"] or evm["deployedBytecode"]["linkReferences"]:
            raise RuntimeError("Unresolved library references: " + target)
        creation = evm["bytecode"]["object"]
        runtime = evm["deployedBytecode"]["object"]
        if not creation or not runtime:
            raise RuntimeError("Missing executable bytecode: " + target)
        artifact = {"contractName": target, "abi": contract["abi"], "creationBytecode": creation, "runtimeBytecode": runtime,
                    "compilerVersion": COMPILER_VERSION, "compilerSha256": COMPILER_SHA256,
                    "sourceManifestSha256": hashlib.sha256(manifest_data).hexdigest(),
                    "bootstrapSourceSha256": hashlib.sha256(own_source).hexdigest(),
                    "settings": {key: value for key, value in settings.items() if key != "outputSelection"}}
        (directory / (target + ".json")).write_text(json.dumps(artifact, sort_keys=True, indent=2) + "\n", encoding="utf-8", newline="\n")
        print(target, "creation", len(creation) // 2, "runtime", len(runtime) // 2)


if __name__ == "__main__":
    main()
