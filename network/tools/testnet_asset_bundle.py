"""Rehearse signed wallet operations on the same disposable chain as DRC20 trading."""
from collections import Counter
import hashlib
import json
import subprocess

from del_delegation_smoke import read_words
from native_launch_media import commit_media_transaction
from withdrawal_genesis_smoke import bech32_payload, check, ethereum, native, private_bytes

UNIT = 10**18
CHAIN = "mintcandy_990098-1"


def native_token(node, asset, token_id, height):
    raw = bytes.fromhex(asset["target"][2:])
    domain = b"mintcandy:nft-binding:v1\0" + bytes([len(CHAIN)]) + CHAIN.encode()
    key = hashlib.sha256(domain + (990098).to_bytes(8, "big") + raw + token_id.to_bytes(32, "big")).hexdigest()
    return native("/decimal/nft/v1/token/evm1_" + key, height, base=node.rest)["token"]


def holdings(node, asset, token_id, height, wallets):
    token = native_token(node, asset, token_id, height)
    serials = [(int(row["id"]), bech32_payload(row["owner"])) for row in token.get("sub_tokens", [])]
    owners = Counter(owner for _, owner in serials)
    balances = []
    for wallet in wallets:
        account = wallet[2:].rjust(64, "0")
        data = ("0x00fdd58e" + account + f"{token_id:064x}" if asset["multi"] else "0x70a08231" + account)
        amount = read_words(asset["target"], data, height, 1, node.evm)[0]
        check(amount == owners[wallet], f"EVM differs from native holder count for token {token_id} at {height}")
        balances.append(amount)
    burnt = int(token.get("burnt", 0))
    check(len(serials) == int(token["minted"]) - burnt and len(set(i for i, _ in serials)) == len(serials),
          "duplicate or missing NFT serial")
    check(token["reserve"]["denom"] == "tmsp", "unexpected NFT reserve denomination")
    return balances, burnt, int(token["reserve"]["amount"]), sorted(serials)


