"""Carry a real ICS-20 packet across the private native binary handoff."""

import hashlib
import json
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / "internal" / "ibclab"))
from hermes_lab import (
    A_ID, B_ID, HERMES_SHA256, Lab, amount, binary, commitments, connect_transfer, escrow, field, fields,
    fund_native_relayer, new_events, open_channel, phase_after, phase_before, query, require, verify_native_unbonding,
)


class NativeUpgradeIBC:
    def __init__(self, args, root, env, report, signals):
        self.report = report
        self.lab = Lab(root, env, report, signals)
        self.reference = binary(args.reference_binary, args.reference_sha256)
        self.hermes = binary(args.hermes_binary, HERMES_SHA256)
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
        self.before = phase_before(self.a, self.b)
        self.send(A_ID, B_ID, self.a_channel, "del", self.b["address"])
        self.lab.wait(lambda: len(commitments(self.a["rpc"], self.a_channel)) == 1, "one genuine outbound packet before upgrade")
        self.pending = commitments(self.a["rpc"], self.a_channel)
        self.assert_pending()
        def send_events():
            return [event for event in new_events(self.a, self.before["a_tx"], "send_packet")
                    if event.get("packet_sequence") == str(self.pending[0]["sequence"])]

        self.lab.wait(lambda: bool(send_events()), "commit and index the genuine outgoing packet")
        sends = send_events()
        require(len(sends) == 1, "one committed send event must identify the pending packet")
        self.deadline = int(sends[0]["packet_timeout_timestamp"])
        require(self.deadline > self.lab.committed_time_ns(host), "pending packet already expired")
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

    def settle_after_handoff(self, target):
        self.assert_pending()
        require(self.lab.committed_time_ns(self.b["rpc"]) < self.deadline, "pending packet expired before successor relay")
        self.run("update", "client", "--host-chain", B_ID, "--client", self.client)
        self.lab.wait(lambda: self.client_height() > max(target, self.initial_client_height),
                      "counterparty must verify a post-upgrade Tendermint header")
        self.report["ibcPacket"]["clientHeightAfter"] = self.client_height()
        self.clear()
        require(escrow(self.a["rpc"], "del") == self.value and amount(self.b["rpc"], self.b["address"], self.voucher) == self.value,
                "pending packet did not credit one voucher with matching source escrow")
        phase_after(self.lab, "pending_packet_across_native_upgrade", self.a, self.b, self.before, -self.value)
        acks = [event for event in new_events(self.b, self.before["b_tx"], "write_acknowledgement")
                if event.get("packet_sequence") == str(self.pending[0]["sequence"])]
        require(len(acks) == 1 and json.loads(bytes.fromhex(acks[0]["packet_ack_hex"])).get("result"),
                "pending packet requires exactly one committed success acknowledgement")
        before = phase_before(self.a, self.b)
        self.send(B_ID, A_ID, self.b_channel, self.voucher, self.a["address"])
        self.clear()
        require(escrow(self.a["rpc"], "del") == 0 and amount(self.b["rpc"], self.b["address"], self.voucher) == 0,
                "voucher return must release exactly the original native principal")
        phase_after(self.lab, "post_native_upgrade_voucher_return", self.a, self.b, before, self.value)
