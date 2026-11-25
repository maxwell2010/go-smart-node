"""Check the reviewed public asset inventory against compiled ABIs, entirely offline."""
import argparse
import json
from pathlib import Path


NETWORK = Path(__file__).resolve().parents[1]
MATRIX = NETWORK / "docs/ru/ASSET_OPERATIONS.md"
ARTIFACTS = NETWORK / "systemcontracts/artifacts"
ROLES = {
    "token-center": "NewNetworkTokenCenter",
    "nft-center": "NewNetworkNFTCenter",
    "drc20-backed": "DRC20",
    "drc20-reserveless": "DRC20Reserveless",
    "drc721-backed": "NewNetworkDRC721",
    "drc721-reserveless": "DRC721Reserveless",
    "drc1155-backed": "MintCandyIsolatedDRC1155",
    "drc1155-reserveless": "DRC1155Reserveless",
}
POLICIES = {"кошелёк", "тестнет", "активация", "закрыто", "служебная"}


def canonical(arg):
    if arg["type"].startswith("tuple"):
        members = ",".join(canonical(item) for item in arg["components"])
        return f"({members})" + arg["type"][len("tuple"):]
    return arg["type"]


def writes(role):
    abi = json.loads((ARTIFACTS / (ROLES[role] + ".json")).read_bytes())["abi"]
    return {
        entry["name"] + "(" + ",".join(map(canonical, entry["inputs"])) + ")"
        for entry in abi if entry["type"] == "function"
        and entry["stateMutability"] not in {"view", "pure"}
    }


def verify(sdk_path=None):
    rows = {}
    role = None
    for line in MATRIX.read_text(encoding="utf-8").splitlines():
        if line.startswith("### "):
            role = line[4:].strip()
        if not line.startswith("| `"):
            continue
        signature, policy, sdk, console = [s.strip() for s in line.strip("|").split("|")]
        signature = signature.strip("`")
        if role not in ROLES or (role, signature) in rows or policy not in POLICIES:
            raise ValueError(f"Unexpected or duplicate row: {role} {line}")
        if sdk not in {"да", "нет", "возврат"} or console not in {"профиль", "скрыта", "нет", "возврат"}:
            raise ValueError(f"Invalid client status: {role} {signature}")
        rows[role, signature] = sdk

    expected = {(role, sig) for role in ROLES for sig in writes(role)}
    if expected != set(rows):
        raise ValueError(f"Unreviewed ABI changes: added={sorted(expected - rows.keys())}, removed={sorted(rows.keys() - expected)}")
    if sdk_path:
        source = sdk_path.read_text(encoding="utf-8")
        raw = source.split("export const NATIVE_EVM_SIGNATURES = ", 1)[1].lstrip()
        sdk, _ = json.JSONDecoder().raw_decode(raw)
        listed = {(role, sig) for role in ROLES for sig in sdk[role]}
        marked = {key for key, status in rows.items() if status == "да"}
        if listed != marked:
            raise ValueError(f"SDK drift: added={sorted(listed - marked)}, removed={sorted(marked - listed)}")
    print(f"Verified {len(rows)} writable signatures across {len(ROLES)} roles")


if __name__ == "__main__":
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--sdk", type=Path, help="optional path to system-abis.generated.ts")
    verify(parser.parse_args().sdk)
