"""Carry a real ICS-20 packet across the private native binary handoff."""

import hashlib
import json
from pathlib import Path
import re
import shutil
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "internal" / "ibclab"))
from hermes_lab import (
    A_ID, B_ID, HERMES_SHA256, Lab, amount, binary, commitments, connect_transfer, escrow, field, fields,
    fund_native_relayer, new_events, open_channel, phase_after, phase_before, query, require, rpc, verify_native_unbonding,
)


class NativeUpgradeIBC:
    def __init__(self, args, root, env, report, signals):
        self.report = report
        self.lab = Lab(root, env, report, signals)
        self.reference, reference_hash = binary(args.reference_binary, args.reference_sha256)
        self.hermes, hermes_hash = binary(args.hermes_binary, HERMES_SHA256)
        self.report["ibcBinaries"] = {"referenceSha256": reference_hash, "hermesSha256": hermes_hash}
        self.value = 10**18

    def fund_relayer(self, mcnd, replicas, expected, genesis, height):
        self.a, funded = fund_native_relayer(self.lab, mcnd, replicas, expected, genesis, height)
        self.report["fundedGenesisSha256"] = hashlib.sha256(funded).hexdigest()
        self.replicas = replicas

    def send(self, source, target, channel, denom, recipient):
        self.run("tx", "ft-transfer", "--src-chain", source, "--dst-chain", target, "--src-port", "transfer",
                 "--src-channel", channel, "--denom", denom, "--amount", self.value, "--receiver", recipient,
                 "--timeout-seconds", 900)

    def client_height(self):
        host = self.b["rpc"]
        any_state = fields(query(host, "/ibc.core.client.v1.Query/ClientState", field(1, self.client))[1][0])
        require(any_state[1] == [b"/ibc.lightclients.tendermint.v1.ClientState"], "counterparty must track a Tendermint client")
        state = fields(any_state[2][0])
        require(state[1] == [A_ID.encode()] and fields(state[4][0])[1] == [28 * 24 * 60 * 60],
                "counterparty client changed the chain ID or unbonding period")
        return fields(state[7][0])[2][0]

    def start_packet(self, mcnd):
        self.a, self.b, self.run, self.a_channel, self.b_channel = connect_transfer(
            self.lab, mcnd, self.reference, self.hermes, owned=self.a)
        self.voucher = "ibc/" + hashlib.sha256(("transfer/" + self.b_channel + "/del").encode()).hexdigest().upper()
        host = self.b["rpc"]
        channel = fields(query(host, "/ibc.core.channel.v1.Query/Channel",
                               field(1, "transfer") + field(2, self.b_channel))[1][0])
        require(len(channel.get(4, [])) == 1, "transfer channel must have one counterparty connection")
        connection = fields(query(host, "/ibc.core.connection.v1.Query/Connection", field(1, channel[4][0]))[1][0])
        self.client = connection[1][0].decode()
        require(re.fullmatch(r"07-tendermint-\d+", self.client) is not None, "unexpected counterparty light client type")
        self.initial_client_height = self.client_height()
        before = phase_before(self.a, self.b)
        self.send(A_ID, B_ID, self.a_channel, "del", self.b["address"])
        self.lab.wait(lambda: len(commitments(self.a["rpc"], self.a_channel)) == 1, "one genuine outbound packet before upgrade")
        self.pending = commitments(self.a["rpc"], self.a_channel)
        self.assert_pending()
        def send_events():
            return [event for event in new_events(self.a, before["a_tx"], "send_packet")
                    if event.get("packet_sequence") == str(self.pending[0]["sequence"])]

        self.lab.wait(lambda: bool(send_events()), "commit and index the genuine outgoing packet")
        sends = send_events()
        require(len(sends) == 1, "one committed send event must identify the pending packet")
        self.deadline = int(sends[0]["packet_timeout_timestamp"])
        require(self.deadline > self.lab.committed_time_ns(host), "pending packet already expired")
        phase_after(self.lab, "pending_packet_submit_before_native_upgrade", self.a, self.b, before, -self.value)
        self.submitted_balances = (amount(self.a["rpc"], self.a["address"], "del"),
                                   amount(host, self.b["address"], "stake"))
        self.report["ibcPacket"] = {"packets": self.pending, "escrowAtomic": str(self.value), "client": self.client,
                                    "clientHeightBefore": self.initial_client_height}

    def assert_pending(self):
        for replica in self.replicas:
            port = replica.p2p + 1
            self.lab.wait(lambda: commitments(port, self.a_channel) == self.pending and escrow(port, "del") == self.value,
                          "native replicas disagree on pending packet or escrow")
            verify_native_unbonding(port)
        require(open_channel(self.a["rpc"]) == (self.a_channel, self.b_channel) and
                open_channel(self.b["rpc"]) == (self.b_channel, self.a_channel),
                "the original ICS-20 channel pair changed during the handoff")
        require(amount(self.b["rpc"], self.b["address"], self.voucher) == 0 and
                not commitments(self.b["rpc"], self.b_channel), "counterparty moved principal while packet was pending")

    def clear(self):
        self.run("clear", "packets", "--chain", A_ID, "--port", "transfer", "--channel", self.a_channel)
        self.lab.wait(lambda: not commitments(self.a["rpc"], self.a_channel) and not commitments(self.b["rpc"], self.b_channel),
                      "genuine packet commitments must clear")

    def reference_header(self, height):
        response = rpc(self.b["rpc"], "block", height=str(height))
        header = response["block"]["header"]
        block_hash, app_hash = response["block_id"]["hash"], header["app_hash"]
        require(header["chain_id"] == B_ID and int(header["height"]) == height and
                all(re.fullmatch(r"[A-Fa-f0-9]{64}", value) for value in (block_hash, app_hash)),
                "invalid committed counterparty checkpoint")
        return {"height": height, "blockHash": block_hash, "appHash": app_hash}

    def pending_ledger(self):
        self.assert_pending()
        return {"ownedRelayerAtomic": str(amount(self.a["rpc"], self.a["address"], "del")),
                "referenceRelayerAtomic": str(amount(self.b["rpc"], self.b["address"], "stake")),
                "referenceClientHeight": self.client_height(), "referenceVoucherAtomic": "0",
                "packets": self.pending, "escrowAtomic": str(self.value)}

    def mark_checkpoint(self):
        reference = self.b["rpc"]
        committed = int(rpc(reference, "abci_info")["response"]["last_block_height"])
        height = min(committed, self.lab.height(reference, B_ID)) - 1
        require(height > 0, "counterparty has no committed rollback anchor")
        self.backup_reference_header = self.reference_header(height)
        self.backup_ledger = self.pending_ledger()

    def copy_private_home(self, source, destination):
        root = self.lab.root.resolve(strict=True)
        require(source.is_dir() and not source.is_symlink() and source.resolve(strict=True).is_relative_to(root) and
                destination.parent.resolve(strict=True).is_relative_to(root),
                "backup requires private, unlinked homes")
        require(not any(entry.is_symlink() for entry in source.rglob("*")), "backup home contains a symbolic link")
        shutil.copytree(source, destination)

    def backup_closed_homes(self):
        require(all(replica.child.returncode == 0 for replica in self.replicas), "close all predecessor signers cleanly before copying")
        self.lab.stop(self.b["process"])
        require(self.b["process"].returncode == 0, "close the counterparty cleanly before copying")
        self.backup = self.lab.root / "pre-upgrade-closed"
        self.backup.mkdir(mode=0o700)
        for replica in self.replicas:
            self.copy_private_home(replica.home, self.backup / replica.label)
        self.copy_private_home(self.b["home"], self.backup / "reference")

    def start_reference(self, label):
        require(self.b["process"].poll() is not None, "old counterparty signer is still running")
        self.b["process"] = self.lab.spawn(self.b["start"], label)
        anchor = self.backup_reference_header["height"]
        self.lab.wait(lambda: self.lab.height(self.b["rpc"], B_ID) > anchor, "counterparty must reopen its backup checkpoint")
        require(self.reference_header(anchor) == self.backup_reference_header, "counterparty checkpoint changed on reopen")

    def restore_closed_homes(self, replicas):
        require(all(replica.child.returncode == 0 for replica in replicas), "stop all advanced native signers before restoring")
        self.lab.stop(self.b["process"])
        require(self.b["process"].returncode == 0, "stop the advanced counterparty before restoring")
        restored = self.lab.root / "restored-pre-upgrade"
        restored.mkdir(mode=0o700)
        for replica in replicas:
            self.copy_private_home(self.backup / replica.label, restored / replica.label)
        home = restored / "reference"
        self.copy_private_home(self.backup / "reference", home)
        self.b["home"] = home
        self.b["start"][self.b["start"].index("--home") + 1] = home
        return restored

    def verify_restored_ledger(self, replicas):
        self.replicas = replicas
        require(self.pending_ledger() == self.backup_ledger, "restored ledger differs from the closed pre-relay checkpoint")
        return {"referenceCheckpoint": self.backup_reference_header, "pendingLedger": self.backup_ledger,
                "mode": "coordinated-private-copy-restore"}

    def settle_after_handoff(self, target):
        self.assert_pending()
        require(self.lab.committed_time_ns(self.b["rpc"]) < self.deadline, "pending packet expired before successor relay")
        # Governance and the signed withdrawal use a separate operator. The
        # send already accounted for the relayer principal and fee.
        before = phase_before(self.a, self.b)
        require((before["a"], before["b"]) == self.submitted_balances,
                "unrelated upgrade activity changed a relayer balance")
        self.run("update", "client", "--host-chain", B_ID, "--client", self.client)
        self.lab.wait(lambda: self.client_height() > max(target, self.initial_client_height),
                      "counterparty must verify a post-upgrade Tendermint header")
        self.report["ibcPacket"]["clientHeightAfter"] = self.client_height()
        self.clear()
        require(escrow(self.a["rpc"], "del") == self.value and amount(self.b["rpc"], self.b["address"], self.voucher) == self.value,
                "pending packet did not credit one voucher with matching source escrow")
        phase_after(self.lab, "pending_packet_across_native_upgrade", self.a, self.b, before)
        acks = [event for event in new_events(self.b, before["b_tx"], "write_acknowledgement")
                if event.get("packet_sequence") == str(self.pending[0]["sequence"])]
        require(len(acks) == 1 and json.loads(bytes.fromhex(acks[0]["packet_ack_hex"])).get("result"),
                "pending packet requires exactly one committed success acknowledgement")
        before = phase_before(self.a, self.b)
        self.send(B_ID, A_ID, self.b_channel, self.voucher, self.a["address"])
        self.clear()
        require(escrow(self.a["rpc"], "del") == 0 and amount(self.b["rpc"], self.b["address"], self.voucher) == 0,
                "voucher return must release exactly the original native principal")
        phase_after(self.lab, "post_native_upgrade_voucher_return", self.a, self.b, before, self.value)
