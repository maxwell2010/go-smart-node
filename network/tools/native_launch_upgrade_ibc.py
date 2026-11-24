"""Keep a signed ICS-20 transfer in flight during a governed MSP upgrade."""

import hashlib
import json
from pathlib import Path
import re
import sys

sys.path.insert(0, str(Path(__file__).resolve().parents[1] / 'internal' / 'ibclab'))
from hermes_lab import (
    B_ID, HERMES_SHA256, OWNED_GAS_PRICE, Lab, amount, binary, clear_packets, commitments, connect_transfer, escrow,
    field, fields, fund_assembled_relayer, new_events, open_channel, patch_toml, phase_after, phase_before, query, require, supply,
)


class GovernedUpgradeTransfer:
    def __init__(self, args, root, env, report, signals):
        root.mkdir(mode=0o700)
        self.lab = Lab(root, env, report, signals)
        self.donor = args.ibc_fixture_donor
        self.hermes, hermes_hash = binary(args.ibc_hermes, HERMES_SHA256)
        self.reference, reference_hash = binary(args.ibc_reference, args.ibc_reference_sha256)
        self.value = 10**18
        self.report = report['ibcUpgrade'] = {'hermesSha256': hermes_hash, 'referenceSha256': reference_hash}

    def close(self):
        self.lab.close()

    def configure_replicas(self, nodes):
        for node in nodes:
            patch_toml(node.home / 'config/app.toml', {('', 'minimum-gas-prices'): f'{OWNED_GAS_PRICE}msp'})

    def start(self, predecessor, nodes):
        self.chain, self.denom = nodes[0].chain_id, 'msp'
        owned, _ = fund_assembled_relayer(self.lab, predecessor, nodes[0], self.donor, self.denom)
        owned.update(chain=self.chain, denom=self.denom)
        try:
            self.a, self.b, self.run, self.a_channel, self.b_channel = connect_transfer(
                self.lab, predecessor, self.reference, self.hermes, owned=owned)
        except RuntimeError as error:
            # Key-import commands suppress their output. Keep only known
            # read-only health diagnostics for this combined rehearsal.
            reason = str(error)
            prefix = 'Hermes chain health warnings: '
            if reason.startswith(prefix):
                warnings = json.loads(reason[len(prefix):])
                messages = [str(row.get('fields', {}).get('error') or row.get('fields', {}).get('message') or
                                row.get('message', '')) for row in warnings]
                raise ValueError('Hermes health check: ' + ' '.join(messages)) from None
            raise
        self.voucher = 'ibc/' + hashlib.sha256(f'transfer/{self.b_channel}/{self.denom}'.encode()).hexdigest().upper()
        peer = self.b['rpc']
        channel = fields(query(peer, '/ibc.core.channel.v1.Query/Channel',
                               field(1, 'transfer') + field(2, self.b_channel))[1][0])
        require(len(channel.get(4, [])) == 1, 'counterparty ICS-20 channel must have one connection')
        connection = fields(query(peer, '/ibc.core.connection.v1.Query/Connection', field(1, channel[4][0]))[1][0])
        self.client = connection[1][0].decode()
        require(re.fullmatch(r'07-tendermint-\d+', self.client), 'unexpected counterparty light client type')
        self.report.update(sourceChannel=self.a_channel, counterpartyChannel=self.b_channel,
                           counterpartyClient=self.client, clientHeightBefore=self.client_height())

        before = phase_before(self.a, self.b)
        self.send(self.chain, B_ID, self.a_channel, self.denom, self.b['address'])
        self.pending = self.lab.wait(lambda: commitments(self.a['rpc'], self.a_channel), 'outbound packet must commit')
        require(len(self.pending) == 1, 'expected exactly one unresolved commitment')
        self.sequence = str(self.pending[0]['sequence'])
        self.report['submitted'] = self.check_pending(nodes)
        phase_after(self.lab, 'submit_before_msp_upgrade', self.a, self.b, before, -self.value)

    def send(self, source, destination, channel, denom, receiver):
        self.run('tx', 'ft-transfer', '--src-chain', source, '--dst-chain', destination, '--src-port', 'transfer',
                 '--src-channel', channel, '--denom', denom, '--amount', self.value, '--receiver', receiver,
                 '--timeout-seconds', 900)

    def client_height(self):
        state = fields(query(self.b['rpc'], '/ibc.core.client.v1.Query/ClientState', field(1, self.client))[1][0])
        require(state[1] == [b'/ibc.lightclients.tendermint.v1.ClientState'], 'counterparty must use a Tendermint client')
        value = fields(state[2][0])
        require(value[1] == [self.chain.encode()], 'counterparty tracks a different chain')
        return fields(value[7][0])[2][0]

    def check_pending(self, nodes):
        for node in nodes:
            port = node.p2p + 1
            self.lab.wait(lambda: commitments(port, self.a_channel) == self.pending and escrow(port, self.denom) == self.value,
                          'MSP replica must retain the exact commitment and escrow')
            require(open_channel(port) == (self.a_channel, self.b_channel), 'MSP channel changed across the upgrade')
        require(open_channel(self.b['rpc']) == (self.b_channel, self.a_channel) and
                supply(self.b['rpc'], self.voucher) == 0 and not commitments(self.b['rpc'], self.b_channel),
                'counterparty channel changed or credited an unresolved transfer')
        return {'commitments': self.pending, 'escrowAtomic': str(self.value), 'matchingNodes': len(nodes)}

    def clear(self):
        clear_packets(self.run, self.chain, self.a_channel)
        self.lab.wait(lambda: not commitments(self.a['rpc'], self.a_channel) and
                      not commitments(self.b['rpc'], self.b_channel), 'both packet commitments must clear')

    def settle(self, height, nodes):
        before = phase_before(self.a, self.b)
        self.run('update', 'client', '--host-chain', B_ID, '--client', self.client)
        client_height = self.lab.wait(lambda: (at := self.client_height()) > height and at,
                                      'counterparty must verify a post-upgrade header')
        self.clear()
        phase_after(self.lab, 'relay_after_msp_upgrade', self.a, self.b, before)
        acknowledgments = [event for event in new_events(self.b, before['b_tx'], 'write_acknowledgement')
                           if event.get('packet_sequence') == self.sequence and event.get('packet_dst_channel') == self.b_channel]
        confirmations = [event for event in new_events(self.a, before['a_tx'], 'acknowledge_packet')
                         if event.get('packet_sequence') == self.sequence and event.get('packet_src_channel') == self.a_channel]
        require(len(acknowledgments) == len(confirmations) == 1 and
                json.loads(bytes.fromhex(acknowledgments[0]['packet_ack_hex'])).get('result'),
                'counterparty must write one success acknowledgment and source must process it once')
        require(supply(self.b['rpc'], self.voucher) == amount(self.b['rpc'], self.b['address'], self.voucher) == self.value and
                escrow(self.a['rpc'], self.denom) == self.value, 'only the escrowed amount may turn into a voucher')
        before = phase_before(self.a, self.b)
        self.send(B_ID, self.chain, self.b_channel, self.voucher, self.a['address'])
        self.lab.wait(lambda: commitments(self.b['rpc'], self.b_channel), 'return packet must commit')
        self.clear()
        phase_after(self.lab, 'return_post_upgrade_voucher', self.a, self.b, before, self.value)
        for node in nodes:
            port = node.p2p + 1
            self.lab.wait(lambda: escrow(port, self.denom) == 0 and not commitments(port, self.a_channel),
                          'MSP replicas must finish with zero escrow and no commitment')
        require(supply(self.b['rpc'], self.voucher) == 0, 'return must burn the counterparty voucher')
        self.report.update(clientHeightAfter=client_height, acknowledgedSequence=self.sequence,
                           acknowledgments=len(acknowledgments), sourceConfirmations=len(confirmations),
                           returnedAtomic=str(self.value), finalEscrowAtomic='0', finalVoucherSupplyAtomic='0')
