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
    patches_data = (ROOT / "pause-source-patches.json").read_bytes()
    patches = json.loads(patches_data)
    for patch in patches["patches"]:
        canonical = (ROOT / patch["canonical"]).read_bytes()
        if hashlib.sha256(canonical).hexdigest() != patch["canonicalSha256"]:
            raise RuntimeError("Owned pause source has unexpected canonical input")
        expected = canonical
        for edit in patch["transformations"]:
            start = expected.index(edit["anchor"].encode())
            count = edit.get("count", 1)
            if "count" in edit:
                if expected[start:].count(edit["before"].encode()) != count:
                    raise RuntimeError("Unexpected owned patch occurrence count")
                expected = expected[:start] + expected[start:].replace(edit["before"].encode(), edit["after"].encode())
            else:
                where = expected.index(edit["before"].encode(), start)
                expected = expected[:where] + edit["after"].encode() + expected[where+len(edit["before"].encode()):]
        owned = (ROOT / patch["owned"]).read_bytes()
        if owned != expected or hashlib.sha256(owned).hexdigest() != patch["ownedSha256"]:
            raise RuntimeError("Undeclared pause-source changes: " + patch["owned"])
        sources[patch["owned"]] = {"content": owned.decode("utf-8")}
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
    for filename in ["NewNetworkTokenCenter.sol", "NewNetworkNFTCenter.sol", "NewNetworkReadableProxies.sol", "NewNetworkPausedViews.sol", "NewNetworkReserveNFTs.sol", "NativeBurnTestTarget.sol", "MintCandyNFTPositionVault.sol", "MintCandyIsolatedDRC1155.sol", "IsolatedNFTTestController.sol", "MintCandyNFTIsolationLimits.sol", "MintCandyCurveConverter.sol"]:
        data = (ROOT / filename).read_bytes()
        factory_sources[filename] = hashlib.sha256(data).hexdigest()
        sources[filename] = {"content": data.decode("utf-8")}
    targets = {
        "sources/DecimalContractCenter.sol": ["DecimalContractCenter"],
        "sources/DecimalContractCenterProxy.sol": ["DecimalContractCenterProxy"],
        own_name: ["NewNetworkProxyBootstrap"],
        wrapper_name: ["MintCandyWrappedDEL", "IMintCandyNativeBurn"],
        fresh_name: ["NewNetworkMasterValidator"],
        config_name: ["ConfiguredBootstrapTestTarget"],
        "NewNetworkTokenCenter.sol": ["NewNetworkTokenCenter", "NewNetworkReservelessTokenBeacon"],
        "NewNetworkNFTCenter.sol": ["NewNetworkNFTCenter"],
        "NewNetworkReserveNFTs.sol": ["NewNetworkDRC721", "NewNetworkDRC1155"],
        "NativeBurnTestTarget.sol": ["NativeBurnTestTarget"],
        "MintCandyNFTPositionVault.sol": ["MintCandyNFTPositionVault"],
        "MintCandyCurveConverter.sol": ["MintCandyCurveConverter"],
        "MintCandyIsolatedDRC1155.sol": ["MintCandyIsolatedDRC1155"],
        "IsolatedNFTTestController.sol": ["IsolatedNFTTestController"],
        "NewNetworkReadableProxies.sol": ["NewNetworkTokenReadableProxy", "NewNetworkNFTReadableProxy"],
        "owned/DecimalNFTCenter.sol": ["BeaconNFT"],
        probe_name: ["WrappedDELTestReceiver"],
        "sources/DecimalMasterValidator.sol": ["DecimalMasterValidator"],
        "sources/DecimalDelegation.sol": ["DecimalDelegation", "DelegationNFTLib"],
        "sources/DecimalDelegationNFT.sol": ["DecimalDelegationNFT"],
        "sources/DecimalTokenCenter.sol": ["DecimalTokenCenter", "DRC20", "DRC20Reserveless"],
        "sources/DecimalNFTCenter.sol": ["DecimalNFTCenter", "DRC721", "DRC1155", "DRC721Reserveless", "DRC1155Reserveless"],
    }
    output_fields = ["abi", "evm.bytecode.object", "evm.bytecode.linkReferences", "evm.deployedBytecode.object", "evm.deployedBytecode.linkReferences", "evm.deployedBytecode.immutableReferences"]
    settings = {
        "optimizer": {"enabled": True, "runs": 200},
        "evmVersion": "paris",
        "viaIR": False,
        "metadata": {"bytecodeHash": "ipfs"},
        "libraries": {},
        "outputSelection": {source: {target: output_fields for target in names} for source, names in targets.items()},
    }
    ast_settings = dict(settings)
    ast_settings["outputSelection"] = {"*": {"*": ["abi"], "": ["ast"]}}
    ast_request = {"language": "Solidity", "sources": sources, "settings": ast_settings}
    parsed = subprocess.run([str(compiler), "--standard-json"], input=json.dumps(ast_request), text=True, capture_output=True, check=True, timeout=120)
    metadata = json.loads(parsed.stdout)
    errors = [error for error in metadata.get("errors", []) if error["severity"] == "error"]
    if errors:
        raise RuntimeError("\n".join(error["formattedMessage"] for error in errors))
    immutable_variables = {}
    contract_kinds = {}
    def walk(node, source, contract=""):
        if isinstance(node, dict):
            if node.get("nodeType") == "ContractDefinition":
                contract = node["name"]
                contract_kinds[source+":"+contract] = node["contractKind"]
            if node.get("nodeType") == "VariableDeclaration" and node.get("mutability") == "immutable":
                immutable_variables[str(node["id"])] = {"key": source+":"+contract+"."+node["name"],
                    "type": node["typeDescriptions"]["typeString"]}
            for value in node.values(): walk(value, source, contract)
        elif isinstance(node, list):
            for value in node: walk(value, source, contract)
    for source, record in metadata["sources"].items(): walk(record["ast"], source)
    def canonical_type(argument):
        kind = argument["type"]
        if kind.startswith("tuple"):
            return "("+",".join(canonical_type(part) for part in argument["components"])+")"+kind[5:]
        return kind
    policy = ["// SPDX-License-Identifier: MIT", "pragma solidity 0.8.20;", "",
        "// Generated from pinned compiler ABIs; build.py verifies the exact selector policy.",
        "library NewNetworkPausedViews {", "    function allowed(bytes4 selector, uint8 family) internal pure returns (bool) {"]
    for family, name in enumerate(["DRC20", "DRC20Reserveless", "DRC721", "DRC1155", "DRC721Reserveless", "DRC1155Reserveless"]):
        source = "sources/DecimalTokenCenter.sol" if family < 2 else "sources/DecimalNFTCenter.sol"
        signatures = sorted({method["name"]+"("+",".join(canonical_type(arg) for arg in method["inputs"])+")"
            for method in metadata["contracts"][source][name]["abi"]
            if method["type"] == "function" and method["stateMutability"] in ("view", "pure")})
        policy.append("        if (family == "+str(family)+") return")
        policy.extend('            selector == bytes4(keccak256("'+signature+'"))'+(";" if index == len(signatures)-1 else " ||") for index,signature in enumerate(signatures))
    policy.extend(["        return false;", "    }", "}"])
    expected_policy = ("\n".join(policy)+"\n").encode()
    if (ROOT / "NewNetworkPausedViews.sol").read_bytes() != expected_policy:
        raise RuntimeError("Paused view selector policy differs from exact pinned compiler ABIs")
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
            if (not creation or not runtime) and target != "IMintCandyNativeBurn":
                raise RuntimeError("Missing executable bytecode: " + target)
            artifact = {"contractName": target, "sourceUnit": source,
                        "abi": contract["abi"], "creationBytecode": creation, "runtimeBytecode": runtime,
                        "creationLinks": evm["bytecode"]["linkReferences"],
                        "runtimeLinks": evm["deployedBytecode"]["linkReferences"],
                        "immutableReferences": evm["deployedBytecode"]["immutableReferences"],
                        "immutableVariables": {key: immutable_variables[key] if key in immutable_variables else
                            {"key":source+":"+target+".library_deploy_address", "type":"address", "origin":"solc-library-deploy-address"}
                            for key in evm["deployedBytecode"]["immutableReferences"]
                            if key in immutable_variables or (key == "library_deploy_address" and contract_kinds[source+":"+target] == "library")},
                        "contractKind": contract_kinds[source+":"+target],
                        "compilerVersion": COMPILER_VERSION, "compilerSha256": COMPILER_SHA256,
                        "sourceManifestSha256": hashlib.sha256(manifest_data).hexdigest(),
                        "bootstrapSourceSha256": hashlib.sha256(own_source).hexdigest(),
                        "wrapperSourceSha256": hashlib.sha256(wrapper_source).hexdigest(),
                        "testReceiverSourceSha256": hashlib.sha256(probe_source).hexdigest(),
                        "freshValidatorSourceSha256": hashlib.sha256(fresh_source).hexdigest(),
                        "testConfigSourceSha256": hashlib.sha256(config_source).hexdigest(),
                        "freshFactorySourcesSha256": factory_sources,
                        "pauseSourcePatchesSha256": hashlib.sha256(patches_data).hexdigest(),
                        "settings": {key: value for key, value in selected.items() if key != "outputSelection"}}
            if len(artifact["immutableVariables"]) != len(artifact["immutableReferences"]):
                raise RuntimeError("Unknown compiler immutable reference")
            (directory / (target + ".json")).write_text(json.dumps(artifact, sort_keys=True, indent=2) + "\n", encoding="utf-8", newline="\n")
            print(target, "creation", len(creation) // 2, "runtime", len(runtime) // 2, flush=True)


if __name__ == "__main__":
    main()
