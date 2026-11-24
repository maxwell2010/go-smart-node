"""Check local history retention on the isolated four-validator release rehearsal."""

import base64
import hashlib
import json
from urllib.parse import urlencode

from configure_history import configure
from del_delegation_smoke import balance, write_config
from native_consensus_smoke import committed_header, create_replica, create_replicas, wait_peer_pair, wait_replicas
from withdrawal_genesis_smoke import check, log_records


KEEP_STATE = 20
STATE_INTERVAL = 10
KEEP_BLOCKS = 10
BLOCK_TEST_EVIDENCE_WINDOW = 24


def supply_query(node, height, supply_key):
    params = urlencode({'path': '"/store/bank/key"', 'data': '0x' + supply_key.hex(),
                        'height': height, 'prove': 'true'})
    return node.rpc('abci_query?' + params)['response']


def supply_proof(response, height, supply_key):
    check(int(response.get('code', 0)) == 0 and int(response['height']) == height,
          'bank proof query did not use the requested height')
    check(base64.b64decode(response['key'], validate=True) == supply_key and
          int(base64.b64decode(response['value'], validate=True)) > 0, 'missing positive native supply proof value')
    proof = (response.get('proof_ops') or response.get('proofOps') or {}).get('ops', [])
    check(len(proof) == 2 and all(op['data'] for op in proof), 'missing IAVL or multistore proof')
    return response['value'], proof


def require_rpc_error(node, method, reason):
    try:
        node.rpc(method)
    except OSError as error:
        if str(error).startswith('HTTP 500 ') and reason in str(error):
            return
        raise
    raise ValueError(f'{method.split("?")[0]} did not reject the disabled history query')


def rehearse_blockstore(binary, root, genesis, expected, keys, env, owner, report):
    """Exercise Comet block pruning on a separate test-only genesis."""
    report['phase'] = 'blockstore-fixture'
    document = json.loads(genesis)
    evidence = document['consensus_params']['evidence']
    check(int(evidence['max_age_num_blocks']) == 100_000, 'unexpected original evidence window')
    evidence['max_age_num_blocks'] = str(BLOCK_TEST_EVIDENCE_WINDOW)
    block_genesis = json.dumps(document, separators=(',', ':')).encode()
    report['blockstoreGenesisSha256'] = hashlib.sha256(block_genesis).hexdigest()
    options = {'chain_id': expected['chainId'], 'evm_chain_id': expected['evmChainId']}
    validators = create_replicas(binary, root, block_genesis, keys, env, full_mesh=True, **options)
    observer = create_replica(binary, root, 'blockstore-observer', len(validators), block_genesis, None, env, **options)
    configure(observer.home, apply=True, role='consensus')
    write_config(observer.home, 'app.toml', {(None, 'min-retain-blocks'): str(KEEP_BLOCKS)})
    archive = validators[0]
    write_config(observer.home, 'config.toml', {
        ('p2p', 'persistent_peers'): json.dumps(f'{archive.node_id}@127.0.0.1:{archive.p2p}'),
        ('p2p', 'allow_duplicate_ip'): 'true',
        ('p2p', 'pex'): 'false',
    })
    run_nodes = (*validators, observer)
    for validator in validators:
        write_config(validator.home, 'app.toml', {(None, 'min-retain-blocks'): '0'})
        write_config(validator.home, 'config.toml', {('consensus', 'timeout_commit'): json.dumps('1s')})
        validator.start(binary, env, owner, 'blockstore-validator')
    report['phase'] = 'blockstore-catchup'
    wait_replicas(validators, 8, 90, report)
    observer.start(binary, env, owner, 'blockstore-observer')
    wait_peer_pair(archive, observer, 60, report)
    wait_replicas(run_nodes, 12, 90, report)
    check(int(observer.rpc('status')['sync_info']['earliest_block_height']) == 1,
          'observer discarded block 1 before reaching the evidence window')
    first_hash = archive.rpc('block?height=1')['block_id']['hash']
    check(observer.rpc('block?height=1')['block_id']['hash'] == first_hash, 'initial block replay differs')

    def verify_window():
        status = observer.rpc('status')
        first = int(status['sync_info']['earliest_block_height'])
        tip = int(status['sync_info']['latest_block_height'])
        check(int(status['validator_info']['voting_power']) == 0, 'observer unexpectedly has voting power')
        check(first > 1 and tip - first >= BLOCK_TEST_EVIDENCE_WINDOW,
              f'blockstore did not prune or shortened the evidence window: first={first}, tip={tip}')
        require_rpc_error(observer, 'block?height=1', 'lowest height')
        check(archive.rpc('block?height=1')['block_id']['hash'] == first_hash, 'archive lost block 1')
        committed_header((archive, observer), tip - BLOCK_TEST_EVIDENCE_WINDOW + 3)
        return first

    report['phase'] = 'blockstore-pruning'
    before_tip = wait_replicas(run_nodes, BLOCK_TEST_EVIDENCE_WINDOW + 8, 90, report)
    first_before = verify_window()
    report['phase'] = 'blockstore-restart'
    owner.stop(observer.child)
    observer.start(binary, env, owner, 'blockstore-restart')
    wait_peer_pair(archive, observer, 60, report)
    after_tip = wait_replicas(run_nodes, before_tip + 5, 90, report)
    first_after = verify_window()
    check(first_after >= first_before, 'pruned blocks reappeared after restart')
    handshakes = log_records(observer.log, 'ABCI Handshake App Info')
    check(len(handshakes) == 1 and int(handshakes[0]['height']) >= before_tip,
          'observer did not load persisted state after restart')
    header = committed_header(run_nodes, after_tip)
    report['blockstoreRetention'] = {
        'evidenceWindowBlocks': BLOCK_TEST_EVIDENCE_WINDOW, 'minRetainBlocks': KEEP_BLOCKS,
        'earliestBeforeRestart': first_before, 'earliestAfterRestart': first_after,
        'loadedHeightAfterRestart': int(handshakes[0]['height']), 'verifiedHeight': after_tip,
        'historicalBlockStillAvailableOnArchive': True, 'sameAppHashOnFiveReplicas': True,
        'appHash': header['app_hash'],
    }


