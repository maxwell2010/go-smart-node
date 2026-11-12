"""Start a fresh native network and replay it on a new ordinary peer."""

import argparse
import base64
from contextlib import nullcontext
import hashlib
import ipaddress
import json
from pathlib import Path
import re
import signal
import stat
import subprocess
import tempfile
import time

from configure_history import validate_fresh_home
from del_delegation_smoke import balance, write_config
from mcnd_process_smoke import ProcessOwner, SignalGuard, private_environment, private_namespace, run_command
from native_consensus_smoke import (
    check_signatures, committed_header, create_replica, decode_pubkey, effective_set, wait_peer_pair, wait_replicas,
)
from withdrawal_genesis_smoke import ADDRESS, SHA256, bech32_payload, check, ethereum, log_records, native, private_bytes, request, safe_diagnostic


GENESIS_LIMIT = 64 * 1024 * 1024


def load_fixture(directory, *, four_validators=False):
    if four_validators:
        evm_id, labels, powers = 990012, ("a", "b", "c", "d"), (10000, 10000, 10000, 10000)
    else:
        evm_id, labels, powers = 990011, ("a", "b", "c"), (4, 4, 2)
    chain_id = f"mintcandy_{evm_id}-1"
    info = directory.lstat()
    check(stat.S_ISDIR(info.st_mode) and stat.S_IMODE(info.st_mode) & 0o077 == 0, "private fixture directory required")
    genesis = private_bytes(directory / "genesis.json", GENESIS_LIMIT)
    public_genesis = private_bytes(directory / "public/genesis.json", GENESIS_LIMIT)
    check(genesis == public_genesis, "public genesis differs from the exact launch fixture")
    doc = json.loads(genesis)
    state = doc["app_state"]
    profile = state["withdrawal_settlement"]
    check(doc["chain_id"] == chain_id and int(doc["initial_height"]) == 1 and not doc.get("validators"),
          "launch rehearsal requires its CI-only height-one chain and application-derived validators")
    check(all(profile.get(flag) is True for flag in (
        "enable_del_delegation", "enable_del_hold", "enable_nft_delegation", "enable_native_consensus",
        "enable_native_fee_rewards", "enable_native_ibc_v1", "enable_native_governance", "enable_native_duplicate_vote",
    )), "the fixture must enable the full native launch profile")
    expected = json.loads(private_bytes(directory / "expectations.json", 8192))
    check(expected["chainId"] == chain_id and expected["evmChainId"] == evm_id, "fixture signing domain mismatch")
    check(re.fullmatch(ADDRESS, expected["actor"]) and bech32_payload(expected["actorNative"]) == expected["actor"].lower(),
          "invalid public balance query account")
    validators = expected["validators"]
    check(tuple(row["name"] for row in validators) == labels and
          tuple(row["initialPower"] for row in validators) == powers, "unexpected initial native validator set")
    keys = []
    identities = set()
    for row in validators:
        key = decode_pubkey(row["publicKey"])
        identity = hashlib.sha256(key).hexdigest()[:40].upper()
        data = private_bytes(directory / f"priv_validator_key_{row['name']}.json", 8192)
        record = json.loads(data)
        check(identity == row["consensusAddress"] == record["address"] and decode_pubkey(record["pub_key"]) == key,
              "private fixture key does not match its public consensus identity")
        identities.add(identity)
        keys.append(data)
    check(len(identities) == len(validators), "repeated genesis consensus identity")
    return genesis, expected, keys


def configure_peers(node, peers):
    addresses = [f"{peer.node_id}@127.0.0.1:{peer.p2p}" for peer in peers]
    write_config(node.home, "config.toml", {
        ("p2p", "persistent_peers"): json.dumps(",".join(addresses)),
        ("p2p", "allow_duplicate_ip"): "true", ("p2p", "pex"): "false", ("p2p", "seeds"): '""',
        ("statesync", "enable"): "false",
    })
    return addresses


