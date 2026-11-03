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
    wrapper_name = "MintCandyWrappedDEL.sol"
    wrapper_source = (ROOT / wrapper_name).read_bytes()
    sources[wrapper_name] = {"content": wrapper_source.decode("utf-8")}
    probe_name = "WrappedDELTestReceiver.sol"
    probe_source = (ROOT / probe_name).read_bytes()
    sources[probe_name] = {"content": probe_source.decode("utf-8")}
    fresh_name = "NewNetworkMasterValidator.sol"
    fresh_source = (ROOT / fresh_name).read_bytes()
    sources[fresh_name] = {"content": fresh_source.decode("utf-8")}
    config_name = "ConfiguredBootstrapTestTarget.sol"
    config_source = (ROOT / config_name).read_bytes()
    sources[config_name] = {"content": config_source.decode("utf-8")}
    factory_sources = {}
    for filename in ["NewNetworkTokenCenter.sol", "NewNetworkNFTCenter.sol"]:
        data = (ROOT / filename).read_bytes()
        factory_sources[filename] = hashlib.sha256(data).hexdigest()
        sources[filename] = {"content": data.decode("utf-8")}
    targets = {
        "sources/DecimalContractCenter.sol": ["DecimalContractCenter"],
        "sources/DecimalContractCenterProxy.sol": ["DecimalContractCenterProxy"],
        own_name: ["NewNetworkProxyBootstrap"],
        wrapper_name: ["MintCandyWrappedDEL"],
        fresh_name: ["NewNetworkMasterValidator"],
        config_name: ["ConfiguredBootstrapTestTarget"],
        "NewNetworkTokenCenter.sol": ["NewNetworkTokenCenter", "NewNetworkReservelessTokenBeacon"],
        "NewNetworkNFTCenter.sol": ["NewNetworkNFTCenter"],
        probe_name: ["WrappedDELTestReceiver"],
        "sources/DecimalMasterValidator.sol": ["DecimalMasterValidator"],
        "sources/DecimalDelegation.sol": ["DecimalDelegation", "DelegationNFTLib"],
        "sources/DecimalDelegationNFT.sol": ["DecimalDelegationNFT"],
        "sources/DecimalTokenCenter.sol": ["DecimalTokenCenter", "DRC20", "DRC20Reserveless"],
        "sources/DecimalNFTCenter.sol": ["DecimalNFTCenter", "DRC721", "DRC1155", "DRC721Reserveless", "DRC1155Reserveless"],
    }
    output_fields = ["abi", "evm.bytecode.object", "evm.bytecode.linkReferences", "evm.deployedBytecode.object", "evm.deployedBytecode.linkReferences"]
    settings = {
        "optimizer": {"enabled": True, "runs": 200},
        "evmVersion": "paris",
        "viaIR": False,
        "metadata": {"bytecodeHash": "ipfs"},
        "libraries": {},
        "outputSelection": {source: {target: output_fields for target in names} for source, names in targets.items()},
    }
    directory = ROOT / "artifacts"
    directory.mkdir(exist_ok=True)
    for source, names in targets.items():
        selected = dict(settings)
        selected["viaIR"] = source == "sources/DecimalDelegation.sol"
        selected["outputSelection"] = {source: {name: output_fields for name in names}}
        request = {"language": "Solidity", "sources": sources, "settings": selected}
        result = subprocess.run([str(compiler), "--standard-json"], input=json.dumps(request), text=True, capture_output=True, check=True, timeout=240)
        response = json.loads(result.stdout)
        errors = [error for error in response.get("errors", []) if error["severity"] == "error"]
        if errors:
            raise RuntimeError("\n".join(error["formattedMessage"] for error in errors))
        for target in names:
            contract = response["contracts"][source][target]
            evm = contract["evm"]
            creation, runtime = evm["bytecode"]["object"], evm["deployedBytecode"]["object"]
            if not creation or not runtime:
                raise RuntimeError("Missing executable bytecode: " + target)
            artifact = {"contractName": target, "sourceUnit": source,
                        "abi": contract["abi"], "creationBytecode": creation, "runtimeBytecode": runtime,
                        "creationLinks": evm["bytecode"]["linkReferences"],
                        "runtimeLinks": evm["deployedBytecode"]["linkReferences"],
                        "compilerVersion": COMPILER_VERSION, "compilerSha256": COMPILER_SHA256,
                        "sourceManifestSha256": hashlib.sha256(manifest_data).hexdigest(),
                        "bootstrapSourceSha256": hashlib.sha256(own_source).hexdigest(),
                        "wrapperSourceSha256": hashlib.sha256(wrapper_source).hexdigest(),
                        "testReceiverSourceSha256": hashlib.sha256(probe_source).hexdigest(),
                        "freshValidatorSourceSha256": hashlib.sha256(fresh_source).hexdigest(),
                        "testConfigSourceSha256": hashlib.sha256(config_source).hexdigest(),
                        "freshFactorySourcesSha256": factory_sources,
                        "settings": {key: value for key, value in selected.items() if key != "outputSelection"}}
            (directory / (target + ".json")).write_text(json.dumps(artifact, sort_keys=True, indent=2) + "\n", encoding="utf-8", newline="\n")
            print(target, "creation", len(creation) // 2, "runtime", len(runtime) // 2, flush=True)


if __name__ == "__main__":
    main()
