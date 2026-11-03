#!/usr/bin/env python3
"""Build the explicitly owned staking correction; canonical sources stay intact."""
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
    manifest_bytes = (ROOT / "owned/UPSTREAM.json").read_bytes()
    manifest = json.loads(manifest_bytes)
    upstream = json.loads((ROOT / "source-manifest.json").read_bytes())
    pinned = next(e for e in upstream["sources"] if e["file"] == "DecimalDelegation.sol")
    if manifest["canonicalSource"] != "sources/DecimalDelegation.sol" or manifest["canonicalSha256"] != pinned["sha256"]:
        raise RuntimeError("Owned patch no longer derives from the pinned canonical delegation")
    for path, digest in [(manifest["canonicalSource"], manifest["canonicalSha256"]),
                         (manifest["ownedSource"], manifest["ownedSha256"])]:
        if hashlib.sha256((ROOT / path).read_bytes()).hexdigest() != digest:
            raise RuntimeError("Owned staking source provenance differs: " + path)
    unit = "owned/DecimalDelegation.sol"
    source = (ROOT / unit).read_text(encoding="utf-8")
    output = ["abi", "evm.bytecode.object", "evm.bytecode.linkReferences", "evm.deployedBytecode.object", "evm.deployedBytecode.linkReferences", "evm.deployedBytecode.immutableReferences"]
    targets = {"DecimalDelegation": "MintCandyDelegation", "DelegationNFTLib": "MintCandyDelegationNFTLib", "MintCandyIsolatedDelegation": "MintCandyIsolatedDelegation"}
    settings = {"optimizer": {"enabled": True, "runs": 200}, "evmVersion": "paris", "viaIR": True,
                "metadata": {"bytecodeHash": "ipfs"}, "libraries": {},
                "outputSelection": {unit: {name: output for name in targets}}}
    request = {"language": "Solidity", "sources": {unit: {"content": source}}, "settings": settings}
    result = subprocess.run([str(compiler), "--standard-json"], input=json.dumps(request), text=True,
                            capture_output=True, check=True, timeout=240)
    response = json.loads(result.stdout)
    errors = [e for e in response.get("errors", []) if e["severity"] == "error"]
    if errors:
        raise RuntimeError("\n".join(e["formattedMessage"] for e in errors))
    for contract_name, artifact_name in targets.items():
        contract = response["contracts"][unit][contract_name]
        evm = contract["evm"]
        creation, runtime = evm["bytecode"]["object"], evm["deployedBytecode"]["object"]
        if not creation or not runtime or len(runtime) // 2 > 24576:
            raise RuntimeError("Missing or oversized owned staking bytecode: " + contract_name + " bytes=" + str(len(runtime) // 2))
        immutable_refs = evm["deployedBytecode"]["immutableReferences"]
        if any(key != "library_deploy_address" or contract_name != "DelegationNFTLib" for key in immutable_refs):
            raise RuntimeError("Unreviewed owned staking constructor immutable")
        artifact = {"contractName": contract_name, "artifactName": artifact_name, "sourceUnit": unit,
                    "abi": contract["abi"], "creationBytecode": creation, "runtimeBytecode": runtime,
                    "creationLinks": evm["bytecode"]["linkReferences"], "runtimeLinks": evm["deployedBytecode"]["linkReferences"],
                    "immutableReferences": immutable_refs,
                    "immutableVariables": {key: {"key": unit + ":" + contract_name + ".library_deploy_address", "type": "address", "origin": "solc-library-deploy-address"} for key in immutable_refs},
                    "compilerVersion": COMPILER_VERSION, "compilerSha256": COMPILER_SHA256,
                    "ownedManifestSha256": hashlib.sha256(manifest_bytes).hexdigest(),
                    "settings": {k: v for k, v in settings.items() if k != "outputSelection"}}
        (ROOT / "artifacts" / (artifact_name + ".json")).write_text(json.dumps(artifact, sort_keys=True, indent=2) + "\n", encoding="utf-8", newline="\n")
        print(artifact_name, "creation", len(creation) // 2, "runtime", len(runtime) // 2, flush=True)

if __name__ == "__main__":
    main()