def loopback_address(address):
    host, port = address.removeprefix("tcp://").rsplit(":", 1)
    parsed = ipaddress.ip_address(host.strip("[]"))
    check(parsed.is_loopback and port.isdecimal() and 0 < int(port) <= 65535, "non-loopback socket in the launch namespace")
    return f"{parsed.compressed}:{int(port)}" if parsed.version == 4 else f"[{parsed.compressed}]:{int(port)}"


def listener_addresses(node):
    ports = (node.p2p, node.p2p + 1, node.grpc)
    addresses = {f"127.0.0.1:{port}" for port in ports}
    addresses.update((node.rest.removeprefix("http://"), node.evm.removeprefix("http://")))
    return addresses


def verify_network(nodes, follower, configured, env, *, stopped=None):
    result = subprocess.run(["ss", "-H", "-ltn"], check=True, capture_output=True, text=True, timeout=10, env=env)
    listeners = {loopback_address(line.split()[3]) for line in result.stdout.splitlines() if line.strip()}
    if stopped is not None:
        check(not (listeners & listener_addresses(stopped)), "the stopped validator still has a P2P or RPC listener")
    members = {node.node_id: node for node in nodes}
    observed = {}
    for node in nodes:
        check(listener_addresses(node) <= listeners, f"{node.label} is missing a required loopback P2P or RPC listener")
        observations = []
        for peer in node.rpc("net_info")["peers"] or []:
            info = peer["node_info"]
            peer_node = members.get(info["id"])
            check(peer_node is not None and peer_node is not node and info["network"] == node.chain_id,
                  "unexpected P2P identity in the private network")
            peer_address = loopback_address(info["listen_addr"])
            remote_ip = ipaddress.ip_address(peer["remote_ip"])
            check(remote_ip.is_loopback and peer_address == f"127.0.0.1:{peer_node.p2p}",
                  "observed P2P peer differs from its configured loopback listener")
            observations.append({"node": peer_node.label, "nodeId": info["id"], "address": peer_address,
                                 "remoteIP": remote_ip.compressed, "outbound": peer["is_outbound"]})
        check(observations, f"{node.label} has no observed P2P connection")
        observed[node.label] = sorted(observations, key=lambda peer: peer["node"])
    check(any(peer["outbound"] for peer in observed[follower.label]), "ordinary peer did not connect using its persistent peers")
    return {"listeners": sorted(listeners), "configuredPersistentPeers": configured, "observedPeers": observed}


def check_signing(nodes, validators, first_height, last_height):
    powers = {row["consensusAddress"]: row["initialPower"] for row in validators}
    total_power = sum(powers.values())
    seen = set()
    for at in range(first_height, last_height + 1):
        signers = []
        for node in nodes:
            result = node.rpc(f"commit?height={at}")
            commit = result["signed_header"]["commit"]
            check(result["canonical"] is True and int(commit["height"]) == at, "expected canonical Comet commits")
            signed = {row["validator_address"] for row in commit["signatures"]
                      if int(row["block_id_flag"]) == 2 and row["signature"]}
            check(signed <= powers.keys() and sum(powers[addr] for addr in signed) * 3 > total_power * 2,
                  "commit must have a quorum of admitted genesis signers")
            signers.append(signed)
        check(all(signed == signers[0] for signed in signers), "nodes disagree on committed signatures")
        seen.update(signers[0])
    check(seen == powers.keys(), "each admitted validator must sign a block during the startup sample")
    return [row["name"] for row in validators if row["consensusAddress"] in seen]


def verify_first_block(nodes):
    first = []
    for node in nodes:
        result = node.rpc("block?height=1")
        header = result["block"]["header"]
        block_hash = result["block_id"]["hash"]
        check(header["chain_id"] == node.chain_id and int(header["height"]) == 1 and re.fullmatch(r"[A-Fa-f0-9]{64}", block_hash),
              "missing block 1 in a node's P2P history")
        check(int(node.rpc("status")["sync_info"]["earliest_block_height"]) == 1, "node did not keep history from block 1")
        first.append(block_hash)
    check(len(set(first)) == 1, "ordinary peer replayed a different block 1")
    return first[0]


