"""Verify snapshot state sync on the isolated signed-package launch network."""

import json
import time

from configure_history import validate_fresh_home
from del_delegation_smoke import balance, observe_state_sync, write_config
from native_consensus_smoke import committed_header, create_replica, wait_replicas
from withdrawal_genesis_smoke import check, ethereum, log_records, private_bytes


def configure_snapshot_source(source):
    write_config(source.home, "app.toml", {
        ("state-sync", "snapshot-interval"): "20",
        ("state-sync", "snapshot-keep-recent"): "4",
    })


def wait_for_snapshot(source, minimum_height):
    deadline = time.monotonic() + 100
    while time.monotonic() < deadline:
        check(source.child.poll() is None, "snapshot source stopped before publishing a snapshot")
        heights = [int(row["height"]) for row in log_records(source.log, "completed state snapshot")]
        if heights and max(heights) >= minimum_height:
            return max(heights)
        time.sleep(0.5)
    raise TimeoutError("source did not publish a snapshot after the restart checkpoint")


def rehearse_snapshot(binary, root, genesis, expected, env, owner, report, *,
                      source, witness, replicas, minimum_height):
    report["phase"] = "snapshot-source"
    check(source.node_id != witness.node_id and source.p2p != witness.p2p,
          "state sync requires two distinct trust RPC peers")
    snapshot_height = wait_for_snapshot(source, minimum_height)
    trust_height = snapshot_height + 2
    trust_peers = (source, witness)
    wait_replicas(trust_peers, trust_height, 60, report)
    trust_hash = committed_header(trust_peers, trust_height)["block_hash"]
    check(int(witness.rpc("status")["sync_info"]["earliest_block_height"]) == report["initialHeight"],
          "trust witness must have replayed from genesis")
    rpc_servers = [f"http://127.0.0.1:{node.p2p + 1}" for node in trust_peers]
    state_sync = {
        "sourceSnapshotHeight": snapshot_height,
        "trustHeight": trust_height,
        "trustHash": trust_hash,
        "trustRpcServers": rpc_servers,
        "sourceNodeId": source.node_id,
        "witnessNodeId": witness.node_id,
    }
    report["stateSync"] = state_sync

    report["phase"] = "snapshot-restore"
    index = max(node.index for node in replicas) + 1
    check(not (root / "snapshot-observer").exists(), "snapshot observer requires a new home")
    observer = create_replica(binary, root, "snapshot-observer", index, genesis, None, env,
                              chain_id=expected["chainId"], evm_chain_id=expected["evmChainId"])
    check(observer.node_id not in {node.node_id for node in replicas}, "snapshot observer reused a P2P identity")
    check(private_bytes(observer.home / "config/genesis.json", len(genesis)) == genesis,
          "snapshot observer genesis differs from the signed package")
    write_config(observer.home, "config.toml", {
        ("p2p", "persistent_peers"): json.dumps(f"{source.node_id}@127.0.0.1:{source.p2p}"),
        ("p2p", "pex"): "false",
        ("statesync", "enable"): "true",
        ("statesync", "rpc_servers"): json.dumps(",".join(rpc_servers)),
        ("statesync", "trust_height"): str(trust_height),
        ("statesync", "trust_hash"): json.dumps(trust_hash),
        ("statesync", "trust_period"): '"1h"',
        ("statesync", "discovery_time"): '"5s"',
    })
    validate_fresh_home(observer.home)
    observer.start(binary, env, owner, "restore")
    nodes = (*replicas, observer)
    try:
        tip = wait_replicas(nodes, trust_height + 3, 150, report)
        deadline = time.monotonic() + 30
        while observer.rpc("status")["sync_info"]["catching_up"]:
            check(time.monotonic() < deadline, "snapshot observer did not finish catch-up")
            time.sleep(0.25)
        restored = log_records(observer.log, "Snapshot restored")
        check(len(restored) == 1, "observer must restore exactly one P2P snapshot")
        restored_height = int(restored[0]["height"])
        source_heights = {int(row["height"]) for row in log_records(source.log, "completed state snapshot")}
        check(restored_height >= snapshot_height and restored_height in source_heights,
              "observer did not restore a snapshot published by the configured source")
    finally:
        state_sync["observerDiagnostics"] = observe_state_sync(observer.log, observer.rpc)

    report["phase"] = "snapshot-verified-state"
    status = observer.rpc("status")
    earliest_height = int(status["sync_info"]["earliest_block_height"])
    check(status["node_info"]["id"] == observer.node_id and int(status["validator_info"]["voting_power"]) == 0,
          "unexpected snapshot observer identity or voting power")
    check(earliest_height >= restored_height > report["initialHeight"],
          "snapshot observer retained block history older than the restored snapshot")
    check(int(ethereum("eth_chainId", [], base=observer.evm), 16) == expected["evmChainId"],
          "snapshot observer EVM signing domain differs from the signed package")
    height = max(tip - 1, restored_height + 2)
    wait_replicas(nodes, height + 1, 60, report)
    snapshot_hash = committed_header(trust_peers, restored_height + 1)["app_hash"]
    header = committed_header(nodes, height + 1)
    balances = {balance(expected["actor"], expected["actorNative"], height, node.evm, node.rest) for node in nodes}
    check(len(balances) == 1 and next(iter(balances)) > 0, "snapshot observer balance differs from its peers")
    state_sync.update(restoredHeight=restored_height, restoredAppHash=snapshot_hash,
                      earliestBlockHeight=earliest_height, observerNodeId=observer.node_id,
                      observerVotingPower=0, catchingUp=False, verifiedHeight=height,
                      appHash=header["app_hash"], delAtomic=str(next(iter(balances))))