def rehearse_bundle(signer, root, nodes, expected, env, report, token_pair, other):
    check(expected["chainId"] == CHAIN and report["loopbackOnlyNamespace"], "isolated tMSP only")
    source, observer = nodes[0], nodes[-1]
    genesis = json.loads(private_bytes(source.home / "config/genesis.json", 64 * 1024 * 1024))
    center = genesis["app_state"]["withdrawal_settlement"]["nft_center"]
    owner = expected["actor"].lower()
    wallets = [owner, other.lower()]
    def nonce(address):
        return str(int(ethereum("eth_getTransactionCount", [address, "latest"], base=source.evm), 16))
    directory = root / "asset-bundle"
    directory.mkdir(mode=0o700)
    subprocess.run([str(signer), "-test.run=^TestSignAssetBundle$"], check=True, timeout=45,
                   env=dict(env, MCND_ASSET_BUNDLE=str(directory), MCND_NONCE_A=nonce(owner),
                            MCND_NONCE_B=nonce(other), MCND_FACTORY_NONCE=nonce(center), MCND_NFT_CENTER=center,
                            MCND_TOKEN_BACKED=token_pair[0], MCND_TOKEN_FREE=token_pair[1],
                            MCND_TESTNET_TARIFF_FOUNDER_FILE=str(root.parent / "tariff-founder.key")))
    fixture = json.loads(private_bytes(directory / "bundle.json", 360000))
    check(wallets == [fixture["owner"].lower(), fixture["operator"].lower()], "incorrect fixture signers")
    assets = {a["asset"]: a for a in fixture["assets"]}
    check(0 < len(fixture["rows"]) <= 64, "asset suite exceeds its block budget")
    outcomes = []
    report["assetBundle"] = {"chainId": CHAIN, "outcomes": outcomes, "observerMatched": False}
    minted = Counter()
    for row in fixture["rows"]:
        label, family = row["label"], row["asset"]
        asset = assets[family]
        report["phase"] = f"asset-{family}-{label}"
        receipt = commit_media_transaction(source, nodes, row, row["nonce"], report, status=int(row["success"]))
        height = int(receipt["blockNumber"], 16)
        check(row["success"] or not receipt["logs"], "rejected asset transaction emitted logs")
        token_id = 123 if asset["multi"] else 1
        token = asset["target"].lower()
        if label == "collection":
            local = [log for log in receipt["logs"] if log["address"].lower() == center.lower()]
            check(len(local) == 1 and ("0x" + local[0]["data"][26:66]).lower() == token,
                  "collection in receipt differs from the signed fixture")
        elif label == "mint":
            token_id += minted[family] if asset["multi"] else 0
            minted[family] += 1
        if "permit" in label:
            success_nonce = int(label in ("permit", "replayed-permit"))
            if success_nonce:
                approval = (1 if asset["multi"] else int(other, 16)) if asset["nft"] else 7
            else:
                approval = 0
            for node in (source, observer):
                for call, want in ((asset["nonceCall"], success_nonce), (asset["approvalCall"], approval)):
                    check(read_words(token, call, height, 1, node.evm)[0] == want,
                          f"unexpected permit state after {family} {label}")
        if asset["nft"] and label != "collection":
            ids = [123, 124] if "batch" in label else [token_id]
            for id_ in ids:
                actual = holdings(source, asset, id_, height, wallets)
                check(actual == holdings(observer, asset, id_, height, wallets), "observer disagrees about NFT state")
                if label == "mint":
                    check(actual[:3] == ([2 if asset["multi"] else 1, 0], 0, UNIT if asset["backed"] else 0),
                          "wrong initial NFT allocation")
                    continue
                old = holdings(source, asset, id_, height - 1, wallets)
                if label == "batch":
                    check(old[0] == [2, 0] and actual[0] == [1, 1] and old[1:3] == actual[1:3],
                          "batch failed to move exactly one unit of each ID")
                elif label == "topup":
                    supply = sum(old[0])
                    check(actual[:2] == old[:2] and actual[3] == old[3] and
                          actual[2] == old[2] + 2*UNIT//supply, "wrong per-unit NFT reserve")
                    pool = read_words(token, asset["reserveCall"], height, 3, observer.evm)
                    check(pool == [0, actual[2] * supply, 1], "reserve differs between NFT and native ledger")
                elif label == "burn":
                    check(actual[0] == [old[0][0]-1, old[0][1]] and actual[1] == old[1]+1
                          and actual[2] == old[2] == 0, "burn changed the wrong units or reserve")
                else:
                    check(actual == old, f"unexpected native NFT change after {label}")
            if label not in ("collection", "mint"):
                heights = (hex(height - 1), hex(height))
                escrow = [int(ethereum("eth_getBalance", [token, h], base=observer.evm), 16) for h in heights]
                check(escrow[1] - escrow[0] == (2*UNIT if label == "topup" else 0), "incorrect custody delta")
                if "topup" in label:
                    donor = row["creator"].lower()
                    balances = [int(ethereum("eth_getBalance", [donor, h], base=observer.evm), 16) for h in heights]
                    gas = int(receipt["gasUsed"], 16) * int(receipt["effectiveGasPrice"], 16)
                    reward = 0
                    for event in observer.rpc(f"block_results?height={height}").get("finalize_block_events") or []:
                        if event["type"] == "decimal_native_fee_reward":
                            attrs = {x["key"]: x["value"] for x in event["attributes"]}
                            if bech32_payload(attrs["recipient"]) == donor:
                                reward += int(attrs["amount"].removesuffix("tmsp"))
                    check(balances[1] - balances[0] == reward - gas - (escrow[1] - escrow[0]),
                          "donor debit differs from reserve payment and fee")
        outcomes.append({"asset": family, "case": label, "status": int(receipt["status"], 16),
                         "height": height, "hash": row["hash"]})
    check(len(assets) == 6 and sum(x["case"] == "permit" and x["status"] for x in outcomes) == 6,
          "missing permit family")
    report["assetBundle"]["observerMatched"] = True