def confirm_outage(validators, stopped, rows, powers, owner, report):
    report["phase"] = "quorum-after-validator-stop"
    check(stopped.child.poll() is None, "validator exited before the planned stop")
    owner.stop(stopped.child)
    check(stopped.child.returncode in (0, -signal.SIGINT), "validator did not exit normally after the planned stop")
    stop_height = max(int(node.rpc("abci_info")["response"]["last_block_height"]) for node in validators)
    # A vote for the next height may have been in flight when the process stopped.
    first_height, last_height = stop_height + 2, stop_height + 4
    wait_replicas(validators, last_height + 1, 100, report)
    members = effective_set(validators, rows, last_height, powers)
    for height in range(first_height, last_height + 1):
        signers = check_signatures(validators, rows, height, powers, missing=(stopped.label,))
    signed_power = sum(members[label] for label in signers)
    total_power = sum(members.values())
    check(signed_power * 3 > total_power * 2, "the live validators did not sign with more than two thirds of total power")
    report["outage"] = {
        "stoppedValidator": stopped.label, "stopExitCode": stopped.child.returncode,
        "highestCommittedHeightAfterStop": stop_height, "firstCanonicalCommitHeight": first_height,
        "lastCanonicalCommitHeight": last_height, "signers": signers, "signedPower": signed_power,
        "totalValidatorPower": total_power, "stoppedValidatorPower": members[stopped.label],
    }


def treasury_state(nodes, fixture, height):
    def read(node):
        state = {}
        for role in ("treasury", "recipient", "feeCollector"):
            address = fixture[role]
            result = native(f"/cosmos/bank/v1beta1/balances/{address}/by_denom?denom=del", height, base=node.rest)
            coin = result["balance"]
            check(coin["denom"] == "del", "treasury balance query returned a different denomination")
            state[role + "Atomic"] = str(int(coin["amount"]))
        account = native(f"/cosmos/auth/v1beta1/account_info/{fixture['treasury']}", height, base=node.rest)["info"]
        check(account["address"] == fixture["treasury"], "treasury sequence query returned a different account")
        state["sequence"] = int(account.get("sequence", "0"))
        return state

    snapshots = [read(node) for node in nodes]
    check(all(state == snapshots[0] for state in snapshots), "nodes disagree on the committed treasury state")
    return snapshots[0]


def broadcast_treasury_tx(node, raw):
    check(isinstance(raw, str) and re.fullmatch(r"(?:[0-9a-f]{2}){100,2048}", raw), "invalid signed treasury fixture")
    tx = bytes.fromhex(raw)
    tx_hash = hashlib.sha256(tx).hexdigest()
    payload = {"jsonrpc": "2.0", "id": 1, "method": "broadcast_tx_sync",
               "params": {"tx": base64.b64encode(tx).decode("ascii")}}
    response, _ = request(f"http://127.0.0.1:{node.p2p + 1}", payload)
    check("error" not in response and "result" in response, "treasury broadcast RPC failed")
    result = response["result"]
    check(result["hash"].lower().removeprefix("0x") == tx_hash, "treasury broadcast returned a different transaction hash")
    return tx_hash, result


