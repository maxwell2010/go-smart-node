"""Connect a disposable observer using the verified P2P values from genesis approval."""

from pathlib import Path
import socket
import tempfile

from check_public_launch_draft import DEFAULT_DRAFT, read_json
from install_validator_genesis import install_genesis
from native_launch_rehearsal import NETWORK_PROFILES
from public_genesis_manifest import prepare_manifest, verify_manifest
from test_founding_ledger_fixture import prepared_fixture
from test_public_genesis_manifest import signed_fixture
from withdrawal_genesis_smoke import check


def install_signed_test_bootstrap(binary, genesis, source_identity, source, follower, inputs, report):
    fixture, cold = inputs
    network_profile = report.get("networkProfile")
    expected_chain = "mintcandy_990014-1" if network_profile is None else f"mintcandy_{NETWORK_PROFILES[network_profile][0]}-1"
    check(report["loopbackOnlyNamespace"] and report["offlineAssembledGenesis"] and
          source.chain_id == expected_chain, "signed bootstrap test requires the isolated launch fixture")
    host = "peer-bootstrap.example.invalid"
    resolved = {row[4][0] for row in socket.getaddrinfo(host, source.p2p, type=socket.SOCK_STREAM)}
    check(resolved == {"127.0.0.1"}, "signed bootstrap test name must resolve only to its loopback listener")
    address = f"{source.node_id}@{host}:{source.p2p}"

    artifacts = fixture.parent
    _, applications, plan, relayers = prepared_fixture(artifacts / "fixture", read_json(artifacts / "roles.json"),
                                                       network_profile=network_profile)
    operator = next(row for row in applications["operators"]
                    if row["consensusPubkey"]["key"] == source_identity["publicKey"])
    operator.update(p2pNodeId=source.node_id, p2pAddress=address, endpointPublicationAllowed=True)
    installed = read_json(artifacts / "evidence/install.json")
    software = {key: installed[key] for key in ("releaseVersion", "sourceRevision", "binarySha256")}
    software["archiveSha256"] = report["archiveSha256"]
    check(software["sourceRevision"] == report["sourceCommit"] and software["binarySha256"] == report["binarySha256"],
          "installed bootstrap binary differs from the launch report")
    draft = read_json(DEFAULT_DRAFT)
    manifest = prepare_manifest(genesis, applications, plan, draft, relayers, software, read_json(cold),
                                include_public_p2p=True)
    # Leave room for the GPG agent's Unix socket names under the runner's temporary path.
    with tempfile.TemporaryDirectory(prefix="boot-", dir=artifacts) as directory:
        with signed_fixture(Path(directory), manifest) as args:
            verified = verify_manifest(args, genesis, plan, draft, software)
            evidence = install_genesis(Path(binary), follower.home, genesis, verified)
    check(evidence["changed"] and evidence["publicP2P"] == {"persistent_peers": address, "seeds": ""},
          "observer must install the exact signed genesis and use the selected peer")
    return {**evidence, "nodeId": source.node_id}