def rehearse_history(binary, root, genesis, expected, env, owner, report, *, replicas, archive):
    report['phase'] = 'retention-profile-catchup'
    initial = report['initialHeight']
    denom = expected.get('nativeDenom', 'del')
    supply_key = b'\x00' + denom.encode('ascii')
    historical_height = initial + 1  # BaseApp accepts proof queries above height one.
    check(int(archive.rpc('block_results?height=1')['height']) == initial, 'archive lacks block 1 results')
    query = 'tx_search?' + urlencode({'query': '"tx.height=1"'})
    check('total_count' in archive.rpc(query), 'archive does not expose transaction search')
    supply_proof(supply_query(archive, historical_height, supply_key), historical_height, supply_key)

    # The snapshot observer already uses index len(validators) + 1.
    observer = create_replica(binary, root, 'retention-observer', len(expected['validators']) + 2,
                              genesis, None, env, chain_id=expected['chainId'], evm_chain_id=expected['evmChainId'])
    configure(observer.home, apply=True, role='consensus')
    # Exercise the rolling windows in a short CI run. The genesis and its
    # consensus evidence window are byte-for-byte the signed package fixture.
    write_config(observer.home, 'app.toml', {
        (None, 'pruning-keep-recent'): json.dumps(str(KEEP_STATE)),
        (None, 'pruning-interval'): json.dumps(str(STATE_INTERVAL)),
        (None, 'min-retain-blocks'): str(KEEP_BLOCKS),
    })
    write_config(observer.home, 'config.toml', {
        ('p2p', 'persistent_peers'): json.dumps(f'{archive.node_id}@127.0.0.1:{archive.p2p}'),
        ('p2p', 'allow_duplicate_ip'): 'true',
        ('p2p', 'pex'): 'false',
    })

    nodes = (*replicas, observer)
    observer.start(binary, env, owner, 'retention')
    wait_peer_pair(archive, observer, 60, report)
    start_tip = int(archive.rpc('abci_info')['response']['last_block_height'])
    tip = wait_replicas(nodes, max(start_tip + 3, KEEP_STATE + 2 * STATE_INTERVAL), 150, report)
    report['phase'] = 'retention-profile-history'
    first_block = committed_header((archive, observer), initial)
    check(first_block['block_hash'] == report['block1Hash'], 'retention observer replaced historical block 1')
    require_rpc_error(observer, 'block_results?height=1', 'node is not persisting finalize block responses')
    require_rpc_error(observer, query, 'transaction indexing is disabled')
    old = supply_query(observer, historical_height, supply_key)
    check(int(old.get('code', 0)) != 0 and not old.get('proof_ops') and not old.get('proofOps') and not old.get('value') and
          'ensure height has not been pruned' in old.get('log', ''),
          'retention profile did not prune the historical IAVL version: ' + str(old.get('log', ''))[:180])

    report['phase'] = 'retention-profile-restart'
    owner.stop(observer.child)
    observer.start(binary, env, owner, 'retention-restart')
    wait_peer_pair(archive, observer, 60, report)
    height = wait_replicas(nodes, tip + 4, 100, report) - 1
    handshakes = log_records(observer.log, 'ABCI Handshake App Info')
    check(len(handshakes) == 1 and int(handshakes[0]['height']) >= tip,
          'retention observer did not load persisted state after restart')
    info = observer.rpc('status')['validator_info']
    check(int(info['voting_power']) == 0, 'observer gained voting power')
    header = committed_header(nodes, height + 1)
    current = supply_proof(supply_query(observer, height, supply_key), height, supply_key)
    check(current == supply_proof(supply_query(archive, height, supply_key), height, supply_key),
          'fresh bank proof differs from archive witness')
    old_after = supply_query(observer, historical_height, supply_key)
    check(int(old_after.get('code', 0)) == int(old['code']) and not old_after.get('proof_ops') and
          not old_after.get('proofOps') and not old_after.get('value'), 'old state reappeared after restart')
    native_atomic = balance(expected['actor'], expected['actorNative'], height, observer.evm, observer.rest, denom=denom)
    check(native_atomic == balance(expected['actor'], expected['actorNative'], height, archive.evm, archive.rest, denom=denom),
          'archive and retention observer disagree on recent balance')

    report['historyRetention'] = {
        'keepStateVersions': KEEP_STATE, 'pruningInterval': STATE_INTERVAL, 'minRetainBlocks': KEEP_BLOCKS,
        'historicalProofHeight': historical_height, 'retainedBlockHeight': initial, 'archivedSupplyProofAvailable': True,
        'prunedSupplyProofUnavailable': True, 'historicalABCIResponsesDisabled': True,
        'transactionSearchDisabled': True, 'loadedHeightAfterRestart': int(handshakes[0]['height']),
        'verifiedHeight': height, 'appHash': header['app_hash'], 'freshProofMatchesArchive': True,
        'freshProofSha256': hashlib.sha256(json.dumps(current, sort_keys=True).encode()).hexdigest(),
    }
    if 'nativeDenom' in expected:
        report['historyRetention'].update(nativeDenom=denom, nativeAtomic=str(native_atomic))
    else:
        report['historyRetention']['delAtomic'] = str(native_atomic)