def rehearse_treasury(nodes, fixture, height, report):
    report["phase"] = "treasury-two-signatures"
    check(len({bech32_payload(fixture[key]) for key in ("treasury", "recipient", "feeCollector")}) == 3,
          "treasury fixture requires distinct bank accounts")
    amount, fee = int(fixture["amountAtomic"]), int(fixture["feeAtomic"])
    check(amount > 0 and fee > 0 and fixture["invalidTx"] != fixture["validTx"], "invalid treasury transfer pair")
    before = treasury_state(nodes, fixture, height)
    check(before["treasuryAtomic"] == str(14_000 * 10**18) and before["feeCollectorAtomic"] == "0" and
          before["sequence"] == fixture["initialSequence"], "unexpected initial treasury state")
    source = nodes[0]
    rejected_hash, rejected = broadcast_treasury_tx(source, fixture["invalidTx"])
    check(rejected.get("codespace") == "sdk" and int(rejected["code"]) == 4 and
          "signature verification failed" in rejected.get("log", ""), "two-signature treasury transfer was not rejected by signature verification")
    tip = int(source.rpc("abci_info")["response"]["last_block_height"])
    after_rejected_height = wait_replicas(nodes, tip + 2, 40, report)
    after_rejected = treasury_state(nodes, fixture, after_rejected_height)
    check(after_rejected == before, "rejected treasury transfer changed a balance or the sequence")
    report["treasury"] = {
        "amountAtomic": str(amount), "feeAtomic": str(fee), "beforeHeight": height, "before": before,
        "rejected": {"signatures": 2, "hash": rejected_hash, "checkTxCode": 4,
                     "verifiedHeight": after_rejected_height, "state": after_rejected},
    }

    report["phase"] = "treasury-three-signatures"
    tx_hash, accepted = broadcast_treasury_tx(source, fixture["validTx"])
    check(int(accepted.get("code", 0)) == 0, f"three-signature treasury transfer failed CheckTx with code {accepted.get('code')}")
    deadline = time.monotonic() + 40
    while True:
        try:
            result = source.rpc(f"tx?hash=0x{tx_hash}&prove=false")
            break
        except (OSError, ValueError):
            check(time.monotonic() < deadline and source.child.poll() is None, "accepted treasury transfer was not indexed")
            time.sleep(0.25)
    check(result["hash"].lower().removeprefix("0x") == tx_hash, "indexed a different treasury transaction")
    check(int(result["tx_result"].get("code", 0)) == 0, "three-signature treasury transfer failed block execution")
    committed_height = int(result["height"])
    check(committed_height > after_rejected_height, "treasury transfer committed before the rejection check")
    wait_replicas(nodes, committed_height + 1, 60, report)
    after = treasury_state(nodes, fixture, committed_height)
    check(after == {"treasuryAtomic": str(int(before["treasuryAtomic"]) - amount - fee),
                    "recipientAtomic": str(int(before["recipientAtomic"]) + amount), "feeCollectorAtomic": str(fee),
                    "sequence": before["sequence"] + 1}, "committed treasury transfer did not reconcile")
    app_hash = committed_header(nodes, committed_height + 1)["app_hash"]
    report["treasury"]["committed"] = {"signatures": 3, "hash": tx_hash, "height": committed_height,
                                         "appHash": app_hash, "state": after}


def wait_caught_up(node):
    deadline = time.monotonic() + 30
    status = node.rpc("status")
    while status["sync_info"]["catching_up"]:
        check(time.monotonic() < deadline, f"{node.label} did not finish P2P block sync")
        time.sleep(0.25)
        status = node.rpc("status")
    return status


def verify_follower_resume(follower, validators, expected, report, previous_handshakes, minimum_loaded,
                           minimum_height=0):
    wait_peer_pair(validators[0], follower, 60, report)
    handshakes = log_records(follower.log, "ABCI Handshake App Info")[previous_handshakes:]
    check(len(handshakes) == 1, "service startup must produce one new application handshake")
    loaded = int(handshakes[0]["height"])
    check(loaded >= minimum_loaded > 0, "follower did not load its previously committed application state")

    nodes = [*validators, follower]
    height = wait_replicas(nodes, max(loaded + 4, minimum_height + 1), 100, report) - 1
    loaded_hash = committed_header(validators, loaded + 1)["app_hash"]
    check(handshakes[0]["hash"].upper() == loaded_hash.upper(), "restarted follower loaded a different AppHash")
    status = wait_caught_up(follower)
    check(status["node_info"]["id"] == follower.node_id and int(status["validator_info"]["voting_power"]) == 0,
          "systemd restart changed the follower's peer identity or voting power")
    check(verify_first_block(nodes) == report["block1Hash"], "systemd restart did not preserve block 1")
    app_hash = committed_header(nodes, height + 1)["app_hash"]
    values = {balance(expected["actor"], expected["actorNative"], height, node.evm, node.rest) for node in nodes}
    check(len(values) == 1 and next(iter(values)) > 0, "restarted follower disagrees on the committed DEL balance")
    return {"loadedHeight": loaded, "loadedAppHash": loaded_hash, "verifiedHeight": height,
            "appHash": app_hash, "sameNodeId": True, "catchingUp": False}


