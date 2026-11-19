"""Read committed native reward liabilities without keys or transaction submission."""

import base64
import hashlib
import re
from urllib.parse import urlencode

from withdrawal_genesis_smoke import check, native


def reward_store(node, key, height):
    check(type(height) is int and height > 0 and isinstance(key, bytes) and 1 <= len(key) <= 42,
          'invalid read-only reward checkpoint query')
    params = urlencode({'path': '"/store/validator/key"', 'data': '0x' + key.hex(),
                        'height': height, 'prove': 'false'})
    result = node.rpc('abci_query?' + params)['response']
    check(int(result.get('code', 0)) == 0 and int(result['height']) == height,
          'reward store query failed or used a different committed height')
    if result.get('key'):
        check(base64.b64decode(result['key'], validate=True) == key, 'reward store returned a different key')
    value = result.get('value')
    return base64.b64decode(value, validate=True) if value else None


def reward_tree_present(node, height):
    marker = reward_store(node, b'\x7e\x00', height)
    check(marker in (None, b'\x01'), 'invalid committed reward tree marker')
    return marker is not None


def decode_reward_sweep(raw):
    if raw is None:
        return {'openedHeight': 0, 'cursorHex': '', 'endHex': ''}
    check(len(raw) >= 12, 'truncated committed reward payout cursor')
    height, size = int.from_bytes(raw[:8], 'big'), int.from_bytes(raw[8:10], 'big')
    check(size in (0, 42) and len(raw) >= 12 + size, 'invalid committed reward cursor length')
    end_size = int.from_bytes(raw[10 + size:12 + size], 'big')
    check(end_size in (0, 42) and len(raw) == 12 + size + end_size,
          'invalid committed reward sweep boundary')
    cursor, end = raw[10:10 + size], raw[12 + size:]
    for key in (cursor, end):
        check(not key or key[0] == 0x7b and key[1] in (0, 1), 'invalid committed reward payout key')
    check(not cursor or end and cursor <= end, 'committed reward cursor passed its captured boundary')
    return {'openedHeight': height, 'cursorHex': cursor.hex(), 'endHex': end.hex()}


def native_module_balance(node, module, height, read_native):
    account = read_native('/cosmos/auth/v1beta1/module_accounts/' + module, height,
                          base=node.rest)['account']
    check(account.get('name') == module, 'unexpected native reward module identity')
    address = account['base_account']['address']
    check(re.fullmatch(r'd0[0-9a-z]{20,80}', address) is not None, 'invalid native reward module address')
    balance = read_native('/cosmos/bank/v1beta1/balances/' + address + '/by_denom?denom=del',
                          height, base=node.rest)['balance']
    check(balance['denom'] == 'del' and re.fullmatch(r'[0-9]{1,78}', balance['amount']) is not None,
          'invalid committed reward module balance')
    return address, balance['amount']


def native_reward_checkpoint(replicas, height, *, require_pending=False, read_native=native):
    check(bool(replicas), 'reward checkpoint requires isolated replicas')
    snapshots = []
    for node in replicas:
        if not reward_tree_present(node, height):
            check(reward_store(node, b'\x7d', height) is None and reward_store(node, b'\x7e\x01', height) is None,
                  'orphan reward liabilities exist without the tree marker')
            check(not require_pending, 'predecessor has no reward tree for the pending-credit gate')
            _, collector = native_module_balance(node, 'fee_collector', height, read_native)
            snapshots.append({'height': height, 'mode': 'legacy-no-reward-tree', 'collectorAtomic': collector})
            continue
        liability = reward_store(node, b'\x7d', height)
        check(liability is not None and len(liability) == 32, 'missing committed native reward liability')
        owed = int.from_bytes(liability, 'big')
        address, escrow = native_module_balance(node, 'native_fee_rewards', height, read_native)
        _, collector = native_module_balance(node, 'fee_collector', height, read_native)
        check(int(escrow) >= owed, 'committed reward escrow does not cover liabilities')
        check(not require_pending or owed > 0, 'upgrade checkpoint must contain unpaid native rewards')
        raw_sweep = reward_store(node, b'\x7e\x01', height)
        sweep = decode_reward_sweep(raw_sweep)
        check(sweep['openedHeight'] <= height, 'committed reward sweep starts in the future')
        snapshots.append({'height': height, 'mode': 'native-reward-tree', 'owedAtomic': str(owed),
                          'escrowAtomic': escrow, 'escrowAddress': address, 'collectorAtomic': collector, 'sweep': sweep,
                          'sweepSha256': hashlib.sha256(raw_sweep or b'').hexdigest()})
    check(all(snapshot == snapshots[0] for snapshot in snapshots),
          'isolated replicas disagree on committed reward liabilities or cursor')
    return snapshots[0]


def compare_reward_migration(before, after):
    check(after['height'] == before['height'] + 1 and after['mode'] == 'native-reward-tree',
          'successor did not initialize or retain native reward accounting')
    check(after['height'] % 120 != 0, 'pending-credit migration gate must run outside the automatic payout tick')
    collector = int(before['collectorAtomic'])
    if before['mode'] == 'legacy-no-reward-tree':
        check(int(after['owedAtomic']) == collector,
              'legacy migration credits differ from real predecessor fees entering escrow')
        return 'legacy-index-to-reward-tree'
    check(int(before['owedAtomic']) > 0, 'tree migration did not carry a genuine unpaid reward liability')
    check(int(after['owedAtomic']) == int(before['owedAtomic']) + collector and
          int(after['escrowAtomic']) == int(before['escrowAtomic']) + collector and
          before['sweepSha256'] == after['sweepSha256'],
          'module migration changed pending rewards, escrow or the payout cursor')
    return 'pending-reward-tree-preserved'


def verify_reward_handoff(replicas, target, report, *, restored=False, read_native=native):
    """Capture once on the primary branch; compare exact records after restore."""
    if 'rewardMigration' not in report:
        return
    checkpoints = report['rewardMigration']
    check(native_reward_checkpoint(replicas, target - 1, read_native=read_native) == checkpoints['before'],
          'reopening changed the committed pre-upgrade reward checkpoint')
    after = native_reward_checkpoint(replicas, target, read_native=read_native)
    if restored:
        check('after' in checkpoints and after == checkpoints['after'],
              'restored approved migration changed the committed reward accounting')
    else:
        check('after' not in checkpoints, 'primary reward checkpoint was already captured')
        checkpoints.update(after=after, coverage=compare_reward_migration(checkpoints['before'], after))