def verify_systemd_restart(systemd, follower, validators, expected, report):
    report["phase"] = "systemd-follower-restart"
    before = int(follower.rpc("abci_info")["response"]["last_block_height"])
    previous_handshakes = len(log_records(follower.log, "ABCI Handshake App Info"))
    check(before > 0 and previous_handshakes > 0, "systemd follower must commit state before restarting")
    restart = systemd.restart()
    synced = verify_follower_resume(follower, validators, expected, report, previous_handshakes, before)
    report["systemdRestart"] = {**restart, "beforeHeight": before, **synced}
    return synced["verifiedHeight"]


def verify_systemd_upgrade(systemd, follower, validators, expected, genesis, upgrade_binary, report):
    report["phase"] = "systemd-follower-upgrade"
    before = int(follower.rpc("abci_info")["response"]["last_block_height"])
    previous_handshakes = len(log_records(follower.log, "ABCI Handshake App Info"))
    check(before > 0 and previous_handshakes > 0, "systemd follower must commit state before upgrading")
    package = report["upgradePackage"]
    genesis_path = follower.home / "config/genesis.json"
    upgraded = systemd.upgrade(upgrade_binary, package["binarySha256"])
    check(upgraded["binarySha256"] == package["binarySha256"] and upgraded["backupBinarySha256"] == report["binarySha256"],
          "updated service or backup executable differs from its signed package")
    check(private_bytes(genesis_path, GENESIS_LIMIT) == genesis, "upgrade changed the follower genesis")
    synced = verify_follower_resume(follower, validators, expected, report, previous_handshakes, before)
    if "iavlBefore" in upgraded:
        check(upgraded["iavlBefore"]["committedHeight"] == synced["loadedHeight"],
              "upgraded daemon did not load the measured IAVL backup height")
    report["systemdUpgrade"] = {**upgraded, "beforeHeight": before, **synced}

    report["phase"] = "systemd-follower-rollback"
    previous_handshakes = len(log_records(follower.log, "ABCI Handshake App Info"))
    rollback = systemd.rollback()
    check(rollback["binarySha256"] == report["binarySha256"] and rollback["backupHomeSha256"] == upgraded["backupHomeSha256"],
          "rollback used a different executable or backup")
    check(private_bytes(genesis_path, GENESIS_LIMIT) == genesis, "rollback changed the follower genesis")
    restored = verify_follower_resume(follower, validators, expected, report, previous_handshakes, synced["loadedHeight"],
                                      minimum_height=synced["verifiedHeight"] + 1)
    check((restored["loadedHeight"], restored["loadedAppHash"]) == (synced["loadedHeight"], synced["loadedAppHash"]),
          "rollback did not load the offline backup checkpoint")
    report["systemdRollback"] = {**rollback, **restored}
    if "iavlBefore" in upgraded:
        check(rollback["iavlAfterUpgrade"]["committedHeight"] >= synced["verifiedHeight"] and
              rollback["iavlAfterUpgrade"]["fastStorageHeight"] >= synced["loadedHeight"] and
              rollback["iavlRestored"]["committedHeight"] == restored["loadedHeight"],
              "offline IAVL database heights differ from the running service checkpoints")
        final_schema = systemd.inspect_iavl_after_rollback()
        check(final_schema["committedHeight"] >= restored["verifiedHeight"],
              "rolled-back follower did not persist its synchronized height")
        report["systemdRollback"]["iavlAfterSync"] = final_schema


def run_launch(binary, root, genesis, expected, keys, env, owner, report, *, four_validators=False, treasury=None,
               systemd=None, restart_snapshot=False, upgrade_binary=None):
    rows = expected["validators"]
    powers = tuple(row["initialPower"] for row in rows)
    options = {"chain_id": expected["chainId"], "evm_chain_id": expected["evmChainId"]}
    validators = [create_replica(binary, root, label, index, genesis, key, env, **options)
                  for index, (label, key) in enumerate(zip((row["name"] for row in rows), keys, strict=True))]
    follower_binary, follower_root, follower_owner = binary, root, owner
    if systemd is not None:
        report["phase"] = "systemd-follower-install"
        follower_binary, follower_root, follower_owner = systemd.binary, systemd.root, systemd
        check(not (follower_root / "follower").exists(), "systemd follower installation requires an absent node home")
    follower = create_replica(follower_binary, follower_root, "follower", len(validators), genesis, None, env, **options)
    nodes = [*validators, follower]
    check(len({node.node_id for node in nodes}) == len(nodes), "duplicate launch P2P identity")
    stopped = validators[-1] if four_validators else None
    live_validators = [node for node in validators if node is not stopped]
    if restart_snapshot:
        from native_launch_snapshot import configure_snapshot_source, rehearse_snapshot

        configure_snapshot_source(live_validators[0])
    configured = {node.label: configure_peers(node, [peer for peer in validators if peer is not node]) for node in validators}
    configured[follower.label] = configure_peers(follower, live_validators)
    try:
        run_command(binary, validators[0].home, ["genesis", "validate-genesis"], env)
        if systemd is not None:
            run_command(follower_binary, follower.home, ["genesis", "validate-genesis"], env)
    except RuntimeError as error:
        raise RuntimeError("packaged daemon rejected the launch genesis: " + safe_diagnostic(str(error))) from None
    for node in nodes:
        check(private_bytes(node.home / "config/genesis.json", GENESIS_LIMIT) == genesis, "a prepared node has a different genesis")
    report["phase"] = "initial-native-consensus"
    for node in validators:
        node.start(binary, env, owner, "launch-validator")
    started_at = wait_replicas(validators, 8, 100, report)
    effective_set(validators, rows, 1, powers)
    if stopped is not None:
        last_startup_height = wait_replicas(validators, started_at + 4, 60, report) - 1
        report["initialConfirmedSigners"] = check_signing(validators, rows, 1, last_startup_height)
        report["lastStartupCanonicalCommitHeight"] = last_startup_height
        confirm_outage(live_validators, stopped, rows, powers, owner, report)
    # The ordinary peer has its own node and signing keys and a clean home.
    # It starts after the validators have committed a history to replay.
    validate_fresh_home(follower.home)
    tip = min(int(node.rpc("abci_info")["response"]["last_block_height"]) for node in live_validators)
    report["validatorHeightBeforeJoin"] = tip
    report["phase"] = "ordinary-peer-p2p-catchup"
    if systemd is not None:
        report["phase"] = "systemd-follower-start"
        systemd.prepare(follower.home)
    follower.start(follower_binary, env, follower_owner, "launch-follower")
    report["phase"] = "ordinary-peer-p2p-catchup"
    nodes = [*live_validators, follower]
    wait_peer_pair(live_validators[0], follower, 60, report)
    observed_height = wait_replicas(nodes, tip + 5, 150, report)
    follower_status = wait_caught_up(follower)
    report["phase"] = "verified-network-state"
    height = observed_height - 1
    report["block1Hash"] = verify_first_block(nodes)
    report["verifiedHeight"] = height
    report["appHash"] = committed_header(nodes, height + 1)["app_hash"]
    report["validators"] = effective_set(nodes, rows, height, powers)
    if stopped is None:
        report["confirmedSigners"] = check_signing(nodes, rows, height - 3, height)
    else:
        report["confirmedSigners"] = check_signatures(nodes, rows, height, powers, missing=(stopped.label,))
    info = follower_status["validator_info"]
    check(int(info["voting_power"]) == 0 and info["address"] not in {row["consensusAddress"] for row in expected["validators"]},
          "ordinary peer unexpectedly participates in validator consensus")
    report["followerVotingPower"] = 0
    report["phase"] = "rpc-and-peer-evidence"
    values = set()
    for node in nodes:
        check(int(ethereum("eth_chainId", [], base=node.evm), 16) == expected["evmChainId"], "wrong JSON-RPC signing domain")
        values.add(balance(expected["actor"], expected["actorNative"], height, node.evm, node.rest))
    check(len(values) == 1 and next(iter(values)) > 0, "nodes disagree on the positive native and EVM balance")
    first_balance = balance(expected["actor"], expected["actorNative"], 1, follower.evm, follower.rest)
    check(first_balance == next(iter(values)), "ordinary peer cannot query the same funded state at block 1")
    report["rpc"] = {"historicalHeight": 1, "verifiedHeight": height, "delAtomic": str(first_balance)}
    report.update(verify_network(nodes, follower, configured, env, stopped=stopped))
    if systemd is not None:
        report["phase"] = "systemd-follower-evidence"
        evidence = systemd.evidence()
        check(evidence["binarySha256"] == report["binarySha256"], "systemd executable differs from the verified package")
        genesis_path = follower.home / "config/genesis.json"
        check(private_bytes(genesis_path, GENESIS_LIMIT) == genesis, "running systemd follower genesis differs from the verified package")
        report["systemdFollower"] = {**evidence, "freshHome": True, "genesisSha256": report["genesisSha256"],
                                     "catchingUp": False, "syncedFromHeight": 1, "verifiedHeight": height}
    if treasury is not None:
        rehearse_treasury(nodes, treasury, height, report)
    if restart_snapshot:
        restarted_height = verify_systemd_restart(systemd, follower, live_validators, expected, report)
        rehearse_snapshot(follower_binary, root, genesis, expected, env, owner, report,
                          source=live_validators[0], witness=follower, replicas=nodes, minimum_height=restarted_height)
    if upgrade_binary is not None:
        verify_systemd_upgrade(systemd, follower, live_validators, expected, genesis, upgrade_binary, report)


def main(argv=None, signals=None):
    parser = argparse.ArgumentParser(description=__doc__)
    for flag in ("binary", "binary-sha256", "archive-sha256", "source-commit", "fixture", "home-parent", "report"):
        parser.add_argument("--" + flag, required=True)
    parser.add_argument("--four-validators", action="store_true", help="check quorum and peer catchup after stopping one of four validators")
    parser.add_argument("--treasury-transfer", action="store_true", help="check the signed transfer pair on the four-validator fixture")
    parser.add_argument("--package-genesis", type=Path, help="use genesis extracted from the verified launch package")
    parser.add_argument("--systemd-follower", action="store_true", help="start the clean installed follower as a systemd service")
    parser.add_argument("--restart-and-snapshot", action="store_true", help="verify systemd restart and a fresh state-sync peer")
    parser.add_argument("--upgrade-package", type=Path, help="verified replacement package directory")
    parser.add_argument("--upgrade-package-report", type=Path, help="signature and checksum report for the replacement package")
    parser.add_argument("--iavl-schema-probe", type=Path, help="inspect the IAVL storage upgrade with the node stopped")
    args = parser.parse_args(argv)
    if args.treasury_transfer and not args.four_validators:
        parser.error("--treasury-transfer requires --four-validators")
    if args.systemd_follower and not args.package_genesis:
        parser.error("--systemd-follower requires --package-genesis")
    if args.restart_and_snapshot and not args.systemd_follower:
        parser.error("--restart-and-snapshot requires --systemd-follower")
    if (args.upgrade_package is None) != (args.upgrade_package_report is None) or (args.upgrade_package and not args.systemd_follower):
        parser.error("--upgrade-package and --upgrade-package-report require each other and --systemd-follower")
    if args.iavl_schema_probe and not args.upgrade_package:
        parser.error("--iavl-schema-probe requires --upgrade-package")
    report = {"status": "FAIL", "phase": "fixture-provenance", "initialHeight": 1}
    with Path(args.report).open("x", encoding="utf8") as stream:
        try:
            private_namespace()
            report["loopbackOnlyNamespace"] = True
            check(re.fullmatch(SHA256, args.binary_sha256) and re.fullmatch(SHA256, args.archive_sha256) and
                  re.fullmatch(r"[0-9a-f]{40}", args.source_commit), "invalid packaged daemon provenance")
            binary = str(Path(args.binary).resolve(strict=True))
            with open(binary, "rb") as daemon:
                check(hashlib.file_digest(daemon, "sha256").hexdigest() == args.binary_sha256, "packaged daemon hash differs")
            genesis, expected, keys = load_fixture(Path(args.fixture), four_validators=args.four_validators)
            if args.package_genesis:
                packaged_genesis = private_bytes(args.package_genesis, GENESIS_LIMIT)
                check(packaged_genesis == genesis, "verified package genesis differs from the cold-init fixture")
                genesis = packaged_genesis
            treasury = json.loads(private_bytes(Path(args.fixture) / "treasury.json", 8192)) if args.treasury_transfer else None
            report.update(sourceCommit=args.source_commit, binarySha256=args.binary_sha256, archiveSha256=args.archive_sha256,
                          genesisSha256=hashlib.sha256(genesis).hexdigest(), chainId=expected["chainId"], evmChainId=expected["evmChainId"])
            with tempfile.TemporaryDirectory(prefix="mcnd-native-launch-", dir=args.home_parent) as directory:
                root = Path(directory)
                env = private_environment(root)
                version = json.loads(run_command(binary, root, ["version", "--long", "--output", "json"], env))
                check(version["server_name"] == "mcnd" and version["commit"] == args.source_commit, "packaged daemon revision differs")
                upgrade_binary = None
                if args.upgrade_package:
                    report["phase"] = "upgrade-package-provenance"
                    package = json.loads(private_bytes(args.upgrade_package_report, 8192))
                    upgrade_binary = str((args.upgrade_package / "bin/mcnd").resolve(strict=True))
                    check(package["archiveSha256"] != args.archive_sha256 and package["binarySha256"] != args.binary_sha256 and
                          package["sourceCommit"] != args.source_commit, "upgrade requires a distinct signed archive, binary and source revision")
                    check(private_bytes(args.upgrade_package / "genesis.json", GENESIS_LIMIT) == genesis,
                          "upgrade package genesis differs from the original signed package")
                    with open(upgrade_binary, "rb") as daemon:
                        check(hashlib.file_digest(daemon, "sha256").hexdigest() == package["binarySha256"], "upgrade binary hash differs")
                    version = json.loads(run_command(upgrade_binary, root, ["version", "--long", "--output", "json"], env))
                    check(version["server_name"] == "mcnd" and version["commit"] == package["sourceCommit"], "upgrade daemon revision differs")
                    report["upgradePackage"] = package
                owner = ProcessOwner(signals)
                try:
                    service = nullcontext()
                    if args.systemd_follower:
                        from native_launch_systemd import SystemdFollower

                        report["phase"] = "systemd-follower-install"
                        service = SystemdFollower(binary, args.binary_sha256, signals=signals,
                                                  iavl_schema_probe=args.iavl_schema_probe)
                    with service as systemd:
                        run_launch(binary, root, genesis, expected, keys, env, owner, report,
                                   four_validators=args.four_validators, treasury=treasury, systemd=systemd,
                                   restart_snapshot=args.restart_and_snapshot, upgrade_binary=upgrade_binary)
                finally:
                    owner.close()
            report.update(status="PASS", phase="complete")
        except Exception as error:
            report["error"] = " ".join(str(error).split())[:480]
        finally:
            json.dump(report, stream, indent=2)
            stream.write("\n")
    print(json.dumps(report, indent=2))
    return 0 if report["status"] == "PASS" else 1


if __name__ == "__main__":
    with SignalGuard() as guard:
        raise SystemExit(main(signals=guard))
